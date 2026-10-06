package filetransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/filetransport/filetest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

const fileSubprotocol = "labbit.connector-file.v1"

// WSS Upgrade 전에 Connector Credential과 subprotocol을 확인한다. 인증되지 않은 요청은 WebSocket connection이 되지 못한다.
func TestDataUpgradeRequiresACredentialAndTheSubprotocol(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	dial := func(header http.Header, subprotocols ...string) (*http.Response, error) {
		dialer := websocket.Dialer{Subprotocols: subprotocols, HandshakeTimeout: 5 * time.Second}
		conn, resp, err := dialer.Dial(h.dataURL(), header)
		if conn != nil {
			_ = conn.Close()
		}
		return resp, err
	}
	bearer := func(credential string) http.Header { return http.Header{"Authorization": {"Bearer " + credential}} }

	cases := []struct {
		name   string
		header http.Header
		protos []string
		want   int
	}{
		{"Authorization 없음", http.Header{}, []string{fileSubprotocol}, 401},
		{"알 수 없는 Credential", bearer("not-a-credential"), []string{fileSubprotocol}, 401},
		{"revoke된 것과 같은 거절(빈 Bearer)", http.Header{"Authorization": {"Bearer "}}, []string{fileSubprotocol}, 401},
		{"Basic 인증", http.Header{"Authorization": {"Basic " + credentialA}}, []string{fileSubprotocol}, 401},
		{"Authorization이 둘", http.Header{"Authorization": {"Bearer " + credentialA, "Bearer " + credentialA}}, []string{fileSubprotocol}, 401},
		{"Credential에 공백", http.Header{"Authorization": {"Bearer " + credentialA + " extra"}}, []string{fileSubprotocol}, 401},
		{"subprotocol 없음", bearer(credentialA), nil, 400},
		{"다른 subprotocol(Control)", bearer(credentialA), []string{"labbit.connector.v1"}, 400},
		{"다른 subprotocol(Terminal Data)", bearer(credentialA), []string{"labbit.connector-terminal.v1"}, 400},
	}
	for _, tc := range cases {
		resp, err := dial(tc.header, tc.protos...)
		if err == nil || resp == nil || resp.StatusCode != tc.want {
			t.Errorf("%s: Upgrade = %v, %v, want %d", tc.name, resp, err, tc.want)
		}
	}
	// 거절된 요청은 Credential 원문을 log에 남기지 않는다.
	if logs := h.logs.String(); strings.Contains(logs, credentialA) || strings.Contains(logs, "not-a-credential") {
		t.Fatalf("log에 Credential이 남음:\n%s", logs)
	}

	// WebSocket Upgrade가 아닌 요청.
	resp, err := http.Get(strings.Replace(h.dataURL(), "ws", "http", 1))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Upgrade가 아닌 요청 = %d, want 400", resp.StatusCode)
	}
	if h.broker.trust.size() != 0 {
		t.Fatal("거절된 요청이 추적됨")
	}
}

// 인증 의존성 오류는 401이 아니다(인증에 실패한 것이 아니다). 오류 원문은 응답과 log에 싣지 않는다.
func TestAuthenticationDependencyFailureIsUnavailableNotUnauthorized(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.auth.fail(errors.New("connection refused to 10.0.0.5:5432 password=hunter2"))
	_, resp, err := h.rawData(credentialA, fileSubprotocol)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Upgrade = %v, %v, want 503", resp, err)
	}
	if strings.Contains(h.logs.String(), "hunter2") || strings.Contains(h.logs.String(), "10.0.0.5") {
		t.Fatalf("log에 저장소 오류 원문이 남음:\n%s", h.logs.String())
	}
}

// closeCode는 connection이 닫힐 때까지 읽고 close code를 반환한다. close frame 없이 끝났으면 -1이다.
func closeCode(t *testing.T, ws *websocket.Conn) int {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, err := ws.ReadMessage()
		if err == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			return closeErr.Code
		}
		return -1
	}
}

func writeJSON(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
}

