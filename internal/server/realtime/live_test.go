package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

type fakeLiveControl struct {
	mu           sync.Mutex
	authorized   map[string]realtime.LiveGrant
	authorizeErr error
	endedSource  []string
	endedReason  []string
}

func newFakeLiveControl() *fakeLiveControl {
	return &fakeLiveControl{
		authorized: make(map[string]realtime.LiveGrant),
	}
}

func (f *fakeLiveControl) AuthorizeLiveSubscribe(_ context.Context, session realtime.SessionToken, liveSessionID string) (realtime.LiveGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authorizeErr != nil {
		return realtime.LiveGrant{}, f.authorizeErr
	}
	grant, ok := f.authorized[liveSessionID]
	if !ok {
		return realtime.LiveGrant{}, realtime.ErrSessionNotFound
	}
	return grant, nil
}

func (f *fakeLiveControl) SourceTerminalEnded(_ context.Context, sourceTerminalSessionID string, end realtime.End) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endedSource = append(f.endedSource, sourceTerminalSessionID)
	f.endedReason = append(f.endedReason, end.Reason)
	return nil
}

type liveClient struct {
	conn      *websocket.Conn
	msgCh     chan message
	errCh     chan error
	closeOnce sync.Once
}

type message struct {
	kind int
	data []byte
}

func dialLive(t *testing.T, e *env, cookie, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	dialer := websocket.Dialer{
		Subprotocols:     []string{realtime.LiveSubprotocol},
		HandshakeTimeout: 2 * time.Second,
	}
	header := http.Header{}
	if cookie != "" {
		header.Set("Cookie", realtime.SessionCookieName+"="+cookie)
	}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return dialer.Dial(e.wsURL(realtime.LivePath), header)
}

func connectLive(t *testing.T, e *env, liveSessionID, cookie string) *liveClient {
	t.Helper()
	ws, resp, err := dialLive(t, e, cookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dialLive error = %v, resp = %v", err, resp)
	}

	subscribeMsg := map[string]any{
		"type":          "LIVE_SUBSCRIBE",
		"messageId":     uuid.NewString(),
		"sentAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"liveSessionId": liveSessionID,
		"payload":       map[string]any{},
	}
	raw, _ := json.Marshal(subscribeMsg)
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("WriteMessage(LIVE_SUBSCRIBE) error = %v", err)
	}

	kind, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(LIVE_SUBSCRIBED) error = %v", err)
	}
	if kind != websocket.TextMessage {
		t.Fatalf("expected TextMessage for LIVE_SUBSCRIBED, got %d", kind)
	}
	var ack map[string]any
	if err := json.Unmarshal(data, &ack); err != nil {
		t.Fatalf("unmarshal LIVE_SUBSCRIBED error = %v", err)
	}
	if ack["type"] != "LIVE_SUBSCRIBED" {
		t.Fatalf("expected LIVE_SUBSCRIBED, got %v", ack["type"])
	}
	payload, _ := ack["payload"].(map[string]any)
	if payload["historyAvailable"] != false {
		t.Fatalf("expected historyAvailable == false, got %v", payload["historyAvailable"])
	}

	lc := &liveClient{
		conn:  ws,
		msgCh: make(chan message, 100),
		errCh: make(chan error, 1),
	}
	go lc.readLoop()
	return lc
}

func (lc *liveClient) readLoop() {
	for {
		kind, data, err := lc.conn.ReadMessage()
		if err != nil {
			lc.errCh <- err
			return
		}
		lc.msgCh <- message{kind: kind, data: data}
	}
}

func (lc *liveClient) readMessage(timeout time.Duration) (int, []byte, error) {
	select {
	case m := <-lc.msgCh:
		return m.kind, m.data, nil
	case err := <-lc.errCh:
		return 0, nil, err
	case <-time.After(timeout):
		return 0, nil, errors.New("read timeout")
	}
}

func (lc *liveClient) close() {
	lc.closeOnce.Do(func() {
		_ = lc.conn.Close()
	})
}

