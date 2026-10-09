// Package vmaccess owns the customer-local SSH transport shared by a VM's
// independent PTY and SFTP sessions. It has no Control or file protocol policy.
package vmaccess

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalidTarget = errors.New("VM access target is invalid or stale")
	ErrUnavailable   = errors.New("VM SSH access is unavailable")
	ErrClosed        = errors.New("VM access manager is closed")
)

// Target comes from the authenticated Control envelope and resolved Server ID.
type Target struct {
	LabInstanceID string
	Generation    int64
	VMKey         string
	ServerID      string
}

// Endpoint is resolved locally by the Provider, never from a supplied VM IP.
// HostKeyCallback must verify an existing ProviderConnection/Server pin.
type Endpoint struct {
	ProviderConnectionID string
	Address              string
	Username             string
	PrivateKeyFile       string
	HostKeyCallback      ssh.HostKeyCallback
}

type Resolver interface {
	ResolveVMAccess(context.Context, Target) (Endpoint, error)
}

type Config struct {
	IdleTimeout  time.Duration
	SetupTimeout time.Duration
}

type Manager struct {
	mu         sync.Mutex
	resolver   Resolver
	config     Config
	entries    map[Target]*connection
	generation map[string]int64
	closed     bool
}

type connection struct {
	target   Target
	endpoint Endpoint
	ready    chan struct{}
	cancel   context.CancelFunc
	client   *ssh.Client
	err      error
	retired  bool
	users    int
	idle     *time.Timer
}

func NewManager(resolver Resolver, config Config) *Manager {
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = time.Minute
	}
	if config.SetupTimeout <= 0 {
		config.SetupTimeout = 10 * time.Second
	}
	return &Manager{resolver: resolver, config: config, entries: make(map[Target]*connection), generation: make(map[string]int64)}
}

// OpenSession revalidates Provider ownership even when a connection is cached.
// Simultaneous opens share one handshake; canceling a waiter leaves it alone.
func (m *Manager) OpenSession(ctx context.Context, target Target) (*Session, error) {
	ctx, cancel := context.WithTimeout(ctx, m.config.SetupTimeout)
	defer cancel()
	if target.LabInstanceID == "" || target.Generation < 1 || target.VMKey == "" || target.ServerID == "" || m.resolver == nil {
		return nil, ErrInvalidTarget
	}
	m.mu.Lock()
	err := m.targetErrorLocked(target)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	endpoint, err := m.resolver.ResolveVMAccess(ctx, target)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return nil, safeError(ctx, ErrUnavailable)
		}
		return nil, safeError(ctx, ErrInvalidTarget)
	}
	if endpoint.ProviderConnectionID == "" || endpoint.Address == "" || endpoint.Username == "" || endpoint.PrivateKeyFile == "" || endpoint.HostKeyCallback == nil {
		return nil, ErrInvalidTarget
	}
	if _, _, err := net.SplitHostPort(endpoint.Address); err != nil {
		return nil, ErrInvalidTarget
	}
	m.mu.Lock()
	if err := m.targetErrorLocked(target); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.generation[target.LabInstanceID] = target.Generation
	for oldTarget, old := range m.entries {
		if oldTarget.LabInstanceID == target.LabInstanceID && (oldTarget.Generation < target.Generation ||
			(oldTarget.VMKey == target.VMKey && (oldTarget != target || !sameEndpoint(old.endpoint, endpoint)))) {
			m.retireLocked(old)
		}
	}
	c := m.entries[target]
	if c == nil {
		dialCtx, dialCancel := context.WithCancel(ctx)
		c = &connection{target: target, endpoint: endpoint, ready: make(chan struct{}), cancel: dialCancel}
		m.entries[target] = c
		m.mu.Unlock()
		client, dialErr := dialSSH(dialCtx, endpoint)
		dialCancel()
		m.mu.Lock()
		c.client, c.err = client, dialErr
		if c.retired || m.closed || ctx.Err() != nil {
			c.err = safeError(ctx, ErrUnavailable)
		}
		if c.err != nil {
			m.retireLocked(c)
		} else {
			go func() { _ = client.Wait(); m.retire(c) }()
		}
		close(c.ready)
	} else {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, safeError(ctx, ErrUnavailable)
		case <-c.ready:
		}
		m.mu.Lock()
	}
	if c.err != nil || c.retired || ctx.Err() != nil {
		err := c.err
		if err == nil {
			err = safeError(ctx, ErrUnavailable)
		}
		m.mu.Unlock()
		return nil, err
	}
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	c.users++ // Includes channel setup so idle expiry cannot race an open.
	m.mu.Unlock()

	// x/crypto/ssh channel-open has no context. A stalled setup retires the
	// transport to release all pending reads rather than leaving a goroutine.
	stop := context.AfterFunc(ctx, func() { m.retire(c) })
	sshSession, err := c.client.NewSession()
	if !stop() || ctx.Err() != nil || err != nil {
		if sshSession != nil {
			_ = sshSession.Close()
		}
		m.release(c)
		return nil, safeError(ctx, ErrUnavailable)
	}
	m.mu.Lock()
	err = m.targetErrorLocked(target)
	if c.retired && err == nil {
		err = ErrUnavailable
	}
	m.mu.Unlock()
	if err != nil {
		_ = sshSession.Close()
		m.release(c)
		return nil, err
	}
	return &Session{Session: sshSession, manager: m, connection: c}, nil
}

