package preview

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestAttachBindsTheExactPendingSessionAndAcknowledges(t *testing.T) {
	e := newEnv(t)
	x := e.expect()
	attached, ended, ok := e.gw.Pending(x.SessionID)
	if !ok {
		t.Fatal("Pending()이 등록한 PreviewSession을 찾지 못함")
	}
	select {
	case <-attached:
		t.Fatal("attach 전에 attached가 닫힘")
	default:
	}

	e.attachAndServe(x)
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("attach 뒤 attached가 닫히지 않음")
	}
	select {
	case <-ended:
		t.Fatal("정상 attach가 PreviewSession을 끝냄")
	default:
	}
	// attach만으로는 Browser가 쓸 수 없다. Activate 전에는 URL이 없고 Origin은 요청을 받지 않는다.
	if resp, _ := e.get(x, nil, "/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Activate 전 요청 status = %d, want 401", resp.StatusCode)
	}
}

func TestDataUpgradeAuthentication(t *testing.T) {
	e := newEnv(t)
	other := uuid.New()
	e.auth.known["other-credential"] = ConnectorIdentity{ConnectorID: other, CredentialID: uuid.New()}

	dialRaw := func(mod func(*http.Request)) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, e.dataServer.URL+DataPath, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		req.Header.Set("Sec-WebSocket-Protocol", previewSubprotocol)
		mod(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	cases := map[string]struct {
		mod  func(*http.Request)
		want int
	}{
		"Authorization 없음":  {func(*http.Request) {}, http.StatusUnauthorized},
		"알 수 없는 Credential": {func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		"Bearer가 아님":        {func(r *http.Request) { r.Header.Set("Authorization", "Basic abc") }, http.StatusUnauthorized},
		"Credential이 비어 있음": {func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }, http.StatusUnauthorized},
		"Credential에 공백":    {func(r *http.Request) { r.Header.Set("Authorization", "Bearer a b") }, http.StatusUnauthorized},
		"Authorization이 둘": {func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer "+testCredential)
			r.Header.Add("Authorization", "Bearer "+testCredential)
		}, http.StatusUnauthorized},
		"subprotocol 없음": {func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+testCredential)
			r.Header.Del("Sec-WebSocket-Protocol")
		}, http.StatusBadRequest},
		"다른 subprotocol": {func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+testCredential)
			r.Header.Set("Sec-WebSocket-Protocol", "labbit.connector-file.v1")
		}, http.StatusBadRequest},
		"WebSocket Upgrade가 아님": {func(r *http.Request) {
			r.Header.Del("Upgrade")
			r.Header.Set("Authorization", "Bearer "+testCredential)
		}, http.StatusBadRequest},
	}
	for name, tc := range cases {
		if got := dialRaw(tc.mod).StatusCode; got != tc.want {
			t.Errorf("%s: status = %d, want %d", name, got, tc.want)
		}
	}

	// 인증 의존성 장애는 인증 실패(401)가 아니라 사용 불가(503)다.
	e.auth.mu.Lock()
	e.auth.failure = ErrDependencyUnavailable
	e.auth.mu.Unlock()
	if got := dialRaw(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testCredential) }).StatusCode; got != http.StatusServiceUnavailable {
		t.Errorf("의존성 장애 status = %d, want 503", got)
	}
	e.auth.mu.Lock()
	e.auth.failure = errors.New("storage exploded")
	e.auth.mu.Unlock()
	if got := dialRaw(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testCredential) }).StatusCode; got != http.StatusServiceUnavailable {
		t.Errorf("알 수 없는 인증 오류 status = %d, want 503", got)
	}
	if strings.Contains(e.logs.String(), "storage exploded") {
		t.Error("저장소 오류 원문을 기록함")
	}
}

