package connectorwss

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 테스트용 Credential 원문이다. 실제 Credential이 아니며, 응답·frame·log에 나타나면 안 되는 고유 값이다.
const (
	testCredential  = "test-ws-credential-unique-91c2"
	wrongCredential = "test-ws-wrong-credential-unique-44be"
	echoCheckMarker = "test-echo-marker-unique-5d10"
)

// fakeAuth는 Credential 원문 → Principal 표로 인증한다. 표에 없으면 ErrUnauthenticated다.
type fakeAuth struct {
	mu         sync.Mutex
	principals map[string]connector.Principal
	failWith   error
	authCalls  int
}

func (f *fakeAuth) add(credential string, principal connector.Principal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.principals[credential] = principal
}

func (f *fakeAuth) failAll(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = err
}

func (f *fakeAuth) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authCalls
}

func (f *fakeAuth) Authenticate(_ context.Context, credential connector.Credential) (connector.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authCalls++
	if f.failWith != nil {
		return connector.Principal{}, f.failWith
	}
	principal, ok := f.principals[string(credential)]
	if !ok {
		return connector.Principal{}, connector.ErrUnauthenticated
	}
	return principal, nil
}

// syncBuffer는 handler goroutine이 쓰는 log를 test가 안전하게 읽게 한다.
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

type harness struct {
	t         *testing.T
	auth      *fakeAuth
	registry  *connector.Registry
	handler   *Handler
	server    *httptest.Server
	logs      *syncBuffer
	principal connector.Principal
}

func newHarness(t *testing.T, mutate ...func(*Options)) *harness {
	t.Helper()
	principal := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	auth := &fakeAuth{principals: map[string]connector.Principal{testCredential: principal}}
	logs := &syncBuffer{}
	registry := connector.NewRegistry()

	opts := Options{
		Auth:     auth,
		Registry: registry,
		Logger:   slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	handler, err := New(opts)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+Path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		handler.Close()
		server.Close()
	})
	return &harness{t: t, auth: auth, registry: registry, handler: handler, server: server, logs: logs, principal: principal}
}

func (h *harness) url() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + Path
}

func bearer(credential string) http.Header {
	return http.Header{"Authorization": []string{"Bearer " + credential}}
}

// dial은 지정한 header/subprotocol로 Upgrade를 시도한다. 성공하지 못하면 응답(resp)을 함께 반환한다.
func (h *harness) dial(header http.Header, subprotocols ...string) (*websocket.Conn, *http.Response, error) {
	h.t.Helper()
	dialer := websocket.Dialer{Subprotocols: subprotocols, HandshakeTimeout: 5 * time.Second}
	return dialer.Dial(h.url(), header)
}

// connect는 올바른 Credential과 subprotocol로 연결한다.
func (h *harness) connect() *websocket.Conn {
	h.t.Helper()
	conn, _, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err != nil {
		h.t.Fatalf("Dial() error = %v", err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func validHello() map[string]any {
	return map[string]any{
		"type":      "HELLO",
		"messageId": "hello-message-1",
		"sentAt":    time.Now().UTC().Format(time.RFC3339),
		"payload": map[string]any{
			"connectorVersion": "1.2.3",
			"runtimeId":        "runtime-1",
			"startedAt":        time.Now().UTC().Format(time.RFC3339),
		},
	}
}

func payloadOf(msg map[string]any) map[string]any { return msg["payload"].(map[string]any) }

func sendJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

func readJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("frame is not JSON: %v: %s", err, data)
	}
	return msg
}

// readUntilClose는 close frame을 받을 때까지 text frame을 모으고 close 정보를 반환한다.
func readUntilClose(t *testing.T, conn *websocket.Conn) (frames []string, closeErr *websocket.CloseError) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if !errors.As(err, &closeErr) {
				t.Fatalf("connection ended without a close frame: %v", err)
			}
			return frames, closeErr
		}
		frames = append(frames, string(data))
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func (h *harness) waitRegistered() connector.Session {
	h.t.Helper()
	var session connector.Session
	waitFor(h.t, "connector registration", func() bool {
		var ok bool
		session, ok = h.registry.Current(h.principal.ConnectorID)
		return ok
	})
	return session
}

func (h *harness) assertNotRegistered() {
	h.t.Helper()
	if session, ok := h.registry.Current(h.principal.ConnectorID); ok {
		h.t.Fatalf("handshake가 완료되지 않았는데 registry에 남아 있음: %+v", session)
	}
}