func sameEndpoint(a, b Endpoint) bool {
	return a.ProviderConnectionID == b.ProviderConnectionID && a.Address == b.Address && a.Username == b.Username && a.PrivateKeyFile == b.PrivateKeyFile
}

func (m *Manager) targetErrorLocked(target Target) error {
	if m.closed {
		return ErrClosed
	}
	if target.Generation < m.generation[target.LabInstanceID] {
		return ErrInvalidTarget
	}
	return nil
}

// InvalidateBefore retires obsolete targets and rejects delayed old opens.
// Reset supplies its new generation; Cleanup supplies the removed generation+1.
func (m *Manager) InvalidateBefore(labInstanceID string, generation int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation > m.generation[labInstanceID] {
		m.generation[labInstanceID] = generation
	}
	for target, c := range m.entries {
		if target.LabInstanceID == labInstanceID && target.Generation < generation {
			m.retireLocked(c)
		}
	}
}

func (m *Manager) retire(c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retireLocked(c)
}

func (m *Manager) retireLocked(c *connection) {
	c.retired = true
	c.cancel()
	if c.idle != nil {
		c.idle.Stop()
	}
	if m.entries[c.target] == c {
		delete(m.entries, c.target)
	}
	if c.client != nil {
		_ = c.client.Close()
	}
}

func (m *Manager) release(c *connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.users--
	if c.users == 0 && !c.retired {
		var timer *time.Timer
		timer = time.AfterFunc(m.config.IdleTimeout, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if c.users == 0 && m.entries[c.target] == c && c.idle == timer {
				m.retireLocked(c)
			}
		})
		c.idle = timer
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, c := range m.entries {
		m.retireLocked(c)
	}
	return nil
}

// Session owns one SSH channel. Close releases only that channel's reservation.
// Use Setup for bounded PTY/SFTP requests before handing the channel to a user.
type Session struct {
	*ssh.Session
	manager    *Manager
	connection *connection
	once       sync.Once
}

func (s *Session) Close() error {
	s.once.Do(func() { _ = s.Session.Close(); s.manager.release(s.connection) })
	return nil
}

func (s *Session) Setup(ctx context.Context, setup func(*ssh.Session) error) error {
	ctx, cancel := context.WithTimeout(ctx, s.manager.config.SetupTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { s.manager.retire(s.connection) })
	err := setup(s.Session)
	if !stop() || ctx.Err() != nil || err != nil {
		_ = s.Close()
		return safeError(ctx, ErrUnavailable)
	}
	s.manager.mu.Lock()
	err = s.manager.targetErrorLocked(s.connection.target)
	if s.connection.retired && err == nil {
		err = ErrUnavailable
	}
	s.manager.mu.Unlock()
	if err != nil {
		_ = s.Close()
		return err
	}
	return nil
}

func safeError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(fallback, err)
	}
	// The socket deadline can fire before the context timer delivers Done.
	// Preserve timeout classification in either scheduling order.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return errors.Join(fallback, context.DeadlineExceeded)
	}
	return fallback
}

func dialSSH(ctx context.Context, endpoint Endpoint) (*ssh.Client, error) {
	key, err := os.ReadFile(endpoint.PrivateKeyFile)
	if err != nil {
		return nil, ErrUnavailable
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return nil, safeError(ctx, ErrUnavailable)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	cc, channels, requests, err := ssh.NewClientConn(conn, endpoint.Address, &ssh.ClientConfig{
		User: endpoint.Username, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: endpoint.HostKeyCallback,
	})
	if !stop() || ctx.Err() != nil || err != nil {
		_ = conn.Close()
		return nil, safeError(ctx, ErrUnavailable)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, ErrUnavailable
	}
	return ssh.NewClient(cc, channels, requests), nil
}
