package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

const workspaceRoot = "/var/lib/labbit-workspaces/workspace"
const workspaceHelper = "/usr/local/libexec/labbit-workspace-sftp"

type op02Run struct {
	peer          *op02Peer
	sequence      int
	sshIdentities map[int64]string
}

func (r *op02Run) env(kind string, generation int64) protocol.BaseEnvelope {
	r.sequence++
	return envelope(kind, fmt.Sprintf("op02-%d", r.sequence), generation)
}
func serverRef(refs []protocol.ProviderResourceRef) string {
	for _, ref := range refs {
		if ref.ResourceType == "SERVER" {
			return ref.ProviderID
		}
	}
	return ""
}
func bounded(ctx context.Context, seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
}
func (r *op02Run) open(ctx context.Context, server string, generation int64) (*dataPlan, error) {
	env := r.env(protocol.MessageTypeTerminalOpen, generation)
	env.TerminalSessionID = env.MessageID + "-terminal"
	p := newDataPlan(env, server)
	r.peer.terminals.Store(env.TerminalSessionID, p)
	request := protocol.TerminalOpenMessage{BaseEnvelope: env, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: server, Cols: 80, Rows: 24}}
	if service.SendRaw(request) != nil {
		return nil, errors.New("Terminal OPEN send failed")
	}
	wait, cancel := bounded(ctx, 30)
	defer cancel()
	b, err := await(wait, protocol.MessageTypeTerminalOpenResult, env.MessageID)
	if err != nil {
		return nil, err
	}
	var result protocol.TerminalOpenResultMessage
	if json.Unmarshal(b, &result) != nil || result.Payload.Outcome != protocol.OutcomeSucceeded || !controlBinding(result.BaseEnvelope, env) {
		return nil, errors.New("Terminal OPEN failed")
	}
	select {
	case <-p.attached:
		return p, nil
	case <-wait.Done():
		return nil, errors.New("Terminal Data attach absent")
	}
}
func marker(ctx context.Context, p *dataPlan, command, expected string) error {
	if p.write(websocket.BinaryMessage, []byte(command)) != nil {
		return errors.New("PTY input failed")
	}
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	var output []byte
	for len(output) < 65536 {
		e, err := p.next(wait)
		if err != nil {
			if wait.Err() != nil {
				break
			}
			continue
		}
		if e.kind == websocket.BinaryMessage {
			output = append(output, e.raw...)
			if bytes.Contains(output, []byte(expected)) {
				return nil
			}
		}
	}
	return errors.New("PTY output assertion failed; content withheld")
}
func connectionIdentity(ctx context.Context, p *dataPlan, helper bool) (string, error) {
	value := "$SSH_CONNECTION"
	if helper {
		value = "$(cat /var/lib/labbit-workspaces/workspace/.helper-connection)"
	}
	if p.write(websocket.BinaryMessage, []byte("printf 'OP02-CON%s:%s\\n' 'NECTION' \""+value+"\"\n")) != nil {
		return "", errors.New("SSH identity probe failed")
	}
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	var output []byte
	for len(output) < 65536 {
		e, err := p.next(wait)
		if err != nil {
			return "", errors.New("SSH identity output absent")
		}
		if e.kind != websocket.BinaryMessage {
			continue
		}
		output = append(output, e.raw...)
		if identity, complete := parseConnectionIdentity(output); complete {
			return identity, nil
		}
	}
	return "", errors.New("SSH identity output invalid; content withheld")
}