// 인증·subprotocol·HELLO가 모두 올바르면 HELLO_ACK을 받고 registry에 인증된 Connector로 등록된다.
func TestHandshakeSucceedsWithHelloAck(t *testing.T) {
	h := newHarness(t)
	conn := h.connect()
	if got := conn.Subprotocol(); got != protocol.SubprotocolControl {
		t.Fatalf("negotiated subprotocol = %q, want %q", got, protocol.SubprotocolControl)
	}

	hello := validHello()
	sendJSON(t, conn, hello)
	ack := readJSON(t, conn)

	if ack["type"] != "HELLO_ACK" {
		t.Fatalf("type = %v, want HELLO_ACK: %v", ack["type"], ack)
	}
	if ack["replyToMessageId"] != hello["messageId"] {
		t.Fatalf("replyToMessageId = %v, want %v", ack["replyToMessageId"], hello["messageId"])
	}
	if id, _ := ack["messageId"].(string); id == "" {
		t.Fatalf("HELLO_ACK messageId가 비어 있음: %v", ack)
	}
	if _, err := time.Parse(time.RFC3339, ack["sentAt"].(string)); err != nil {
		t.Fatalf("sentAt = %v: %v", ack["sentAt"], err)
	}
	payload := payloadOf(ack)
	if payload["heartbeatIntervalSeconds"] != float64(15) || payload["offlineTimeoutSeconds"] != float64(45) {
		t.Fatalf("heartbeat/offline = %v/%v, want 15/45", payload["heartbeatIntervalSeconds"], payload["offlineTimeoutSeconds"])
	}
	if _, err := time.Parse(time.RFC3339, payload["serverTime"].(string)); err != nil {
		t.Fatalf("serverTime = %v: %v", payload["serverTime"], err)
	}

	session := h.waitRegistered()
	if session.ConnectorID != h.principal.ConnectorID {
		t.Fatalf("registered connector = %s, want authenticated %s", session.ConnectorID, h.principal.ConnectorID)
	}

	// Connector가 연결을 끊으면 registry에서 제거된다.
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	_ = conn.Close()
	waitFor(t, "registry release after disconnect", func() bool {
		_, ok := h.registry.Current(h.principal.ConnectorID)
		return !ok
	})
}

