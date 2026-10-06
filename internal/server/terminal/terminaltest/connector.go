package terminaltest

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	controlSubprotocol = "labbit.connector.v1"
	dataSubprotocol    = "labbit.connector-terminal.v1"
)

// OpenMode는 Connector가 TERMINAL_OPEN을 받았을 때의 동작이다.
type OpenMode int

const (
	// OpenSucceeds는 Terminal Data WSS를 붙이고 TERMINAL_DATA_ATTACHED를 받은 뒤 OPEN_RESULT SUCCEEDED를 보낸다(계약의 정상 순서).
	OpenSucceeds OpenMode = iota
	// OpenFails는 OPEN_RESULT FAILED를 보낸다.
	OpenFails
	// OpenIgnored는 아무 응답도 하지 않는다.
	OpenIgnored
	// OpenSucceedsWithoutData는 Terminal Data WSS를 붙이지 않고 OPEN_RESULT SUCCEEDED만 보낸다.
	OpenSucceedsWithoutData
	// OpenWrongCorrelation은 Data WSS는 붙이지만 OPEN_RESULT의 generation이 틀리다(어느 pending에도 연결되지 않는다).
	OpenWrongCorrelation
)

// Open은 Connector가 받은 TERMINAL_OPEN이다.
type Open struct {
	MessageID         string
	RequestID         string
	TerminalSessionID string
	LabInstanceID     string
	Generation        int64
	TargetVMKey       string
	ProviderServerID  string
	// Cols와 Rows는 payload의 JSON 원문이다.
	Cols, Rows string
	// Raw는 message 전체다. Connector에 전달되면 안 되는 field가 없는지 확인할 때 쓴다.
	Raw string
	// Traceparent와 Tracestate는 TERMINAL_OPEN이 실은 W3C Trace Context다. 없으면 빈 문자열이다.
	// Connector(propagation-only)는 이 Context를 보존해 TERMINAL_OPEN_RESULT로 돌려준다(contracts/connector/README.md §9).
	Traceparent, Tracestate string
}

// Close는 Connector가 받은 TERMINAL_CLOSE다.
type Close struct {
	MessageID         string
	RequestID         string
	OperationID       string
	TerminalSessionID string
	LabInstanceID     string
	Generation        int64
	Reason            string
	// Traceparent와 Tracestate는 TERMINAL_CLOSE가 실은 W3C Trace Context이며 없으면 빈 문자열이다.
	Traceparent, Tracestate string
}

// Resize는 Terminal Data WSS로 받은 TERMINAL_DATA_RESIZE다. 값은 JSON 원문이다.
// Traceparent와 Tracestate는 그 message가 실은 W3C Trace Context이며 없으면 빈 문자열이다.
type Resize struct{ Cols, Rows, Traceparent, Tracestate string }

type dataLink struct {
	ws      *websocket.Conn
	session string
	open    Open

	writeMu sync.Mutex

	// done은 이 connection의 read loop가 끝나면 닫힌다. err는 done이 닫힌 뒤에만 읽는다(close frame이면 *websocket.CloseError).
	done chan struct{}
	err  error
}

func (l *dataLink) write(kind int, data []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	_ = l.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return l.ws.WriteMessage(kind, data)
}

// Connector는 고객 환경 Connector를 흉내 내는 contract peer다. 실제 WebSocket으로 Control WSS와 Terminal Data WSS에 연결하고
// contracts/connector/의 message를 주고받는다. 이 helper에는 실제 OpenStack/SSH/PTY가 없다.
// Connector→실제 Workspace VM SSH/PTY acceptance는 LBT-20, Browser/SaaS까지 포함한 full-stack 공동 통합은 LBT-22(C2)에서 검증한다.
type Connector struct {
	t          *testing.T
	base       string
	credential string

	// Mode와 EndOnClose는 Start 전에 설정한다.
	Mode OpenMode
	// EndOnClose이면 TERMINAL_CLOSE를 받았을 때 PTY를 끝내고 TERMINAL_ENDED를 돌려준다.
	EndOnClose bool

	ctrl      *websocket.Conn
	ctrlWrite sync.Mutex
	// ctrlDone은 Control connection의 read loop가 끝나면 닫힌다. ctrlErr는 ctrlDone이 닫힌 뒤에만 읽는다.
	ctrlDone chan struct{}
	ctrlErr  error

	mu         sync.Mutex
	opens      []Open
	closes     []Close
	links      map[string]*dataLink
	inputs     map[string][][]byte
	resizes    map[string][]Resize
	dataCloses map[string][]string
	attaches   map[string][]map[string]any // TERMINAL_DATA_ATTACHED payload
	dataDials  int

	// resultTrace는 TERMINAL_OPEN_RESULT가 싣는 Trace Context의 동작이다. 기본은 받은 Context를 그대로 돌려준다.
	resultTrace resultTraceMode
	// resultTraceparent는 resultTraceReplaced에서 돌려줄 traceparent다.
	resultTraceparent string
}