// PTY output is a byte stream, with prompts/ANSI before the probe marker and
// arbitrary frame boundaries. The split printf format above keeps command echo
// from containing this exact marker. Never accept an unfinished port field.
func parseConnectionIdentity(output []byte) (string, bool) {
	lines := strings.Split(string(output), "\n")
	for _, line := range lines[:len(lines)-1] {
		at := strings.Index(line, "OP02-CONNECTION:")
		if at < 0 {
			continue
		}
		fields := strings.Fields(line[at+len("OP02-CONNECTION:"):])
		if len(fields) != 4 || net.ParseIP(fields[0]) == nil || net.ParseIP(fields[2]) == nil {
			continue
		}
		valid := true
		for _, port := range []string{fields[1], fields[3]} {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
				valid = false
			}
		}
		if valid {
			return strings.Join(fields, " "), true
		}
	}
	return "", false
}
func terminalEnded(ctx context.Context, p *dataPlan, reason string, exit *int) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	b, err := awaitMatch(wait, func(e protocol.BaseEnvelope) bool {
		return e.Type == protocol.MessageTypeTerminalEnded && e.TerminalSessionID == p.envelope.TerminalSessionID
	})
	if err != nil {
		return err
	}
	var control protocol.TerminalEndedMessage
	if json.Unmarshal(b, &control) != nil || !controlBinding(control.BaseEnvelope, p.envelope) || control.Payload.Reason != reason || (exit != nil && (control.Payload.ExitCode == nil || *control.Payload.ExitCode != *exit)) {
		return errors.New("Control ENDED mismatch")
	}
	for {
		e, err := p.next(wait)
		if err != nil {
			return errors.New("Data ENDED missing")
		}
		if e.kind != websocket.TextMessage {
			continue
		}
		var data protocol.TerminalDataEndedMessage
		if json.Unmarshal(e.raw, &data) != nil || !controlBinding(data.BaseEnvelope, p.envelope) || data.Type != protocol.MessageTypeTerminalDataEnded || data.Payload.Reason != reason || (exit != nil && (data.Payload.ExitCode == nil || *data.Payload.ExitCode != *exit)) {
			return errors.New("Data ENDED mismatch")
		}
		return nil
	}
}
func (r *op02Run) closeTerminal(ctx context.Context, p *dataPlan, reason string) error {
	env := r.env(protocol.MessageTypeTerminalClose, p.envelope.Generation)
	env.TerminalSessionID = p.envelope.TerminalSessionID
	if service.SendRaw(protocol.TerminalCloseMessage{BaseEnvelope: env, Payload: protocol.TerminalClosePayload{Reason: reason}}) != nil {
		return errors.New("Terminal CLOSE send failed")
	}
	return terminalEnded(ctx, p, reason, nil)
}
func (r *op02Run) startFile(ctx context.Context, server string, generation int64, operation string, payload map[string]any, body []byte, fixtureMode ...string) (*dataPlan, error) {
	env := r.env(protocol.MessageTypeFileOpen, generation)
	env.FileRequestID = env.MessageID + "-file"
	p := newDataPlan(env, server)
	p.operation = operation
	p.payload = payload
	p.body = body
	if len(fixtureMode) > 0 {
		p.mode = fixtureMode[0]
	}
	r.peer.files.Store(env.FileRequestID, p)
	request := protocol.FileOpenMessage{BaseEnvelope: env, Payload: protocol.FileOpenPayload{Operation: operation, TargetVMKey: "workspace", ProviderServerID: server}}
	if service.SendRaw(request) != nil {
		return nil, errors.New("File OPEN send failed")
	}
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	b, err := await(wait, protocol.MessageTypeFileOpenResult, env.MessageID)
	if err != nil {
		return nil, err
	}
	var result protocol.FileOpenResultMessage
	if json.Unmarshal(b, &result) != nil || result.Payload.Outcome != protocol.OutcomeSucceeded || !controlBinding(result.BaseEnvelope, env) {
		return nil, errors.New("File OPEN failed")
	}
	select {
	case <-p.attached:
		return p, nil
	case <-wait.Done():
		return nil, errors.New("File Data attach absent")
	}
}
func fileResult(ctx context.Context, p *dataPlan, code string) (dataFrame, []byte, error) {
	wait, cancel := bounded(ctx, 40)
	defer cancel()
	e, err := p.next(wait)
	if err != nil || e.kind != websocket.TextMessage {
		return dataFrame{}, nil, errors.New("File result absent")
	}
	var frame dataFrame
	if json.Unmarshal(e.raw, &frame) != nil || frame.Type != "FILE_"+p.operation+"_RESULT" || frame.FileRequestID != p.envelope.FileRequestID || frame.ReplyToMessageID != p.envelope.FileRequestID+"-request" || frame.Generation != p.envelope.Generation || frame.LabInstanceID != p.envelope.LabInstanceID {
		return frame, nil, errors.New("File result correlation mismatch")
	}
	var outcome string
	_ = json.Unmarshal(frame.Payload["outcome"], &outcome)
	var safe protocol.SafeError
	_ = json.Unmarshal(frame.Payload["error"], &safe)
	if (code == "" && outcome != protocol.OutcomeSucceeded) || (code != "" && (outcome != protocol.OutcomeFailed || safe.Code != code)) {
		return frame, nil, errors.New("File result outcome mismatch")
	}
	var body []byte
	if code == "" && p.operation == "READ" {
		e, err = p.next(wait)
		if err != nil || e.kind != websocket.BinaryMessage {
			return frame, nil, errors.New("File binary result absent")
		}
		body = e.raw
		var size int
		if json.Unmarshal(frame.Payload["size"], &size) != nil || size != len(body) {
			return frame, nil, errors.New("File binary size mismatch")
		}
	}
	select {
	case <-p.ended:
		if p.invalid.Load() {
			return frame, nil, errors.New("File post-result frame/close contract mismatch")
		}
		return frame, body, nil
	case <-wait.Done():
		return frame, nil, errors.New("File Data did not close")
	}
}
func (r *op02Run) file(ctx context.Context, server string, generation int64, operation string, payload map[string]any, body []byte, code string) (dataFrame, []byte, error) {
	p, err := r.startFile(ctx, server, generation, operation, payload, body)
	if err != nil {
		return dataFrame{}, nil, err
	}
	return fileResult(ctx, p, code)
}
func revision(f dataFrame) string {
	var s string
	_ = json.Unmarshal(f.Payload["revision"], &s)
	return s
}
func readPayload(path string) map[string]any {
	return map[string]any{"path": path, "maxBytes": 1 << 20}
}
func (r *op02Run) normal(ctx context.Context, refs []protocol.ProviderResourceRef, generation int64) error {
	server := serverRef(refs)
	if err := installWorkspace(ctx, server); err != nil {
		return err
	}
	a, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	b, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	if err = marker(ctx, a, "printf 'OP02-SIZE:%s\\n' \"$(stty size)\"\n", "OP02-SIZE:24 80"); err != nil {
		return err
	}
	env := r.env(protocol.MessageTypeTerminalDataResize, generation)
	env.TerminalSessionID = a.envelope.TerminalSessionID
	if a.json(protocol.TerminalDataResizeMessage{BaseEnvelope: env, Payload: protocol.TerminalDataResizePayload{Cols: 120, Rows: 40}}) != nil {
		return errors.New("PTY RESIZE send failed")
	}
	if err = marker(ctx, a, "printf 'OP02-SIZE:%s\\n' \"$(stty size)\"\n", "OP02-SIZE:40 120"); err != nil {
		return err
	}
	tree, _, err := r.file(ctx, server, generation, "TREE", map[string]any{"path": ""}, nil, "")
	if err != nil {
		return err
	}
	if err = validateFixtureTree(tree.Payload["entries"]); err != nil {
		return err
	}
	f, body, err := r.file(ctx, server, generation, "READ", readPayload("file"), nil, "")
	if err != nil || !bytes.Equal(body, []byte("initial")) {
		return errors.New("File initial Read failed")
	}
	old := revision(f)
	ptyA, err := connectionIdentity(ctx, a, false)
	if err != nil {
		return err
	}
	ptyB, err := connectionIdentity(ctx, b, false)
	if err != nil {
		return err
	}
	sftp, err := connectionIdentity(ctx, a, true)
	if err != nil {
		return err
	}
	if ptyA != ptyB || ptyA != sftp {
		return errors.New("Production PTY/SFTP channels did not share SSH transport")
	}
	if r.sshIdentities == nil {
		r.sshIdentities = map[int64]string{}
	}
	r.sshIdentities[generation] = ptyA
	if err = save(fmt.Sprintf("OP02_G%d_TWO_PTY_AND_SFTP_SAME_SSH_CONNECTION_PASS", generation)); err != nil {
		return err
	}
	replacement := []byte{'n', 0, 255, 'w'}
	f, _, err = r.file(ctx, server, generation, "SAVE", map[string]any{"path": "file", "expectedRevision": old, "size": len(replacement)}, replacement, "")
	if err != nil {
		return err
	}
	current := revision(f)
	f, body, err = r.file(ctx, server, generation, "READ", readPayload("file"), nil, "")
	if err != nil || revision(f) != current || !bytes.Equal(body, replacement) {
		return errors.New("File binary/revision round trip failed")
	}
	if _, _, err = r.file(ctx, server, generation, "SAVE", map[string]any{"path": "file", "expectedRevision": old, "size": 1}, []byte{'x'}, "REVISION_CONFLICT"); err != nil {
		return err
	}
	if _, _, err = r.file(ctx, server, generation, "SAVE", map[string]any{"path": "file", "expectedRevision": current, "size": 0}, nil, ""); err != nil {
		return err
	}
	_, body, err = r.file(ctx, server, generation, "READ", readPayload("file"), nil, "")
	if err != nil || len(body) != 0 {
		return errors.New("Empty File round trip failed")
	}
	for path, code := range map[string]string{"../outside": "INVALID_PATH", "link": "INVALID_PATH", "missing": "NOT_FOUND", "dir": "NOT_A_FILE", "denied": "PERMISSION_DENIED", "large": "TOO_LARGE"} {
		if _, _, err = r.file(ctx, server, generation, "READ", readPayload(path), nil, code); err != nil {
			return err
		}
	}
	if err = r.closeTerminal(ctx, a, protocol.TerminalReasonSessionClosed); err != nil {
		return err
	}
	if err = marker(ctx, b, "OP02_STATE=kept; printf 'OP02-%s\\n' 'INDEPENDENT'\n", "OP02-INDEPENDENT"); err != nil {
		return err
	}
	if _, _, err = r.file(ctx, server, generation, "READ", readPayload("file"), nil, ""); err != nil {
		return err
	}
	b.disconnect(websocket.CloseGoingAway)
	select {
	case <-b.attached:
	case <-time.After(15 * time.Second):
		return errors.New("Automatic Terminal Data reconnect absent")
	}
	if err = marker(ctx, b, "printf 'OP02-STATE:%s\\n' \"$OP02_STATE\"\n", "OP02-STATE:kept"); err != nil {
		return err
	}
	if err = marker(ctx, b, "printf 'OP02-%s\\n' 'FINAL'; exit 7\n", "OP02-FINAL"); err != nil {
		return err
	}
	seven := 7
	if err = terminalEnded(ctx, b, protocol.TerminalReasonPtyExited, &seven); err != nil {
		return err
	}
	return save(fmt.Sprintf("OP02_G%d_PRODUCTION_PTY_RESIZE_INDEPENDENCE_RECONNECT_FILE_BINARY_BOUNDARIES_PASS", generation))
}

