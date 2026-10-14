package filetransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
)

type testFiles struct {
	mu            sync.Mutex
	content       []byte
	entries       []workspacefile.Entry
	err           error
	calls         int
	targets       []vmaccess.Target
	entered       chan vmaccess.Target
	block         bool
	active        atomic.Int32
	saveCommitted chan struct{}
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func (f *testFiles) begin(ctx context.Context, target vmaccess.Target) error {
	f.mu.Lock()
	f.calls++
	f.targets = append(f.targets, target)
	block, err := f.block, f.err
	f.mu.Unlock()
	if f.entered != nil {
		f.entered <- target
	}
	if block {
		f.active.Add(1)
		defer f.active.Add(-1)
		<-ctx.Done()
		return workspacefile.ErrUnavailable
	}
	return err
}
func (f *testFiles) Tree(ctx context.Context, target vmaccess.Target, p string) ([]workspacefile.Entry, error) {
	if err := f.begin(ctx, target); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries, nil
}
func (f *testFiles) Read(ctx context.Context, target vmaccess.Target, p string, max int64) (workspacefile.File, error) {
	if err := f.begin(ctx, target); err != nil {
		return workspacefile.File{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if int64(len(f.content)) > max {
		return workspacefile.File{}, workspacefile.ErrTooLarge
	}
	return workspacefile.File{Content: bytes.Clone(f.content), Revision: digest(f.content)}, nil
}
func (f *testFiles) Save(ctx context.Context, target vmaccess.Target, p, revision string, b []byte) (string, error) {
	if err := f.begin(ctx, target); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if revision != digest(f.content) {
		return "", workspacefile.ErrRevisionConflict
	}
	f.content = bytes.Clone(b)
	if f.saveCommitted != nil {
		close(f.saveCommitted)
		<-ctx.Done() // Mutation committed, but the result is lost at the peer.
		return "", workspacefile.ErrUnavailable
	}
	return digest(b), nil
}
func (f *testFiles) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type exchangePlan struct {
	payload   map[string]any
	body      []byte
	declared  *int64
	attached  func(map[string]any)
	request   func(map[string]any)
	afterSend func(*websocket.Conn)
	omitBody  bool
	textBody  bool
	oversized bool
	result    chan map[string]any
	binary    chan []byte
	closed    chan int
}
type tlsPeer struct {
	t      *testing.T
	server *httptest.Server
	config Config
	plans  sync.Map
	dials  atomic.Int32
}

func newPeer(t *testing.T, files Files, mutate ...func(*Config)) (*tlsPeer, *Manager) {
	t.Helper()
	p := &tlsPeer{t: t}
	p.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer control-credential" || r.URL.RawQuery != "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		up := websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolFileData}}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		p.dials.Add(1)
		defer ws.Close()
		_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var attach map[string]any
		if json.Unmarshal(raw, &attach) != nil {
			return
		}
		id, _ := attach["fileRequestId"].(string)
		v, ok := p.plans.Load(id)
		if !ok {
			t.Error("unplanned File attach")
			return
		}
		plan := v.(*exchangePlan)
		defer func() {
			if plan.closed != nil {
				select {
				case plan.closed <- 0:
				default:
				}
			}
		}()
		payload, ok := attach["payload"].(map[string]any)
		if attach["type"] != protocol.MessageTypeFileDataAttach || payload["runtimeId"] != "runtime" || payload["targetVmKey"] != "workspace" || payload["providerServerId"] != "server" {
			t.Error("attach target/runtime contract mismatch")
			return
		}
		response := testFrame(id, "FILE_DATA_ATTACHED", map[string]any{})
		response["replyToMessageId"] = attach["messageId"]
		if plan.attached != nil {
			plan.attached(response)
		}
		if ws.WriteJSON(response) != nil {
			return
		}
		request := testFrame(id, "FILE_"+strings.Split(id, "-")[0], plan.payload)
		if plan.request != nil {
			plan.request(request)
		}
		if plan.oversized {
			raw, _ := json.Marshal(request)
			// Exactly JSON-limit+1 fits the independent hard Binary read limit.
			// Consume a fragmented valid JSON document before the predecode guard
			// closes, avoiding a TCP reset from an unfinished huge frame write.
			raw = append(raw, bytes.Repeat([]byte{' '}, int(protocol.MaxJSONMessageSize)+1-len(raw))...)
			written := make(chan struct{})
			go func() {
				defer close(written)
				writer, err := ws.NextWriter(websocket.TextMessage)
				if err != nil {
					return
				}
				defer writer.Close()
				for len(raw) > 0 {
					n := min(len(raw), 4096)
					if _, err := writer.Write(raw[:n]); err != nil {
						return
					}
					raw = raw[n:]
				}
			}()
			_, _, err := ws.ReadMessage()
			code := 0
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				code = ce.Code
			}
			plan.closed <- code
			_ = ws.Close()
			<-written
			return
		} else if ws.WriteJSON(request) != nil {
			return
		}
		if strings.HasPrefix(id, "SAVE-") && !plan.omitBody && !plan.oversized {
			kind := websocket.BinaryMessage
			if plan.textBody {
				kind = websocket.TextMessage
			}
			if ws.WriteMessage(kind, plan.body) != nil {
				return
			}
		}
		if plan.afterSend != nil {
			plan.afterSend(ws)
		}
		kind, raw, err := ws.ReadMessage()
		if err != nil {
			if plan.closed != nil {
				code := 0
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					code = ce.Code
				}
				plan.closed <- code
			}
			return
		}
		if kind != websocket.TextMessage {
			t.Error("result is not JSON")
			return
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			t.Error("invalid result")
			return
		}
		if result["fileRequestId"] != id || result["labInstanceId"] != "lab" || result["generation"] != float64(1) || result["replyToMessageId"] != request["messageId"] || result["type"] != request["type"].(string)+"_RESULT" {
			t.Error("result correlation mismatch")
			return
		}
		if plan.result != nil {
			plan.result <- result
		}
		out := result["payload"].(map[string]any)
		if out["outcome"] == "SUCCEEDED" && strings.HasPrefix(id, "READ-") {
			kind, b, err := ws.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || float64(len(b)) != out["size"] {
				t.Error("Read binary contract mismatch")
				return
			}
			if plan.binary != nil {
				plan.binary <- b
			}
		}
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "done"), time.Now().Add(time.Second))
	}))
	pool := x509.NewCertPool()
	pool.AddCert(p.server.Certificate())
	p.config = Config{EndpointURL: "wss" + strings.TrimPrefix(p.server.URL, "https") + "/connector/v1/file-data", RuntimeID: "runtime", Timeout: 2 * time.Second, AttachTimeout: time.Second, Dialer: &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	for _, change := range mutate {
		change(&p.config)
	}
	m, err := New(files, p.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if m.Shutdown(ctx) != nil {
			t.Error("File shutdown leaked a worker")
		}
		p.server.Close()
	})
	return p, m
}
func testFrame(id, kind string, payload map[string]any) map[string]any {
	return map[string]any{"type": kind, "messageId": "message-" + id, "sentAt": time.Now().UTC().Format(time.RFC3339Nano), "fileRequestId": id, "labInstanceId": "lab", "generation": 1, "payload": payload}
}
func openFor(id string) protocol.FileOpenMessage {
	return protocol.FileOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpen, MessageID: "open-" + id, SentAt: time.Now().UTC(), FileRequestID: id, LabInstanceID: "lab", Generation: 1}, Payload: protocol.FileOpenPayload{Operation: strings.Split(id, "-")[0], TargetVMKey: "workspace", ProviderServerID: "server"}}
}
func planFor(op string) *exchangePlan {
	payload := map[string]any{"path": "file"}
	if op == "TREE" {
		payload["path"] = ""
	}
	if op == "READ" {
		payload["maxBytes"] = 64
	}
	if op == "SAVE" {
		payload["size"] = 0
		payload["expectedRevision"] = digest(nil)
	}
	return &exchangePlan{payload: payload, result: make(chan map[string]any, 1), binary: make(chan []byte, 1), closed: make(chan int, 2)}
}
func start(t *testing.T, p *tlsPeer, m *Manager, id string, plan *exchangePlan) {
	t.Helper()
	p.plans.Store(id, plan)
	if reused, err := m.Open(context.Background(), openFor(id), "control-credential", nil); err != nil || reused {
		t.Fatal("request admission failed")
	}
}
func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("File test signal timed out")
		var zero T
		return zero
	}
}
func idle(t *testing.T, m *Manager) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for m.Active() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if m.Active() != 0 {
		t.Fatal("File request remained active")
	}
}
func code(result map[string]any) string {
	p := result["payload"].(map[string]any)
	if p["outcome"] == "SUCCEEDED" {
		return ""
	}
	return p["error"].(map[string]any)["code"].(string)
}

