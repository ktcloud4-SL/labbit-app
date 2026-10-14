package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"golang.org/x/crypto/ssh"
)

// Loopback uses genuine TCP/SSH and the unmodified production runControl,
// VM-access manager, PTY and Data dialer. Only peers and generated keys are fake.
func TestRunControlShutdownWithUnreadDataPeer(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	block, err := ssh.MarshalPrivateKey(key, "audit-generated-test-only")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	sshClosed := make(chan struct{})
	accepted := make(chan net.Conn, 1)
	var chunks atomic.Int64
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(sshClosed)
			return
		}
		accepted <- conn
		defer conn.Close()
		defer close(sshClosed)
		cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(public) == ssh.FingerprintSHA256(signer.PublicKey()) {
				return nil, nil
			}
			return nil, ssh.ErrNoAuth
		}}
		cfg.AddHostKey(signer)
		server, channels, requests, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			channel, requests, err := incoming.Accept()
			if err != nil {
				return
			}
			go func() {
				defer channel.Close()
				for req := range requests {
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					if req.Type == "shell" {
						<-start
						body := make([]byte, 4096)
						for {
							if _, err := channel.Write(body); err != nil {
								return
							}
							chunks.Add(1)
						}
					}
				}
			}()
		}
	}()
	var startOnce sync.Once
	begin := func() { startOnce.Do(func() { close(start) }) }
	defer begin()
	defer func() {
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	}()
	p := &pinnedTestProvider{MockProvider: &provider.MockProvider{
		ConnectionID:             "provider-1",
		ResolveServerAddressFunc: func(context.Context, string, string) (string, error) { return listener.Addr().String(), nil },
	}, callback: ssh.FixedHostKey(signer.PublicKey()), keyFile: keyFile}
	target := vmaccess.Target{LabInstanceID: "lab-1", Generation: 1, VMKey: "workspace", ServerID: "server-1"}
	runControlWithUnreadDataPeer(t, p, target, begin, sshClosed, &chunks)
}