func TestHelloAckUsesConfiguredHeartbeatSettings(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.HeartbeatInterval = 20 * time.Second
		o.OfflineTimeout = 60 * time.Second
	})
	conn := h.connect()
	sendJSON(t, conn, validHello())

	payload := payloadOf(readJSON(t, conn))
	if payload["heartbeatIntervalSeconds"] != float64(20) || payload["offlineTimeoutSeconds"] != float64(60) {
		t.Fatalf("heartbeat/offline = %v/%v, want 20/60", payload["heartbeatIntervalSeconds"], payload["offlineTimeoutSeconds"])
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	auth := &fakeAuth{}
	registry := connector.NewRegistry()
	tests := map[string]Options{
		"Authenticator 없음":        {Registry: registry},
		"Registry 없음":             {Auth: auth},
		"1초 미만 HeartbeatInterval": {Auth: auth, Registry: registry, HeartbeatInterval: 500 * time.Millisecond},
		"1초 미만 OfflineTimeout":    {Auth: auth, Registry: registry, OfflineTimeout: time.Millisecond},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(opts); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

// Connector identity는 인증된 Credential에서만 결정한다. HELLO 안의 임의 connector 주장은 identity가 아니다.
func TestConnectorIdentityComesFromCredentialNotHello(t *testing.T) {
	h := newHarness(t)
	claimed := uuid.New()
	conn := h.connect()

	hello := validHello()
	payloadOf(hello)["connectorId"] = claimed.String()
	payloadOf(hello)["runtimeId"] = claimed.String()
	hello["connectorId"] = claimed.String()
	sendJSON(t, conn, hello)
	if got := readJSON(t, conn); got["type"] != "HELLO_ACK" {
		t.Fatalf("type = %v, want HELLO_ACK", got["type"])
	}

	h.waitRegistered()
	if _, ok := h.registry.Current(claimed); ok {
		t.Fatal("HELLO가 주장한 Connector ID로 등록됨")
	}
}

func TestEachConnectorRegistersUnderItsOwnCredentialIdentity(t *testing.T) {
	h := newHarness(t)
	other := connector.Principal{ConnectorID: uuid.New(), OrganizationID: uuid.New(), CredentialID: uuid.New()}
	h.auth.add("test-ws-second-credential-unique", other)

	first := h.connect()
	sendJSON(t, first, validHello())
	readJSON(t, first)

	second, _, err := h.dial(bearer("test-ws-second-credential-unique"), protocol.SubprotocolControl)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	sendJSON(t, second, validHello())
	readJSON(t, second)

	a := h.waitRegistered()
	var b connector.Session
	waitFor(t, "second connector registration", func() bool {
		var ok bool
		b, ok = h.registry.Current(other.ConnectorID)
		return ok
	})
	if a.ID == b.ID || a.ConnectorID == b.ConnectorID {
		t.Fatalf("두 Connector의 Session이 섞임: %+v %+v", a, b)
	}
}

// 인증, Authorization 형식, subprotocol, Upgrade 요청 형식이 잘못되면 WebSocket connection이 성립하지 않는다.
func TestUpgradeIsRejectedBeforeWebSocketIsEstablished(t *testing.T) {
	multiAuth := http.Header{"Authorization": []string{"Bearer " + testCredential, "Bearer " + testCredential}}
	tests := []struct {
		name         string
		header       http.Header
		subprotocols []string
		wantStatus   int
	}{
		{"Authorization 없음", http.Header{}, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"Bearer가 아닌 scheme", http.Header{"Authorization": []string{"Basic " + testCredential}}, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"token 없는 Bearer", http.Header{"Authorization": []string{"Bearer"}}, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"빈 token", http.Header{"Authorization": []string{"Bearer "}}, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"token 안의 공백", http.Header{"Authorization": []string{"Bearer " + testCredential + " extra"}}, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"중복 Authorization", multiAuth, []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"존재하지 않거나 revoke된 Credential", bearer(wrongCredential), []string{protocol.SubprotocolControl}, http.StatusUnauthorized},
		{"subprotocol 없음", bearer(testCredential), nil, http.StatusBadRequest},
		{"다른 subprotocol", bearer(testCredential), []string{"labbit.connector.v2"}, http.StatusBadRequest},
		{"Terminal Data subprotocol", bearer(testCredential), []string{protocol.SubprotocolTerminalData}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			conn, resp, err := h.dial(tt.header, tt.subprotocols...)
			if err == nil {
				_ = conn.Close()
				t.Fatal("Upgrade가 성공함")
			}
			if !errors.Is(err, websocket.ErrBadHandshake) || resp == nil {
				t.Fatalf("Dial() error = %v, resp = %v, want bad handshake response", err, resp)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q, want Bearer", resp.Header.Get("WWW-Authenticate"))
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if strings.Contains(string(body), testCredential) || strings.Contains(string(body), wrongCredential) {
				t.Fatalf("응답 body가 Credential을 노출함: %s", body)
			}
			h.assertNotRegistered()
		})
	}
}

func TestRevokedCredentialAuthenticationFailureIsUnauthorized(t *testing.T) {
	h := newHarness(t)
	h.auth.failAll(connector.ErrUnauthenticated)

	_, resp, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Dial() error = %v, resp = %v, want 401", err, resp)
	}
	h.assertNotRegistered()
}

// 저장소 장애는 401이 아닌 503이고, 오류 원문은 응답·log에 나타나지 않는다.
func TestAuthenticationDependencyFailureIsUnavailableWithoutLeakingDetails(t *testing.T) {
	h := newHarness(t)
	h.auth.failAll(errors.New("pq: password authentication failed for user test-internal-db-detail"))

	_, resp, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Dial() error = %v, resp = %v, want 503", err, resp)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, leaked := range []string{"test-internal-db-detail", "password authentication", testCredential} {
		if strings.Contains(string(body), leaked) || strings.Contains(h.logs.String(), leaked) {
			t.Fatalf("%q가 응답 또는 log에 노출됨: body=%s logs=%s", leaked, body, h.logs.String())
		}
	}
	h.assertNotRegistered()
}

func TestNonWebSocketRequestIsRejectedWithoutAuthentication(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequest(http.MethodGet, h.server.URL+Path, nil)
	req.Header.Set("Authorization", "Bearer "+testCredential)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := h.auth.calls(); got != 0 {
		t.Fatalf("WebSocket 요청이 아닌데 인증 조회 %d회 수행", got)
	}
}

