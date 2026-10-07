package realtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime/realtimetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// 테스트용 값이다. 실제 Credential이나 token이 아니다.
const (
	trustedOrigin  = "https://labbit.test"
	ownerCookie    = "owner-session-cookie-value"
	otherCookie    = "other-session-cookie-value"
	connectorCred  = "connector-credential-one"
	connectorCred2 = "connector-credential-two"
	connectorID1   = "11111111-1111-4111-8111-111111111111"
	connectorID2   = "22222222-2222-4222-8222-222222222222"

	// connectorCred1b는 connectorID1의 두 번째 유효한 Credential이다. 같은 Connector라도 다른 Credential이다.
	connectorCred1b = "connector-credential-one-b"
	// Credential 식별자다. Credential 원문이 아니며 revoke 통지가 이 값으로 connection을 찾는다.
	credentialID1  = "c1111111-1111-4111-8111-111111111111"
	credentialID1b = "c1111111-1111-4111-8111-11111111111b"
	credentialID2  = "c2222222-2222-4222-8222-222222222222"

	// 이 값들이 log나 DB에 나타나면 안 된다.
	inputMarker  = "INPUT-MARKER-0f3a9c"
	outputMarker = "OUTPUT-MARKER-7be21d"
)

var startTime = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// syncBuffer는 여러 goroutine이 쓰는 log를 담는다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// session은 test가 만든 TerminalSession 하나의 correlation과 token이다.
type session struct {
	ID          string
	LabID       string
	Generation  int64
	ConnectorID string
	Token       string
	OwnerCookie string
}

type detachCall struct {
	ID             string
	At, GraceUntil time.Time
}

type endedCall struct {
	ID  string
	End realtime.End
}

// fakeControl은 realtime.Control의 fake다. 권한 판정 로직 자체가 아니라 Relay가 Control에 전달하는 값과
// Control의 결과를 Browser에 옮기는 방식을 검증하기 위한 것이다.
type fakeControl struct {
	mu       sync.Mutex
	cookies  map[string]bool
	sessions map[string]session
	// authorizeErr이 있으면 AuthorizeAttach가 그 오류를 반환한다.
	authorizeErr error
	// recordAttachedErr이 있으면 RecordAttached가 그 오류를 반환한다.
	recordAttachedErr error
	// attachGate가 있으면 다음 RecordAttached 호출이 release될 때까지 멈춘다(한 번만). 경쟁 조건을 결정적으로 만들 때 쓴다.
	attachGate *gate

	attached []string
	detached []detachCall
	closed   []endedCall
	ended    []endedCall
	authn    int

	// 아래는 control event(TERMINAL_ATTACH, TERMINAL_DATA_ENDED)로 Control을 호출할 때 ctx에 담겨 온 Trace Context다.
	// 유효한 Context가 없으면 zero value다.
	authorizeTraces []tracecontext.Context
	attachedTraces  []tracecontext.Context
	endedTraces     []tracecontext.Context
}

func newFakeControl() *fakeControl {
	return &fakeControl{
		cookies:  map[string]bool{ownerCookie: true, otherCookie: true},
		sessions: map[string]session{},
	}
}

func (c *fakeControl) AuthenticateBrowser(_ context.Context, cookie realtime.SessionToken) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authn++
	if !c.cookies[string(cookie)] {
		return realtime.ErrUnauthenticated
	}
	return nil
}

func (c *fakeControl) AuthorizeAttach(ctx context.Context, cookie realtime.SessionToken, req realtime.AttachRequest) (realtime.AttachGrant, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authorizeTraces = append(c.authorizeTraces, tracecontext.FromContext(ctx))
	if c.authorizeErr != nil {
		return realtime.AttachGrant{}, c.authorizeErr
	}
	if !c.cookies[string(cookie)] {
		return realtime.AttachGrant{}, realtime.ErrUnauthenticated
	}
	s, ok := c.sessions[req.TerminalSessionID]
	if !ok {
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}
	if s.OwnerCookie != string(cookie) {
		return realtime.AttachGrant{}, realtime.ErrForbidden
	}
	if string(req.Token) != s.Token {
		return realtime.AttachGrant{}, realtime.ErrInvalidToken
	}
	return realtime.AttachGrant{TerminalSessionID: s.ID, LabInstanceID: s.LabID, Generation: s.Generation}, nil
}

// gate는 호출을 멈췄다가 풀어 주는 동기화 도구다.
type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate { return &gate{entered: make(chan struct{}, 1), release: make(chan struct{})} }