// attach 전의 잘못된 첫 message는 어떤 요청에도 연결되지 않고 connection이 거절된다.
func TestMalformedFirstMessageIsRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 500 * time.Millisecond })

	cases := map[string]func(*websocket.Conn){
		"Binary가 먼저": func(ws *websocket.Conn) { _ = ws.WriteMessage(websocket.BinaryMessage, []byte("abc")) },
		"JSON이 아님":   func(ws *websocket.Conn) { _ = ws.WriteMessage(websocket.TextMessage, []byte("not json")) },
		"JSON 배열":    func(ws *websocket.Conn) { _ = ws.WriteMessage(websocket.TextMessage, []byte("[]")) },
		"ATTACH가 아님": func(ws *websocket.Conn) {
			writeJSON(t, ws, dataFrame("FILE_TREE", "x", 1, map[string]any{"path": ""}))
		},
		"알 수 없는 fileRequestId": func(ws *websocket.Conn) {
			writeJSON(t, ws, dataFrame("FILE_DATA_ATTACH", uuid.NewString(), 3, map[string]any{
				"runtimeId": "r", "targetVmKey": "vk-web", "providerServerId": "srv-web-g3",
			}))
		},
		"ATTACH payload 누락": func(ws *websocket.Conn) {
			writeJSON(t, ws, dataFrame("FILE_DATA_ATTACH", uuid.NewString(), 3, map[string]any{}))
		},
	}
	for name, send := range cases {
		ws, _, err := h.rawData(credentialA, fileSubprotocol)
		if err != nil {
			t.Fatalf("%s: Dial() error = %v", name, err)
		}
		send(ws)
		if code := closeCode(t, ws); code != websocket.ClosePolicyViolation {
			t.Errorf("%s: close code = %d, want %d", name, code, websocket.ClosePolicyViolation)
		}
		_ = ws.Close()
	}
	h.waitIdle(nil)
}

func TestFirstMessageMustArriveInTime(t *testing.T) {
	t.Parallel()
	h := newHarness(t, func(o *Options) { o.AttachTimeout = 150 * time.Millisecond })
	ws, _, err := h.rawData(credentialA, fileSubprotocol)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	start := time.Now()
	closeCode(t, ws)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("attach하지 않는 connection이 %v 동안 유지됨", elapsed)
	}
	h.waitIdle(nil)
}

