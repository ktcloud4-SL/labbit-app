// Package previewtest는 계약(contracts/connector/README.md §7b)대로 동작하는 contract peer(fake Connector)다. 실제 Connector가 아니다.
//
// SaaS의 Connector Control WSS에 연결해 HELLO로 preview-v1을 선언하고, PREVIEW_OPEN을 받으면 Workspace VM 대신 쓰는 실제 TCP 서버(fake application)에
// 연결한 뒤 Preview Data WSS를 열어 PREVIEW_ATTACH를 보내고 Binary frame과 TCP byte를 양방향으로 옮긴다. 실제 SSH TCP forwarding, 관리 주소 조회,
// OpenStack은 구현하지 않는다(LBT-23, LBT-24). 이 peer가 통과해도 실제 Workspace VM 왕복이 검증된 것이 아니다(LBT-25 C3).
package previewtest

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// 계약(contracts/connector/)의 값이다. internal 구현의 상수와 일부러 분리해 wire 값 자체를 검증한다.
const (
	controlSubprotocol = "labbit.connector.v1"
	previewSubprotocol = "labbit.connector-preview.v1"
)

// Config는 Peer 구성이다.
type Config struct {
	// ControlURL과 DataURL은 SaaS의 Connector Control WSS와 Preview Data WSS의 ws:// URL이다.
	ControlURL string
	DataURL    string
	// Credential은 Control WSS의 Connector Credential이다. DataCredential이 비어 있으면 Preview Data WSS에도 같은 값을 쓴다.
	Credential     string
	DataCredential string
	// Capabilities는 HELLO가 선언하는 선택 기능이다. nil이면 capabilities field를 보내지 않는다(preview-v1을 모르는 기존 Connector).
	Capabilities []string
	// WorkspaceAddr은 Workspace VM의 application 대신 쓰는 TCP 서버(host:port)다. Behavior.WorkspaceAddr로 PREVIEW_OPEN마다 바꿀 수 있다.
	WorkspaceAddr string
	// Behavior는 PREVIEW_OPEN마다 이 peer가 무엇을 할지 정한다. nil이면 계약을 지키는 Connector처럼 동작한다.
	Behavior func(Open) Behavior
}

// Open은 peer가 받은 PREVIEW_OPEN이다.
type Open struct {
	MessageID        string
	RequestID        string
	PreviewSessionID string
	LabInstanceID    string
	Generation       int64
	TargetVMKey      string
	ProviderServerID string
	TargetPort       int
}

// Close는 peer가 받은 PREVIEW_CLOSE다.
type Close struct {
	PreviewSessionID string
	Reason           string
}

// Frame은 JSON object 하나다. Behavior가 frame을 계약에서 벗어나게 바꾸는 데 쓴다.
type Frame map[string]any

// Behavior는 PREVIEW_OPEN 하나에 대한 peer의 동작이다. zero value는 계약을 지키는 동작이다.
type Behavior struct {
	// FailOpen이 비어 있지 않으면 Preview Data WSS를 열지 않고 이 code로 PREVIEW_OPEN_RESULT FAILED를 보낸다.
	FailOpen string
	// NoAttach이면 아무것도 하지 않는다(attach timeout).
	NoAttach bool
	// Attach는 PREVIEW_ATTACH를 보내기 전에 frame을 바꾼다.
	Attach func(Frame)
	// DataCredential이 비어 있지 않으면 이 PreviewSession의 Preview Data WSS에만 다른 Credential을 쓴다.
	DataCredential string
	// WorkspaceAddr이 비어 있지 않으면 이 PreviewSession만 다른 TCP 서버에 연결한다.
	WorkspaceAddr string
	// SkipOpenResult이면 PREVIEW_OPEN_RESULT SUCCEEDED를 보내지 않는다(SUCCEEDED는 통지일 뿐이다).
	SkipOpenResult bool
}

// DataFrame은 Preview Data WSS에서 주고받은 frame 하나다. byte도 그대로 담으므로 test에서 노출 여부를 검사할 수 있다.
type DataFrame struct {
	// FromSaaS이면 SaaS → Connector, 아니면 Connector → SaaS다.
	FromSaaS bool
	Binary   bool
	Raw      []byte
}

// Peer는 SaaS에 연결된 contract peer 하나다.
type Peer struct {
	t   testing.TB
	cfg Config

	ctrl    *websocket.Conn
	writeMu sync.Mutex

	mu            sync.Mutex
	opens         []Open
	closes        []Close
	controlFrames [][]byte
	sentControl   [][]byte
	dataFrames    []DataFrame
	tunnels       map[string]*tunnel
	dataCodes     map[string]chan int
	dataDials     int

	wg   sync.WaitGroup
	done chan struct{}
	once sync.Once
}