// attach가 거절되는 모든 경우에 그 connection만 close 1008로 끝나고 기다리던 PreviewSession은 건드리지 않는다.
func TestMismatchedAttachIsRejectedAndLeavesThePendingSessionIntact(t *testing.T) {
	e := newEnv(t)
	otherConnector := uuid.New()
	e.auth.known["other-connector"] = ConnectorIdentity{ConnectorID: otherConnector, CredentialID: uuid.New()}
	// 같은 Connector의 다른 Credential이다(예: 교체 전에 쓰던 Credential). PREVIEW_OPEN을 전달한 Control Session의 Credential이 아니다.
	e.auth.known["other-credential"] = ConnectorIdentity{ConnectorID: e.connectorID, CredentialID: uuid.New()}

	x := e.expect()

	cases := []struct {
		name       string
		credential string
		frame      map[string]any
	}{
		{"알 수 없는 previewSessionId", testCredential, attachFrame(x, map[string]any{"previewSessionId": uuid.NewString()})},
		{"다른 PreviewSession의 ID", testCredential, attachFrame(x, map[string]any{"previewSessionId": e.expected().SessionID})},
		{"다른 labInstanceId", testCredential, attachFrame(x, map[string]any{"labInstanceId": "lab-2"})},
		{"다른 generation", testCredential, attachFrame(x, map[string]any{"generation": 2})},
		{"더 큰 generation", testCredential, attachFrame(x, map[string]any{"generation": 4})},
		{"int64를 넘는 generation", testCredential, attachFrame(x, map[string]any{"generation": 1e30})},
		{"다른 targetVmKey", testCredential, mutated(x, "targetVmKey", "vk-db")},
		{"다른 providerServerId", testCredential, mutated(x, "providerServerId", "srv-web-g2")},
		{"다른 targetPort", testCredential, mutated(x, "targetPort", 5174)},
		{"SSH 관리 port", testCredential, mutated(x, "targetPort", 22)},
		{"다른 Connector의 Credential", "other-connector", attachFrame(x, nil)},
		{"같은 Connector의 다른 Credential", "other-credential", attachFrame(x, nil)},
	}
	for _, tc := range cases {
		ws, _, err := e.dialData(tc.credential)
		if err != nil {
			t.Fatalf("%s: Data WSS 연결 실패: %v", tc.name, err)
		}
		sendJSON(t, ws, tc.frame)
		if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
			t.Errorf("%s: close code = %d, want 1008", tc.name, code)
		}
		ws.Close()
	}

	// PreviewSession은 pending 그대로이며 올바른 attach는 여전히 성공한다.
	attached, ended, _ := e.gw.Pending(x.SessionID)
	select {
	case <-ended:
		t.Fatal("거절된 attach가 PreviewSession을 끝냄")
	case <-attached:
		t.Fatal("거절된 attach가 PreviewSession을 attach시킴")
	default:
	}
	e.attachAndServe(x)
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Fatal("거절 뒤의 정상 attach가 성립하지 않음")
	}
}

// mutated는 PREVIEW_ATTACH payload의 field 하나를 바꾼다.
func mutated(x Expected, key string, value any) map[string]any {
	msg := attachFrame(x, nil)
	payloadOf(msg)[key] = value
	return msg
}

func TestMalformedAttachFramesAreProtocolViolations(t *testing.T) {
	e := newEnv(t)
	x := e.expect()

	drop := func(key string) map[string]any {
		msg := attachFrame(x, nil)
		delete(payloadOf(msg), key)
		return msg
	}
	cases := map[string]map[string]any{
		"type이 PREVIEW_ATTACH가 아님":  attachFrame(x, map[string]any{"type": "PREVIEW_ATTACHED"}),
		"type 소문자":                  attachFrame(x, map[string]any{"type": "preview_attach"}),
		"messageId 없음":              attachFrame(x, map[string]any{"messageId": nil}),
		"sentAt 형식 오류":              attachFrame(x, map[string]any{"sentAt": "yesterday"}),
		"previewSessionId 없음":       attachFrame(x, map[string]any{"previewSessionId": nil}),
		"previewSessionId 빈 문자열":    attachFrame(x, map[string]any{"previewSessionId": ""}),
		"previewSessionId가 문자열이 아님": attachFrame(x, map[string]any{"previewSessionId": 7}),
		"labInstanceId 없음":          attachFrame(x, map[string]any{"labInstanceId": nil}),
		"generation 없음":             attachFrame(x, map[string]any{"generation": nil}),
		"generation 0":              attachFrame(x, map[string]any{"generation": 0}),
		"generation 문자열":            attachFrame(x, map[string]any{"generation": "3"}),
		"replyToMessageId 빈 문자열":    attachFrame(x, map[string]any{"replyToMessageId": ""}),
		"payload가 object가 아님":       attachFrame(x, map[string]any{"payload": "x"}),
		"payload 없음":                attachFrame(x, map[string]any{"payload": nil}),
		"runtimeId 없음":              drop("runtimeId"),
		"targetVmKey 없음":            drop("targetVmKey"),
		"providerServerId 없음":       drop("providerServerId"),
		"targetPort 없음":             drop("targetPort"),
		"targetPort 0":              mutated(x, "targetPort", 0),
		"targetPort 음수":             mutated(x, "targetPort", -5173),
		"targetPort 65536":          mutated(x, "targetPort", 65536),
		"targetPort 소수":             mutated(x, "targetPort", 5173.5),
		"targetPort 문자열":            mutated(x, "targetPort", "5173"),
		"targetPort null":           mutated(x, "targetPort", nil),
		"targetVmKey가 빈 문자열":        mutated(x, "targetVmKey", ""),
		"providerServerId가 문자열이 아님": mutated(x, "providerServerId", 7),
	}
	for name, frame := range cases {
		ws, _, err := e.dialData(testCredential)
		if err != nil {
			t.Fatalf("%s: Data WSS 연결 실패: %v", name, err)
		}
		sendJSON(t, ws, frame)
		if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
			t.Errorf("%s: close code = %d, want 1008", name, code)
		}
		ws.Close()
	}

	// JSON이 아닌 frame과 attach 전의 Binary frame도 protocol 위반이다.
	for name, send := range map[string]func(*websocket.Conn){
		"JSON이 아님": func(ws *websocket.Conn) { _ = ws.WriteMessage(websocket.TextMessage, []byte("not json")) },
		"JSON 배열":  func(ws *websocket.Conn) { _ = ws.WriteMessage(websocket.TextMessage, []byte("[]")) },
		"attach 전 Binary": func(ws *websocket.Conn) {
			_ = ws.WriteMessage(websocket.BinaryMessage, []byte("GET / HTTP/1.1\r\n\r\n"))
		},
	} {
		ws, _, err := e.dialData(testCredential)
		if err != nil {
			t.Fatal(err)
		}
		send(ws)
		if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
			t.Errorf("%s: close code = %d, want 1008", name, code)
		}
		ws.Close()
	}

	// PreviewSession은 이 모든 거절 뒤에도 pending이다.
	attached, ended, _ := e.gw.Pending(x.SessionID)
	select {
	case <-ended:
		t.Fatal("거절된 frame이 PreviewSession을 끝냄")
	case <-attached:
		t.Fatal("거절된 frame이 PreviewSession을 attach시킴")
	default:
	}
}