// HELLO가 계약에 맞지 않으면 fatal ERROR와 4004 close로 끝나고, 정상 active connection으로 남지 않는다.
func TestInvalidFirstMessageIsRejectedAndNotRegistered(t *testing.T) {
	mutate := func(fn func(map[string]any)) func() any {
		return func() any {
			m := validHello()
			fn(m)
			return m
		}
	}
	delKey := func(m map[string]any, key string) { delete(m, key) }
	const zeroTime = "0001-01-01T00:00:00Z"

	tests := []struct {
		name     string
		message  func() any
		wantCode string
		binary   bool
	}{
		{"JSON이 아님", func() any { return "{not-json " + echoCheckMarker }, errorCodeInvalidMessage, false},
		{"JSON 배열", func() any { return "[\"" + echoCheckMarker + "\"]" }, errorCodeInvalidMessage, false},
		{"Binary frame", func() any { return "binary-" + echoCheckMarker }, errorCodeInvalidMessage, true},
		{"HELLO가 아닌 최초 message", mutate(func(m map[string]any) {
			m["type"] = "HEARTBEAT"
			m["payload"] = map[string]any{"observedAt": time.Now().UTC().Format(time.RFC3339)}
		}), errorCodeUnsupportedMessageType, false},
		{"type이 문자열이 아님", mutate(func(m map[string]any) { m["type"] = 7 }), errorCodeInvalidMessage, false},
		{"type 없음", mutate(func(m map[string]any) { delKey(m, "type") }), errorCodeUnsupportedMessageType, false},
		{"messageId 없음", mutate(func(m map[string]any) { delKey(m, "messageId") }), errorCodeInvalidMessage, false},
		{"빈 messageId", mutate(func(m map[string]any) { m["messageId"] = "" }), errorCodeInvalidMessage, false},
		{"messageId가 문자열이 아님", mutate(func(m map[string]any) { m["messageId"] = 12 }), errorCodeInvalidMessage, false},
		{"sentAt 없음", mutate(func(m map[string]any) { delKey(m, "sentAt") }), errorCodeInvalidMessage, false},
		{"zero sentAt", mutate(func(m map[string]any) { m["sentAt"] = zeroTime }), errorCodeInvalidMessage, false},
		{"잘못된 sentAt 형식", mutate(func(m map[string]any) { m["sentAt"] = "yesterday " + echoCheckMarker }), errorCodeInvalidMessage, false},
		{"payload 없음", mutate(func(m map[string]any) { delKey(m, "payload") }), errorCodeInvalidMessage, false},
		{"payload null", mutate(func(m map[string]any) { m["payload"] = nil }), errorCodeInvalidMessage, false},
		{"payload 배열", mutate(func(m map[string]any) { m["payload"] = []any{} }), errorCodeInvalidMessage, false},
		{"connectorVersion 없음", mutate(func(m map[string]any) { delKey(payloadOf(m), "connectorVersion") }), errorCodeInvalidMessage, false},
		{"빈 connectorVersion", mutate(func(m map[string]any) { payloadOf(m)["connectorVersion"] = "" }), errorCodeInvalidMessage, false},
		{"runtimeId 없음", mutate(func(m map[string]any) { delKey(payloadOf(m), "runtimeId") }), errorCodeInvalidMessage, false},
		{"빈 runtimeId", mutate(func(m map[string]any) { payloadOf(m)["runtimeId"] = "" }), errorCodeInvalidMessage, false},
		{"startedAt 없음", mutate(func(m map[string]any) { delKey(payloadOf(m), "startedAt") }), errorCodeInvalidMessage, false},
		{"zero startedAt", mutate(func(m map[string]any) { payloadOf(m)["startedAt"] = zeroTime }), errorCodeInvalidMessage, false},
		{"capabilities가 배열이 아님", mutate(func(m map[string]any) { payloadOf(m)["capabilities"] = "terminal" }), errorCodeInvalidMessage, false},
		{"capabilities null", mutate(func(m map[string]any) { payloadOf(m)["capabilities"] = nil }), errorCodeInvalidMessage, false},
		{"capabilities 중복", mutate(func(m map[string]any) { payloadOf(m)["capabilities"] = []string{"a", "a"} }), errorCodeInvalidMessage, false},
		{"capabilities 요소가 문자열이 아님", mutate(func(m map[string]any) { payloadOf(m)["capabilities"] = []any{"a", 1} }), errorCodeInvalidMessage, false},
		{"capabilities 요소 null", mutate(func(m map[string]any) { payloadOf(m)["capabilities"] = []any{"a", nil} }), errorCodeInvalidMessage, false},

		// Schema가 정의한 선택 Envelope field는 없으면 유효하지만, 있으면 제약을 만족해야 한다.
		{"requestId가 number", mutate(func(m map[string]any) { m["requestId"] = 42 }), errorCodeInvalidMessage, false},
		{"빈 requestId", mutate(func(m map[string]any) { m["requestId"] = "" }), errorCodeInvalidMessage, false},
		{"requestId null", mutate(func(m map[string]any) { m["requestId"] = nil }), errorCodeInvalidMessage, false},
		{"requestId가 object", mutate(func(m map[string]any) { m["requestId"] = map[string]any{"k": echoCheckMarker} }), errorCodeInvalidMessage, false},
		{"operationId가 boolean", mutate(func(m map[string]any) { m["operationId"] = false }), errorCodeInvalidMessage, false},
		{"빈 operationId", mutate(func(m map[string]any) { m["operationId"] = "" }), errorCodeInvalidMessage, false},
		{"labInstanceId가 배열", mutate(func(m map[string]any) { m["labInstanceId"] = []any{echoCheckMarker} }), errorCodeInvalidMessage, false},
		{"빈 labInstanceId", mutate(func(m map[string]any) { m["labInstanceId"] = "" }), errorCodeInvalidMessage, false},
		{"replyToMessageId가 number", mutate(func(m map[string]any) { m["replyToMessageId"] = 1 }), errorCodeInvalidMessage, false},
		{"빈 replyToMessageId", mutate(func(m map[string]any) { m["replyToMessageId"] = "" }), errorCodeInvalidMessage, false},
		{"generation 0", mutate(func(m map[string]any) { m["generation"] = 0 }), errorCodeInvalidMessage, false},
		{"음수 generation", mutate(func(m map[string]any) { m["generation"] = -1 }), errorCodeInvalidMessage, false},
		{"소수 generation", mutate(func(m map[string]any) { m["generation"] = 1.5 }), errorCodeInvalidMessage, false},
		{"1 미만 소수 generation", mutate(func(m map[string]any) { m["generation"] = json.RawMessage("0.9") }), errorCodeInvalidMessage, false},
		{"지수 표기로 1 미만인 generation", mutate(func(m map[string]any) { m["generation"] = json.RawMessage("1e-1") }), errorCodeInvalidMessage, false},
		{"string generation", mutate(func(m map[string]any) { m["generation"] = "1" }), errorCodeInvalidMessage, false},
		{"generation null", mutate(func(m map[string]any) { m["generation"] = nil }), errorCodeInvalidMessage, false},
		{"generation boolean", mutate(func(m map[string]any) { m["generation"] = true }), errorCodeInvalidMessage, false},
		{"잘못된 Trace와 함께 잘못된 generation", mutate(func(m map[string]any) {
			m["traceparent"] = "not-a-trace"
			m["generation"] = 0
		}), errorCodeInvalidMessage, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			conn := h.connect()

			switch msg := tt.message().(type) {
			case string:
				kind := websocket.TextMessage
				if tt.binary {
					kind = websocket.BinaryMessage
				}
				if err := conn.WriteMessage(kind, []byte(msg)); err != nil {
					t.Fatal(err)
				}
			default:
				sendJSON(t, conn, msg)
			}

			frames, closeErr := readUntilClose(t, conn)
			if closeErr.Code != closeProtocolError {
				t.Fatalf("close code = %d, want %d", closeErr.Code, closeProtocolError)
			}
			if len(frames) != 1 {
				t.Fatalf("close 전 frame = %v, want 단일 ERROR", frames)
			}
			var errMsg map[string]any
			if err := json.Unmarshal([]byte(frames[0]), &errMsg); err != nil {
				t.Fatal(err)
			}
			payload := payloadOf(errMsg)
			if errMsg["type"] != "ERROR" || payload["code"] != tt.wantCode || payload["fatal"] != true {
				t.Fatalf("ERROR = %v, want code %s fatal", errMsg, tt.wantCode)
			}
			if id, _ := errMsg["messageId"].(string); id == "" {
				t.Fatalf("ERROR messageId가 비어 있음: %v", errMsg)
			}
			// 입력 값은 응답과 log에 되돌려 주지 않는다.
			for _, out := range []string{frames[0], closeErr.Text, h.logs.String()} {
				if strings.Contains(out, echoCheckMarker) || strings.Contains(out, testCredential) {
					t.Fatalf("입력 또는 Credential이 노출됨: %s", out)
				}
			}
			h.assertNotRegistered()
		})
	}
}