func (c *fakeControl) RecordAttached(ctx context.Context, id string, _ time.Time) error {
	c.mu.Lock()
	g := c.attachGate
	c.attachGate = nil
	c.mu.Unlock()
	if g != nil {
		g.entered <- struct{}{}
		<-g.release
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.attachedTraces = append(c.attachedTraces, tracecontext.FromContext(ctx))
	if c.recordAttachedErr != nil {
		return c.recordAttachedErr
	}
	c.attached = append(c.attached, id)
	return nil
}

func (c *fakeControl) RecordDetached(_ context.Context, id string, at, graceUntil time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.detached = append(c.detached, detachCall{ID: id, At: at, GraceUntil: graceUntil})
	return nil
}

func (c *fakeControl) CloseSession(_ context.Context, id, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = append(c.closed, endedCall{ID: id, End: realtime.End{Reason: reason}})
	return nil
}

func (c *fakeControl) SessionEnded(ctx context.Context, id string, end realtime.End) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ended = append(c.ended, endedCall{ID: id, End: end})
	c.endedTraces = append(c.endedTraces, tracecontext.FromContext(ctx))
	return nil
}

// traces는 Control 호출의 ctx에 담겨 온 Trace Context다.
func (c *fakeControl) traces() (authorize, attached, ended []tracecontext.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]tracecontext.Context(nil), c.authorizeTraces...), append([]tracecontext.Context(nil), c.attachedTraces...),
		append([]tracecontext.Context(nil), c.endedTraces...)
}

func (c *fakeControl) snapshot() (attached []string, detached []detachCall, closed, ended []endedCall) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.attached...), append([]detachCall(nil), c.detached...),
		append([]endedCall(nil), c.closed...), append([]endedCall(nil), c.ended...)
}

type fakeCredential struct{ connectorID, credentialID string }

// fakeConnectors는 credential을 Connector identity로 바꾸는 fake다. 저장소에서 Credential이 revoke된 것을 흉내 낼 수 있다(revoke).
// 운영에서는 저장소 상태를 바꾼 뒤에 revoke 통지가 오므로 test도 revoke 뒤에 relay.RevokeConnectorCredential을 호출한다.
type fakeConnectors struct {
	mu      sync.Mutex
	creds   map[string]fakeCredential
	revoked map[string]bool
	// stall이 있으면 다음 인증이 성공 결과를 얻은 뒤 release될 때까지 멈춘다(한 번만).
	// "인증은 끝났지만 connection으로 등록되기 전"을 결정적으로 만든다.
	stall *gate
}

func newFakeConnectors() *fakeConnectors {
	return &fakeConnectors{
		creds: map[string]fakeCredential{
			connectorCred:   {connectorID1, credentialID1},
			connectorCred1b: {connectorID1, credentialID1b},
			connectorCred2:  {connectorID2, credentialID2},
		},
		revoked: map[string]bool{},
	}
}

func (f *fakeConnectors) AuthenticateConnector(_ context.Context, credential realtime.ConnectorCredential) (realtime.ConnectorIdentity, error) {
	f.mu.Lock()
	c, ok := f.creds[string(credential)]
	if !ok || f.revoked[string(credential)] {
		f.mu.Unlock()
		return realtime.ConnectorIdentity{}, realtime.ErrUnauthenticated
	}
	g := f.stall
	f.stall = nil
	f.mu.Unlock()

	if g != nil {
		g.entered <- struct{}{}
		<-g.release
	}
	return realtime.ConnectorIdentity{ConnectorID: c.connectorID, CredentialID: c.credentialID}, nil
}

// revoke는 저장소에서 credential이 revoke된 것을 흉내 낸다. 이후의 인증은 실패한다.
func (f *fakeConnectors) revoke(credential string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[credential] = true
}

// stallNextAuth는 다음 성공한 인증을 그 결과를 얻은 직후에 멈춘다.
func (f *fakeConnectors) stallNextAuth() *gate {
	g := newGate()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stall = g
	return g
}

// env는 실제 WebSocket endpoint를 가진 Relay와 그 의존성이다.
type env struct {
	t       *testing.T
	relay   *realtime.Relay
	clock   *realtimetest.FakeClock
	control *fakeControl
	// connectors는 Connector credential 인증 fake다.
	connectors *fakeConnectors
	server     *httptest.Server
	logs       *syncBuffer
	seq        int
}

