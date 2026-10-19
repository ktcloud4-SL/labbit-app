package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	openstackprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider/openstack"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
)

func validatePreviewTransportScope(enabled, smoke, op02 bool, generations int, recovery bool) error {
	if enabled && (smoke || op02 || generations != 2 || recovery) {
		return errors.New("Preview transport requires a fresh exclusive two-generation fixture")
	}
	return nil
}

type previewTransportRun struct {
	peer     *previewTransportPeer
	sequence int
	apps     []func()
	pending  *previewTransportPlan
}

func newPreviewTransportRun(token string) *previewTransportRun {
	return &previewTransportRun{peer: newPreviewTransportPeer(token)}
}
func (r *previewTransportRun) close() {
	r.peer.close()
	for _, closeApp := range r.apps {
		closeApp()
	}
}
func (r *previewTransportRun) message(server string, generation int64, session string, port int) protocol.PreviewOpenMessage {
	r.sequence++
	env := envelope(protocol.MessageTypePreviewOpen, fmt.Sprintf("preview-transport-%d", r.sequence), generation)
	if session == "" {
		session = env.MessageID + "-session"
	}
	env.PreviewSessionID = session
	return protocol.PreviewOpenMessage{BaseEnvelope: env, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: server, TargetPort: port}}
}

func previewResultMatches(result protocol.PreviewOpenResultMessage, open protocol.PreviewOpenMessage) bool {
	return result.Type == protocol.MessageTypePreviewOpenResult && result.ReplyToMessageID == open.MessageID && result.PreviewSessionID == open.PreviewSessionID && result.LabInstanceID == open.LabInstanceID && result.Generation == open.Generation
}

func previewSucceeded(raw []byte, open protocol.PreviewOpenMessage) bool {
	var result protocol.PreviewOpenResultMessage
	return json.Unmarshal(raw, &result) == nil && previewResultMatches(result, open) && result.Payload.Outcome == protocol.OutcomeSucceeded && result.Payload.Error == nil
}

func (r *previewTransportRun) open(ctx context.Context, msg protocol.PreviewOpenMessage, want string) (*previewTransportPlan, error) {
	p := r.peer.plan(msg, want != "")
	if service.SendRaw(msg) != nil {
		return nil, errors.New("Preview Control OPEN send failed")
	}
	wait, cancel := bounded(ctx, 35)
	defer cancel()
	b, err := await(wait, protocol.MessageTypePreviewOpenResult, msg.MessageID)
	if err != nil {
		return nil, err
	}
	var result protocol.PreviewOpenResultMessage
	if json.Unmarshal(b, &result) != nil || !previewResultMatches(result, msg) {
		return nil, errors.New("Preview Control result binding invalid")
	}
	if want != "" {
		if result.Payload.Outcome != protocol.OutcomeFailed || result.Payload.Error == nil || result.Payload.Error.Code != want || p.attaches.Load() != 0 {
			return nil, errors.New("Preview failure mapping/attach guard invalid")
		}
		return p, nil
	}
	if result.Payload.Outcome != protocol.OutcomeSucceeded || result.Payload.Error != nil {
		return nil, errors.New("Preview Control OPEN did not succeed")
	}
	select {
	case <-p.attached:
		return p, nil
	case <-wait.Done():
		return nil, errors.New("Preview outbound WSS attach missing")
	}
}

const previewRequestPath = "/lbt175-private-path?fixture=withheld"
const previewRequestHeader = "lbt175-private-header"

var previewRequestBody = bytes.Repeat([]byte("lbt175-private-body;"), 200)

func previewTransportHTTP(ctx context.Context, p *previewTransportPlan) error {
	request := []byte(fmt.Sprintf("POST %s HTTP/1.1\r\nHost: fixture.invalid\r\nCookie: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", previewRequestPath, previewRequestHeader, len(previewRequestBody)))
	request = append(request, previewRequestBody...)
	for _, part := range [][]byte{request[:17], request[17:101], request[101:]} {
		if p.write(websocket.BinaryMessage, part) != nil {
			return errors.New("Preview binary request failed")
		}
	}
	wait, cancel := bounded(ctx, 20)
	defer cancel()
	var response []byte
	for {
		select {
		case e, ok := <-p.events:
			if !ok {
				if err := p.closed(wait, true); err != nil {
					return err
				}
				return validatePreviewTransportHTTP(response)
			}
			if e.kind != websocket.BinaryMessage || len(response)+len(e.raw) > 32768 {
				return errors.New("Preview response framing/budget invalid")
			}
			response = append(response, e.raw...)
		case <-wait.Done():
			return errors.New("Preview HTTP response timed out")
		}
	}
}