// tunnel은 PreviewSession 하나의 Data WSS와 TCP 연결이다.
type tunnel struct {
	ws   *websocket.Conn
	tcp  net.Conn
	once sync.Once
	wmu  sync.Mutex
}

func (tn *tunnel) close() {
	tn.once.Do(func() {
		_ = tn.ws.Close()
		_ = tn.tcp.Close()
	})
}

func (tn *tunnel) send(kind int, data []byte) error {
	tn.wmu.Lock()
	defer tn.wmu.Unlock()
	_ = tn.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return tn.ws.WriteMessage(kind, data)
}

// Start는 SaaS의 Control WSS에 연결해 HELLO를 보내고 HELLO_ACK를 받은 Peer를 반환한다. test가 끝나면 정리한다.
func Start(t testing.TB, cfg Config) *Peer {
	t.Helper()
	p := &Peer{t: t, cfg: cfg, tunnels: map[string]*tunnel{}, dataCodes: map[string]chan int{}, done: make(chan struct{})}
	if p.cfg.DataCredential == "" {
		p.cfg.DataCredential = cfg.Credential
	}

	dialer := websocket.Dialer{Subprotocols: []string{controlSubprotocol}, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(cfg.ControlURL, http.Header{"Authorization": {"Bearer " + cfg.Credential}})
	if err != nil {
		t.Fatalf("Control WSS 연결 실패: %v (response %v)", err, resp)
	}
	p.ctrl = conn

	payload := map[string]any{
		"connectorVersion": "previewtest",
		"runtimeId":        "runtime-" + uuid.NewString(),
		"startedAt":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	if cfg.Capabilities != nil {
		payload["capabilities"] = cfg.Capabilities
	}
	p.sendControl(Frame{"type": "HELLO", "messageId": uuid.NewString(), "sentAt": now(), "payload": payload})

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := conn.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		t.Fatalf("HELLO_ACK 수신 실패: %v", err)
	}
	var ack Frame
	if err := json.Unmarshal(data, &ack); err != nil || ack["type"] != "HELLO_ACK" {
		t.Fatalf("HELLO_ACK가 아닌 message: %s", data)
	}
	_ = conn.SetReadDeadline(time.Time{})

	p.wg.Add(1)
	go p.controlLoop()
	t.Cleanup(p.Stop)
	return p
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (p *Peer) sendControl(frame Frame) {
	data, err := json.Marshal(frame)
	if err != nil {
		p.t.Errorf("Control frame 직렬화 실패: %v", err)
		return
	}
	p.mu.Lock()
	p.sentControl = append(p.sentControl, data)
	p.mu.Unlock()
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.ctrl.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := p.ctrl.WriteMessage(websocket.TextMessage, data); err != nil {
		select {
		case <-p.done:
		default:
			p.t.Logf("Control 전송 실패: %v", err)
		}
	}
}

// Stop은 Control과 진행 중인 Data WSS를 닫고 goroutine이 끝나기를 기다린다. 여러 번 호출해도 안전하다.
func (p *Peer) Stop() {
	p.once.Do(func() {
		close(p.done)
		p.mu.Lock()
		for _, tn := range p.tunnels {
			tn.close()
		}
		p.mu.Unlock()
		_ = p.ctrl.Close()
	})
	p.wg.Wait()
}

// Opens는 받은 PREVIEW_OPEN의 복사본이다.
func (p *Peer) Opens() []Open {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Open(nil), p.opens...)
}

// Closes는 받은 PREVIEW_CLOSE의 복사본이다.
func (p *Peer) Closes() []Close {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Close(nil), p.closes...)
}

// ControlFrames는 Control WSS에서 SaaS로부터 받은 모든 frame의 원문이다. 경로, Cookie, 본문이 Control에 실리지 않았는지 검사하는 데 쓴다.
func (p *Peer) ControlFrames() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.controlFrames))
	copy(out, p.controlFrames)
	return out
}

// SentControlFrames는 이 peer가 Control WSS로 SaaS에 보낸 모든 frame의 원문이다(HELLO, PREVIEW_OPEN_RESULT).
func (p *Peer) SentControlFrames() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.sentControl))
	copy(out, p.sentControl)
	return out
}

// DataFrames는 Preview Data WSS에서 주고받은 모든 frame이다.
func (p *Peer) DataFrames() []DataFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]DataFrame(nil), p.dataFrames...)
}

