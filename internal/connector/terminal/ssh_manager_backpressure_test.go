package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type unreadManagerSSHResolver struct{ endpoint vmaccess.Endpoint }

func (r unreadManagerSSHResolver) ResolveVMAccess(context.Context, vmaccess.Target) (vmaccess.Endpoint, error) {
	return r.endpoint, nil
}

// Real production VMAccess Manager + NewManagedSSHPTY + SessionManager, with
// only a synthetic SSH peer and generated key. The actual TCP peer stops
// reading after setup; the assertion does not depend on an OS-specific kernel
// write stack because SSH flow control can become the first blocking layer.
func TestManagedSSHShutdownWithUnreadPeer(t *testing.T) {
	for _, mode := range []string{"input-close", "resize-close"} {
		t.Run(mode, func(t *testing.T) {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal("key fixture")
			}
			signer, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal("signer fixture")
			}
			block, err := ssh.MarshalPrivateKey(key, "generated-fixture-only")
			if err != nil {
				t.Fatal("key encoding")
			}
			keyPath := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
				t.Fatal("key file fixture")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal("listener fixture")
			}
			defer listener.Close()
			pause := make(chan struct{})
			resume := make(chan struct{})
			serverDone := make(chan struct{})
			var resumeOnce sync.Once
			defer resumeOnce.Do(func() { close(resume) })
			go func() {
				defer close(serverDone)
				raw, err := listener.Accept()
				if err != nil {
					return
				}
				defer raw.Close()
				_ = raw.(*net.TCPConn).SetReadBuffer(1024)
				gate := &unreadSSHReadGate{Conn: raw, resume: resume}
				cfg := &ssh.ServerConfig{NoClientAuth: true}
				cfg.AddHostKey(signer)
				sc, incoming, requests, err := ssh.NewServerConn(gate, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go ssh.DiscardRequests(requests)
				ch, reqs, err := (<-incoming).Accept()
				if err != nil {
					return
				}
				defer ch.Close()
				for req := range reqs {
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					if req.Type == "shell" {
						gate.pause.Store(true)
						close(pause)
						<-resume
						// Consume the TCP/SSH receive backlog accumulated while the
						// synthetic application was unread, then wait for transport EOF.
						_, _ = io.Copy(io.Discard, ch)
						_ = sc.Wait()
						return
					}
				}
			}()
			target := vmaccess.Target{LabInstanceID: "audit-lab", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
			access := vmaccess.NewManager(unreadManagerSSHResolver{endpoint: vmaccess.Endpoint{ProviderConnectionID: "fixture", Address: listener.Addr().String(), Username: "fixture", PrivateKeyFile: keyPath, HostKeyCallback: ssh.FixedHostKey(signer.PublicKey())}}, vmaccess.Config{IdleTimeout: time.Minute})
			defer access.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			pty, err := NewManagedSSHPTY(ctx, access, target, 80, 24)
			if err != nil {
				t.Fatal("managed PTY fixture setup")
			}
			terminalManager := NewSessionManager(time.Minute, nil)
			session, _, err := terminalManager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID}, protocol.BaseEnvelope{TerminalSessionID: "actual-manager", LabInstanceID: target.LabInstanceID, Generation: 1}, func() (PTYChannel, error) { return pty, nil })
			if err != nil {
				t.Fatal("terminal fixture setup")
			}
			<-pause
			inputDone := make(chan struct{})
			go func() { defer close(inputDone); _, _ = pty.Write(make([]byte, 16<<20)) }()
			waitPending := func(wanted string) {
				t.Helper()
				deadline := time.Now().Add(4 * time.Second)
				var first time.Time
				for {
					snapshot := make([]byte, 128<<10)
					n := runtime.Stack(snapshot, true)
					matched := false
					for _, goroutine := range strings.Split(string(snapshot[:n]), "\n\n") {
						if strings.Contains(goroutine, wanted) {
							matched = true
							break
						}
					}
					clear(snapshot)
					if matched {
						if first.IsZero() {
							first = time.Now()
						}
						if time.Since(first) > 200*time.Millisecond {
							return
						}
					} else {
						first = time.Time{}
					}
					if time.Now().After(deadline) {
						t.Fatal("managed SSH write did not remain pending against unread TCP peer")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			waitPending("terminal.(*SSHPTY).Write")
			select {
			case <-inputDone:
				t.Fatal("managed SSH input unexpectedly completed while TCP peer was unread")
			default:
			}
			resizeDone := make(chan struct{})
			var resizeErr error
			resizePendingObserved := false
			if mode == "resize-close" {
				go func() { resizeErr = session.Resize(81, 25); close(resizeDone) }()
				select {
				case <-resizeDone:
					// A channel request is not subject to the SSH data window and can
					// fit in an OS send buffer even while input remains blocked.
					if resizeErr != nil {
						t.Fatalf("managed resize failed before shutdown: %v", resizeErr)
					}
				case <-time.After(200 * time.Millisecond):
					resizePendingObserved = true
				}
			}
			done := make(chan struct{})
			go func() {
				terminalManager.Shutdown(protocol.TerminalReasonServiceRestart)
				_ = access.Close()
				close(done)
			}()
			normalShutdownReturned := false
			select {
			case <-done:
				normalShutdownReturned = true
			case <-time.After(750 * time.Millisecond):
				t.Error("real terminal/shared manager shutdown blocked on unread SSH peer")
				_ = access.Close()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("runtime fixture cleanup did not recover")
			}
			select {
			case <-inputDone:
			case <-time.After(time.Second):
				t.Error("actual managed input worker not released")
			}
			if mode == "resize-close" {
				select {
				case <-resizeDone:
				case <-time.After(time.Second):
					t.Error("actual managed resize worker not released")
				}
			}
			resumeOnce.Do(func() { close(resume) })
			select {
			case <-serverDone:
			case <-time.After(time.Second):
				t.Error("actual SSH server did not observe shared socket closure")
			}
			if _, err := access.OpenSession(context.Background(), target); err != vmaccess.ErrClosed {
				t.Error("closed shared manager accepted a new session")
			}
			t.Logf("mode=%s realVMAccessManager=true actualTCPUnread=true inputWritePending=true resizePendingObserved=%t normalShutdownReturned=%t forcedFixtureCleanup=%t", mode, resizePendingObserved, normalShutdownReturned, !normalShutdownReturned)
		})
	}
}