// Schema에서 integer는 5173.0도 포함한다(JSON Schema 2020-12). 그 표기도 같은 port다.
func TestAttachAcceptsAnIntegerValuedNumberForTargetPort(t *testing.T) {
	e := newEnv(t)
	x := e.expect()
	ws, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	frame, _ := json.Marshal(attachFrame(x, nil))
	raw := strings.Replace(string(frame), `"targetPort":5173`, `"targetPort":5173.0`, 1)
	if raw == string(frame) {
		t.Fatal("test frame을 바꾸지 못함")
	}
	if err := ws.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, data, err := ws.ReadMessage(); err != nil || !strings.Contains(string(data), "PREVIEW_ATTACHED") {
		t.Fatalf("PREVIEW_ATTACHED 수신 실패: %v %s", err, data)
	}
}

// Control Session에 묶이기 전(PREVIEW_OPEN을 보내기 전)에는 어떤 Data WSS도 attach할 수 없다.
func TestAttachBeforeTheSessionIsBoundToAControlSessionIsRejected(t *testing.T) {
	e := newEnv(t)
	x := e.expected()
	if err := e.gw.Expect(x); err != nil {
		t.Fatal(err)
	}
	ws, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	sendJSON(t, ws, attachFrame(x, nil))
	if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
	if !strings.Contains(e.logs.String(), "control_not_bound") {
		t.Fatalf("거절 사유가 기록되지 않음: %s", e.logs.String())
	}
}

func TestBindRejectsAControlSessionOfAnotherConnector(t *testing.T) {
	e := newEnv(t)
	x := e.expected()
	if err := e.gw.Expect(x); err != nil {
		t.Fatal(err)
	}
	err := e.gw.Bind(x.SessionID, Binding{ConnectorID: uuid.New(), ControlSessionID: uuid.New(), CredentialID: uuid.New()})
	if !errors.Is(err, ErrControlMismatch) {
		t.Fatalf("Bind() error = %v, want ErrControlMismatch", err)
	}
	if err := e.gw.Bind("no-such-session", Binding{ConnectorID: e.connectorID}); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("알 수 없는 PreviewSession Bind() error = %v", err)
	}
}

// 이미 attach된 PreviewSession에 두 번째 Data WSS가 attach할 수 없다. 첫 tunnel은 영향을 받지 않는다.
func TestDuplicateAttachIsRejectedWithoutDisturbingTheFirstTunnel(t *testing.T) {
	e := newEnv(t)
	s := e.activate()
	cookie := e.login(s)

	ws, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	sendJSON(t, ws, attachFrame(s.Expected, nil))
	if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
		t.Fatalf("중복 attach close code = %d, want 1008", code)
	}

	resp, body := e.get(s.Expected, cookie, "/")
	if resp.StatusCode != http.StatusOK || body != "hello from workspace" {
		t.Fatalf("중복 attach 뒤 요청 = %d %q", resp.StatusCode, body)
	}
}