func newEnv(t *testing.T, mods ...func(*realtime.Options)) *env {
	t.Helper()
	e := &env{t: t, clock: realtimetest.NewFakeClock(startTime), control: newFakeControl(), connectors: newFakeConnectors(), logs: &syncBuffer{}}
	opts := realtime.Options{
		Control:       e.control,
		Connectors:    e.connectors,
		AllowOrigin:   func(origin string) bool { return origin == trustedOrigin },
		Clock:         e.clock,
		Logger:        observability.NewJSONLoggerTo(e.logs, "labbit-server", "realtime", "development", "debug"),
		AttachTimeout: 2 * time.Second,
		WriteTimeout:  2 * time.Second,
		CloseGrace:    200 * time.Millisecond,
	}
	for _, mod := range mods {
		mod(&opts)
	}
	relay, err := realtime.New(opts)
	if err != nil {
		t.Fatalf("realtime.New() error = %v", err)
	}
	e.relay = relay

	mux := http.NewServeMux()
	mux.Handle("GET "+realtime.BrowserPath, relay.BrowserHandler())
	mux.Handle("GET "+realtime.LivePath, relay.LiveHandler())
	mux.Handle("GET "+realtime.DataPath, relay.DataHandler())
	e.server = httptest.NewServer(mux)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := relay.Shutdown(ctx); err != nil {
			t.Errorf("relay.Shutdown() error = %v", err)
		}
		e.server.Close()
	})
	return e
}

func (e *env) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(e.server.URL, "http") + path
}

// newSession은 Control에 TerminalSession을 알리고 Relay에 예상 correlation을 등록한다. Activate는 하지 않는다.
func (e *env) newSession() session { return e.newSessionOn(connectorID1) }

// newSessionOn은 newSession을 connectorID의 Connector가 PTY를 소유하는 TerminalSession으로 만든다.
func (e *env) newSessionOn(connectorID string) session {
	e.t.Helper()
	e.seq++
	s := session{
		ID:          fmt.Sprintf("00000000-0000-4000-8000-%012d", e.seq),
		LabID:       fmt.Sprintf("10000000-0000-4000-8000-%012d", e.seq),
		Generation:  1,
		ConnectorID: connectorID,
		Token:       fmt.Sprintf("test-attach-token-%d", e.seq),
		OwnerCookie: ownerCookie,
	}
	e.control.mu.Lock()
	e.control.sessions[s.ID] = s
	e.control.mu.Unlock()
	if err := e.relay.Expect(realtime.Expected{TerminalSessionID: s.ID, ConnectorID: s.ConnectorID, LabInstanceID: s.LabID, Generation: s.Generation}); err != nil {
		e.t.Fatalf("Expect() error = %v", err)
	}
	return s
}

// liveSession은 Expect, data channel bind, Activate까지 마친 TerminalSession과 그 data peer를 만든다.
func (e *env) liveSession() (session, *dataPeer) {
	e.t.Helper()
	s := e.newSession()
	d := e.connectData(s)
	if err := e.relay.Activate(s.ID, e.clock.Now().Add(realtime.DefaultGrace)); err != nil {
		e.t.Fatalf("Activate() error = %v", err)
	}
	return s, d
}

// peer는 WebSocket client connection이다.
//
// 읽기는 첫 읽기 helper 호출 때 시작하는 reader goroutine 하나가 channel로 넘긴다. gorilla/websocket은 read deadline이 한 번
// 만료되면 그 connection의 읽기가 영구히 실패하므로, "아무 message도 오지 않음"을 확인하는 test가 connection을 망가뜨리지 않도록
// 시간 제한은 socket이 아니라 channel에서 건다. 읽기를 시작하기 전에는 socket을 읽지 않으므로 느린 수신자를 흉내 낼 수 있다.
type peer struct {
	t    *testing.T
	conn *websocket.Conn

	start   sync.Once
	msgs    chan received
	readErr error // msgs가 닫힌 뒤에만 읽는다.
}

func newPeer(t *testing.T, conn *websocket.Conn) *peer {
	p := &peer{t: t, conn: conn, msgs: make(chan received, 4096)}
	t.Cleanup(p.close)
	return p
}

func (p *peer) readLoop() {
	defer close(p.msgs)
	for {
		kind, data, err := p.conn.ReadMessage()
		if err != nil {
			p.readErr = err
			return
		}
		p.msgs <- received{kind: kind, data: data}
	}
}

const readTimeout = 5 * time.Second