// 계약은 알 수 없는 선택 field와 잘못된 Trace 값 때문에 업무 Envelope를 거절하지 않도록 요구한다.
func TestHelloToleratesUnknownOptionalFieldsAndInvalidTraceMetadata(t *testing.T) {
	tests := map[string]func(map[string]any){
		"알 수 없는 envelope field":  func(m map[string]any) { m["futureField"] = map[string]any{"a": 1} },
		"알 수 없는 payload field":   func(m map[string]any) { payloadOf(m)["futureField"] = []int{1} },
		"타입이 잘못된 traceparent":    func(m map[string]any) { m["traceparent"] = 123 },
		"W3C 형식이 아닌 traceparent": func(m map[string]any) { m["traceparent"] = "not-a-trace" },
		"512자 초과 traceparent":    func(m map[string]any) { m["traceparent"] = strings.Repeat("a", 600) },
		"타입이 잘못된 tracestate":     func(m map[string]any) { m["tracestate"] = []string{"x"} },
		"1024자 초과 tracestate":    func(m map[string]any) { m["tracestate"] = strings.Repeat("a", 2000) },
		"replyToMessageId 포함":    func(m map[string]any) { m["replyToMessageId"] = "previous" },
		"유효한 capabilities":       func(m map[string]any) { payloadOf(m)["capabilities"] = []string{"terminal", "preview"} },
		"빈 capabilities":         func(m map[string]any) { payloadOf(m)["capabilities"] = []string{} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			conn := h.connect()
			hello := validHello()
			mutate(hello)
			sendJSON(t, conn, hello)

			ack := readJSON(t, conn)
			if ack["type"] != "HELLO_ACK" || ack["replyToMessageId"] != hello["messageId"] {
				t.Fatalf("ack = %v, want HELLO_ACK replying to %v", ack, hello["messageId"])
			}
			h.waitRegistered()
		})
	}
}