func TestTreeReadSaveAndEmptyBinaryOverTrustedTLS(t *testing.T) {
	f := &testFiles{entries: []workspacefile.Entry{{Name: "file", Kind: "file"}}}
	p, m := newPeer(t, f)
	for i, op := range []string{"TREE", "READ", "SAVE", "READ", "SAVE", "READ"} {
		plan := planFor(op)
		id := fmt.Sprintf("%s-%d", op, i)
		if i == 2 {
			plan.body = []byte{'a', 0, 'b', 255}
			plan.payload["size"] = len(plan.body)
		}
		if i == 4 {
			plan.payload["expectedRevision"] = digest([]byte{'a', 0, 'b', 255})
		}
		start(t, p, m, id, plan)
		result := await(t, plan.result)
		if code(result) != "" {
			t.Fatal("normal File operation failed")
		}
		if op == "READ" {
			b := await(t, plan.binary)
			if i == 3 && !bytes.Equal(b, []byte{'a', 0, 'b', 255}) {
				t.Fatal("raw bytes changed")
			}
			if i != 3 && len(b) != 0 {
				t.Fatal("empty Read did not return empty binary")
			}
		}
		idle(t, m)
	}
	if p.dials.Load() != 6 || f.count() != 6 {
		t.Fatal("requests did not use independent Data connections")
	}
}
func TestBinaryBodyCanExceedJSONLimit(t *testing.T) {
	b := bytes.Repeat([]byte{0xff}, (1<<20)+20)
	f := &testFiles{}
	p, m := newPeer(t, f, func(c *Config) { c.MaxFileBytes = 2 << 20 })
	save := planFor("SAVE")
	save.body = b
	save.payload["size"] = len(b)
	start(t, p, m, "SAVE-big", save)
	if code(await(t, save.result)) != "" {
		t.Fatal("bounded binary was treated as JSON")
	}
	idle(t, m)
	read := planFor("READ")
	read.payload["maxBytes"] = 2 << 20
	start(t, p, m, "READ-big", read)
	if code(await(t, read.result)) != "" || !bytes.Equal(await(t, read.binary), b) {
		t.Fatal("large binary Read changed")
	}
	idle(t, m)
}
func TestSafeBusinessErrorsAndNoBinaryOnFailure(t *testing.T) {
	for _, err := range []error{workspacefile.ErrNotFound, workspacefile.ErrNotAFile, workspacefile.ErrNotADirectory, workspacefile.ErrTooLarge, workspacefile.ErrRevisionConflict, workspacefile.ErrPermissionDenied, workspacefile.ErrInvalidPath, workspacefile.ErrUnavailable, errors.New("raw SSH /secret path and body")} {
		t.Run(fmt.Sprint(err), func(t *testing.T) {
			f := &testFiles{err: err}
			p, m := newPeer(t, f)
			plan := planFor("READ")
			start(t, p, m, "READ-error", plan)
			result := await(t, plan.result)
			expected := string(workspacefile.ErrInternal)
			var safe workspacefile.Error
			if errors.As(err, &safe) {
				expected = string(safe)
			}
			if code(result) != expected {
				t.Fatal("wrong safe code")
			}
			raw, _ := json.Marshal(result)
			if bytes.Contains(raw, []byte("/secret")) || bytes.Contains(raw, []byte("raw SSH")) {
				t.Fatal("raw error leaked")
			}
			idle(t, m)
			if len(plan.binary) != 0 {
				t.Fatal("failed Read sent binary")
			}
		})
	}
}
func TestMalformedFramesNeverReachSave(t *testing.T) {
	cases := []struct {
		name string
		edit func(*exchangePlan)
	}{
		{"attach_reply", func(p *exchangePlan) { p.attached = func(f map[string]any) { f["replyToMessageId"] = "wrong" } }},
		{"attach_request", func(p *exchangePlan) { p.attached = func(f map[string]any) { f["fileRequestId"] = "other" } }},
		{"request_lab", func(p *exchangePlan) { p.request = func(f map[string]any) { f["labInstanceId"] = "other" } }},
		{"request_generation", func(p *exchangePlan) { p.request = func(f map[string]any) { f["generation"] = 2 } }},
		{"request_operation", func(p *exchangePlan) { p.request = func(f map[string]any) { f["type"] = "FILE_READ" } }},
		{"case_sensitive", func(p *exchangePlan) {
			p.request = func(f map[string]any) { f["FileRequestId"] = f["fileRequestId"]; delete(f, "fileRequestId") }
		}},
		{"null_path", func(p *exchangePlan) { p.payload["path"] = nil }},
		{"empty_path", func(p *exchangePlan) { p.payload["path"] = "" }},
		{"long_path", func(p *exchangePlan) { p.payload["path"] = strings.Repeat("x", 4097) }},
		{"missing_size", func(p *exchangePlan) { delete(p.payload, "size") }},
		{"fractional_size", func(p *exchangePlan) { p.payload["size"] = 0.5 }},
		{"negative_size", func(p *exchangePlan) { p.payload["size"] = -1 }},
		{"revision_charset", func(p *exchangePlan) { p.payload["expectedRevision"] = "bad revision" }},
		{"text_body", func(p *exchangePlan) { p.textBody = true }},
		{"long_body", func(p *exchangePlan) { p.body = []byte("x") }},
		{"short_body", func(p *exchangePlan) { p.payload["size"] = 1 }},
		{"json_limit", func(p *exchangePlan) { p.oversized = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &testFiles{}
			p, m := newPeer(t, f)
			plan := planFor("SAVE")
			tc.edit(plan)
			start(t, p, m, "SAVE-malformed", plan)
			closeCode := await(t, plan.closed)
			if closeCode != 1008 && !(tc.name == "json_limit" && closeCode == 1009) {
				t.Fatalf("wrong protocol close: %d", closeCode)
			}
			idle(t, m)
			if f.count() != 0 {
				t.Fatal("malformed request reached Save")
			}
		})
	}
}
func TestSemanticLimitsAndUnknownOptionalFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		op     string
		change func(*exchangePlan)
		want   string
	}{
		{"invalid_path", "READ", func(p *exchangePlan) { p.payload["path"] = "../private" }, "INVALID_PATH"},
		{"read_ceiling", "READ", func(p *exchangePlan) { p.payload["maxBytes"] = (1 << 20) + 1 }, ""},
		{"huge_exponent", "READ", func(p *exchangePlan) { p.payload["maxBytes"] = json.RawMessage("1e999999999") }, ""},
		{"optional", "READ", func(p *exchangePlan) {
			p.payload["unknown"] = map[string]any{"future": true}
			p.request = func(f map[string]any) {
				f["generation"] = json.RawMessage("1.0")
				f["traceparent"] = []any{"invalid"}
				f["tracestate"] = true
				f["future"] = true
			}
			p.payload["maxBytes"] = json.RawMessage("64e0")
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &testFiles{}
			p, m := newPeer(t, f)
			plan := planFor(tc.op)
			tc.change(plan)
			start(t, p, m, tc.op+"-semantic", plan)
			if code(await(t, plan.result)) != tc.want {
				t.Fatal("wrong semantic outcome")
			}
			if tc.op == "READ" && tc.want == "" {
				await(t, plan.binary)
			}
			idle(t, m)
			if tc.want != "" && f.count() != 0 {
				t.Fatal("invalid path/size reached SFTP")
			}
		})
	}
}
func TestUnknownSaveOutcomeNeverReplaysAndRequiresFreshRead(t *testing.T) {
	f := &testFiles{saveCommitted: make(chan struct{})}
	p, m := newPeer(t, f)
	plan := planFor("SAVE")
	plan.body = []byte("committed")
	plan.payload["size"] = len(plan.body)
	plan.afterSend = func(ws *websocket.Conn) { <-f.saveCommitted; _ = ws.Close() }
	start(t, p, m, "SAVE-unknown", plan)
	await(t, plan.closed)
	idle(t, m)
	m.RetireControl(1000)
	if reused, err := m.Open(context.Background(), openFor("SAVE-unknown"), "control-credential", nil); err != nil || !reused {
		t.Fatal("Unknown Save was replayed")
	}
	if p.dials.Load() != 1 || f.count() != 1 {
		t.Fatal("Unknown Save retried automatically")
	}
	read := planFor("READ")
	start(t, p, m, "READ-reconcile", read)
	result := await(t, read.result)
	body := await(t, read.binary)
	idle(t, m)
	if code(result) != "" || !bytes.Equal(body, plan.body) || result["payload"].(map[string]any)["revision"] != digest(plan.body) {
		t.Fatal("Fresh Read did not resolve committed Save state")
	}
	if p.dials.Load() != 2 || f.count() != 2 {
		t.Fatal("Reconciliation repeated mutation")
	}
}