// 정리된 PreviewSession(취소, 시간 초과)에 늦게 도착한 attach는 어떤 PreviewSession도 완료시키지 못한다.
func TestLateAttachAfterTheSessionWasForgottenIsRejected(t *testing.T) {
	e := newEnv(t)
	x := e.expect()
	e.gw.Forget(x.SessionID)

	ws, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	sendJSON(t, ws, attachFrame(x, nil))
	if code := readCloseCode(ws); code != websocket.ClosePolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
	if _, ok := e.gw.Info(x.SessionID); ok {
		t.Fatal("Forget한 PreviewSession이 남음")
	}
	if got := e.lifecyle.all(); len(got) != 0 {
		t.Fatalf("활성화하지 않은 PreviewSession의 정리가 Lifecycle에 통지됨: %+v", got)
	}
}

func TestAttachTimeoutClosesTheConnectionAndKeepsThePendingSession(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.AttachTimeout = 100 * time.Millisecond })
	x := e.expect()
	ws, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	start := time.Now()
	// attach frame을 보내지 않는다. SaaS가 시간 안에 connection을 닫는다.
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ws.ReadMessage(); err == nil {
		t.Fatal("attach 없이 연결이 유지됨")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("attach timeout이 %v 뒤에야 동작함", elapsed)
	}
	if _, ended, _ := e.gw.Pending(x.SessionID); true {
		select {
		case <-ended:
			t.Fatal("attach timeout이 PreviewSession을 끝냄")
		default:
		}
	}
}

// attach 뒤에는 Binary frame만 허용한다. Text frame은 protocol 위반이며 PreviewSession을 끝낸다. SaaS가 시작한 종료라 Connector에 알린다.
func TestTextFrameAfterAttachEndsTheSessionAsAProtocolViolation(t *testing.T) {
	e := newEnv(t)
	s := e.activate()

	if err := s.Peer.send(websocket.TextMessage, []byte(`{"type":"PREVIEW_ATTACH"}`)); err != nil {
		t.Fatal(err)
	}
	if code := s.Peer.closeCode(t); code != websocket.ClosePolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
	eventually(t, "Lifecycle 종료 통지", func() bool { return len(e.lifecyle.all()) == 1 })
	got := e.lifecyle.all()[0]
	if got.Reason != EndProtocolViolation || !got.NotifyConnector || got.SessionID != s.SessionID {
		t.Fatalf("통지 = %+v", got)
	}
	info, _ := e.gw.Info(s.SessionID)
	if !info.Ended {
		t.Fatal("PreviewSession이 끝나지 않음")
	}
}

func TestOversizedBinaryFrameIsClosedWith1009(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.MaxFrameBytes = 1024 })
	s := e.activate()

	if err := s.Peer.send(websocket.BinaryMessage, make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	if code := s.Peer.closeCode(t); code != websocket.CloseMessageTooBig {
		t.Fatalf("close code = %d, want 1009", code)
	}
	eventually(t, "Lifecycle 종료 통지", func() bool { return len(e.lifecyle.all()) == 1 })
	if got := e.lifecyle.all()[0]; got.Reason != EndProtocolViolation || !got.NotifyConnector {
		t.Fatalf("통지 = %+v", got)
	}
}

// 종료 중인 Gateway는 새 Data WSS Upgrade를 받지 않고 attach를 기다리던 connection을 닫는다.
func TestShutdownRejectsNewDataUpgradesAndClosesWaitingConnections(t *testing.T) {
	e := newEnv(t)
	x := e.expect()
	waiting, _, err := e.dialData(testCredential)
	if err != nil {
		t.Fatal(err)
	}
	defer waiting.Close()

	e.gw.Close()
	if code := readCloseCode(waiting); code != websocket.CloseGoingAway {
		t.Fatalf("attach를 기다리던 connection close code = %d, want 1001", code)
	}
	if _, resp, err := e.dialData(testCredential); err == nil {
		t.Fatal("종료 중인 Gateway가 새 Data WSS를 받음")
	} else if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("response = %v, want 503", resp)
	}
	// 활성화되지 않은 PreviewSession은 Lifecycle에 통지하지 않는다. 생성을 기다리던 호출자가 ended channel로 알고 정리한다.
	_, ended, ok := e.gw.Pending(x.SessionID)
	if ok {
		select {
		case <-ended:
		default:
			t.Fatal("종료한 Gateway의 pending PreviewSession이 끝나지 않음")
		}
	}
}
