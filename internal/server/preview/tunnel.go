package preview

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Preview Data WSS의 close code다(contracts/connector/README.md §15, §7b).
const (
	closeNormal            = websocket.CloseNormalClosure
	closeGoingAway         = websocket.CloseGoingAway
	closePolicy            = websocket.ClosePolicyViolation
	closeTooBig            = websocket.CloseMessageTooBig
	closeCredentialRevoked = 4001
	closeControlReplaced   = 4002
)

// copyBufferBytes는 tunnel이 net.Conn의 byte를 Binary frame으로 옮길 때 한 번에 읽는 크기다. frame 크기는 계약이 정하지 않는다.
const copyBufferBytes = 32 << 10

// dataConn은 인증과 Upgrade를 마친 Preview Data WSS connection 하나다.
type dataConn struct {
	ws *websocket.Conn
	// bound는 이 connection이 PreviewSession에 attach되었음이다. 그 뒤의 종료는 PreviewSession이 끝나는 경로가 맡는다.
	bound atomic.Bool
}

// closeNow는 close frame을 보내고 connection을 닫는다. 어느 goroutine에서 호출해도 안전하다(gorilla는 Close/WriteControl을
// 다른 read/write와 동시에 호출하는 것을 허용한다). 여러 번 호출해도 안전하다.
func (d *dataConn) closeNow(code int, text string) {
	_ = d.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(controlWriteTimeout))
	_ = d.ws.Close()
}

// tunnel은 attach된 Preview Data WSS를 net.Conn 하나로 보여 준다. Gateway의 HTTP/1.1 Transport가 이 net.Conn(local)을 TCP 연결처럼
// 쓰고, 두 goroutine이 반대쪽 끝(remote)과 WSS Binary frame 사이에서 byte를 옮긴다. net.Pipe는 buffer가 없어 느린 쪽이 반대쪽을 막으므로
// 별도 queue나 history가 생기지 않는다(Preview 본문은 이 tunnel에만 있다).
type tunnel struct {
	ws     *websocket.Conn
	local  net.Conn
	remote net.Conn

	writeMu sync.Mutex

	once  sync.Once
	cause tunnelCause
}

// tunnelCause는 tunnel이 닫힌 이유다. 처음 닫은 쪽의 값이 남는다.
type tunnelCause struct {
	// Reason은 End* 상수다. EndTunnelClosed이면 Connector 또는 Gateway의 HTTP transport가 연결을 끝낸 것이다.
	Reason string
	// Notify이면 SaaS가 시작한 종료라 Connector에 PREVIEW_CLOSE가 필요하다(프로토콜 위반 등).
	Notify bool
}

func newTunnel(ws *websocket.Conn, maxFrame int64) *tunnel {
	local, remote := net.Pipe()
	ws.SetReadLimit(maxFrame)
	return &tunnel{ws: ws, local: local, remote: remote}
}

// run은 tunnel이 닫힐 때까지 byte를 옮기고 닫힌 이유를 반환한다. WSS의 읽기와 쓰기는 각각 goroutine 하나가 맡는다.
func (t *tunnel) run() tunnelCause {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		t.wsToLocal()
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		t.localToWS()
	}()
	<-done
	// 한쪽이 끝났다. 다른 쪽도 끝나게 닫는다. 처음 닫은 이유가 남는다.
	t.shutdown(closeNormal, "tunnel closed", tunnelCause{Reason: EndTunnelClosed})
	<-done
	return t.cause
}

// close는 SaaS가 시작한 종료다(만료, 명시적 종료, revoke 등). 여러 번 호출해도 처음 이유만 남는다.
func (t *tunnel) close(code int, text string) {
	t.shutdown(code, text, tunnelCause{Reason: EndTunnelClosed})
}

func (t *tunnel) shutdown(code int, text string, cause tunnelCause) {
	t.once.Do(func() {
		t.cause = cause
		_ = t.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(controlWriteTimeout))
		_ = t.ws.Close()
		_ = t.local.Close()
		_ = t.remote.Close()
	})
}

// wsToLocal은 Connector가 보낸 Binary frame의 byte를 HTTP transport에 전달한다. Text frame은 protocol 위반이고 너무 큰 frame은 1009다.
func (t *tunnel) wsToLocal() {
	for {
		kind, r, err := t.ws.NextReader()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				// frame의 길이가 한도를 넘는다고 header에서 알 수 있다. gorilla가 이미 1009 close frame을 보냈다.
				t.shutdown(closeTooBig, "message too big", tunnelCause{Reason: EndProtocolViolation, Notify: true})
			} else {
				t.shutdown(closeNormal, "connection closed", tunnelCause{Reason: EndTunnelClosed})
			}
			return
		}
		if kind != websocket.BinaryMessage {
			t.shutdown(closePolicy, "protocol violation", tunnelCause{Reason: EndProtocolViolation, Notify: true})
			return
		}
		if _, err := io.Copy(t.remote, r); err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				t.shutdown(closeTooBig, "message too big", tunnelCause{Reason: EndProtocolViolation, Notify: true})
			} else {
				t.shutdown(closeNormal, "connection closed", tunnelCause{Reason: EndTunnelClosed})
			}
			return
		}
	}
}

// localToWS는 HTTP transport가 쓴 byte를 Binary frame으로 Connector에 보낸다.
func (t *tunnel) localToWS() {
	buf := make([]byte, copyBufferBytes)
	for {
		n, err := t.remote.Read(buf)
		if n > 0 {
			if werr := t.writeBinary(buf[:n]); werr != nil {
				t.shutdown(closeNormal, "connection closed", tunnelCause{Reason: EndTunnelClosed})
				return
			}
		}
		if err != nil {
			// HTTP transport가 연결을 닫았다(응답이 Connection: close이거나 정리). Connector가 TCP 연결을 닫도록 정상 종료한다.
			t.shutdown(closeNormal, "tunnel closed", tunnelCause{Reason: EndTunnelClosed})
			return
		}
	}
}

func (t *tunnel) writeBinary(p []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_ = t.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return t.ws.WriteMessage(websocket.BinaryMessage, p)
}