// Schema가 정의한 선택 Envelope field는 유효한 값이면 받아들이고, HELLO_ACK은 HELLO의 messageId에 응답한다.
func TestHelloAcceptsValidKnownOptionalEnvelopeFields(t *testing.T) {
	tests := map[string]func(map[string]any){
		"requestId":            func(m map[string]any) { m["requestId"] = "req-1" },
		"operationId":          func(m map[string]any) { m["operationId"] = "op-1" },
		"labInstanceId":        func(m map[string]any) { m["labInstanceId"] = "lab-1" },
		"replyToMessageId":     func(m map[string]any) { m["replyToMessageId"] = "msg-0" },
		"generation 1":         func(m map[string]any) { m["generation"] = 1 },
		"generation 2":         func(m map[string]any) { m["generation"] = 2 },
		"소수부가 0인 generation":   func(m map[string]any) { m["generation"] = json.RawMessage("1.0") },
		"지수 표기 generation":     func(m map[string]any) { m["generation"] = json.RawMessage("1e2") },
		"int64를 넘는 generation": func(m map[string]any) { m["generation"] = json.RawMessage("10000000000000000000000") },
		"모든 선택 field": func(m map[string]any) {
			m["replyToMessageId"] = "msg-0"
			m["requestId"] = "req-1"
			m["operationId"] = "op-1"
			m["labInstanceId"] = "lab-1"
			m["generation"] = 1
		},
		"유효한 field와 잘못된 Trace": func(m map[string]any) {
			m["requestId"] = "req-1"
			m["generation"] = 1
			m["traceparent"] = "not-a-trace"
			m["tracestate"] = 7
		},
		// Schema의 property 이름은 대소문자를 구분하므로 정의되지 않은 알 수 없는 field로 무시한다.
		"대소문자만 다른 이름은 알 수 없는 field": func(m map[string]any) {
			m["RequestId"] = 42
			m["Generation"] = 0
			m["GENERATION"] = "x"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			conn := h.connect()
			hello := validHello()
			mutate(hello)
			sendJSON(t, conn, hello)

			ack := readJSON(t, conn)
			if ack["type"] != "HELLO_ACK" || ack["replyToMessageId"] != hello["messageId"] {
				t.Fatalf("ack = %v, want HELLO_ACK replying to %v", ack, hello["messageId"])
			}
			h.waitRegistered()
		})
	}
}

