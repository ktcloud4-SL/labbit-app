package connectorwss

import (
	"encoding/json"
	"fmt"
	"sync"
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

// controlConn은 Control connection 하나의 write와 close를 직렬화한다.
//
// gorilla/websocket은 동시에 하나의 goroutine만 write하도록 요구한다(Close와 WriteControl은 예외).
// ERROR, 4001/4002 종료, shutdown 1001과 이후 command 전송이 서로 다른 goroutine에서 올 수 있으므로
// text message write는 connection별 mutex로, close는 한 번만(먼저 요청한 이유가 이긴다) 수행한다.
// 서로 다른 connection의 write는 서로 막지 않는다.
type controlConn struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	closeMu sync.Mutex
	closing bool
	grace   *time.Timer
}

func newControlConn(conn *websocket.Conn) *controlConn {
	return &controlConn{conn: conn}
}

// writeJSON은 v를 JSON text message 하나로 보낸다. 다른 write가 진행 중이면 기다린다.
func (c *controlConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("connectorwss: message marshal: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// close는 code/reason의 close frame을 보내고 closeGrace 뒤에 TCP connection을 닫는다.
// 처음 호출한 이유만 적용하고 이후 호출은 아무것도 하지 않는다. 여러 goroutine에서 호출해도 안전하다.
// read loop는 상대의 close 응답이나 connection 종료로 끝나며, 이 함수는 read loop를 기다리지 않는다.
func (c *controlConn) close(code int, reason string) {
	c.closeMu.Lock()
	if c.closing {
		c.closeMu.Unlock()
		return
	}
	c.closing = true
	c.closeMu.Unlock()

	// WriteControl은 다른 write와 동시에 호출해도 안전하다.
	_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(writeTimeout))

	timer := time.AfterFunc(closeGrace, func() { _ = c.conn.Close() })
	c.closeMu.Lock()
	c.grace = timer
	c.closeMu.Unlock()
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
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closing
}

// release는 connection 종료 뒤에 남은 grace timer를 정리한다.
func (c *controlConn) release() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.grace != nil {
		c.grace.Stop()
	}
}
