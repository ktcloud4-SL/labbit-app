package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func waitFileClosed(ctx context.Context, p *dataPlan) error {
	wait, cancel := bounded(ctx, 45)
	defer cancel()
	select {
	case <-p.ended:
		return nil
	case <-wait.Done():
		return errors.New("File Data worker did not close")
	}
}
func (r *op02Run) closeFile(p *dataPlan, reason string) error {
	env := r.env(protocol.MessageTypeFileClose, p.envelope.Generation)
	env.FileRequestID = p.envelope.FileRequestID
	if service.SendRaw(protocol.FileCloseMessage{BaseEnvelope: env, Payload: protocol.FileClosePayload{Reason: reason}}) != nil {
		return errors.New("FILE_CLOSE send failed")
	}
	return nil
}
func (r *op02Run) failures(ctx context.Context, refs []protocol.ProviderResourceRef, generation int64) error {
	server := serverRef(refs)
	if err := r.unknownSave(ctx, server, generation); err != nil {
		return err
	}
	pty, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	if err = delayedHelper(ctx, server, true); err != nil {
		return op02Error("enable-delay", err)
	}
	defer delayedHelper(context.Background(), server, false)
	if err = r.concurrentFiles(ctx, server, generation, pty); err != nil {
		return op02Error("concurrent-files", err)
	}
	// Observe the actual remote process before triggering each failure, then
	// assert it disappears; a Data socket closing alone is insufficient.
	for _, mode := range []string{"cancel", "disconnect", "revoke", "timeout"} {
		start := time.Now()
		p, err := r.startFile(ctx, server, generation, "READ", readPayload("file"), nil)
		if err != nil {
			return err
		}
		if err = waitHelper(ctx, server, true); err != nil {
			return err
		}
		switch mode {
		case "cancel":
			err = r.closeFile(p, "REQUEST_CANCELED")
		case "disconnect":
			p.disconnect(websocket.CloseGoingAway)
		case "revoke":
			p.disconnect(protocol.CloseCredentialRevoked)
		}
		if err != nil {
			return err
		}
		if err = waitFileClosed(ctx, p); err != nil {
			return err
		}
		if mode == "timeout" && time.Since(start) < 29*time.Second {
			return errors.New("Production 30-second File timeout was not exercised")
		}
		if err = waitHelper(ctx, server, false); err != nil {
			return err
		}
		if p.dials.Load() != 1 {
			return errors.New("File automatically retried after failure")
		}
		if err = marker(ctx, pty, "printf 'OP02-%s\\n' 'SURVIVED'\n", "OP02-SURVIVED"); err != nil {
			return err
		}
		if err = save("OP02_FILE_" + mode + "_REAL_HELPER_DRAIN_OTHER_PTY_SURVIVES_NO_RETRY_PASS"); err != nil {
			return err
		}
	}
	if err = delayedHelper(ctx, server, false); err != nil {
		return err
	}
	if err = r.closeTerminal(ctx, pty, protocol.TerminalReasonSessionClosed); err != nil {
		return err
	}
	return r.unreachable(ctx, server, generation)
}

func (r *op02Run) concurrentFiles(ctx context.Context, server string, generation int64, pty *dataPlan) error {
	a, err := r.startFile(ctx, server, generation, "READ", readPayload("file"), nil)
	if err != nil {
		return err
	}
	// Same-path work is intentionally serialized by the Workspace service.
	// Distinct files exercise independent, concurrently live SFTP channels.
	b, err := r.startFile(ctx, server, generation, "READ", readPayload("empty"), nil)
	if err != nil {
		return err
	}
	if err = waitHelperCount(ctx, server, 2); err != nil {
		return err
	}
	ptyIdentity, err := connectionIdentity(ctx, pty, false)
	if err != nil {
		return err
	}
	helperIdentity, err := connectionIdentity(ctx, pty, true)
	if err != nil || helperIdentity != ptyIdentity {
		return errors.New("Concurrent File channels did not share PTY SSH connection")
	}
	if err = r.closeFile(a, "REQUEST_CANCELED"); err != nil {
		return err
	}
	if err = waitFileClosed(ctx, a); err != nil {
		return err
	}
	if err = waitHelperCount(ctx, server, 1); err != nil {
		return err
	}
	select {
	case <-b.ended:
		return errors.New("Closing one File channel closed another")
	default:
	}
	if err = marker(ctx, pty, "printf 'OP02-%s\\n' 'CONCURRENT-SURVIVED'\n", "OP02-CONCURRENT-SURVIVED"); err != nil {
		return err
	}
	if err = r.closeFile(b, "REQUEST_CANCELED"); err != nil {
		return err
	}
	if err = waitFileClosed(ctx, b); err != nil {
		return err
	}
	if err = waitHelperCount(ctx, server, 0); err != nil {
		return err
	}
	if a.dials.Load() != 1 || b.dials.Load() != 1 {
		return errors.New("Concurrent File request redialed")
	}
	return save("OP02_TWO_CONCURRENT_SFTP_CHANNELS_SHARED_SSH_CLOSE_ONE_OTHER_FILE_AND_PTY_SURVIVE_PASS")
}