func (p *peer) writeText(data []byte) {
	p.t.Helper()
	if err := p.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		p.t.Fatalf("WriteMessage(text) error = %v", err)
	}
}

func (p *peer) writeBinary(data []byte) {
	p.t.Helper()
	if err := p.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
		p.t.Fatalf("WriteMessage(binary) error = %v", err)
	}
}

// next는 message 하나를 기다린다. connection이 끝났으면 그 오류를, 시간 안에 오지 않으면 errReadTimeout을 반환한다.
func (p *peer) next(timeout time.Duration) (received, error) {
	p.start.Do(func() { go p.readLoop() })
	select {
	case m, ok := <-p.msgs:
		if !ok {
			return received{}, p.readErr
		}
		return m, nil
	case <-time.After(timeout):
		return received{}, errReadTimeout
	}
}

var errReadTimeout = errors.New("시간 안에 message가 오지 않음")

// read는 message 하나를 읽는다.
func (p *peer) read() (int, []byte, error) {
	m, err := p.next(readTimeout)
	return m.kind, m.data, err
}

// readJSON은 JSON Text message 하나를 읽어 object로 반환한다.
func (p *peer) readJSON() map[string]any {
	p.t.Helper()
	m, err := p.next(readTimeout)
	if err != nil {
		p.t.Fatalf("ReadMessage() error = %v", err)
	}
	return jsonOf(p.t, m)
}

// readBinary는 Binary message 하나를 읽는다.
func (p *peer) readBinary() []byte {
	p.t.Helper()
	m, err := p.next(readTimeout)
	if err != nil {
		p.t.Fatalf("ReadMessage() error = %v", err)
	}
	if m.kind != websocket.BinaryMessage {
		p.t.Fatalf("message kind = %d (%q), want binary", m.kind, m.data)
	}
	return m.data
}

// expectClose는 close frame이 올 때까지 읽고(그 전의 message는 버린다) close code를 반환한다.
func (p *peer) expectClose() int {
	p.t.Helper()
	_, code := p.collect()
	return code
}

// expectNoMessage는 짧은 시간 동안 아무 message도 오지 않음을 확인한다. 이후에도 이 connection을 계속 읽을 수 있다.
func (p *peer) expectNoMessage(wait time.Duration) {
	p.t.Helper()
	m, err := p.next(wait)
	switch {
	case err == nil:
		p.t.Fatalf("예상하지 못한 message: kind=%d %q", m.kind, m.data)
	case errors.Is(err, errReadTimeout):
	default:
		p.t.Fatalf("예상하지 못한 connection 종료: %v", err)
	}
}

func (p *peer) close() { _ = p.conn.Close() }

// dataPeer는 Connector 역할의 Terminal Data WSS client다.
type dataPeer struct {
	*peer
	attached map[string]any
}

