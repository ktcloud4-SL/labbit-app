package workspacefile

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefs"
	"golang.org/x/crypto/ssh"
)

type peer struct {
	root            string
	listener        net.Listener
	keyFile         string
	signer          ssh.Signer
	connections     atomic.Int64
	active          atomic.Int64
	hang            atomic.Bool
	hangUntilSignal atomic.Bool
	helperStops     atomic.Int64
	started         chan struct{}
	mu              sync.Mutex
	clients         []net.Conn
}

func newPeer(t *testing.T) *peer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	p := &peer{root: t.TempDir(), keyFile: filepath.Join(t.TempDir(), "key"), signer: signer, started: make(chan struct{}, 64)}
	if err = os.WriteFile(p.keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	p.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := p.listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.clients = append(p.clients, conn)
			p.mu.Unlock()
			p.connections.Add(1)
			go p.serve(conn)
		}
	}()
	t.Cleanup(func() { p.listener.Close(); p.disconnect() })
	return p
}
func (p *peer) disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		c.Close()
	}
}
func (p *peer) serve(conn net.Conn) {
	defer conn.Close()
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(p.signer)
	server, channels, requests, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for opening := range channels {
		ch, requests, err := opening.Accept()
		if err != nil {
			continue
		}
		p.active.Add(1)
		go func() {
			defer ch.Close()
			defer p.active.Add(-1)
			for req := range requests {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if ssh.Unmarshal(req.Payload, &payload) != nil {
					return
				}
				req.Reply(true, nil)
				if payload.Command == "echo-fixture" {
					go ssh.DiscardRequests(requests)
					_, _ = io.Copy(ch, ch)
					return
				}
				p.started <- struct{}{}
				if p.hangUntilSignal.Load() {
					// A remote exec process can delay EOF after channel CLOSE.
					// Model an sshd helper that exits only when its process is killed.
					for req := range requests {
						if req.Type == "signal" {
							var payload struct{ Signal string }
							if ssh.Unmarshal(req.Payload, &payload) == nil && payload.Signal == "KILL" {
								p.helperStops.Add(1)
								return
							}
						}
					}
					return
				}
				go ssh.DiscardRequests(requests)
				if p.hang.Load() {
					_, _ = io.Copy(io.Discard, ch)
					return
				}
				_ = workspacefs.Serve(ch, p.root)
				return
			}
		}()
	}
}
func (p *peer) ResolveVMAccess(ctx context.Context, target vmaccess.Target) (vmaccess.Endpoint, error) {
	if err := p.ValidateWorkspace(ctx, target); err != nil {
		return vmaccess.Endpoint{}, err
	}
	return vmaccess.Endpoint{ProviderConnectionID: "fixture", Address: p.listener.Addr().String(), Username: "test", PrivateKeyFile: p.keyFile, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), p.signer.PublicKey().Marshal()) {
			return vmaccess.ErrUnavailable
		}
		return nil
	}}, nil
}
func (p *peer) ValidateWorkspace(ctx context.Context, target vmaccess.Target) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if target != fixtureTarget() {
		return vmaccess.ErrInvalidTarget
	}
	return nil
}
func fixtureTarget() vmaccess.Target {
	return vmaccess.Target{LabInstanceID: "lab", Generation: 1, VMKey: "workspace", ServerID: "server"}
}
func serviceFixture(t *testing.T) (*Service, *peer, *vmaccess.Manager) {
	t.Helper()
	p := newPeer(t)
	m := vmaccess.NewManager(p, vmaccess.Config{IdleTimeout: time.Second})
	t.Cleanup(func() { m.Close() })
	s, err := New(m, p, Config{Root: "/fixture-root", HelperPath: "/fixture-helper", MaxFileBytes: 64, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, m
}
func fixtureFile(t *testing.T, p *peer, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.root, name), body, 0600); err != nil {
		t.Fatal(err)
	}
}
func assertError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error=%v want=%v", err, want)
	}
}