// 1. Fan-out to multiple student subscribers:
func TestLiveFanOutMultipleSubscribers(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}

	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	studentA := connectLive(t, e, liveID, ownerCookie)
	studentB := connectLive(t, e, liveID, ownerCookie)
	studentC := connectLive(t, e, liveID, ownerCookie)
	defer studentA.close()
	defer studentB.close()
	defer studentC.close()

	// Connector sends PTY OUTPUT binary frame
	outputPayload := []byte("HELLO LIVE FANOUT STREAM 12345\r\n")
	d.writeBinary(outputPayload)

	// Owner browser receives exact bytes
	bData := b.readBinary()
	if !bytes.Equal(bData, outputPayload) {
		t.Fatalf("browser got %q, want %q", bData, outputPayload)
	}

	// Students A, B, C each receive exact identical bytes
	for name, student := range map[string]*liveClient{"A": studentA, "B": studentB, "C": studentC} {
		kind, data, err := student.readMessage(2 * time.Second)
		if err != nil {
			t.Fatalf("student %s read error = %v", name, err)
		}
		if kind != websocket.BinaryMessage {
			t.Fatalf("student %s got kind %d, want BinaryMessage", name, kind)
		}
		if !bytes.Equal(data, outputPayload) {
			t.Fatalf("student %s got %q, want %q", name, data, outputPayload)
		}
	}
}

// 2. Instructor Browser detached -> Students STILL receive output (D-21):
func TestLiveFanOutWhenInstructorBrowserDetached(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	student := connectLive(t, e, liveID, ownerCookie)
	defer student.close()

	// Instructor browser detaches!
	b.close()
	time.Sleep(50 * time.Millisecond)

	// Terminal session is now DETACHED, but source PTY is alive in grace.
	// Connector sends OUTPUT
	detachedPayload := []byte("OUTPUT WHILE DETACHED 999\r\n")
	d.writeBinary(detachedPayload)

	// Student MUST receive the output!
	kind, data, err := student.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("student read error when instructor detached = %v", err)
	}
	if kind != websocket.BinaryMessage || !bytes.Equal(data, detachedPayload) {
		t.Fatalf("student got %q, want %q", data, detachedPayload)
	}
}

// 3. Slow subscriber isolation:
func TestLiveSlowConsumerIsolation(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	// Use very small queue limits to easily trigger overflow
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
		o.BrowserQueueMessages = 2
		o.BrowserQueueBytes = 64
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	// Connect slow student (we dial raw websocket and DO NOT read from it)
	slowWS, _, err := dialLive(t, e, ownerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dialLive slow error = %v", err)
	}
	defer slowWS.Close()
	subMsg, _ := json.Marshal(map[string]any{
		"type":          "LIVE_SUBSCRIBE",
		"messageId":     uuid.NewString(),
		"sentAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"liveSessionId": liveID,
		"payload":       map[string]any{},
	})
	_ = slowWS.WriteMessage(websocket.TextMessage, subMsg)

	// Read LIVE_SUBSCRIBED ack
	_, _, _ = slowWS.ReadMessage()

	// Connect fast student
	fastStudent := connectLive(t, e, liveID, ownerCookie)
	defer fastStudent.close()

	// Now burst enough data to overflow slow student queue
	chunk := bytes.Repeat([]byte("X"), 60)
	for i := 0; i < 10; i++ {
		d.writeBinary(chunk)
		// fast student reads to drain
		_, _, _ = fastStudent.readMessage(100 * time.Millisecond)
		// owner browser reads
		_ = b.readBinary()
	}

	// Slow student must be closed with 4005 (slow consumer)!
	_ = slowWS.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		kind, data, err := slowWS.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				if closeErr.Code != 4005 {
					t.Fatalf("slow student close code = %d, want 4005", closeErr.Code)
				}
			}
			break
		}
		if kind == websocket.TextMessage {
			var errMsg map[string]any
			if err := json.Unmarshal(data, &errMsg); err == nil && errMsg["type"] == "ERROR" {
				payload, _ := errMsg["payload"].(map[string]any)
				if payload["code"] != "SLOW_CONSUMER" {
					t.Errorf("error code = %v, want SLOW_CONSUMER", payload["code"])
				}
			}
		}
	}

	// Fast student and owner browser continue to work normally
	d.writeBinary([]byte("AFTER SLOW CONSUMER DROPPED"))
	bData := b.readBinary()
	if !bytes.Equal(bData, []byte("AFTER SLOW CONSUMER DROPPED")) {
		t.Fatalf("browser got %q", bData)
	}
	fKind, fData, err := fastStudent.readMessage(2 * time.Second)
	if err != nil || fKind != websocket.BinaryMessage || !bytes.Equal(fData, []byte("AFTER SLOW CONSUMER DROPPED")) {
		t.Fatalf("fast student got %q, err = %v", fData, err)
	}
}