// generation 판정은 JSON Schema 2020-12 integer(minimum 1)와 같고, 지수가 과도한 표기는 거절한다.
func TestIntegerAtLeastOne(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"2", true},
		{"1.0", true},
		{"1e2", true},
		{"10000000000000000000000", true},
		{"0", false},
		{"-0", false},
		{"-1", false},
		{"0.5", false},
		{"1.5", false},
		{"1e-1", false},
		{"1.00000000000000000001", false},
		{"1e999999999", false},
		{`"1"`, false},
		{"null", false},
		{"true", false},
		{"[1]", false},
		{"{}", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := integerAtLeastOne(json.RawMessage(tt.raw)); got != tt.want {
				t.Fatalf("integerAtLeastOne(%s) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// padToSize는 HELLO가 JSON으로 직렬화되었을 때 정확히 size byte가 되도록 payload에 padding을 채운다.
func padToSize(t *testing.T, size int) []byte {
	t.Helper()
	hello := validHello()
	payloadOf(hello)["padding"] = ""
	base, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	payloadOf(hello)["padding"] = strings.Repeat("x", size-len(base))
	data, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != size {
		t.Fatalf("padded message = %d bytes, want %d", len(data), size)
	}
	return data
}

// 1 MiB 이하의 JSON Text message는 처리하고, 1 byte라도 넘으면 JSON을 해석하기 전에 1009로 종료한다.
func TestControlMessageSizeLimit(t *testing.T) {
	limit := int(protocol.MaxJSONMessageSize)

	t.Run("정확히 1 MiB인 HELLO는 수락", func(t *testing.T) {
		h := newHarness(t)
		conn := h.connect()
		if err := conn.WriteMessage(websocket.TextMessage, padToSize(t, limit)); err != nil {
			t.Fatal(err)
		}
		if ack := readJSON(t, conn); ack["type"] != "HELLO_ACK" {
			t.Fatalf("type = %v, want HELLO_ACK", ack["type"])
		}
	})

	t.Run("1 MiB 초과 HELLO는 1009", func(t *testing.T) {
		h := newHarness(t)
		conn := h.connect()
		// 유효한 HELLO 구조여도 크기 제한이 먼저 적용된다.
		if err := conn.WriteMessage(websocket.TextMessage, padToSize(t, limit+1)); err != nil {
			t.Fatal(err)
		}
		frames, closeErr := readUntilClose(t, conn)
		if closeErr.Code != protocol.CloseMessageTooBig {
			t.Fatalf("close code = %d, want %d", closeErr.Code, protocol.CloseMessageTooBig)
		}
		if len(frames) != 0 {
			t.Fatalf("크기 초과 message에 별도 frame을 보냄: %v", frames)
		}
		h.assertNotRegistered()
	})

	t.Run("HELLO 이후 1 MiB 초과 message도 1009", func(t *testing.T) {
		h := newHarness(t)
		conn := h.connect()
		sendJSON(t, conn, validHello())
		readJSON(t, conn)
		h.waitRegistered()

		if err := conn.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("a"), limit+1)); err != nil {
			t.Fatal(err)
		}
		_, closeErr := readUntilClose(t, conn)
		if closeErr.Code != protocol.CloseMessageTooBig {
			t.Fatalf("close code = %d, want %d", closeErr.Code, protocol.CloseMessageTooBig)
		}
		waitFor(t, "registry release after 1009", func() bool {
			_, ok := h.registry.Current(h.principal.ConnectorID)
			return !ok
		})
	})
}

func TestMissingHelloTimesOutAndIsNotRegistered(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.HelloTimeout = 100 * time.Millisecond })
	conn := h.connect()

	_, closeErr := readUntilClose(t, conn)
	if closeErr.Code != closeProtocolError {
		t.Fatalf("close code = %d, want %d", closeErr.Code, closeProtocolError)
	}
	h.assertNotRegistered()
}

// HELLO_ACK 이후에는 메시지를 해석하지 않으므로, 어떤 message를 보내도 registry 상태와 연결이 유지된다.
func TestMessagesAfterHandshakeAreNotInterpreted(t *testing.T) {
	h := newHarness(t)
	conn := h.connect()
	sendJSON(t, conn, validHello())
	readJSON(t, conn)
	session := h.waitRegistered()

	sendJSON(t, conn, map[string]any{"type": "HEARTBEAT", "messageId": "hb-1", "sentAt": time.Now().UTC().Format(time.RFC3339), "payload": map[string]any{}})
	sendJSON(t, conn, map[string]any{"type": "OPERATION_RESULT", "messageId": "r-1", "payload": map[string]any{}})
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	// 같은 연결의 ping이 pong으로 돌아오면 read loop가 위 message들을 처리하고 계속 살아 있다.
	pong := make(chan struct{}, 1)
	conn.SetPongHandler(func(string) error { pong <- struct{}{}; return nil })
	if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	go func() { _, _, _ = conn.ReadMessage() }() // pong 처리를 위해 read를 진행한다.
	select {
	case <-pong:
	case <-time.After(5 * time.Second):
		t.Fatal("ping에 대한 pong이 없음: 연결이 닫혔거나 read loop가 멈춤")
	}
	if got, ok := h.registry.Current(h.principal.ConnectorID); !ok || got != session {
		t.Fatalf("registry = %+v, %v, want unchanged %+v", got, ok, session)
	}
}

