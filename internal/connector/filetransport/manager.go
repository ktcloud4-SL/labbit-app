package filetransport

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
)

var ErrUnavailable = errors.New("File transport unavailable")

type Files interface {
	Tree(context.Context, vmaccess.Target, string) ([]workspacefile.Entry, error)
	Read(context.Context, vmaccess.Target, string, int64) (workspacefile.File, error)
	Save(context.Context, vmaccess.Target, string, string, []byte) (string, error)
}
type Config struct {
	EndpointURL   string
	RuntimeID     string
	Timeout       time.Duration
	AttachTimeout time.Duration
	WriteTimeout  time.Duration
	MaxFileBytes  int64
	MaxConcurrent int
	MaxRemembered int
	AllowInsecure bool // Only loopback ws is allowed for local development/tests.
	Dialer        *websocket.Dialer
}
type Manager struct {
	mu     sync.Mutex
	files  Files
	config Config
	active map[string]*request
	seen   map[[32]byte]struct{}
	closed bool
}
type request struct {
	open   protocol.FileOpenMessage
	target vmaccess.Target
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	conn   *websocket.Conn
}

func New(files Files, cfg Config) (*Manager, error) {
	u, err := url.Parse(cfg.EndpointURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || cfg.RuntimeID == "" || files == nil {
		return nil, ErrUnavailable
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "wss" && !(u.Scheme == "ws" && cfg.AllowInsecure && loopback) {
		return nil, ErrUnavailable
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.AttachTimeout <= 0 {
		cfg.AttachTimeout = 5 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 1 << 20
	}
	if cfg.MaxFileBytes == int64(^uint64(0)>>1) {
		return nil, ErrUnavailable
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 16
	}
	if cfg.MaxRemembered <= 0 {
		cfg.MaxRemembered = 16384
	}
	return &Manager{files: files, config: cfg, active: make(map[string]*request), seen: make(map[[32]byte]struct{})}, nil
}

// Open registers synchronously, then performs the outbound exchange without
// blocking the Control reader. notify is bound to this OPEN's Control sender.
// The exact credential used for that authenticated Control dial is supplied,
// rather than reloading a rotated credential file midway through a request.
func (m *Manager) Open(ctx context.Context, open protocol.FileOpenMessage, credential string, notify func(context.Context, bool) error, completed ...func()) (bool, error) {
	if ctx.Err() != nil || credential == "" || strings.IndexFunc(credential, func(r rune) bool { return r <= ' ' || r == 127 }) >= 0 {
		return false, ErrUnavailable
	}
	if open.FileRequestID == "" || open.LabInstanceID == "" || open.Generation < 1 || open.MessageID == "" || open.Payload.TargetVMKey == "" || open.Payload.ProviderServerID == "" || (open.Payload.Operation != "TREE" && open.Payload.Operation != "READ" && open.Payload.Operation != "SAVE") {
		return false, ErrUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || ctx.Err() != nil {
		return false, ErrUnavailable
	}
	key := sha256.Sum256([]byte(open.FileRequestID))
	if old := m.active[open.FileRequestID]; old != nil {
		if old.open.LabInstanceID != open.LabInstanceID || old.open.Generation != open.Generation || old.open.Payload != open.Payload {
			return false, ErrUnavailable
		}
		return true, nil
	}
	if _, exists := m.seen[key]; exists {
		return true, nil
	}
	if len(m.active) >= m.config.MaxConcurrent || len(m.seen) >= m.config.MaxRemembered {
		return false, ErrUnavailable
	}
	rctx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	r := &request{open: open, target: vmaccess.Target{LabInstanceID: open.LabInstanceID, Generation: open.Generation, VMKey: open.Payload.TargetVMKey, ServerID: open.Payload.ProviderServerID}, ctx: rctx, cancel: cancel, done: make(chan struct{})}
	m.seen[key] = struct{}{}
	m.active[open.FileRequestID] = r
	go func() {
		defer func() {
			for _, done := range completed {
				if done != nil {
					defer done()
				}
			}
			r.stop(0)
			m.mu.Lock()
			if m.active[open.FileRequestID] == r {
				delete(m.active, open.FileRequestID)
			}
			m.mu.Unlock()
			close(r.done)
		}()
		m.exchange(r, credential, notify)
	}()
	return false, nil
}

// Close is idempotent and never falls back from a mismatched Lab/generation.
// A CLOSE arriving before OPEN remembers cancellation to reject a late OPEN.
func (m *Manager) Close(env protocol.BaseEnvelope) {
	m.mu.Lock()
	r := m.active[env.FileRequestID]
	if r != nil && (r.open.LabInstanceID != env.LabInstanceID || r.open.Generation != env.Generation) {
		m.mu.Unlock()
		return
	}
	if r == nil && len(m.seen) < m.config.MaxRemembered {
		m.seen[sha256.Sum256([]byte(env.FileRequestID))] = struct{}{}
	}
	m.mu.Unlock()
	if r != nil {
		r.stop(websocket.CloseNormalClosure)
	}
}

// RetireControl cancels old request work without retrying it. Never evict an
// admitted/canceled ID during this process, including across Control reconnect.
func (m *Manager) RetireControl(code int) {
	m.mu.Lock()
	requests := make([]*request, 0, len(m.active))
	for _, r := range m.active {
		requests = append(requests, r)
	}
	m.mu.Unlock()
	for _, r := range requests {
		r.stop(code)
	}
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	requests := make([]*request, 0, len(m.active))
	for _, r := range m.active {
		requests = append(requests, r)
	}
	m.mu.Unlock()
	for _, r := range requests {
		r.stop(websocket.CloseGoingAway)
	}
	for _, r := range requests {
		select {
		case <-r.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (m *Manager) Active() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.active) }
func (r *request) stop(code int) {
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn != nil && code != 0 {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "request ended"), time.Now().Add(100*time.Millisecond))
	}
	r.cancel()
	if conn != nil {
		_ = conn.Close()
	}
}
func newID() string { return uuid.NewString() }