// browserPeer는 Browser 역할의 Terminal WSS client다.
type browserPeer struct {
	*peer
	attached map[string]any
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

// dataAttachMessage는 TERMINAL_DATA_ATTACH다. fields로 임의의 값을 덮어쓴다.
func dataAttachMessage(t *testing.T, s session, fields map[string]any) []byte {
	t.Helper()
	msg := map[string]any{
		"type": "TERMINAL_DATA_ATTACH", "messageId": "data-attach-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": s.ID, "labInstanceId": s.LabID, "generation": s.Generation,
		"payload": map[string]any{"runtimeId": "runtime-1"},
	}
	for k, v := range fields {
		if v == nil {
			delete(msg, k)
		} else {
			msg[k] = v
		}
	}
	return marshal(t, msg)
}

func (e *env) dialData(credential string, protocols ...string) (*peer, *http.Response, error) {
	header := http.Header{}
	if credential != "" {
		header.Set("Authorization", "Bearer "+credential)
	}
	if protocols == nil {
		protocols = []string{realtime.DataSubprotocol}
	}
	dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(e.wsURL(realtime.DataPath), header)
	if err != nil {
		return nil, resp, err
	}
	return newPeer(e.t, conn), resp, nil
}

// connectData는 Connector credential로 Data WSS를 열고 attach해 TERMINAL_DATA_ATTACHED까지 받는다.
func (e *env) connectData(s session) *dataPeer {
	e.t.Helper()
	cred := connectorCred
	if s.ConnectorID == connectorID2 {
		cred = connectorCred2
	}
	return e.connectDataWith(s, cred)
}

// connectDataWith는 지정한 credential로 connectData를 한다.
func (e *env) connectDataWith(s session, cred string) *dataPeer {
	e.t.Helper()
	p, _, err := e.dialData(cred)
	if err != nil {
		e.t.Fatalf("Data WSS dial error = %v", err)
	}
	p.writeText(dataAttachMessage(e.t, s, nil))
	attached := p.readJSON()
	if attached["type"] != "TERMINAL_DATA_ATTACHED" {
		e.t.Fatalf("첫 응답 = %v, want TERMINAL_DATA_ATTACHED", attached)
	}
	return &dataPeer{peer: p, attached: attached}
}

// browserAttachMessage는 TERMINAL_ATTACH다. payloadFields와 fields로 값을 덮어쓴다(nil이면 삭제).
func browserAttachMessage(t *testing.T, s session, fields, payloadFields map[string]any) []byte {
	t.Helper()
	payload := map[string]any{"sessionToken": s.Token, "cols": 120, "rows": 40}
	for k, v := range payloadFields {
		if v == nil {
			delete(payload, k)
		} else {
			payload[k] = v
		}
	}
	msg := map[string]any{
		"type": "TERMINAL_ATTACH", "messageId": "attach-1", "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"terminalSessionId": s.ID, "payload": payload,
	}
	for k, v := range fields {
		if v == nil {
			delete(msg, k)
		} else {
			msg[k] = v
		}
	}
	return marshal(t, msg)
}

func (e *env) dialBrowser(cookie, origin string, protocols ...string) (*peer, *http.Response, error) {
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	if cookie != "" {
		header.Set("Cookie", realtime.SessionCookieName+"="+cookie)
	}
	if protocols == nil {
		protocols = []string{realtime.BrowserSubprotocol}
	}
	dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(e.wsURL(realtime.BrowserPath), header)
	if err != nil {
		return nil, resp, err
	}
	return newPeer(e.t, conn), resp, nil
}

// connectBrowser는 Browser로 attach해 TERMINAL_ATTACHED까지 받는다.
func (e *env) connectBrowser(s session) *browserPeer {
	e.t.Helper()
	p, _, err := e.dialBrowser(s.OwnerCookie, trustedOrigin)
	if err != nil {
		e.t.Fatalf("Browser WSS dial error = %v", err)
	}
	p.writeText(browserAttachMessage(e.t, s, nil, nil))
	attached := p.readJSON()
	if attached["type"] != "TERMINAL_ATTACHED" {
		e.t.Fatalf("첫 응답 = %v, want TERMINAL_ATTACHED", attached)
	}
	return &browserPeer{peer: p, attached: attached}
}

// eventually는 cond가 참이 될 때까지 기다린다. 비동기 전이(timer callback, read loop 정리)를 확인할 때 쓴다.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("시간 안에 %s이(가) 성립하지 않음", what)
}

// payload는 message의 payload object다.
func payload(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	p, ok := msg["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload가 object가 아님: %v", msg)
	}
	return p
}

// readText는 JSON Text message 하나의 원문을 읽는다. 값이 정규화되지 않고 그대로 전달되는지 확인할 때 쓴다.
func (p *peer) readText() string {
	p.t.Helper()
	m, err := p.next(readTimeout)
	if err != nil {
		p.t.Fatalf("ReadMessage() error = %v", err)
	}
	if m.kind != websocket.TextMessage {
		p.t.Fatalf("message kind = %d, want text", m.kind)
	}
	return string(m.data)
}

// received는 받은 message 하나다.
type received struct {
	kind int
	data []byte
}

// collect는 close frame이 올 때까지의 모든 message와 close code를 반환한다.
func (p *peer) collect() ([]received, int) {
	p.t.Helper()
	var all []received
	for {
		m, err := p.next(readTimeout)
		if err == nil {
			all = append(all, m)
			continue
		}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			return all, closeErr.Code
		}
		p.t.Fatalf("close frame 없이 종료: %v", err)
	}
}

// jsonOf는 Text message를 object로 해석한다. Text가 아니면 test를 실패시킨다.
func jsonOf(t *testing.T, m received) map[string]any {
	t.Helper()
	if m.kind != websocket.TextMessage {
		t.Fatalf("message kind = %d (%q), want text", m.kind, m.data)
	}
	var obj map[string]any
	if err := json.Unmarshal(m.data, &obj); err != nil {
		t.Fatalf("JSON decode error = %v (%q)", err, m.data)
	}
	return obj
}