// 성공·실패 경로 전체에서 Credential 원문, Authorization Header가 응답·frame·log에 나타나지 않는다.
func TestCredentialAndAuthorizationAreNeverExposed(t *testing.T) {
	h := newHarness(t)
	var exposed []string

	collect := func(resp *http.Response) {
		if resp == nil {
			return
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		exposed = append(exposed, string(body), fmt.Sprint(resp.Header))
	}

	// 인증 실패 / subprotocol 실패
	_, resp, _ := h.dial(bearer(wrongCredential), protocol.SubprotocolControl)
	collect(resp)
	_, resp, _ = h.dial(bearer(testCredential))
	collect(resp)

	// 잘못된 HELLO
	bad := h.connect()
	if err := bad.WriteMessage(websocket.TextMessage, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	frames, closeErr := readUntilClose(t, bad)
	exposed = append(exposed, strings.Join(frames, "\n"), closeErr.Text)

	// 성공한 HELLO
	ok := h.connect()
	sendJSON(t, ok, validHello())
	exposed = append(exposed, fmt.Sprint(readJSON(t, ok)))
	h.waitRegistered()
	_ = ok.Close()
	waitFor(t, "registry release", func() bool {
		_, present := h.registry.Current(h.principal.ConnectorID)
		return !present
	})

	exposed = append(exposed, h.logs.String())
	if h.logs.String() == "" {
		t.Fatal("log가 비어 있어 검증이 무의미함")
	}
	for _, out := range exposed {
		for _, secret := range []string{testCredential, wrongCredential, "Bearer " + testCredential, "Bearer " + wrongCredential} {
			if strings.Contains(out, secret) {
				t.Fatalf("%q가 노출됨: %s", secret, out)
			}
		}
	}
	// 인증에 사용한 값은 redact된 형태로만 log에 올 수 있다.
	if strings.Contains(h.logs.String(), "Authorization") && strings.Contains(h.logs.String(), "Bearer") {
		t.Fatalf("log가 Authorization header를 기록함: %s", h.logs.String())
	}
}

// Close 이후에는 새 Upgrade를 거절하고 열려 있는 connection은 1001로 닫아 registry에서 제거한다.
func TestCloseRejectsNewUpgradesAndDrainsConnections(t *testing.T) {
	h := newHarness(t)
	conn := h.connect()
	sendJSON(t, conn, validHello())
	readJSON(t, conn)
	h.waitRegistered()

	h.handler.Close()
	h.handler.Close() // 중복 Close도 안전하다.

	_, closeErr := readUntilClose(t, conn)
	if closeErr.Code != websocket.CloseGoingAway {
		t.Fatalf("close code = %d, want %d", closeErr.Code, websocket.CloseGoingAway)
	}
	waitFor(t, "registry release after Close", func() bool {
		_, ok := h.registry.Current(h.principal.ConnectorID)
		return !ok
	})

	_, resp, err := h.dial(bearer(testCredential), protocol.SubprotocolControl)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Close 후 Dial() error = %v, resp = %v, want 503", err, resp)
	}
}

func TestBearerCredentialParsing(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
		ok     bool
	}{
		{"정상", []string{"Bearer abc.DEF-123_~+/="}, "abc.DEF-123_~+/=", true},
		{"scheme 대소문자 무시", []string{"bearer abc"}, "abc", true},
		{"header 없음", nil, "", false},
		{"중복", []string{"Bearer a", "Bearer b"}, "", false},
		{"다른 scheme", []string{"Basic abc"}, "", false},
		{"token 없음", []string{"Bearer"}, "", false},
		{"빈 token", []string{"Bearer "}, "", false},
		{"선행 공백", []string{"Bearer  abc"}, "", false},
		{"token 안의 공백", []string{"Bearer a b"}, "", false},
		{"tab", []string{"Bearer a\tb"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			for _, v := range tt.values {
				header.Add("Authorization", v)
			}
			got, ok := bearerCredential(header)
			if ok != tt.ok || string(got) != tt.want {
				t.Fatalf("bearerCredential() = %q, %v, want %q, %v", string(got), ok, tt.want, tt.ok)
			}
		})
	}
}