func (r *op02Run) unknownSave(ctx context.Context, server string, generation int64) error {
	f, _, err := r.file(ctx, server, generation, "READ", readPayload("file"), nil, "")
	if err != nil {
		return err
	}
	body := []byte{'c', 0, 255, 'm'}
	p, err := r.startFile(ctx, server, generation, "SAVE", map[string]any{"path": "file", "expectedRevision": revision(f), "size": len(body)}, body, "lost-save-result")
	if err != nil {
		return err
	}
	if err = waitFileClosed(ctx, p); err != nil || p.invalid.Load() {
		return errors.New("Lost Save result fixture did not commit safely")
	}
	// The contract requires no second Data WSS for a repeated old ID; it does
	// not require a new OPEN_RESULT when no new attach takes place.
	env := r.env(protocol.MessageTypeFileOpen, generation)
	env.FileRequestID = p.envelope.FileRequestID
	if service.SendRaw(protocol.FileOpenMessage{BaseEnvelope: env, Payload: protocol.FileOpenPayload{Operation: "SAVE", TargetVMKey: "workspace", ProviderServerID: server}}) != nil {
		return errors.New("Repeated old Save OPEN send failed")
	}
	_, read, err := r.file(ctx, server, generation, "READ", readPayload("file"), nil, "")
	if err != nil || string(read) != string(body) || p.dials.Load() != 1 || p.invalid.Load() {
		return errors.New("Unknown Save was replayed or fresh Read did not observe committed state")
	}
	// Fresh Read's Control response orders the repeated OPEN on this connection.
	// Also observe a full production attach-timeout window for a late redial.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
	}
	if p.dials.Load() != 1 || p.invalid.Load() {
		return errors.New("Completed Save ID created another Data connection")
	}
	return save("OP02_REAL_SAVE_RESULT_LOST_OLD_ID_NOT_REPLAYED_FRESH_READ_REQUIRED_PASS")
}
func (r *op02Run) unreachable(ctx context.Context, server string, generation int64) error {
	p, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	if err = marker(ctx, p, "printf 'OP02-%s\\n' 'BEFORE-SSH-FAULT'\n", "OP02-BEFORE-SSH-FAULT"); err != nil {
		return err
	}
	// This transient unit is confined to the tracked, disposable Workspace VM.
	// The rollback runs under systemd independently of the SSH connection.
	cmd := "sudo -n systemd-run --quiet --unit=labbit-op02-ssh-fault --collect /bin/sh -c 'sleep 3; systemctl stop ssh.socket ssh.service; pkill -x sshd || true; sleep 25; systemctl start ssh.socket ssh.service'"
	if _, err = workspaceExec(ctx, server, nil, cmd); err != nil {
		return err
	}
	if err = terminalEnded(ctx, p, protocol.TerminalReasonSSHDisconnected, nil); err != nil {
		return err
	}
	if _, _, err = r.file(ctx, server, generation, "READ", readPayload("file"), nil, "UNAVAILABLE"); err != nil {
		return err
	}
	// An unavailable management socket must fail an OPEN, not use the retired
	// transport. No implicit transport retry is claimed by this explicit probe.
	env := r.env(protocol.MessageTypeTerminalOpen, generation)
	env.TerminalSessionID = env.MessageID + "-unreachable"
	if service.SendRaw(protocol.TerminalOpenMessage{BaseEnvelope: env, Payload: protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: server, Cols: 80, Rows: 24}}) != nil {
		return errors.New("unreachable Terminal OPEN send failed")
	}
	wait, cancel := bounded(ctx, 20)
	defer cancel()
	b, err := await(wait, protocol.MessageTypeTerminalOpenResult, env.MessageID)
	if err != nil {
		return err
	}
	var result protocol.TerminalOpenResultMessage
	if json.Unmarshal(b, &result) != nil || !controlBinding(result.BaseEnvelope, env) || result.Payload.Outcome != protocol.OutcomeFailed || result.Payload.Error == nil {
		return errors.New("Unreachable VM Terminal was accepted")
	}
	recovery, stop := bounded(ctx, 90)
	defer stop()
	for {
		if _, err = workspaceExec(recovery, server, nil, "true"); err == nil {
			break
		}
		select {
		case <-recovery.Done():
			return errors.New("Owned VM SSH fault rollback not observed")
		case <-time.After(time.Second):
		}
	}
	probe, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	if err = r.closeTerminal(ctx, probe, protocol.TerminalReasonSessionClosed); err != nil {
		return err
	}
	return save("OP02_REAL_SSH_DISCONNECT_VM_UNREACHABLE_SAFE_FAILURE_EXPLICIT_RECOVERY_PASS")
}
func (r *op02Run) beforeMutation(ctx context.Context, server string, generation int64, reason string) error {
	p, err := r.open(ctx, server, generation)
	if err != nil {
		return err
	}
	if err = delayedHelper(ctx, server, true); err != nil {
		return err
	}
	f, err := r.startFile(ctx, server, generation, "READ", readPayload("file"), nil)
	if err != nil {
		return err
	}
	if err = waitHelper(ctx, server, true); err != nil {
		return err
	}
	// Apply the owning SaaS contract: close active File/Terminal work before
	// changing the Workspace generation, then observe remote helper drain.
	if err = r.closeFile(f, reason); err != nil {
		return err
	}
	if err = r.closeTerminal(ctx, p, reason); err != nil {
		return err
	}
	if err = waitFileClosed(ctx, f); err != nil {
		return err
	}
	if err = waitHelper(ctx, server, false); err != nil {
		return err
	}
	if err = delayedHelper(ctx, server, false); err != nil {
		return err
	}
	return save("OP02_" + reason + "_ACTIVE_TERMINAL_FILE_CLOSED_HELPER_DRAINED_BEFORE_MUTATION_PASS")
}
func (r *op02Run) drained(ctx context.Context) error {
	wait, cancel := bounded(ctx, 15)
	defer cancel()
	for r.peer.active.Load() != 0 {
		select {
		case <-wait.Done():
			return errors.New("Data connections remain after Cleanup")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return save("OP02_ALL_TERMINAL_FILE_DATA_CONNECTIONS_DRAINED_PASS")
}