func TestCanonicalPaths(t *testing.T) {
	for _, p := range []string{"", "/file", "a/", "a//b", "a/./b", "a/../b", "..", "a\\b", "a\x00b", "a\x7fb", "a%2fb", "%252e", strings.Repeat("a", 256), string([]byte{0xff})} {
		if err := ValidatePath(p, false); err != ErrInvalidPath {
			t.Fatalf("invalid path case accepted")
		}
	}
	for _, p := range []string{"file", "한글/문서.txt", "literal%name", "a b", "file.txt"} {
		if ValidatePath(p, false) != nil {
			t.Fatal("canonical path rejected")
		}
	}
	if ValidatePath("", true) != nil {
		t.Fatal("Tree root rejected")
	}
}
func TestTreeReadSaveContract(t *testing.T) {
	s, p, _ := serviceFixture(t)
	ctx := context.Background()
	target := fixtureTarget()
	fixtureFile(t, p, "file", []byte("original"))
	fixtureFile(t, p, "empty", nil)
	if err := os.Mkdir(filepath.Join(p.root, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Tree(ctx, target, "")
	if err != nil || len(entries) != 3 {
		t.Fatalf("Tree=%v %v", entries, err)
	}
	if entries[0] != (Entry{Name: "dir", Kind: "directory"}) {
		t.Fatal("Tree kind/direct child mapping")
	}
	file, err := s.Read(ctx, target, "file", 64)
	if err != nil || string(file.Content) != "original" || file.Revision != revision(file.Content) {
		t.Fatal("Read/revision mismatch", err)
	}
	next, err := s.Save(ctx, target, "file", file.Revision, []byte("new"))
	if err != nil || next != revision([]byte("new")) {
		t.Fatal("Save failed", err)
	}
	read, err := s.Read(ctx, target, "file", 64)
	if err != nil || string(read.Content) != "new" || read.Revision != next {
		t.Fatal("Save did not truncate")
	}
	_, err = s.Save(ctx, target, "file", file.Revision, []byte("stale"))
	assertError(t, err, ErrRevisionConflict)
	_, err = s.Save(ctx, target, "file", next, nil)
	if err != nil {
		t.Fatal(err)
	}
	read, err = s.Read(ctx, target, "file", 1)
	if err != nil || len(read.Content) != 0 || read.Revision != revision(nil) {
		t.Fatal("empty Save/Read")
	}
	_, err = s.Read(ctx, target, "dir", 64)
	assertError(t, err, ErrNotAFile)
	_, err = s.Tree(ctx, target, "file")
	assertError(t, err, ErrNotADirectory)
	_, err = s.Save(ctx, target, "missing", next, []byte("no create"))
	assertError(t, err, ErrNotFound)
	if _, err = os.Stat(filepath.Join(p.root, "missing")); !os.IsNotExist(err) {
		t.Fatal("Save created missing file")
	}
	fixtureFile(t, p, "large", bytes.Repeat([]byte{'x'}, 65))
	_, err = s.Read(ctx, target, "large", 64)
	assertError(t, err, ErrTooLarge)
	_, err = s.Save(ctx, target, "file", next, bytes.Repeat([]byte{'x'}, 65))
	assertError(t, err, ErrTooLarge)
	_, err = s.Read(ctx, target, "large", 65)
	assertError(t, err, ErrTooLarge)
	_, err = s.Read(ctx, target, "file", 0)
	assertError(t, err, ErrTooLarge)
}
func TestConcurrentSaveRejectsOneStaleWriter(t *testing.T) {
	s, p, _ := serviceFixture(t)
	fixtureFile(t, p, "file", []byte("before"))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, body := range []string{"winner-a", "winner-b"} {
		go func() {
			<-start
			_, err := s.Save(context.Background(), fixtureTarget(), "file", revision([]byte("before")), []byte(body))
			results <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	if !(a == nil && errors.Is(b, ErrRevisionConflict) || b == nil && errors.Is(a, ErrRevisionConflict)) {
		t.Fatalf("concurrent saves=%v %v", a, b)
	}
}
func TestBoundaryAndSharedPTY(t *testing.T) {
	s, p, m := serviceFixture(t)
	fixtureFile(t, p, "file", []byte("body"))
	if data, err := s.Read(context.Background(), fixtureTarget(), "file", 65); err != nil || string(data.Content) != "body" {
		t.Fatal("Small file rejected by larger requested ceiling")
	}
	ctx := context.Background()
	target := fixtureTarget()
	pty, err := m.OpenSession(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	var stdin io.WriteCloser
	var stdout io.Reader
	if err = pty.Setup(ctx, func(raw *ssh.Session) error {
		stdin, _ = raw.StdinPipe()
		stdout, _ = raw.StdoutPipe()
		return raw.Start("echo-fixture")
	}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []vmaccess.Target{{LabInstanceID: "other", Generation: 1, VMKey: target.VMKey, ServerID: target.ServerID}, {LabInstanceID: target.LabInstanceID, Generation: 2, VMKey: target.VMKey, ServerID: target.ServerID}, {LabInstanceID: target.LabInstanceID, Generation: 1, VMKey: "other", ServerID: target.ServerID}} {
		_, err = s.Read(ctx, bad, "file", 64)
		assertError(t, err, ErrUnavailable)
	}
	_, err = s.Read(ctx, target, "../file", 64)
	assertError(t, err, ErrInvalidPath)
	p.hang.Store(true)
	request, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.Read(request, target, "file", 64); done <- err }()
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("SFTP setup did not start")
	}
	cancel()
	select {
	case err := <-done:
		assertError(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	p.hang.Store(false)
	_, err = s.Read(ctx, target, "file", 64)
	if err != nil {
		t.Fatal("new request after canceled SFTP", err)
	}
	echo := make(chan bool, 1)
	go func() {
		_, err := stdin.Write([]byte("ok"))
		b := make([]byte, 2)
		_, readErr := io.ReadFull(stdout, b)
		echo <- err == nil && readErr == nil && string(b) == "ok"
	}()
	select {
	case ok := <-echo:
		if !ok {
			t.Fatal("SFTP cancellation damaged PTY")
		}
	case <-time.After(time.Second):
		t.Fatal("PTY stalled")
	}
	if p.connections.Load() != 1 {
		t.Fatal("SFTP did not reuse PTY SSH connection")
	}
	m.InvalidateBefore(target.LabInstanceID, 2)
	_, err = s.Read(ctx, target, "file", 64)
	assertError(t, err, ErrUnavailable)
}
func TestTimeoutDisconnectAndSafeErrors(t *testing.T) {
	s, p, _ := serviceFixture(t)
	fixtureFile(t, p, "file", []byte("body"))
	p.hang.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := s.Read(ctx, fixtureTarget(), "file", 64)
	assertError(t, err, context.DeadlineExceeded)
	p.hang.Store(false)
	p.disconnect()
	// A disconnect is observed asynchronously by SSH. A READ issued before
	// retirement may fail; a subsequent independent request must reconnect.
	deadline := time.Now().Add(time.Second)
	for {
		_, err = s.Read(context.Background(), fixtureTarget(), "file", 64)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new READ after retirement did not reconnect", err)
		}
		time.Sleep(time.Millisecond)
	}
	if got := classify(errors.New("raw provider secret /private/path content")); got != ErrUnavailable {
		t.Fatal("raw error escaped")
	}
	deadline = time.Now().Add(time.Second)
	for p.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.active.Load() != 0 {
		t.Fatal("SFTP channels leaked")
	}
}

func TestInterruptedHelperDrainsSFTPAndPreservesPTY(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			s, p, m := serviceFixture(t)
			if mode == "timeout" {
				s.config.Timeout = 200 * time.Millisecond
			}
			pty, err := m.OpenSession(context.Background(), fixtureTarget())
			if err != nil {
				t.Fatal(err)
			}
			defer pty.Close()
			var stdin io.WriteCloser
			var stdout io.Reader
			if err = pty.Setup(context.Background(), func(raw *ssh.Session) error {
				stdin, _ = raw.StdinPipe()
				stdout, _ = raw.StdoutPipe()
				return raw.Start("echo-fixture")
			}); err != nil {
				t.Fatal(err)
			}
			p.hangUntilSignal.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := s.Read(ctx, fixtureTarget(), "file", 64); done <- err }()
			select {
			case <-p.started:
			case <-time.After(time.Second):
				t.Fatal("Helper never started")
			}
			want := context.DeadlineExceeded
			if mode == "cancel" {
				cancel()
				want = context.Canceled
			}
			select {
			case err := <-done:
				assertError(t, err, want)
			case <-time.After(time.Second):
				t.Fatal("Caller never returned")
			}
			until := time.Now().Add(time.Second)
			for s.ActiveOperations() != 0 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if s.ActiveOperations() != 0 {
				t.Fatal("Canceled helper retained a SFTP bridge and slot")
			}
			if p.helperStops.Load() != 1 {
				t.Fatal("Interrupted remote helper was not stopped")
			}
			echo := make(chan bool, 1)
			go func() {
				_, err := stdin.Write([]byte("ok"))
				body := make([]byte, 2)
				_, readErr := io.ReadFull(stdout, body)
				echo <- err == nil && readErr == nil && string(body) == "ok"
			}()
			select {
			case ok := <-echo:
				if !ok {
					t.Fatal("File cleanup damaged PTY")
				}
			case <-time.After(time.Second):
				t.Fatal("PTY stalled")
			}
			if p.connections.Load() != 1 {
				t.Fatal("File cleanup replaced shared SSH")
			}
		})
	}
}
func TestSymlinkRejectedAndExcludedFromTree(t *testing.T) {
	s, p, _ := serviceFixture(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.root, "link")); err != nil {
		t.Skip("host cannot create symlink; real Linux fixture covers this")
	}
	for _, operation := range []func() error{func() error { _, e := s.Read(context.Background(), fixtureTarget(), "link", 64); return e }, func() error {
		_, e := s.Save(context.Background(), fixtureTarget(), "link", revision([]byte("secret")), nil)
		return e
	}} {
		assertError(t, operation(), ErrInvalidPath)
	}
	entries, err := s.Tree(context.Background(), fixtureTarget(), "")
	if err != nil || len(entries) != 0 {
		t.Fatal("Tree returned symlink", err)
	}
}