func validatePreviewTransportHTTP(raw []byte) error {
	reader := bufio.NewReader(bytes.NewReader(raw))
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		return errors.New("Preview HTTP response invalid; contents withheld")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	want := append([]byte("LBT175-RESPONSE:"), previewRequestBody...)
	if err != nil || response.StatusCode != 200 || !bytes.Equal(body, want) {
		return errors.New("Preview HTTP exact response assertion failed; contents withheld")
	}
	// This fixture sends one Connection: close request. Content-Length can end
	// the body before the raw Data stream ends; no trailing bytes may pass.
	if _, err := reader.Peek(1); err != io.EOF {
		return errors.New("Preview HTTP response has trailing data; contents withheld")
	}
	return nil
}

// The SSH exec is only application setup/independent counters. All tested HTTP
// bytes go through the separate production Connector child and its Data WSS.
const previewApplication = `import http.server,json,os,threading
path='/home/ubuntu/.lbt175-preview-counts'
lock=threading.Lock()
counts={'active':0,'accepted':0,'peak':0}
def change(delta):
 with lock:
  counts['active']+=delta
  if delta>0: counts['accepted']+=1
  counts['peak']=max(counts['peak'],counts['active'])
  with open(path+'.tmp','w') as f: json.dump(counts,f)
  os.chmod(path+'.tmp',0o600)
  os.replace(path+'.tmp',path)
class Server(http.server.ThreadingHTTPServer):
 daemon_threads=True
 def get_request(self):
  request=super().get_request()
  change(1)
  return request
 def close_request(self,request):
  super().close_request(request)
  change(-1)
class Handler(http.server.BaseHTTPRequestHandler):
 def do_POST(self):
  size=int(self.headers.get('Content-Length','0'))
  if size>8192: self.send_error(413); return
  body=b'LBT175-RESPONSE:'+self.rfile.read(size)
  self.send_response(200)
  self.send_header('Content-Length',str(len(body)))
  self.end_headers()
  self.wfile.write(body)
 def log_message(self,*args): pass
change(0)
server=Server(('127.0.0.1',3000),Handler)
print('READY',flush=True)
server.serve_forever()
`

func (r *previewTransportRun) application(ctx context.Context, server string, generation int64) error {
	adapter, err := openstackprovider.New(ctx, openstackprovider.ConfigFromEnvironment())
	if err != nil {
		return errors.New("Preview app setup Provider unavailable")
	}
	manager := vmaccess.NewManager(adapter, vmaccess.Config{SetupTimeout: 40 * time.Second})
	session, err := manager.OpenSession(ctx, vmaccess.Target{LabInstanceID: journal.LabID, Generation: generation, VMKey: "workspace", ServerID: server})
	if err != nil {
		_ = manager.Close()
		return errors.New("Preview app pinned SSH unavailable")
	}
	r.apps = append(r.apps, func() { _ = session.Close(); _ = manager.Close() })
	var stdout io.Reader
	err = session.Setup(ctx, func(raw *ssh.Session) error {
		var e error
		stdout, e = raw.StdoutPipe()
		if e != nil {
			return e
		}
		raw.Stderr = io.Discard
		return raw.Start("exec python3 -u -c 'exec(__import__(\"base64\").b64decode(\"" + base64.StdEncoding.EncodeToString([]byte(previewApplication)) + "\"))'")
	})
	if err != nil {
		return errors.New("Preview app setup failed")
	}
	ready := make(chan bool, 1)
	go func() { line, e := bufio.NewReader(stdout).ReadString('\n'); ready <- e == nil && line == "READY\n" }()
	wait, cancel := bounded(ctx, 20)
	defer cancel()
	select {
	case ok := <-ready:
		if !ok {
			return errors.New("Preview app did not start")
		}
		return nil
	case <-wait.Done():
		return errors.New("Preview app readiness timed out")
	}
}