func validateFixtureTree(raw []byte) error {
	var entries []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &entries) != nil || entries == nil {
		return errors.New("Tree result malformed")
	}
	seen := map[string]string{}
	for _, e := range entries {
		if e.Name == "" || e.Name == "." || e.Name == ".." || strings.ContainsAny(e.Name, "/\\\x00") || (e.Kind != "file" && e.Kind != "directory") || seen[e.Name] != "" || e.Name == "link" {
			return errors.New("Tree direct-child/type/symlink assertion failed; names withheld")
		}
		seen[e.Name] = e.Kind
	}
	for name, kind := range map[string]string{"file": "file", "empty": "file", "dir": "directory"} {
		if seen[name] != kind {
			return errors.New("Expected Tree file/directory missing; names withheld")
		}
	}
	return nil
}
func (r *op02Run) grace(ctx context.Context, server string, generation int64) error {
	p, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	r.peer.blocked.Store(true)
	defer r.peer.blocked.Store(false)
	start := time.Now()
	p.disconnect(websocket.CloseGoingAway)
	wait, cancel := bounded(ctx, 90)
	defer cancel()
	b, err := awaitMatch(wait, func(e protocol.BaseEnvelope) bool {
		return e.Type == protocol.MessageTypeTerminalEnded && e.TerminalSessionID == p.envelope.TerminalSessionID
	})
	if err != nil {
		return err
	}
	var ended protocol.TerminalEndedMessage
	if json.Unmarshal(b, &ended) != nil || !controlBinding(ended.BaseEnvelope, p.envelope) || ended.Payload.Reason != protocol.TerminalReasonGraceTimeout || time.Since(start) < 59*time.Second {
		return errors.New("Production 60-second grace was not observed")
	}
	r.peer.blocked.Store(false)
	env := r.env(protocol.MessageTypeTerminalOpen, generation)
	env.TerminalSessionID = p.envelope.TerminalSessionID
	if service.SendRaw(protocol.TerminalOpenMessage{BaseEnvelope: env, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: server, Cols: 80, Rows: 24}}) != nil {
		return errors.New("Ended-session OPEN send failed")
	}
	b, err = await(wait, protocol.MessageTypeTerminalOpenResult, env.MessageID)
	if err != nil {
		return err
	}
	var result protocol.TerminalOpenResultMessage
	if json.Unmarshal(b, &result) != nil || !controlBinding(result.BaseEnvelope, env) || result.Payload.Outcome != protocol.OutcomeFailed || result.Payload.Error == nil || result.Payload.Error.Code != protocol.TerminalErrInvalidSession {
		return errors.New("Ended TerminalSession resurrected")
	}
	return save("OP02_PRODUCTION_60_SECOND_GRACE_EXPIRES_ENDED_SESSION_NOT_REUSED_PASS")
}
func (r *op02Run) stale(ctx context.Context, server string, generation int64) error {
	return r.rejectTarget(ctx, server, generation, fmt.Sprintf("OP02_G%d_RETIRED_TARGET_TERMINAL_FILE_REJECTED_PASS", generation))
}
func (r *op02Run) rejectTarget(ctx context.Context, server string, generation int64, stage string) error {
	if _, _, err := r.file(ctx, server, generation, "READ", readPayload("file"), nil, "UNAVAILABLE"); err != nil {
		return err
	}
	env := r.env(protocol.MessageTypeTerminalOpen, generation)
	env.TerminalSessionID = env.MessageID + "-stale"
	if service.SendRaw(protocol.TerminalOpenMessage{BaseEnvelope: env, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: server, Cols: 80, Rows: 24}}) != nil {
		return errors.New("stale Terminal OPEN send failed")
	}
	wait, cancel := bounded(ctx, 30)
	defer cancel()
	b, err := await(wait, protocol.MessageTypeTerminalOpenResult, env.MessageID)
	if err != nil {
		return err
	}
	var result protocol.TerminalOpenResultMessage
	if json.Unmarshal(b, &result) != nil || !controlBinding(result.BaseEnvelope, env) || result.Payload.Outcome != protocol.OutcomeFailed || result.Payload.Error == nil {
		return errors.New("Old target was accepted")
	}
	return save(stage)
}

func op02Enabled() bool { return os.Getenv("LABBIT_KTCLOUD_OP02_TEST") == "1" }

func controlBinding(actual, expected protocol.BaseEnvelope) bool {
	return actual.LabInstanceID == expected.LabInstanceID && actual.Generation == expected.Generation && actual.TerminalSessionID == expected.TerminalSessionID && actual.FileRequestID == expected.FileRequestID
}
func requireFileCapability(raw []byte) error {
	var hello struct {
		Payload struct {
			Capabilities []string `json:"capabilities"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &hello) != nil {
		return errors.New("HELLO malformed")
	}
	for _, capability := range hello.Payload.Capabilities {
		if capability == protocol.CapabilityFileV1 {
			return nil
		}
	}
	return errors.New("Production HELLO file-v1 capability absent")
}

// Keep content/error details out of evidence even when an assertion fails.
func op02Error(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("OP02 %s failed: %s", stage, strings.TrimSpace(err.Error()))
}