func runControlWithUnreadDataPeer(t *testing.T, p runtimeProvider, target vmaccess.Target, begin func(), sshClosed <-chan struct{}, chunks *atomic.Int64) {
	t.Helper()
	const token = "audit-unit-token"
	saas := mock.NewMockSaaS(token)
	defer saas.Close()
	peerCh := make(chan *websocket.Conn, 1)
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopPeer := func() { stopOnce.Do(func() { close(stop) }) }
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		peer, err := (&websocket.Upgrader{Subprotocols: []string{protocol.SubprotocolTerminalData}, CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer peer.Close()
		if tcp, ok := peer.UnderlyingConn().(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		var attach protocol.TerminalDataAttachMessage
		if err := peer.ReadJSON(&attach); err != nil {
			return
		}
		no := false
		if err := peer.WriteJSON(protocol.TerminalDataAttachedMessage{
			BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalDataAttached, MessageID: "audit-attached", ReplyToMessageID: attach.MessageID, SentAt: time.Now().UTC(), TerminalSessionID: attach.TerminalSessionID, LabInstanceID: attach.LabInstanceID, Generation: attach.Generation},
			Payload:      protocol.TerminalDataAttachedPayload{Resumed: &no, HistoryAvailable: &no},
		}); err != nil {
			return
		}
		peerCh <- peer
		<-stop // No frame reads after genuine attach acknowledgement.
	}))
	defer relay.Close()
	defer stopPeer()
	t.Setenv("LABBIT_ENVIRONMENT", "development")
	t.Setenv("LABBIT_TERMINAL_RELAY_URL", "ws"+strings.TrimPrefix(relay.URL, "http"))
	lazy := newLazyProvider(p.ProviderConnectionID(), func(context.Context) (runtimeProvider, error) { return p, nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runControl(ctx, wss.Config{BaseURL: saas.URL(), Credential: token, RuntimeID: "audit-runtime", AllowInsecure: true}, lazy, "audit-connector", slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	_ = waitForMessageType(t, saas, protocol.MessageTypeHello, 3*time.Second)
	if err := saas.SendRaw(protocol.TerminalOpenMessage{
		BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeTerminalOpen, MessageID: "audit-open", SentAt: time.Now().UTC(), TerminalSessionID: "audit-session", LabInstanceID: target.LabInstanceID, Generation: target.Generation},
		Payload:      protocol.TerminalOpenPayload{TargetVmKey: target.VMKey, ProviderServerID: target.ServerID, Cols: 80, Rows: 24},
	}); err != nil {
		t.Fatal("Control OPEN send failed")
	}
	var peer *websocket.Conn
	select {
	case peer = <-peerCh:
	case <-time.After(12 * time.Second):
		t.Fatal("Data attach did not complete")
	}
	defer peer.Close()
	var opened protocol.TerminalOpenResultMessage
	if err := json.Unmarshal(waitForControlReply(t, saas, "audit-open"), &opened); err != nil || opened.Payload.Outcome != protocol.OutcomeSucceeded {
		t.Fatal("Control OPEN failed")
	}
	begin()

	deadline := time.Now().Add(8 * time.Second)
	for !terminalTCPWriteBlocked() {
		if time.Now().After(deadline) {
			t.Fatal("fixture did not reach production Data TCP write backpressure")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// This delay verifies a sustained kernel write, not a transient stack sample.
	time.Sleep(200 * time.Millisecond)
	if !terminalTCPWriteBlocked() {
		t.Fatal("backpressure did not persist")
	}
	cancel()
	blocked := false
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("runtime returned unexpected error")
		}
	case <-time.After(750 * time.Millisecond):
		blocked = true
		sshStillOpen := false
		if sshClosed != nil {
			select {
			case <-sshClosed:
			default:
				sshStillOpen = true
			}
		}
		count := serviceRestartEndedCount(t, saas, "audit-session")
		countChunks := int64(-1)
		if chunks != nil {
			countChunks = chunks.Load()
		}
		t.Logf("root-cancel blocked: TCPWriteBlocked=%t ControlStillOpen=%t ControlEnded=%d SSHStillOpenObserved=%t syntheticSSHChunks=%d", terminalTCPWriteBlocked(), saas.ActiveConn() != nil, count, sshStillOpen, countChunks)
	}
	// Release the test-owned peer after observing whether shutdown completed.
	_ = peer.Close()
	stopPeer()
	if blocked {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal("runtime cleanup returned unexpected error")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("runtime did not recover after test peer close")
		}
	}
	if sshClosed != nil {
		select {
		case <-sshClosed:
		case <-time.After(2 * time.Second):
			t.Fatal("managed SSH did not close")
		}
	}
	deadline = time.Now().Add(time.Second)
	for {
		count := serviceRestartEndedCount(t, saas, "audit-session")
		if count == 1 {
			break
		}
		if count > 1 || time.Now().After(deadline) {
			t.Fatalf("Control ENDED count=%d, want exactly 1", count)
		}
		time.Sleep(5 * time.Millisecond)
	}
	deadline = time.Now().Add(time.Second)
	for saas.ActiveConn() != nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if saas.ActiveConn() != nil {
		t.Error("Control client not closed")
	}
	if blocked {
		t.Error("root cancellation did not bound unread Data WSS: shared SSH close and Control ENDED/close were blocked")
	}
}

func terminalTCPWriteBlocked() bool {
	buf := make([]byte, 2<<20)
	n := runtime.Stack(buf, true)
	for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(stack, "terminal.(*Session).WriteMessage") && strings.Contains(stack, "gorilla/websocket.(*Conn).write") && strings.Contains(stack, "internal/poll.(*FD).Write") {
			return true
		}
	}
	return false
}

func serviceRestartEndedCount(t *testing.T, saas *mock.MockSaaS, sessionID string) int {
	t.Helper()
	count := 0
	for _, raw := range saas.ReceivedMessages() {
		var ended protocol.TerminalEndedMessage
		if json.Unmarshal(raw, &ended) == nil && ended.Type == protocol.MessageTypeTerminalEnded {
			count++
			if ended.TerminalSessionID != sessionID || ended.Payload.Reason != protocol.TerminalReasonServiceRestart {
				t.Error("incorrect Control ENDED identity/reason")
			}
		}
	}
	return count
}