// 4. No replay / no history:
func TestLiveNoReplayLateJoinAndReconnect(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	_ = e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	// Output A happens before student joins
	d.writeBinary([]byte("CHUNK_A_BEFORE_JOIN"))

	// Late-joining student connects
	student := connectLive(t, e, liveID, ownerCookie)

	// Output B happens
	d.writeBinary([]byte("CHUNK_B_AFTER_JOIN"))

	// Student must receive CHUNK_B directly (NEVER CHUNK_A)
	kind, data, err := student.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if kind != websocket.BinaryMessage || !bytes.Equal(data, []byte("CHUNK_B_AFTER_JOIN")) {
		t.Fatalf("student got %q, want CHUNK_B_AFTER_JOIN", data)
	}

	// Student disconnects
	student.close()
	time.Sleep(50 * time.Millisecond)

	// Output C happens while disconnected
	d.writeBinary([]byte("CHUNK_C_WHILE_DISCONNECTED"))

	// Student reconnects (new subscription)
	student2 := connectLive(t, e, liveID, ownerCookie)
	defer student2.close()

	// Output D happens
	d.writeBinary([]byte("CHUNK_D_AFTER_RECONNECT"))

	// Student must receive CHUNK_D directly (NEVER CHUNK_C)
	kind2, data2, err := student2.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	if kind2 != websocket.BinaryMessage || !bytes.Equal(data2, []byte("CHUNK_D_AFTER_RECONNECT")) {
		t.Fatalf("student got %q, want CHUNK_D_AFTER_RECONNECT", data2)
	}
}

