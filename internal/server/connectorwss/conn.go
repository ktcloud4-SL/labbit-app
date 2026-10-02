package connectorwss

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// contracts/connector/README.md §15의 Control close code다.
const (
	// closeCredentialRevoked는 Connector Credential(또는 Connector) revoke로 현재 connection을 더 이상 신뢰하지 않을 때다.
	closeCredentialRevoked = 4001
	// closeReplaced는 같은 Connector의 새 Control connection이 기존 connection을 대체했을 때다.
	closeReplaced = 4002
)

// closeGrace는 close frame을 보낸 뒤 상대의 close 응답을 기다리는 시간이다. 이 시간이 지나면 TCP connection을 닫는다.
// 곧바로 닫으면 아직 읽지 않은 입력 때문에 상대가 close frame을 받기 전에 연결이 reset될 수 있다.
const closeGrace = 2 * time.Second

// errConnClosing은 종료가 시작된 connection에는 application message를 쓸 수 없음을 나타낸다.
// peer나 TCP connection의 상태가 아니라 이 connection의 closing 상태로 판정한 결과다.
var errConnClosing = errors.New("connectorwss: connection is closing")

// wsConn은 controlConn이 사용하는 *websocket.Conn의 method다. 외부 WebSocket 경계이므로 test에서 fake로 대체해
// write와 close의 순서를 결정적으로 검증한다.
type wsConn interface {
	SetWriteDeadline(t time.Time) error
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}

var _ wsConn = (*websocket.Conn)(nil)

// controlConn은 Control connection 하나의 application write와 close를 직렬화하는 writer다.
//
// ERROR, 4001/4002 종료, shutdown 1001과 이후 command 전송이 서로 다른 goroutine에서 올 수 있으므로
// closing 상태, application message write, close frame 전송을 connection별 mutex 하나가 함께 소유한다.
// 그래서 한 connection에서 가능한 순서는 둘뿐이다.
//
//   - write가 먼저 소유권을 얻으면: write가 끝난 뒤에 close가 진행된다.
//   - close가 먼저 소유권을 얻으면: closing이 된 뒤 close frame이 나가고, 이후 모든 writeJSON은 errConnClosing으로 거절된다.
//
// close 뒤에는 상대가 close에 응답했는지, TCP connection이 열려 있는지와 무관하게 application message를 보내지 않는다.
// 처음 요청한 close 이유만 적용한다. 서로 다른 connection의 write는 서로 막지 않는다(전역 lock 없음).
type controlConn struct {
	conn wsConn

	// mu는 closing 전환, writeJSON, close frame 전송의 순서를 정한다.
	mu sync.Mutex
	// closing은 mu를 잡고서만 true로 바꾼다. isClosing이 진행 중인 write를 기다리지 않도록 lock 없이 읽는다.
	closing atomic.Bool
	grace   atomic.Pointer[time.Timer]
}

func newControlConn(conn wsConn) *controlConn {
	return &controlConn{conn: conn}
}

// writeJSON은 v를 JSON text message 하나로 보낸다. 다른 write나 close가 진행 중이면 기다린다.
// 종료가 시작되었거나 기다리는 동안 시작되었다면 아무것도 쓰지 않고 errConnClosing을 반환한다.
func (c *controlConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("connectorwss: message marshal: %w", err)
	}
	return c.writeText(data)
}

// writeText는 이미 직렬화된 JSON text message 하나를 보낸다. writeJSON과 같은 mutex 아래에서 closing을 확인하므로
// server의 모든 application write(ERROR, HELLO_ACK, command)와 close frame은 이 connection에서 하나의 순서로 직렬화된다.
// 종료가 시작되었다면 아무것도 쓰지 않고 errConnClosing을 반환한다.
func (c *controlConn) writeText(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing.Load() {
		return errConnClosing
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// route는 HELLO_ACK를 마친 이 connection의 command writer다. Registration.MarkReady로 등록해 Router가 exact Session의
// connection에만 쓰게 한다. 종료 중이라 쓰지 못했다면 아무 byte도 나가지 않았으므로 connector.ErrRouteClosed를 반환한다.
func (c *controlConn) route(data []byte) error {
	err := c.writeText(data)
	if errors.Is(err, errConnClosing) {
		return connector.ErrRouteClosed
	}
	return err
}

// close는 code/reason의 close frame을 보내고 closeGrace 뒤에 TCP connection을 닫는다.
// 진행 중인 writeJSON이 끝난 뒤에 closing으로 전환하고 close frame을 쓴다. 처음 호출한 이유만 적용하고 이후 호출은
// 아무것도 하지 않는다. 여러 goroutine에서 호출해도 안전하다.
// read loop는 상대의 close 응답이나 connection 종료로 끝나며, 이 함수는 read loop를 기다리지 않는다.
func (c *controlConn) close(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing.Load() {
		return
	}
	c.closing.Store(true)

	// close frame이 막혀도 closeGrace 뒤에는 TCP connection을 닫아 정리되게 한다.
	c.grace.Store(time.AfterFunc(closeGrace, func() { _ = c.conn.Close() }))
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(writeTimeout))
}

// closeFor는 Registry의 종료 요청을 Control close code로 바꾼다. Registry가 호출자를 오래 막지 않도록 기다리지 않고 돌아온다.
func (c *controlConn) closeFor(reason connector.CloseReason) {
	switch reason {
	case connector.CloseRevoked:
		go c.close(closeCredentialRevoked, "credential revoked")
	case connector.CloseReplaced:
		go c.close(closeReplaced, "replaced by a newer connection")
	default:
		go c.close(websocket.CloseGoingAway, "connection closed")
	}
}

// isClosing은 종료가 요청되었는지 반환한다.
func (c *controlConn) isClosing() bool {
	return c.closing.Load()
}

// release는 connection 종료 뒤에 남은 grace timer를 정리한다.
func (c *controlConn) release() {
	if timer := c.grace.Load(); timer != nil {
		timer.Stop()
	}
}