// ReceivedFromSaaS는 SaaS가 Data WSS Binary frame으로 보낸 byte 전체다. Workspace application이 받게 될 HTTP 요청 byte이며
// Labbit credential이 전달되지 않았는지 검사하는 데 쓴다.
func (p *Peer) ReceivedFromSaaS() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []byte
	for _, f := range p.dataFrames {
		if f.FromSaaS && f.Binary {
			out = append(out, f.Raw...)
		}
	}
	return out
}

// DataDials는 Preview Data WSS에 연결한 횟수다.
func (p *Peer) DataDials() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dataDials
}

// DataCloseCode는 SaaS가 previewSessionID의 Data WSS를 닫을 때 보낸 close code다. timeout 안에 닫히지 않으면 ok가 false이고,
// close frame 없이 끊겼으면 -1이다.
func (p *Peer) DataCloseCode(previewSessionID string, timeout time.Duration) (code int, ok bool) {
	p.mu.Lock()
	ch := p.dataCodes[previewSessionID]
	p.mu.Unlock()
	if ch == nil {
		return 0, false
	}
	select {
	case code = <-ch:
		// 다시 읽을 수 있게 되돌린다.
		select {
		case ch <- code:
		default:
		}
		return code, true
	case <-time.After(timeout):
		return 0, false
	}
}

// DropData는 previewSessionID의 Data WSS와 TCP 연결을 close frame 없이 끊는다(Connector와 VM 사이의 연결 손실을 흉내 낸다).
func (p *Peer) DropData(previewSessionID string) {
	p.mu.Lock()
	tn := p.tunnels[previewSessionID]
	p.mu.Unlock()
	if tn != nil {
		tn.close()
	}
}

func (p *Peer) controlLoop() {
	defer p.wg.Done()
	for {
		kind, data, err := p.ctrl.ReadMessage()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			continue
		}
		p.mu.Lock()
		p.controlFrames = append(p.controlFrames, append([]byte(nil), data...))
		p.mu.Unlock()

		var frame Frame
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		payload, _ := frame["payload"].(map[string]any)
		switch frame["type"] {
		case "PREVIEW_OPEN":
			open := Open{
				MessageID: str(frame["messageId"]), RequestID: str(frame["requestId"]),
				PreviewSessionID: str(frame["previewSessionId"]), LabInstanceID: str(frame["labInstanceId"]),
				Generation: int64(num(frame["generation"])), TargetVMKey: str(payload["targetVmKey"]),
				ProviderServerID: str(payload["providerServerId"]), TargetPort: int(num(payload["targetPort"])),
			}
			p.mu.Lock()
			p.opens = append(p.opens, open)
			p.mu.Unlock()
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.handleOpen(open)
			}()
		case "PREVIEW_CLOSE":
			c := Close{PreviewSessionID: str(frame["previewSessionId"]), Reason: str(payload["reason"])}
			p.mu.Lock()
			p.closes = append(p.closes, c)
			tn := p.tunnels[c.PreviewSessionID]
			p.mu.Unlock()
			// PREVIEW_CLOSE는 TCP forwarding channel과 Data WSS를 정리하라는 통지다.
			if tn != nil {
				tn.close()
			}
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	n, _ := v.(float64)
	return n
}