// 5. Read-only enforcement:
func TestLiveReadOnlyEnforcement(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	_ = e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	// 5a. Student sends binary message
	student := connectLive(t, e, liveID, ownerCookie)
	if err := student.conn.WriteMessage(websocket.BinaryMessage, []byte("INPUT DATA FROM STUDENT")); err != nil {
		t.Fatalf("WriteMessage error = %v", err)
	}
	kind, data, err := student.readMessage(2 * time.Second)
	if err == nil && kind == websocket.TextMessage {
		var errMsg map[string]any
		_ = json.Unmarshal(data, &errMsg)
		if errMsg["type"] != "ERROR" {
			t.Fatalf("expected ERROR message, got %v", errMsg)
		}
	}
	student.close()

	// Verify connector received NO binary input
	d.expectNoMessage(100 * time.Millisecond)

	// 5b. Student sends text message
	student2 := connectLive(t, e, liveID, ownerCookie)
	if err := student2.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"TERMINAL_RESIZE"}`)); err != nil {
		t.Fatalf("WriteMessage error = %v", err)
	}
	kind2, data2, err := student2.readMessage(2 * time.Second)
	if err == nil && kind2 == websocket.TextMessage {
		var errMsg map[string]any
		_ = json.Unmarshal(data2, &errMsg)
		if errMsg["type"] != "ERROR" {
			t.Fatalf("expected ERROR message, got %v", errMsg)
		}
	}
	student2.close()
}

// 6. Explicit Live close:
func TestLiveExplicitCloseLeavesTerminalAlive(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	student := connectLive(t, e, liveID, ownerCookie)
	defer student.close()

	// Explicit Live Close
	e.relay.TerminateLive(liveID, realtime.End{Reason: "SESSION_CLOSED"})

	// Student receives LIVE_ENDED
	kind, data, err := student.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("student read error = %v", err)
	}
	if kind != websocket.TextMessage {
		t.Fatalf("expected TextMessage for LIVE_ENDED, got %d", kind)
	}
	var endedMsg map[string]any
	if err := json.Unmarshal(data, &endedMsg); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if endedMsg["type"] != "LIVE_ENDED" {
		t.Fatalf("expected LIVE_ENDED, got %v", endedMsg["type"])
	}
	payload, _ := endedMsg["payload"].(map[string]any)
	if payload["reason"] != "SESSION_CLOSED" {
		t.Fatalf("expected reason SESSION_CLOSED, got %v", payload["reason"])
	}

	// Explicit close should result in normal WebSocket close code 1000
	_, _, err = student.readMessage(2 * time.Second)
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("expected close code 1000 for explicit live close, got %v", err)
	}

	// Instructor terminal is STILL alive!
	d.writeBinary([]byte("TERMINAL STILL ALIVE"))
	bData := b.readBinary()
	if !bytes.Equal(bData, []byte("TERMINAL STILL ALIVE")) {
		t.Fatalf("terminal owner browser did not receive output: %q", bData)
	}
}

// 7. Source terminal end terminates Live:
func TestLiveSourceTerminalEndTerminatesLive(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	_ = e.connectBrowser(s)
	d.readJSON() // resize

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive() error = %v", err)
	}

	student := connectLive(t, e, liveID, ownerCookie)
	defer student.close()

	// Terminate source terminal
	e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonSessionClosed})

	// Student receives LIVE_ENDED
	_, data, err := student.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("student read error = %v", err)
	}
	var endedMsg map[string]any
	_ = json.Unmarshal(data, &endedMsg)
	if endedMsg["type"] != "LIVE_ENDED" {
		t.Fatalf("expected LIVE_ENDED, got %v", endedMsg["type"])
	}

	// Source terminal termination is a lifecycle termination, so close code must be 4006
	_, _, err = student.readMessage(2 * time.Second)
	var termCloseErr *websocket.CloseError
	if !errors.As(err, &termCloseErr) || termCloseErr.Code != 4006 {
		t.Fatalf("expected close code 4006 for source terminal end, got %v", err)
	}

	// SourceTerminalEnded hook called
	liveCtrl.mu.Lock()
	defer liveCtrl.mu.Unlock()
	if len(liveCtrl.endedSource) == 0 || liveCtrl.endedSource[0] != s.ID {
		t.Fatalf("SourceTerminalEnded hook not called for %s", s.ID)
	}
}

// 8. Content Non-Leakage test (Section 57):
func TestLiveContentNonLeakage(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON()

	liveID := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID] = realtime.LiveGrant{
		LiveSessionID:           liveID,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	_ = e.relay.RegisterLive(liveID, s.ID, classID)

	student := connectLive(t, e, liveID, ownerCookie)
	defer student.close()

	marker := "SUPER_SECRET_PAYLOAD_MARKER_LIVE_8888"
	d.writeBinary([]byte("echo " + marker + "\r\n"))
	_ = b.readBinary()
	_, _, _ = student.readMessage(time.Second)

	// Check logs
	logs := e.logs.String()
	if strings.Contains(logs, marker) {
		t.Fatalf("logs contain secret content marker %q:\n%s", marker, logs)
	}
}

// 9. Blocker 1 Regression: ErrLabMutation Live subscribe returns ERROR LAB_MUTATION and close code 4006
func TestLiveSubscribeLabMutationCloseCode4006(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	liveID := uuid.NewString()
	liveCtrl.authorizeErr = realtime.ErrLabMutation

	ws, resp, err := dialLive(t, e, ownerCookie, trustedOrigin)
	if err != nil {
		t.Fatalf("dialLive error = %v, resp = %v", err, resp)
	}
	defer ws.Close()

	subscribeMsg := map[string]any{
		"type":          "LIVE_SUBSCRIBE",
		"messageId":     uuid.NewString(),
		"sentAt":        time.Now().UTC().Format(time.RFC3339Nano),
		"liveSessionId": liveID,
		"payload":       map[string]any{},
	}
	raw, _ := json.Marshal(subscribeMsg)
	if err := ws.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("WriteMessage error = %v", err)
	}

	kind, data, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error = %v", err)
	}
	if kind != websocket.TextMessage {
		t.Fatalf("expected TextMessage, got %d", kind)
	}
	var errMsg map[string]any
	if err := json.Unmarshal(data, &errMsg); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if errMsg["type"] != "ERROR" {
		t.Fatalf("expected ERROR, got %v", errMsg["type"])
	}
	payload, _ := errMsg["payload"].(map[string]any)
	if payload["code"] != "LAB_MUTATION" {
		t.Fatalf("expected error code LAB_MUTATION, got %v", payload["code"])
	}

	// Next read must return CloseError with code 4006
	_, _, err = ws.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != 4006 {
		t.Fatalf("expected close code 4006, got err = %v", err)
	}
}

// 10. Blocker 2 Regression: RegisterLive ↔ source Terminal finish lifecycle race serialization
func TestLiveRegisterConcurrentTerminalFinishRace(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		liveCtrl := newFakeLiveControl()
		e := newEnv(t, func(o *realtime.Options) {
			o.LiveControl = liveCtrl
		})

		s, _ := e.liveSession()
		liveID := uuid.NewString()
		classID := uuid.NewString()
		liveCtrl.authorized[liveID] = realtime.LiveGrant{
			LiveSessionID:           liveID,
			ClassID:                 classID,
			SourceTerminalSessionID: s.ID,
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		var regErr error
		go func() {
			defer wg.Done()
			<-start
			regErr = e.relay.RegisterLive(liveID, s.ID, classID)
		}()

		go func() {
			defer wg.Done()
			<-start
			e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonSessionClosed})
		}()

		close(start)
		wg.Wait()

		// 불변식: 두 고루틴이 모두 완료된 후 Relay에 dangling live session이 절대 남지 않아야 함.
		// 스케줄러 실행 순서에 따라 RegisterLive가 먼저 이기거나(Terminate가 정리) Terminate가 먼저 이김(RegisterLive 거절).
		if activeLives := e.relay.LiveSessions(); activeLives != 0 {
			t.Fatalf("iter %d: expected 0 active live sessions, got %d (regErr=%v)", iter, activeLives, regErr)
		}

		if regErr == nil {
			// RegisterLive가 먼저 성공한 경우: Terminate가 해당 LiveSession을 정상 정리하고 SourceTerminalEnded hook 호출
			liveCtrl.mu.Lock()
			endedCount := len(liveCtrl.endedSource)
			liveCtrl.mu.Unlock()
			if endedCount != 1 {
				t.Fatalf("iter %d: RegisterLive succeeded but SourceTerminalEnded was not called", iter)
			}
		} else {
			// Terminate가 먼저 종료한 경우: RegisterLive는 ErrSessionEnded 또는 ErrSessionNotFound로 안전하게 실패
			if !errors.Is(regErr, realtime.ErrSessionEnded) && !errors.Is(regErr, realtime.ErrSessionNotFound) {
				t.Fatalf("iter %d: expected ErrSessionEnded or ErrSessionNotFound, got %v", iter, regErr)
			}
		}
	}
}

// 11. Blocker 3 Regression: Explicit Live close does NOT trigger SourceTerminalEnded hook, and allows new Live create
func TestLiveExplicitCloseDoesNotEndNewLiveOnSameTerminal(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, d := e.liveSession()
	b := e.connectBrowser(s)
	d.readJSON() // resize

	liveID1 := uuid.NewString()
	classID := uuid.NewString()
	liveCtrl.authorized[liveID1] = realtime.LiveGrant{
		LiveSessionID:           liveID1,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID1, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive(1) error = %v", err)
	}

	student1 := connectLive(t, e, liveID1, ownerCookie)
	defer student1.close()

	// 1. Explicitly terminate Live 1
	e.relay.TerminateLive(liveID1, realtime.End{Reason: "SESSION_CLOSED"})

	// Verify SourceTerminalEnded was NOT called by TerminateLive
	liveCtrl.mu.Lock()
	if len(liveCtrl.endedSource) != 0 {
		t.Fatalf("SourceTerminalEnded hook should not be called on TerminateLive, got %v", liveCtrl.endedSource)
	}
	liveCtrl.mu.Unlock()

	// Student 1 receives LIVE_ENDED and closes with 1000
	_, _, err := student1.readMessage(2 * time.Second)
	if err != nil {
		t.Fatalf("student1 read error = %v", err)
	}
	_, _, err = student1.readMessage(2 * time.Second)
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
		t.Fatalf("expected close code 1000 for explicit close, got %v", err)
	}

	// 2. Immediately register Live 2 on the SAME source terminal
	liveID2 := uuid.NewString()
	liveCtrl.authorized[liveID2] = realtime.LiveGrant{
		LiveSessionID:           liveID2,
		ClassID:                 classID,
		SourceTerminalSessionID: s.ID,
	}
	if err := e.relay.RegisterLive(liveID2, s.ID, classID); err != nil {
		t.Fatalf("RegisterLive(2) error = %v", err)
	}

	student2 := connectLive(t, e, liveID2, ownerCookie)
	defer student2.close()

	// 3. Send PTY output and verify student 2 receives it
	d.writeBinary([]byte("HELLO LIVE 2"))
	_ = b.readBinary()
	kind, data, err := student2.readMessage(2 * time.Second)
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(data, []byte("HELLO LIVE 2")) {
		t.Fatalf("student2 got %q, err=%v", data, err)
	}
}

// 12. Final Ordering Regression: LIVE_ENDED 이후에는 어떠한 PTY Binary 프레임도 수신되지 않음을 동시성 하에서 검증
func TestLiveSubscriberNoBinaryAfterLiveEndedConcurrently(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		liveCtrl := newFakeLiveControl()
		e := newEnv(t, func(o *realtime.Options) {
			o.LiveControl = liveCtrl
		})

		s, d := e.liveSession()
		b := e.connectBrowser(s)
		d.readJSON() // resize

		liveID := uuid.NewString()
		classID := uuid.NewString()
		liveCtrl.authorized[liveID] = realtime.LiveGrant{
			LiveSessionID:           liveID,
			ClassID:                 classID,
			SourceTerminalSessionID: s.ID,
		}
		if err := e.relay.RegisterLive(liveID, s.ID, classID); err != nil {
			t.Fatalf("RegisterLive error = %v", err)
		}

		student := connectLive(t, e, liveID, ownerCookie)

		stopOutput := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		// G1: 지속적으로 PTY Binary 출력 생성
		go func() {
			defer wg.Done()
			chunk := []byte("DATA CHUNK")
			for {
				select {
				case <-stopOutput:
					return
				default:
					d.writeBinary(chunk)
					_ = b.readBinary()
					time.Sleep(time.Millisecond)
				}
			}
		}()

		// G2: 잠시 후 명시적 Live 종료 호출
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			e.relay.TerminateLive(liveID, realtime.End{Reason: "SESSION_CLOSED"})
			close(stopOutput)
		}()

		// 학생 측에서 메시지 수신: LIVE_ENDED가 오기 전까지는 Binary 수신 가능,
		// LIVE_ENDED 수신 후에는 절대로 Binary가 오면 안 되며 오직 CloseError(1000)여야 함
		seenLiveEnded := false
		for {
			kind, data, err := student.readMessage(2 * time.Second)
			if err != nil {
				// WebSocket 종료
				var closeErr *websocket.CloseError
				if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
					t.Fatalf("iter %d: expected CloseNormalClosure (1000), got %v", iter, err)
				}
				break
			}
			if seenLiveEnded {
				t.Fatalf("iter %d: received frame (kind=%d, data=%q) AFTER LIVE_ENDED! Binary must never follow LIVE_ENDED", iter, kind, data)
			}
			if kind == websocket.TextMessage {
				var msg map[string]any
				if err := json.Unmarshal(data, &msg); err == nil && msg["type"] == "LIVE_ENDED" {
					seenLiveEnded = true
				}
			}
		}

		if !seenLiveEnded {
			t.Fatalf("iter %d: LIVE_ENDED was never received by student", iter)
		}

		student.close()
		wg.Wait()
	}
}

// 13. Deterministic Invariant: 이미 종료된 Terminal에 대해서는 RegisterLive가 항상 결정적으로 실패
func TestLiveRegisterDeterministicTerminalAlreadyEnded(t *testing.T) {
	liveCtrl := newFakeLiveControl()
	e := newEnv(t, func(o *realtime.Options) {
		o.LiveControl = liveCtrl
	})

	s, _ := e.liveSession()
	liveID := uuid.NewString()
	classID := uuid.NewString()

	// Terminal 명시적 종료
	e.relay.Terminate(s.ID, realtime.End{Reason: realtime.EndReasonSessionClosed})

	// 이미 종료된 Terminal에 대해 RegisterLive 호출
	err := e.relay.RegisterLive(liveID, s.ID, classID)
	if !errors.Is(err, realtime.ErrSessionEnded) && !errors.Is(err, realtime.ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionEnded or ErrSessionNotFound, got %v", err)
	}

	if e.relay.LiveSessions() != 0 {
		t.Fatalf("expected 0 active live sessions, got %d", e.relay.LiveSessions())
	}
}
