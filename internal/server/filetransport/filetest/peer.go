package filetest

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// 계약(contracts/connector/)의 subprotocol이다. internal 구현의 상수와 일부러 분리해 wire 값 자체를 검증한다.
const (
	controlSubprotocol = "labbit.connector.v1"
	fileSubprotocol    = "labbit.connector-file.v1"
)

// Config는 Peer 구성이다.
type Config struct {
	// ControlURL과 DataURL은 SaaS의 Connector Control WSS와 File Data WSS의 ws:// URL이다.
	ControlURL string
	DataURL    string
	// Credential은 Control WSS의 Connector Credential이다. DataCredential이 비어 있으면 File Data WSS에도 같은 값을 쓴다.
	Credential     string
	DataCredential string
	// Capabilities는 HELLO가 선언하는 선택 기능이다. nil이면 capabilities field를 보내지 않는다(file-v1을 모르는 기존 Connector).
	Capabilities []string
	// FS는 Workspace VM 대신 쓰는 메모리 파일 시스템이다. nil이면 빈 FS다.
	FS *FS
	// Behavior는 FILE_OPEN마다 이 peer가 무엇을 할지 정한다. nil이면 계약을 지키는 Connector처럼 동작한다.
	Behavior func(Open) Behavior
}

// Open은 peer가 받은 FILE_OPEN이다.
type Open struct {
	MessageID        string
	RequestID        string
	FileRequestID    string
	LabInstanceID    string
	Generation       int64
	Operation        string
	TargetVMKey      string
	ProviderServerID string
}

// Close는 peer가 받은 FILE_CLOSE다.
type Close struct {
	FileRequestID string
	Reason        string
}

// Frame은 JSON object 하나다. Behavior가 frame을 계약에서 벗어나게 바꾸는 데 쓴다.
type Frame map[string]any

// Behavior는 FILE_OPEN 하나에 대한 peer의 동작이다. zero value는 계약을 지키는 동작이다.
type Behavior struct {
	// FailOpen이 비어 있지 않으면 File Data WSS를 열지 않고 이 code로 FILE_OPEN_RESULT FAILED를 보낸다.
	FailOpen string
	// NoAttach이면 아무것도 하지 않는다(attach timeout).
	NoAttach bool
	// Attach는 FILE_DATA_ATTACH를 보내기 전에 frame을 바꾼다.
	Attach func(Frame)
	// Result는 결과 frame(JSON)을 보내기 전에 바꾼다.
	Result func(Frame)
	// Body는 Read 결과의 Binary frame 본문을 바꾼다.
	Body func([]byte) []byte
	// Drop이면 요청 frame을 읽은 뒤 결과 없이 연결을 닫는다.
	Drop bool
	// Stall이 nil이 아니면 요청 frame을 읽은 뒤 이 channel이 닫힐 때까지(또는 요청이 취소될 때까지) 기다린다.
	Stall <-chan struct{}
	// DataCredential이 비어 있지 않으면 이 요청의 File Data WSS에만 다른 Credential을 쓴다(다른 Connector를 흉내 낸다).
	DataCredential string
}

// DataFrame은 File Data WSS에서 주고받은 frame 하나다. 본문 byte도 그대로 담으므로 test에서 노출 여부를 검사할 수 있다.
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
	fs  *FS

	ctrl    *websocket.Conn
	writeMu sync.Mutex

	mu            sync.Mutex
	opens         []Open
	closes        []Close
	controlFrames [][]byte
	dataFrames    []DataFrame
	dialFailures  int
	inflight      map[string]context.CancelFunc
	active        int

	wg   sync.WaitGroup
	done chan struct{}
	once sync.Once
}

// Start는 SaaS의 Control WSS에 연결해 HELLO를 보내고 HELLO_ACK를 받은 Peer를 반환한다. test가 끝나면 정리한다.
func Start(t testing.TB, cfg Config) *Peer {
	t.Helper()
	p := &Peer{t: t, cfg: cfg, fs: cfg.FS, inflight: map[string]context.CancelFunc{}, done: make(chan struct{})}
	if p.fs == nil {
		p.fs = NewFS()
	}
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
		"connectorVersion": "filetest",
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

// Stop은 Control과 진행 중인 Data WSS를 닫고 goroutine이 끝나기를 기다린다. 여러 번 호출해도 안전하다.
func (p *Peer) Stop() {
	p.once.Do(func() {
		close(p.done)
		p.mu.Lock()
		for _, cancel := range p.inflight {
			cancel()
		}
		p.mu.Unlock()
		_ = p.ctrl.Close()
	})
	p.wg.Wait()
}

// Opens는 받은 FILE_OPEN의 복사본이다.
func (p *Peer) Opens() []Open {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Open(nil), p.opens...)
}