func previewTCPCounts(ctx context.Context, server string) (active, accepted, peak int, err error) {
	raw, err := workspaceExec(ctx, server, nil, "cat /home/ubuntu/.lbt175-preview-counts")
	if err != nil {
		return 0, 0, 0, err
	}
	var counts struct {
		Active   int `json:"active"`
		Accepted int `json:"accepted"`
		Peak     int `json:"peak"`
	}
	if json.Unmarshal(raw, &counts) != nil || counts.Active < 0 || counts.Accepted < counts.Active || counts.Peak < counts.Active {
		return 0, 0, 0, errors.New("Preview app counters invalid")
	}
	return counts.Active, counts.Accepted, counts.Peak, nil
}

func (r *previewTransportRun) noTCP(ctx context.Context, server string) error {
	wait, cancel := bounded(ctx, 30)
	defer cancel()
	for {
		active, _, _, err := previewTCPCounts(wait, server)
		if err != nil {
			return err
		}
		if active == 0 {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("Preview app TCP remained active")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (r *previewTransportRun) closeSession(ctx context.Context, p *previewTransportPlan) error {
	msg := protocol.PreviewCloseMessage{BaseEnvelope: p.open.BaseEnvelope, Payload: protocol.PreviewClosePayload{Reason: protocol.PreviewReasonSessionClosed}}
	msg.Type = protocol.MessageTypePreviewClose
	msg.MessageID += "-close"
	msg.SentAt = time.Now().UTC()
	for range 2 {
		if service.SendRaw(msg) != nil {
			return errors.New("Preview Control CLOSE failed")
		}
	}
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	return p.closed(wait, true)
}

func (r *previewTransportRun) normal(ctx context.Context, refs []protocol.ProviderResourceRef, generation int64) error {
	server := serverRef(refs)
	if err := r.application(ctx, server, generation); err != nil {
		return err
	}
	session := ""
	for range 3 {
		p, err := r.open(ctx, r.message(server, generation, session, 3000), "")
		if err != nil {
			return err
		}
		session = p.open.PreviewSessionID
		if err = previewTransportHTTP(ctx, p); err != nil {
			return err
		}
		if err = r.noTCP(ctx, server); err != nil {
			return err
		}
	}
	active, accepted, peak, err := previewTCPCounts(ctx, server)
	if err != nil || active != 0 || accepted != 3 || peak != 1 {
		return errors.New("Preview sequential TCP ownership/count assertion failed")
	}
	// An attached duplicate must not create another Data WSS/TCP connection.
	first, err := r.open(ctx, r.message(server, generation, "", 3000), "")
	if err != nil {
		return err
	}
	if service.SendRaw(first.open) != nil {
		return errors.New("Preview duplicate send failed")
	}
	wait, cancel := bounded(ctx, 15)
	raw, err := await(wait, protocol.MessageTypePreviewOpenResult, first.open.MessageID)
	cancel()
	if err != nil || !previewSucceeded(raw, first.open) {
		return errors.New("Preview duplicate result was not an exact success")
	}
	if err = waitPreviewTCP(ctx, 1, func(ctx context.Context) (int, error) {
		active, _, _, err := previewTCPCounts(ctx, server)
		return active, err
	}); err != nil {
		return err
	}
	select {
	case <-first.done:
		return errors.New("Preview duplicate closed the attached tunnel")
	default:
	}
	if err = previewTransportHTTP(ctx, first); err != nil {
		return err
	}
	first, err = r.open(ctx, r.message(server, generation, first.open.PreviewSessionID, 3000), "")
	if err != nil {
		return err
	}
	second, err := r.open(ctx, r.message(server, generation, "", 3000), "")
	if err != nil {
		return err
	}
	if err = r.closeSession(ctx, first); err != nil {
		return err
	}
	if err = previewTransportHTTP(ctx, second); err != nil {
		return err
	}
	if err = r.noTCP(ctx, server); err != nil {
		return err
	}
	_, accepted, _, err = previewTCPCounts(ctx, server)
	if err != nil || accepted != 6 || first.attaches.Load() != 1 {
		return errors.New("Preview duplicate/session isolation assertion failed")
	}
	// Gateway closure must release TCP; the same logical session can resume.
	for _, code := range []int{1000, 1011} {
		p, err := r.open(ctx, r.message(server, generation, session, 3000), "")
		if err != nil {
			return err
		}
		p.disconnect(code)
		wait, cancel := bounded(ctx, 15)
		err = p.closed(wait, false)
		cancel()
		if err != nil {
			return err
		}
		if err = r.noTCP(ctx, server); err != nil {
			return err
		}
	}
	p, err := r.open(ctx, r.message(server, generation, session, 3000), "")
	if err != nil {
		return err
	}
	if err = previewTransportHTTP(ctx, p); err != nil {
		return err
	}
	for _, tc := range []struct {
		port             int
		server, vm, want string
	}{
		{22, server, "workspace", protocol.PreviewErrorCodePortRejected},
		{3001, server, "workspace", protocol.PreviewErrorCodeAppNotRunning},
		{3000, "missing-server", "workspace", protocol.PreviewErrorCodeVMUnreachable},
		{3000, server, "other", protocol.PreviewErrorCodeVMUnreachable},
	} {
		msg := r.message(tc.server, generation, "", tc.port)
		msg.Payload.TargetVmKey = tc.vm
		if _, err = r.open(ctx, msg, tc.want); err != nil {
			return err
		}
	}
	if err = r.noTCP(ctx, server); err != nil {
		return err
	}
	fmt.Printf("KT Preview transport g%d: production Control OPEN/exact TLS Data ATTACH/Binary POST HTTP200, sequential TCP peak=1, duplicate/session isolation, CLOSE/retry, Gateway normal/error close, target/port/refused guards PASS; content withheld\n", generation)
	return save(fmt.Sprintf("PREVIEW_TRANSPORT_G%d_WSS_HTTP_TCP_BOUNDARIES_PASS", generation))
}

func (r *previewTransportRun) beforeMutation(ctx context.Context, server string, generation int64) error {
	p, err := r.open(ctx, r.message(server, generation, "", 3000), "")
	if err != nil {
		return err
	}
	r.pending = p
	return waitPreviewTCP(ctx, 1, func(ctx context.Context) (int, error) {
		active, _, _, err := previewTCPCounts(ctx, server)
		return active, err
	})
}

func waitPreviewTCP(ctx context.Context, want int, observe func(context.Context) (int, error)) error {
	wait, cancel := bounded(ctx, 30)
	defer cancel()
	for {
		active, err := observe(wait)
		if err != nil {
			return err
		}
		if active == want {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("Preview application TCP count not observed")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *previewTransportRun) afterMutation(ctx context.Context, server string, generation int64, stage string) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	if r.pending == nil {
		return errors.New("Preview active mutation fixture missing")
	}
	if err := r.pending.closed(wait, true); err != nil {
		return err
	}
	r.pending = nil
	if _, err := r.open(ctx, r.message(server, generation, "", 3000), protocol.PreviewErrorCodeUnavailable); err != nil {
		return err
	}
	return save("PREVIEW_TRANSPORT_" + stage + "_ACTIVE_WSS_CLOSED_STALE_TARGET_REJECTED_PASS")
}

func (r *previewTransportRun) finish(ctx context.Context, token string) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	for !r.peer.drained() {
		select {
		case <-wait.Done():
			return errors.New("Preview Data peer did not drain cleanly")
		case <-time.After(20 * time.Millisecond):
		}
	}
	for _, raw := range service.ReceivedMessages() {
		for _, forbidden := range []string{previewRequestPath, previewRequestHeader, string(previewRequestBody), "LBT175-RESPONSE:", token} {
			if strings.Contains(string(raw), forbidden) {
				return errors.New("Preview HTTP/credential content leaked onto Control")
			}
		}
	}
	return save("PREVIEW_TRANSPORT_DATA_DRAINED_CONTROL_CONTENT_SECRECY_PASS")
}