// JSON Text message는 1 MiB를 넘을 수 없다. 넘으면 1009로 종료한다.
func TestOversizedJSONFrameIsRejectedWith1009(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ws, _, err := h.rawData(credentialA, fileSubprotocol)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"FILE_DATA_ATTACH","pad":"`+strings.Repeat("a", 1<<20)+`"}`))
	if code := closeCode(t, ws); code != websocket.CloseMessageTooBig {
		t.Fatalf("close code = %d, want %d", code, websocket.CloseMessageTooBig)
	}
}

// rawConnector는 FILE_OPEN을 받은 contract peer(NoAttach) 대신 test가 직접 Connector의 Data WSS 쪽을 수행하게 한다.
type rawConnector struct {
	t    *testing.T
	ws   *websocket.Conn
	open filetest.Open
}

// startRaw는 요청 하나를 시작하고(요청은 별도 goroutine에서 진행한다) peer가 받은 FILE_OPEN을 반환한다.
func startRaw(t *testing.T, h *harness, p *filetest.Peer, run func(context.Context) error) (filetest.Open, <-chan error) {
	t.Helper()
	before := len(p.Opens())
	errc := make(chan error, 1)
	go func() { errc <- run(context.Background()) }()
	waitFor(t, "FILE_OPEN", func() bool { return len(p.Opens()) > before })
	return p.Opens()[before], errc
}

func (h *harness) dialRaw(t *testing.T, open filetest.Open) *rawConnector {
	t.Helper()
	ws, _, err := h.rawData(credentialA, fileSubprotocol)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return &rawConnector{t: t, ws: ws, open: open}
}

func (r *rawConnector) frame(kind, replyTo string, payload map[string]any) map[string]any {
	f := map[string]any{
		"type": kind, "messageId": uuid.NewString(), "sentAt": time.Now().UTC().Format(time.RFC3339Nano),
		"fileRequestId": r.open.FileRequestID, "labInstanceId": r.open.LabInstanceID, "generation": r.open.Generation, "payload": payload,
	}
	if replyTo != "" {
		f["replyToMessageId"] = replyTo
	}
	return f
}

// attach는 FILE_DATA_ATTACH를 보내고 ATTACHED를 받는다.
func (r *rawConnector) attach() map[string]any {
	r.t.Helper()
	f := r.frame("FILE_DATA_ATTACH", "", map[string]any{
		"runtimeId": "raw", "targetVmKey": r.open.TargetVMKey, "providerServerId": r.open.ProviderServerID,
	})
	writeJSON(r.t, r.ws, f)
	attached := r.readJSON()
	if attached["type"] != "FILE_DATA_ATTACHED" || attached["replyToMessageId"] != f["messageId"] {
		r.t.Fatalf("ATTACHED = %v", attached)
	}
	return attached
}

func (r *rawConnector) readJSON() map[string]any {
	r.t.Helper()
	_ = r.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := r.ws.ReadMessage()
	if err != nil || kind != websocket.TextMessage {
		r.t.Fatalf("JSON frame을 읽지 못함: kind=%d err=%v", kind, err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		r.t.Fatalf("JSON이 아님: %s", data)
	}
	return msg
}

func (r *rawConnector) readBinary() []byte {
	r.t.Helper()
	_ = r.ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, data, err := r.ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage {
		r.t.Fatalf("Binary frame을 읽지 못함: kind=%d err=%v", kind, err)
	}
	return data
}

// SaaS가 보내는 frame의 형태와 순서를 확인한다: ATTACHED, 요청 frame 하나(Save는 JSON 뒤에 Binary 하나), 결과 뒤 정상 종료(1000).
func TestWireSequenceSeenByTheConnector(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fs := sampleFS()
	p := h.fileV1Peer(fs, func(filetest.Open) filetest.Behavior { return filetest.Behavior{NoAttach: true} })

	t.Run("Tree", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.tree(ctx, "src"); return err })
		if open.Operation != "TREE" {
			t.Fatalf("operation = %q", open.Operation)
		}
		r := h.dialRaw(t, open)
		attached := r.attach()
		if attached["fileRequestId"] != open.FileRequestID || attached["labInstanceId"] != open.LabInstanceID || attached["generation"] != float64(open.Generation) {
			t.Fatalf("ATTACHED correlation = %v", attached)
		}
		req := r.readJSON()
		payload, _ := req["payload"].(map[string]any)
		if req["type"] != "FILE_TREE" || payload["path"] != "src" || len(payload) != 1 || req["fileRequestId"] != open.FileRequestID {
			t.Fatalf("FILE_TREE = %v", req)
		}
		writeJSON(t, r.ws, r.frame("FILE_TREE_RESULT", req["messageId"].(string), map[string]any{
			"outcome": "SUCCEEDED", "entries": []any{map[string]any{"name": "app.py", "kind": "file"}},
		}))
		if code := closeCode(t, r.ws); code != websocket.CloseNormalClosure {
			t.Fatalf("close code = %d, want 1000", code)
		}
		if err := <-errc; err != nil {
			t.Fatalf("Tree() error = %v", err)
		}
	})

	t.Run("Read", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.read(ctx, "main.py", 4096); return err })
		r := h.dialRaw(t, open)
		r.attach()
		req := r.readJSON()
		payload, _ := req["payload"].(map[string]any)
		if req["type"] != "FILE_READ" || payload["path"] != "main.py" || payload["maxBytes"] != float64(4096) || len(payload) != 2 {
			t.Fatalf("FILE_READ = %v", req)
		}
		writeJSON(t, r.ws, r.frame("FILE_READ_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-1", "size": 3}))
		if err := r.ws.WriteMessage(websocket.BinaryMessage, []byte("abc")); err != nil {
			t.Fatal(err)
		}
		if code := closeCode(t, r.ws); code != websocket.CloseNormalClosure {
			t.Fatalf("close code = %d, want 1000", code)
		}
		if err := <-errc; err != nil {
			t.Fatalf("Read() error = %v", err)
		}
	})

	t.Run("Save", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error {
			_, err := h.save(ctx, "main.py", "rev-1", "새 본문\n"+sourceMarker)
			return err
		})
		r := h.dialRaw(t, open)
		r.attach()
		req := r.readJSON()
		payload, _ := req["payload"].(map[string]any)
		body := "새 본문\n" + sourceMarker
		if req["type"] != "FILE_SAVE" || payload["path"] != "main.py" || payload["expectedRevision"] != "rev-1" ||
			payload["size"] != float64(len(body)) || len(payload) != 3 {
			t.Fatalf("FILE_SAVE = %v", req)
		}
		// 본문은 JSON이 아니라 바로 다음 Binary frame 하나다. 본문이 JSON frame에 실리지 않는다.
		if strings.Contains(mustJSON(req), sourceMarker) {
			t.Fatalf("FILE_SAVE JSON에 본문이 실림: %v", req)
		}
		if got := r.readBinary(); string(got) != body {
			t.Fatalf("Save Binary frame = %q", got)
		}
		writeJSON(t, r.ws, r.frame("FILE_SAVE_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-2"}))
		if code := closeCode(t, r.ws); code != websocket.CloseNormalClosure {
			t.Fatalf("close code = %d, want 1000", code)
		}
		if err := <-errc; err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	})

	t.Run("Save of an empty file still sends one empty Binary frame", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.save(ctx, "main.py", "rev-1", ""); return err })
		r := h.dialRaw(t, open)
		r.attach()
		req := r.readJSON()
		if payload, _ := req["payload"].(map[string]any); payload["size"] != float64(0) {
			t.Fatalf("FILE_SAVE = %v", req)
		}
		if got := r.readBinary(); len(got) != 0 {
			t.Fatalf("Binary frame = %q, want empty", got)
		}
		writeJSON(t, r.ws, r.frame("FILE_SAVE_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-3"}))
		if err := <-errc; err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	})
	h.waitIdle(nil)
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

// 같은 요청에 두 번째 attach, 끝난 요청에 늦은 attach는 거절된다. 이미 진행 중인 요청에는 영향이 없다.
func TestDuplicateAndLateAttachAreRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	stall := make(chan struct{})
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{Stall: stall} })

	open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.read(ctx, "main.py", 4096); return err })
	waitFor(t, "첫 attach", func() bool { return h.broker.trust.size() == 1 })
	waitFor(t, "요청 frame", func() bool {
		for _, f := range p.DataFrames() {
			if f.FromSaaS && strings.Contains(string(f.Raw), `"FILE_READ"`) {
				return true
			}
		}
		return false
	})

	// 같은 요청에 두 번째 connection이 attach하려 한다(같은 Connector, 올바른 correlation).
	dup := h.dialRaw(t, open)
	writeJSON(t, dup.ws, dup.frame("FILE_DATA_ATTACH", "", map[string]any{
		"runtimeId": "dup", "targetVmKey": open.TargetVMKey, "providerServerId": open.ProviderServerID,
	}))
	if code := closeCode(t, dup.ws); code != websocket.ClosePolicyViolation {
		t.Fatalf("중복 attach close code = %d, want %d", code, websocket.ClosePolicyViolation)
	}

	// 중복 attach 거절이 진행 중인 요청을 방해하지 않는다.
	close(stall)
	if err := <-errc; err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	h.waitIdle(p)

	// 끝난 요청에는 attach할 수 없다.
	late := h.dialRaw(t, open)
	writeJSON(t, late.ws, late.frame("FILE_DATA_ATTACH", "", map[string]any{
		"runtimeId": "late", "targetVmKey": open.TargetVMKey, "providerServerId": open.ProviderServerID,
	}))
	if code := closeCode(t, late.ws); code != websocket.ClosePolicyViolation {
		t.Fatalf("늦은 attach close code = %d, want %d", code, websocket.ClosePolicyViolation)
	}
	h.waitIdle(p)
}

// attach한 Connector가 계약을 어기는 방식으로 응답하면 요청은 성공하지 않는다.
func TestConnectorThatBreaksTheFramingFailsTheRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	p := h.fileV1Peer(sampleFS(), func(filetest.Open) filetest.Behavior { return filetest.Behavior{NoAttach: true} })

	t.Run("Read의 본문이 JSON Text frame", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.read(ctx, "main.py", 4096); return err })
		r := h.dialRaw(t, open)
		r.attach()
		req := r.readJSON()
		writeJSON(t, r.ws, r.frame("FILE_READ_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-1", "size": 3}))
		_ = r.ws.WriteMessage(websocket.TextMessage, []byte("abc")) // Binary여야 한다.
		if err := <-errc; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
			t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
		}
	})

	t.Run("Read의 본문 없이 결과만", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.read(ctx, "main.py", 4096); return err })
		r := h.dialRaw(t, open)
		r.attach()
		req := r.readJSON()
		writeJSON(t, r.ws, r.frame("FILE_READ_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-1", "size": 3}))
		// Binary frame을 보내지 않고 두 번째 JSON frame을 보낸다.
		writeJSON(t, r.ws, r.frame("FILE_READ_RESULT", req["messageId"].(string), map[string]any{"outcome": "SUCCEEDED", "revision": "rev-1", "size": 3}))
		if err := <-errc; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
			t.Fatalf("Read() error = %v, want ErrTransportUnavailable", err)
		}
	})

	t.Run("결과가 1 MiB를 넘는 JSON Text", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.tree(ctx, ""); return err })
		r := h.dialRaw(t, open)
		r.attach()
		_ = r.readJSON()
		_ = r.ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"FILE_TREE_RESULT","pad":"`+strings.Repeat("a", 1<<20)+`"}`))
		if err := <-errc; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
			t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
		}
		if code := closeCode(t, r.ws); code != websocket.CloseMessageTooBig && code != -1 {
			t.Fatalf("close code = %d, want 1009", code)
		}
	})

	t.Run("결과 없이 두 번째 요청 frame처럼 보이는 message", func(t *testing.T) {
		open, errc := startRaw(t, h, p, func(ctx context.Context) error { _, err := h.tree(ctx, ""); return err })
		r := h.dialRaw(t, open)
		r.attach()
		_ = r.readJSON()
		writeJSON(t, r.ws, r.frame("FILE_TREE", "", map[string]any{"path": ""}))
		if err := <-errc; !errors.Is(err, workspacefile.ErrTransportUnavailable) {
			t.Fatalf("Tree() error = %v, want ErrTransportUnavailable", err)
		}
	})
	h.waitIdle(nil)
}