// Closes는 받은 FILE_CLOSE의 복사본이다.
func (p *Peer) Closes() []Close {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Close(nil), p.closes...)
}

// ControlFrames는 Control WSS에서 SaaS로부터 받은 모든 frame의 원문이다. 경로나 본문이 Control에 실리지 않았는지 검사하는 데 쓴다.
func (p *Peer) ControlFrames() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.controlFrames))
	copy(out, p.controlFrames)
	return out
}

// DataFrames는 File Data WSS에서 주고받은 모든 frame이다.
func (p *Peer) DataFrames() []DataFrame {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]DataFrame(nil), p.dataFrames...)
}

// Active는 지금 FILE_OPEN을 처리 중인 goroutine 수다.
func (p *Peer) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// SendControl은 Control WSS로 임의의 JSON frame을 보낸다. 잘못된 correlation의 FILE_OPEN_RESULT 같은 계약 위반을 흉내 내는 데 쓴다.
func (p *Peer) SendControl(frame Frame) {
	p.t.Helper()
	p.sendControl(frame)
}

func (p *Peer) sendControl(frame Frame) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	data, err := json.Marshal(frame)
	if err != nil {
		p.t.Errorf("frame 직렬화 실패: %v", err)
		return
	}
	_ = p.ctrl.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = p.ctrl.WriteMessage(websocket.TextMessage, data)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

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

		var frame struct {
			Type          string `json:"type"`
			MessageID     string `json:"messageId"`
			RequestID     string `json:"requestId"`
			FileRequestID string `json:"fileRequestId"`
			LabInstanceID string `json:"labInstanceId"`
			Generation    int64  `json:"generation"`
			Payload       struct {
				Operation        string `json:"operation"`
				TargetVMKey      string `json:"targetVmKey"`
				ProviderServerID string `json:"providerServerId"`
				Reason           string `json:"reason"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		switch frame.Type {
		case "FILE_OPEN":
			p.startOpen(Open{
				MessageID: frame.MessageID, RequestID: frame.RequestID, FileRequestID: frame.FileRequestID,
				LabInstanceID: frame.LabInstanceID, Generation: frame.Generation, Operation: frame.Payload.Operation,
				TargetVMKey: frame.Payload.TargetVMKey, ProviderServerID: frame.Payload.ProviderServerID,
			})
		case "FILE_CLOSE":
			p.mu.Lock()
			p.closes = append(p.closes, Close{FileRequestID: frame.FileRequestID, Reason: frame.Payload.Reason})
			cancel := p.inflight[frame.FileRequestID]
			p.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (p *Peer) startOpen(open Open) {
	var beh Behavior
	if p.cfg.Behavior != nil {
		beh = p.cfg.Behavior(open)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.opens = append(p.opens, open)
	p.inflight[open.FileRequestID] = cancel
	p.active++
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			cancel()
			p.mu.Lock()
			p.active--
			p.mu.Unlock()
		}()
		p.serveOpen(ctx, open, beh)
	}()
}

func (p *Peer) recordData(fromSaaS, binary bool, raw []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dataFrames = append(p.dataFrames, DataFrame{FromSaaS: fromSaaS, Binary: binary, Raw: append([]byte(nil), raw...)})
}

func (p *Peer) frame(typ string, open Open, replyTo string, payload map[string]any) Frame {
	f := Frame{
		"type": typ, "messageId": uuid.NewString(), "sentAt": now(),
		"fileRequestId": open.FileRequestID, "labInstanceId": open.LabInstanceID, "generation": open.Generation,
		"payload": payload,
	}
	if replyTo != "" {
		f["replyToMessageId"] = replyTo
	}
	return f
}

func failedPayload(code failure) map[string]any {
	return map[string]any{"outcome": "FAILED", "error": map[string]any{"code": string(code), "message": "filetest failure"}}
}

func (p *Peer) serveOpen(ctx context.Context, open Open, beh Behavior) {
	if beh.FailOpen != "" {
		p.sendControl(p.frame("FILE_OPEN_RESULT", open, open.MessageID, map[string]any{
			"outcome": "FAILED", "error": map[string]any{"code": beh.FailOpen, "message": "filetest open failure"},
		}))
		return
	}
	if beh.NoAttach {
		<-ctx.Done()
		return
	}

	credential := p.cfg.DataCredential
	if beh.DataCredential != "" {
		credential = beh.DataCredential
	}
	dialer := websocket.Dialer{Subprotocols: []string{fileSubprotocol}, HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.DialContext(ctx, p.cfg.DataURL, http.Header{"Authorization": {"Bearer " + credential}})
	if err != nil {
		p.mu.Lock()
		p.dialFailures++
		p.mu.Unlock()
		return
	}
	defer ws.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = ws.Close()
		case <-stop:
		}
	}()

	write := func(kind int, data []byte) bool {
		p.recordData(false, kind == websocket.BinaryMessage, data)
		_ = ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return ws.WriteMessage(kind, data) == nil
	}
	read := func() (int, []byte, bool) {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return 0, nil, false
		}
		p.recordData(true, kind == websocket.BinaryMessage, data)
		return kind, data, true
	}
	send := func(f Frame) bool {
		data, err := json.Marshal(f)
		if err != nil {
			p.t.Errorf("frame 직렬화 실패: %v", err)
			return false
		}
		return write(websocket.TextMessage, data)
	}

	attach := p.frame("FILE_DATA_ATTACH", open, "", map[string]any{
		"runtimeId": "runtime-filetest", "targetVmKey": open.TargetVMKey, "providerServerId": open.ProviderServerID,
	})
	attachID := attach["messageId"].(string)
	if beh.Attach != nil {
		beh.Attach(attach)
	}
	if !send(attach) {
		return
	}
	kind, data, ok := read()
	if !ok || kind != websocket.TextMessage {
		return // SaaS가 attach를 거절하고 연결을 닫았다.
	}
	var attached struct {
		Type             string `json:"type"`
		ReplyToMessageID string `json:"replyToMessageId"`
	}
	if json.Unmarshal(data, &attached) != nil || attached.Type != "FILE_DATA_ATTACHED" {
		return
	}
	if attached.ReplyToMessageID != attachID && beh.Attach == nil {
		p.t.Errorf("FILE_DATA_ATTACHED의 replyToMessageId가 ATTACH와 다름")
	}

	kind, data, ok = read()
	if !ok || kind != websocket.TextMessage {
		return
	}
	var request struct {
		Type      string         `json:"type"`
		MessageID string         `json:"messageId"`
		Payload   map[string]any `json:"payload"`
	}
	if json.Unmarshal(data, &request) != nil {
		return
	}
	if beh.Drop {
		return
	}
	if beh.Stall != nil {
		select {
		case <-beh.Stall:
		case <-ctx.Done():
			return
		}
	}

	var (
		resultType string
		payload    map[string]any
		body       []byte
		hasBody    bool
	)
	switch request.Type {
	case "FILE_TREE":
		resultType = "FILE_TREE_RESULT"
		path, _ := request.Payload["path"].(string)
		entries, fail := p.fs.tree(path)
		if fail != "" {
			payload = failedPayload(fail)
		} else {
			payload = map[string]any{"outcome": "SUCCEEDED", "entries": entries}
		}

	case "FILE_READ":
		resultType = "FILE_READ_RESULT"
		path, _ := request.Payload["path"].(string)
		maxBytes, _ := request.Payload["maxBytes"].(float64)
		content, revision, fail := p.fs.read(path, int64(maxBytes))
		if fail != "" {
			payload = failedPayload(fail)
		} else {
			if beh.Body != nil {
				content = beh.Body(content)
			}
			payload = map[string]any{"outcome": "SUCCEEDED", "revision": revision, "size": len(content)}
			body, hasBody = content, true
		}

	case "FILE_SAVE":
		resultType = "FILE_SAVE_RESULT"
		path, _ := request.Payload["path"].(string)
		expected, _ := request.Payload["expectedRevision"].(string)
		size, _ := request.Payload["size"].(float64)
		kind, content, ok := read()
		if !ok || kind != websocket.BinaryMessage || float64(len(content)) != size {
			return
		}
		revision, fail := p.fs.save(path, expected, content)
		if fail != "" {
			payload = failedPayload(fail)
		} else {
			payload = map[string]any{"outcome": "SUCCEEDED", "revision": revision}
		}

	default:
		return
	}

	result := p.frame(resultType, open, request.MessageID, payload)
	if beh.Result != nil {
		beh.Result(result)
	}
	if !send(result) {
		return
	}
	if hasBody {
		if !write(websocket.BinaryMessage, body) {
			return
		}
	}
	// SaaS가 정상 종료할 때까지 읽는다.
	for {
		if _, _, ok := read(); !ok {
			return
		}
	}
}