// handleOpen은 PREVIEW_OPEN 하나를 계약대로 처리한다. Workspace TCP 연결을 먼저 열고(열지 못하면 attach하지 않고 FAILED), 열리면 Data WSS를 attach한다.
func (p *Peer) handleOpen(open Open) {
	var b Behavior
	if p.cfg.Behavior != nil {
		b = p.cfg.Behavior(open)
	}
	if b.NoAttach {
		return
	}
	fail := func(code string) {
		p.sendControl(p.openResult(open, "FAILED", code))
	}
	if b.FailOpen != "" {
		fail(b.FailOpen)
		return
	}
	// 계약: Connector는 SSH 관리 port(22)를 거절한다(SaaS의 승인을 대체하지 않는 심층 방어).
	if open.TargetPort == 22 {
		fail("PORT_REJECTED")
		return
	}

	addr := p.cfg.WorkspaceAddr
	if b.WorkspaceAddr != "" {
		addr = b.WorkspaceAddr
	}
	tcp, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		code := "VM_UNREACHABLE"
		if errors.Is(err, syscall.ECONNREFUSED) {
			code = "APP_NOT_RUNNING"
		}
		fail(code)
		return
	}

	if !b.SkipOpenResult {
		p.sendControl(p.openResult(open, "SUCCEEDED", ""))
	}

	credential := p.cfg.DataCredential
	if b.DataCredential != "" {
		credential = b.DataCredential
	}
	dialer := websocket.Dialer{Subprotocols: []string{previewSubprotocol}, HandshakeTimeout: 5 * time.Second}
	p.mu.Lock()
	p.dataDials++
	p.mu.Unlock()
	ws, _, err := dialer.Dial(p.cfg.DataURL, http.Header{"Authorization": {"Bearer " + credential}})
	if err != nil {
		_ = tcp.Close()
		return
	}
	tn := &tunnel{ws: ws, tcp: tcp}
	codes := make(chan int, 1)
	p.mu.Lock()
	p.tunnels[open.PreviewSessionID] = tn
	p.dataCodes[open.PreviewSessionID] = codes
	p.mu.Unlock()

	attach := Frame{
		"type": "PREVIEW_ATTACH", "messageId": uuid.NewString(), "sentAt": now(),
		"previewSessionId": open.PreviewSessionID, "labInstanceId": open.LabInstanceID, "generation": open.Generation,
		"payload": map[string]any{
			"runtimeId": "runtime-" + uuid.NewString(), "targetVmKey": open.TargetVMKey,
			"providerServerId": open.ProviderServerID, "targetPort": open.TargetPort,
		},
	}
	if b.Attach != nil {
		b.Attach(attach)
	}
	data, _ := json.Marshal(attach)
	if err := tn.send(websocket.TextMessage, data); err != nil {
		tn.close()
		return
	}
	p.record(DataFrame{FromSaaS: false, Binary: false, Raw: data})

	// PREVIEW_ATTACHED를 기다린다. 거절(close 1008 등)되면 여기서 끝난다.
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, ackData, err := ws.ReadMessage()
	if err != nil {
		p.reportClose(open.PreviewSessionID, err)
		tn.close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})
	p.record(DataFrame{FromSaaS: true, Binary: kind == websocket.BinaryMessage, Raw: append([]byte(nil), ackData...)})

	p.pipe(open.PreviewSessionID, tn)
}

func (p *Peer) openResult(open Open, outcome, code string) Frame {
	payload := map[string]any{"outcome": outcome}
	if code != "" {
		payload["error"] = map[string]any{"code": code}
	}
	return Frame{
		"type": "PREVIEW_OPEN_RESULT", "messageId": uuid.NewString(), "sentAt": now(),
		"replyToMessageId": open.MessageID, "previewSessionId": open.PreviewSessionID,
		"labInstanceId": open.LabInstanceID, "generation": open.Generation, "payload": payload,
	}
}

// pipe는 Data WSS의 Binary frame과 TCP 연결 사이에서 byte를 옮긴다. 한쪽이 끝나면 다른 쪽도 닫는다.
func (p *Peer) pipe(id string, tn *tunnel) {
	finished := make(chan struct{}, 2)
	go func() {
		defer func() { finished <- struct{}{} }()
		for {
			kind, data, err := tn.ws.ReadMessage()
			if err != nil {
				p.reportClose(id, err)
				tn.close()
				return
			}
			p.record(DataFrame{FromSaaS: true, Binary: kind == websocket.BinaryMessage, Raw: append([]byte(nil), data...)})
			if kind != websocket.BinaryMessage {
				continue
			}
			if _, err := tn.tcp.Write(data); err != nil {
				tn.close()
				return
			}
		}
	}()
	go func() {
		defer func() { finished <- struct{}{} }()
		buf := make([]byte, 16<<10)
		for {
			n, err := tn.tcp.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				p.record(DataFrame{FromSaaS: false, Binary: true, Raw: chunk})
				if werr := tn.send(websocket.BinaryMessage, chunk); werr != nil {
					return
				}
			}
			if err != nil {
				// application이 TCP 연결을 닫았다. 남은 byte를 모두 보낸 뒤 Data WSS를 정상 종료한다(계약).
				tn.wmu.Lock()
				_ = tn.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "tcp closed"), time.Now().Add(time.Second))
				tn.wmu.Unlock()
				return
			}
		}
	}()
	<-finished
	tn.close()
	<-finished
}

func (p *Peer) record(f DataFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dataFrames = append(p.dataFrames, f)
}

// reportClose는 SaaS가 Data WSS를 닫았을 때의 close code를 기록한다.
func (p *Peer) reportClose(id string, err error) {
	code := -1
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		code = ce.Code
	}
	p.mu.Lock()
	ch := p.dataCodes[id]
	p.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- code:
	default:
	}
}