type resultTraceMode int

const (
	resultTraceEchoed resultTraceMode = iota
	resultTraceOmitted
	resultTraceReplaced
)

// NewConnector는 baseURL(http://host:port)의 SaaS에 연결할 Connector를 만든다. Start로 연결한다.
func NewConnector(t *testing.T, baseURL, credential string) *Connector {
	t.Helper()
	return &Connector{
		t: t, base: "ws" + strings.TrimPrefix(baseURL, "http"), credential: credential, EndOnClose: true,
		ctrlDone: make(chan struct{}),
		links:    map[string]*dataLink{}, inputs: map[string][][]byte{}, resizes: map[string][]Resize{},
		dataCloses: map[string]([]string){}, attaches: map[string][]map[string]any{},
	}
}

func (c *Connector) bearer() http.Header {
	return http.Header{"Authorization": []string{"Bearer " + c.credential}}
}

// Start는 Control WSS에 연결해 HELLO/HELLO_ACK를 마치고 message loop를 시작한다.
func (c *Connector) Start() {
	c.t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{controlSubprotocol}, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial(c.base+"/connector/v1/control", c.bearer())
	if err != nil {
		c.t.Fatalf("Control WSS 연결 실패: %v", err)
	}
	c.ctrl = ws
	c.t.Cleanup(c.Stop)

	c.writeControl(map[string]any{
		"type": "HELLO", "messageId": uuid.NewString(), "sentAt": now(),
		"payload": map[string]any{"connectorVersion": "terminaltest", "runtimeId": "runtime-terminaltest", "startedAt": now()},
	})
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack map[string]any
	if err := ws.ReadJSON(&ack); err != nil || ack["type"] != "HELLO_ACK" {
		c.t.Fatalf("HELLO_ACK = %v, %v", ack, err)
	}
	_ = ws.SetReadDeadline(time.Time{})
	go c.controlLoop()
}