func TestCapacityRejectsNewWorkWithoutClosingActiveRequest(t *testing.T) {
	f := &testFiles{block: true, entered: make(chan vmaccess.Target, 1)}
	p, m := newPeer(t, f, func(c *Config) { c.MaxConcurrent = 1 })
	start(t, p, m, "READ-first", planFor("READ"))
	await(t, f.entered)
	if _, err := m.Open(context.Background(), openFor("READ-second"), "control-credential", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("Capacity did not reject new request")
	}
	if m.Active() != 1 || p.dials.Load() != 1 || f.active.Load() != 1 {
		t.Fatal("Capacity rejection changed active request")
	}
	m.Close(openFor("READ-first").BaseEnvelope)
	idle(t, m)
}

func TestReadCeilingAndOversizeSaveWithRequiredBinary(t *testing.T) {
	f := &testFiles{content: []byte("x")}
	p, m := newPeer(t, f, func(c *Config) { c.MaxFileBytes = 64 })
	read := planFor("READ")
	read.payload["maxBytes"] = 65
	start(t, p, m, "READ-ceiling", read)
	if code(await(t, read.result)) != "" || string(await(t, read.binary)) != "x" {
		t.Fatal("Small file rejected by larger requested ceiling")
	}
	idle(t, m)
	plan := planFor("SAVE")
	plan.payload["size"] = 65
	plan.body = bytes.Repeat([]byte{'x'}, 65)
	plan.payload["expectedRevision"] = digest(f.content)
	start(t, p, m, "SAVE-limit", plan)
	if code(await(t, plan.result)) != "TOO_LARGE" {
		t.Fatal("Required oversize Binary did not return stable TOO_LARGE")
	}
	idle(t, m)
	if f.count() != 1 {
		t.Fatal("Oversize Save reached filesystem")
	}
}