// Stop은 모든 연결을 닫는다.
func (c *Connector) Stop() {
	if c.ctrl != nil {
		_ = c.ctrl.Close()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, link := range c.links {
		_ = link.ws.Close()
	}
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (c *Connector) writeControl(msg map[string]any) {
	data, err := json.Marshal(msg)
	if err != nil {
		c.t.Errorf("message 직렬화 실패: %v", err)
		return
	}
	c.ctrlWrite.Lock()
	defer c.ctrlWrite.Unlock()
	_ = c.ctrl.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.ctrl.WriteMessage(websocket.TextMessage, data); err != nil {
		// test가 끝나며 연결을 닫는 중일 수 있다.
		return
	}
}

// controlLoop는 Control WSS의 message를 처리한다. HELLO_ACK 이후 SaaS가 보내는 것은 TERMINAL_OPEN/TERMINAL_CLOSE(와 이 test가 쓰지 않는
// Operation 계열)이다.
func (c *Connector) controlLoop() {
	for {
		_, data, err := c.ctrl.ReadMessage()
		if err != nil {
			c.ctrlErr = err
			close(c.ctrlDone)
			return
		}
		var msg struct {
			Type              string          `json:"type"`
			MessageID         string          `json:"messageId"`
			RequestID         string          `json:"requestId"`
			OperationID       string          `json:"operationId"`
			TerminalSessionID string          `json:"terminalSessionId"`
			LabInstanceID     string          `json:"labInstanceId"`
			Generation        int64           `json:"generation"`
			Payload           json.RawMessage `json:"payload"`
			Traceparent       string          `json:"traceparent"`
			Tracestate        string          `json:"tracestate"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "TERMINAL_OPEN":
			var p struct {
				TargetVMKey      string          `json:"targetVmKey"`
				ProviderServerID string          `json:"providerServerId"`
				Cols             json.RawMessage `json:"cols"`
				Rows             json.RawMessage `json:"rows"`
			}
			_ = json.Unmarshal(msg.Payload, &p)
			open := Open{
				MessageID: msg.MessageID, RequestID: msg.RequestID, TerminalSessionID: msg.TerminalSessionID,
				LabInstanceID: msg.LabInstanceID, Generation: msg.Generation, TargetVMKey: p.TargetVMKey,
				ProviderServerID: p.ProviderServerID, Cols: string(p.Cols), Rows: string(p.Rows), Raw: string(data),
				Traceparent: msg.Traceparent, Tracestate: msg.Tracestate,
			}
			c.mu.Lock()
			c.opens = append(c.opens, open)
			mode := c.Mode
			c.mu.Unlock()
			go c.handleOpen(open, mode)
		case "TERMINAL_CLOSE":
			var p struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(msg.Payload, &p)
			closeMsg := Close{
				MessageID: msg.MessageID, RequestID: msg.RequestID, OperationID: msg.OperationID, TerminalSessionID: msg.TerminalSessionID,
				LabInstanceID: msg.LabInstanceID, Generation: msg.Generation, Reason: p.Reason,
				Traceparent: msg.Traceparent, Tracestate: msg.Tracestate,
			}
			c.mu.Lock()
			c.closes = append(c.closes, closeMsg)
			endOnClose := c.EndOnClose
			c.mu.Unlock()
			if endOnClose {
				go c.endViaControl(closeMsg.TerminalSessionID, closeMsg.LabInstanceID, closeMsg.Generation, "SESSION_CLOSED", nil, nil)
			}
		}
	}
}

func (c *Connector) handleOpen(open Open, mode OpenMode) {
	result := func(outcome string, generation int64, errCode string) {
		payload := map[string]any{"outcome": outcome}
		if errCode != "" {
			payload["error"] = map[string]any{"code": errCode}
		}
		msg := map[string]any{
			"type": "TERMINAL_OPEN_RESULT", "messageId": uuid.NewString(), "sentAt": now(), "replyToMessageId": open.MessageID,
			"terminalSessionId": open.TerminalSessionID, "labInstanceId": open.LabInstanceID, "generation": generation,
			"payload": payload,
		}
		// propagation-only Connector는 받은 command의 Trace Context를 새 Span을 가장하지 않고 그대로 돌려준다.
		c.mu.Lock()
		traceMode, replaced := c.resultTrace, c.resultTraceparent
		c.mu.Unlock()
		switch traceMode {
		case resultTraceEchoed:
			if open.Traceparent != "" {
				msg["traceparent"] = open.Traceparent
			}
			if open.Tracestate != "" {
				msg["tracestate"] = open.Tracestate
			}
		case resultTraceReplaced:
			msg["traceparent"] = replaced
		}
		c.writeControl(msg)
	}
	switch mode {
	case OpenSucceeds:
		if c.attachData(open) {
			result("SUCCEEDED", open.Generation, "")
		}
	case OpenFails:
		result("FAILED", open.Generation, "SSH_UNREACHABLE")
	case OpenIgnored:
	case OpenSucceedsWithoutData:
		result("SUCCEEDED", open.Generation, "")
	case OpenWrongCorrelation:
		if c.attachData(open) {
			result("SUCCEEDED", open.Generation+1, "")
		}
	}
}

// attachData는 TerminalSession의 Terminal Data WSS를 붙인다. TERMINAL_DATA_ATTACH를 보내고 TERMINAL_DATA_ATTACHED까지 받는다.
func (c *Connector) attachData(open Open) bool {
	c.t.Helper()
	c.mu.Lock()
	c.dataDials++
	c.mu.Unlock()
	dialer := websocket.Dialer{Subprotocols: []string{dataSubprotocol}, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial(c.base+"/connector/v1/terminal-data", c.bearer())
	if err != nil {
		c.t.Errorf("Terminal Data WSS 연결 실패: %v", err)
		return false
	}
	link := &dataLink{ws: ws, session: open.TerminalSessionID, open: open, done: make(chan struct{})}
	if err := link.write(websocket.TextMessage, mustJSON(map[string]any{
		"type": "TERMINAL_DATA_ATTACH", "messageId": uuid.NewString(), "sentAt": now(),
		"terminalSessionId": open.TerminalSessionID, "labInstanceId": open.LabInstanceID, "generation": open.Generation,
		"payload": map[string]any{"runtimeId": "runtime-terminaltest"},
	})); err != nil {
		c.t.Errorf("TERMINAL_DATA_ATTACH 전송 실패: %v", err)
		return false
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var attached struct {
		Type    string         `json:"type"`
		Payload map[string]any `json:"payload"`
	}
	if err := ws.ReadJSON(&attached); err != nil || attached.Type != "TERMINAL_DATA_ATTACHED" {
		c.t.Errorf("TERMINAL_DATA_ATTACHED = %+v, %v", attached, err)
		_ = ws.Close()
		return false
	}
	_ = ws.SetReadDeadline(time.Time{})

	c.mu.Lock()
	if old := c.links[open.TerminalSessionID]; old != nil {
		_ = old.ws.Close()
	}
	c.links[open.TerminalSessionID] = link
	c.attaches[open.TerminalSessionID] = append(c.attaches[open.TerminalSessionID], attached.Payload)
	c.mu.Unlock()
	go c.dataLoop(link)
	return true
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// dataLoop는 Terminal Data WSS에서 받은 PTY INPUT(Binary)과 control(JSON)을 기록한다.
func (c *Connector) dataLoop(link *dataLink) {
	for {
		kind, data, err := link.ws.ReadMessage()
		if err != nil {
			link.err = err
			close(link.done)
			return
		}
		c.mu.Lock()
		if kind == websocket.BinaryMessage {
			c.inputs[link.session] = append(c.inputs[link.session], data)
			c.mu.Unlock()
			continue
		}
		var msg struct {
			Type        string `json:"type"`
			Traceparent string `json:"traceparent"`
			Tracestate  string `json:"tracestate"`
			Payload     struct {
				Cols   json.RawMessage `json:"cols"`
				Rows   json.RawMessage `json:"rows"`
				Reason string          `json:"reason"`
			} `json:"payload"`
		}
		_ = json.Unmarshal(data, &msg)
		switch msg.Type {
		case "TERMINAL_DATA_RESIZE":
			c.resizes[link.session] = append(c.resizes[link.session], Resize{
				Cols: string(msg.Payload.Cols), Rows: string(msg.Payload.Rows), Traceparent: msg.Traceparent, Tracestate: msg.Tracestate,
			})
		case "TERMINAL_DATA_CLOSE":
			c.dataCloses[link.session] = append(c.dataCloses[link.session], msg.Payload.Reason)
		}
		c.mu.Unlock()
	}
}

func (c *Connector) link(session string) *dataLink {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	link := c.links[session]
	if link == nil {
		c.t.Fatalf("TerminalSession %s의 Terminal Data WSS가 없음", session)
	}
	return link
}

// Output은 PTY OUTPUT raw bytes를 Terminal Data WSS로 보낸다.
func (c *Connector) Output(session string, data []byte) {
	c.t.Helper()
	if err := c.link(session).write(websocket.BinaryMessage, data); err != nil {
		c.t.Fatalf("OUTPUT 전송 실패: %v", err)
	}
}

// TryOutput은 Output과 같지만 전송 실패를 test 실패로 만들지 않고 돌려준다. 서버가 종료한 connection으로 더 보내 보는 용도다.
func (c *Connector) TryOutput(session string, data []byte) error {
	c.t.Helper()
	return c.link(session).write(websocket.BinaryMessage, data)
}

// Heartbeat는 Control WSS로 유효한 HEARTBEAT를 보낸다. 서버는 이때 Credential이 아직 유효한지 저장소에서 확인한다.
func (c *Connector) Heartbeat() {
	c.t.Helper()
	c.writeControl(map[string]any{
		"type": "HEARTBEAT", "messageId": uuid.NewString(), "sentAt": now(),
		"payload": map[string]any{"observedAt": now()},
	})
}

// ControlClosed는 Control connection이 끝날 때까지 기다려 close code를 반환한다. 시간 안에 끝나지 않으면 ok는 false다.
// close frame이 아닌 오류로 끝났으면 code는 0이다.
func (c *Connector) ControlClosed(timeout time.Duration) (code int, ok bool) {
	c.t.Helper()
	select {
	case <-c.ctrlDone:
		return closeCode(c.ctrlErr), true
	case <-time.After(timeout):
		return 0, false
	}
}

// DataClosed는 session의 현재 Terminal Data WSS connection이 끝날 때까지 기다려 close code를 반환한다. 시간 안에 끝나지 않으면 ok는 false다.
func (c *Connector) DataClosed(session string, timeout time.Duration) (code int, ok bool) {
	c.t.Helper()
	link := c.link(session)
	select {
	case <-link.done:
		return closeCode(link.err), true
	case <-time.After(timeout):
		return 0, false
	}
}

func closeCode(err error) int {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code
	}
	return 0
}

// DropData는 Terminal Data WSS의 transport만 끊는다(PTY는 살아 있다). close frame 없이 TCP를 닫는다.
func (c *Connector) DropData(session string) {
	c.t.Helper()
	_ = c.link(session).ws.Close()
}

// ReattachData는 같은 TerminalSession에 Terminal Data WSS를 다시 붙인다(transport reconnect).
func (c *Connector) ReattachData(session string) bool {
	c.t.Helper()
	return c.attachData(c.link(session).open)
}

// EndData는 PTY가 종료되었음을 Terminal Data WSS로 알린다.
func (c *Connector) EndData(session, reason string, exitCode *int) {
	c.t.Helper()
	link := c.link(session)
	payload := map[string]any{"reason": reason}
	if exitCode != nil {
		payload["exitCode"] = *exitCode
	}
	if err := link.write(websocket.TextMessage, mustJSON(map[string]any{
		"type": "TERMINAL_DATA_ENDED", "messageId": uuid.NewString(), "sentAt": now(),
		"terminalSessionId": link.open.TerminalSessionID, "labInstanceId": link.open.LabInstanceID, "generation": link.open.Generation,
		"payload": payload,
	})); err != nil {
		c.t.Fatalf("TERMINAL_DATA_ENDED 전송 실패: %v", err)
	}
}

// EndControl은 PTY가 종료되었음을 Control WSS의 TERMINAL_ENDED로 알린다. 주장하는 correlation을 호출자가 정한다.
func (c *Connector) EndControl(session, labInstanceID string, generation int64, reason string, exitCode *int) {
	c.t.Helper()
	c.endViaControl(session, labInstanceID, generation, reason, exitCode, nil)
}

// EndControlWithTrace는 EndControl이며 TERMINAL_ENDED에 trace 원문(traceparent, tracestate)을 그대로 싣는다. 잘못된 값도 넣을 수 있다.
func (c *Connector) EndControlWithTrace(session, labInstanceID string, generation int64, reason string, trace map[string]any) {
	c.t.Helper()
	c.endViaControl(session, labInstanceID, generation, reason, nil, trace)
}

func (c *Connector) endViaControl(session, labInstanceID string, generation int64, reason string, exitCode *int, trace map[string]any) {
	payload := map[string]any{"reason": reason}
	if exitCode != nil {
		payload["exitCode"] = *exitCode
	}
	msg := map[string]any{
		"type": "TERMINAL_ENDED", "messageId": uuid.NewString(), "sentAt": now(),
		"terminalSessionId": session, "labInstanceId": labInstanceID, "generation": generation,
		"payload": payload,
	}
	for k, v := range trace {
		msg[k] = v
	}
	c.writeControl(msg)
}

// Opens는 받은 TERMINAL_OPEN이다.
func (c *Connector) Opens() []Open {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Open(nil), c.opens...)
}

// Closes는 받은 TERMINAL_CLOSE다.
func (c *Connector) Closes() []Close {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Close(nil), c.closes...)
}

// Inputs는 session의 Terminal Data WSS로 받은 PTY INPUT(Binary)을 받은 순서대로 이어 붙인 것이다.
func (c *Connector) Inputs(session string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []byte
	for _, chunk := range c.inputs[session] {
		all = append(all, chunk...)
	}
	return all
}

// Resizes는 session의 Terminal Data WSS로 받은 TERMINAL_DATA_RESIZE다.
func (c *Connector) Resizes(session string) []Resize {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Resize(nil), c.resizes[session]...)
}

// DataCloses는 session의 Terminal Data WSS로 받은 TERMINAL_DATA_CLOSE의 reason이다.
func (c *Connector) DataCloses(session string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.dataCloses[session]...)
}

// Attached는 session의 Terminal Data WSS attach마다 받은 TERMINAL_DATA_ATTACHED payload다.
func (c *Connector) Attached(session string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.attaches[session]...)
}

// DataDials는 Terminal Data WSS를 연 횟수다. 같은 TerminalSession의 Browser 재접속이 새 data channel을 만들지 않음을 확인할 때 쓴다.
func (c *Connector) DataDials() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dataDials
}

// EchoResultTrace는 이후 TERMINAL_OPEN_RESULT에 받은 Context를 그대로 싣는다(기본값).
func (c *Connector) EchoResultTrace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resultTrace, c.resultTraceparent = resultTraceEchoed, ""
}

// OmitResultTrace는 이후 TERMINAL_OPEN_RESULT에 Trace Context를 싣지 않는다. Trace를 돌려주지 않아도 업무 결과가 실패하지 않는지 확인하는 데 쓴다.
func (c *Connector) OmitResultTrace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resultTrace = resultTraceOmitted
}

// ReplaceResultTrace는 이후 TERMINAL_OPEN_RESULT에 받은 Context 대신 traceparent를 싣는다(잘못된 값도 가능).
func (c *Connector) ReplaceResultTrace(traceparent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resultTrace, c.resultTraceparent = resultTraceReplaced, traceparent
}

// SetMode는 이후 TERMINAL_OPEN의 동작을 바꾼다.
func (c *Connector) SetMode(mode OpenMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Mode = mode
}