func TestCompletedAndCanceledIDsNeverRedialAcrossControlReconnect(t *testing.T) {
	f := &testFiles{}
	p, m := newPeer(t, f, func(c *Config) { c.MaxRemembered = 2 })
	plan := planFor("SAVE")
	start(t, p, m, "SAVE-once", plan)
	await(t, plan.result)
	idle(t, m)
	m.RetireControl(1000)
	if reused, err := m.Open(context.Background(), openFor("SAVE-once"), "control-credential", nil); err != nil || !reused {
		t.Fatal("completed id lost at reconnect")
	}
	m.Close(openFor("SAVE-canceled").BaseEnvelope)
	if reused, err := m.Open(context.Background(), openFor("SAVE-canceled"), "control-credential", nil); err != nil || !reused {
		t.Fatal("late OPEN revived canceled request")
	}
	if _, err := m.Open(context.Background(), openFor("SAVE-new"), "control-credential", nil); err == nil {
		t.Fatal("dedup capacity evicted an id")
	}
	if p.dials.Load() != 1 || f.count() != 1 {
		t.Fatal("Save automatically replayed")
	}
}
func TestConcurrentRequestsAndCorrelatedClose(t *testing.T) {
	f := &testFiles{block: true, entered: make(chan vmaccess.Target, 2)}
	p, m := newPeer(t, f)
	a, b := planFor("READ"), planFor("READ")
	start(t, p, m, "READ-a", a)
	start(t, p, m, "READ-b", b)
	await(t, f.entered)
	await(t, f.entered)
	for i := 0; i < 10; i++ {
		if reused, err := m.Open(context.Background(), openFor("READ-a"), "control-credential", nil); err != nil || !reused {
			t.Fatal("duplicate admission")
		}
	}
	wrong := openFor("READ-a").BaseEnvelope
	wrong.Generation = 2
	m.Close(wrong)
	if m.Active() != 2 {
		t.Fatal("wrong generation canceled request")
	}
	m.Close(openFor("READ-a").BaseEnvelope)
	until := time.Now().Add(time.Second)
	for m.Active() == 2 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if m.Active() != 1 || f.active.Load() != 1 {
		t.Fatal("cancel affected sibling")
	}
	m.Close(openFor("READ-b").BaseEnvelope)
	idle(t, m)
	if f.active.Load() != 0 || p.dials.Load() != 2 || f.count() != 2 {
		t.Fatal("pending operation/dial leak")
	}
}
func TestTimeoutPeerDisconnectAndCredentialRevokeCancelSFTP(t *testing.T) {
	for _, mode := range []string{"timeout", "peer_disconnect", "data_revoke", "control_revoke", "control_replaced", "shutdown", "extra_request"} {
		t.Run(mode, func(t *testing.T) {
			f := &testFiles{block: true, entered: make(chan vmaccess.Target, 1)}
			p, m := newPeer(t, f, func(c *Config) { c.Timeout = 200 * time.Millisecond })
			plan := planFor("READ")
			signal := make(chan *websocket.Conn, 1)
			plan.afterSend = func(ws *websocket.Conn) { signal <- ws }
			start(t, p, m, "READ-stop", plan)
			await(t, f.entered)
			ws := await(t, signal)
			switch mode {
			case "peer_disconnect":
				ws.Close()
			case "data_revoke":
				ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "revoked"), time.Now().Add(time.Second))
			case "control_revoke":
				m.RetireControl(4001)
			case "control_replaced":
				m.RetireControl(1000)
			case "shutdown":
				if m.Shutdown(context.Background()) != nil {
					t.Fatal("shutdown failed")
				}
			case "extra_request":
				ws.WriteJSON(testFrame("READ-stop", "FILE_READ", plan.payload))
			}
			idle(t, m)
			if f.active.Load() != 0 || f.count() != 1 {
				t.Fatal("SFTP did not retire once")
			}
			if reused, err := m.Open(context.Background(), openFor("READ-stop"), "control-credential", nil); mode != "shutdown" && (err != nil || !reused) {
				t.Fatal("stopped request replayed")
			}
		})
	}
}
func TestTreeFinalEnvelopeBudget(t *testing.T) {
	f := &testFiles{}
	listingBytes := 2
	for i := 0; ; i++ {
		entry := workspacefile.Entry{Name: fmt.Sprintf("%05d%s", i, strings.Repeat("a", 225)), Kind: "file"}
		encoded, _ := json.Marshal(entry)
		if listingBytes+len(encoded)+1 > int(protocol.MaxJSONMessageSize)-100 {
			break
		}
		f.entries = append(f.entries, entry)
		listingBytes += len(encoded) + 1
	}
	p, m := newPeer(t, f)
	plan := planFor("TREE")
	start(t, p, m, "TREE-large", plan)
	result := await(t, plan.result)
	if code(result) != "TOO_LARGE" {
		t.Fatal("Tree returned partial/oversized success")
	}
	idle(t, m)
}
func TestTLSAuthenticationAndEndpointConfiguration(t *testing.T) {
	f := &testFiles{}
	p, m := newPeer(t, f)
	notified := make(chan bool, 1)
	_, err := m.Open(context.Background(), openFor("READ-auth"), "wrong-credential", func(_ context.Context, ok bool) error { notified <- ok; return nil })
	if err != nil || await(t, notified) {
		t.Fatal("401 did not fail OPEN")
	}
	idle(t, m)
	if p.dials.Load() != 0 || f.count() != 0 {
		t.Fatal("unauthenticated request reached service")
	}
	for _, endpoint := range []string{"ws://example.com/file", "wss://user:secret@example.com/file", "wss://example.com/file?token=secret", "https://example.com/file"} {
		if _, err := New(f, Config{EndpointURL: endpoint, RuntimeID: "runtime", AllowInsecure: true}); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	bad := p.config
	bad.Dialer = nil
	untrusted, err := New(f, bad)
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.Shutdown(context.Background())
	failed := make(chan bool, 1)
	_, err = untrusted.Open(context.Background(), openFor("READ-tls"), "control-credential", func(_ context.Context, ok bool) error { failed <- ok; return nil })
	if err != nil || await(t, failed) {
		t.Fatal("untrusted TLS certificate accepted")
	}
	idle(t, untrusted)
}
