// Package vmaccess owns the customer-local SSH transport shared by a VM's
// independent PTY, SFTP and TCP forwarding channels. It has no Control policy.
package vmaccess

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
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
	mu              sync.Mutex
	resolver        Resolver
	config          Config
	entries         map[Target]*connection
	generation      map[string]int64
	closed          bool
	failureObserver func(Target, FailureInfo)
}

type connection struct {
	target      Target
	endpoint    Endpoint
	ready       chan struct{}
	cancel      context.CancelCauseFunc
	client      *ssh.Client
	err         error
	retired     bool
	users       int
	waiters     int // Each pending caller owns only its own reservation.
	progress    *dialProgress
	dialCtx     context.Context
	tcpChannels int // Includes channels awaiting peer CLOSE after EOF/caller Close.
	idle        *time.Timer
	done        chan struct{}
	opening     chan struct{}
	closing     []io.Closer
	closeWake   chan struct{}
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

// acquire revalidates Provider ownership even when a connection is cached.
// The caller owns one reservation, including channel setup, until release.
func (m *Manager) acquire(ctx context.Context, target Target) (_ *connection, retErr error) {
	started, shared := time.Now(), false
	defer func() {
		_, diagnostic := Diagnostic(retErr)
		if retErr != nil && (diagnostic || ctx.Err() != nil || errors.Is(retErr, context.DeadlineExceeded)) {
			retErr = acquireDiagnostic(retErr, ctx, started, shared)
		}
		if info, ok := Diagnostic(retErr); ok {
			m.mu.Lock()
			observer := m.failureObserver
			m.mu.Unlock()
			if observer != nil {
				observer(target, info)
			}
		}
	}()
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
		if errors.Is(err, ErrInvalidTarget) {
			return nil, Diagnose(StageResolveManagement, errors.Join(err, safeError(ctx, ErrInvalidTarget)), time.Time{})
		}
		return nil, Diagnose(StageResolveManagement, errors.Join(err, safeError(ctx, ErrUnavailable)), time.Time{})
	}
	if endpoint.ProviderConnectionID == "" || endpoint.Address == "" || endpoint.Username == "" || endpoint.PrivateKeyFile == "" || endpoint.HostKeyCallback == nil {
		return nil, ErrUnavailable
	}
	if _, _, err := net.SplitHostPort(endpoint.Address); err != nil {
		return nil, ErrUnavailable
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
	shared = c != nil
	if c == nil {
		// The shared dial has its own finite lifetime. No single caller owns
		// cancellation while another authorized caller is still waiting.
		budgetCtx, budgetCancel := context.WithTimeoutCause(context.Background(), m.config.SetupTimeout, errSetupTimeout)
		dialCtx, dialCancel := context.WithCancelCause(budgetCtx)
		c = &connection{target: target, endpoint: endpoint, ready: make(chan struct{}), cancel: dialCancel,
			done: make(chan struct{}), opening: make(chan struct{}, 1), closeWake: make(chan struct{}, 1)}
		c.progress, c.dialCtx = &dialProgress{started: time.Now()}, dialCtx
		c.progress.stage.Store(uint32(StagePrivateKey))
		m.entries[target] = c
		go m.dial(dialCtx, budgetCancel, c)
	}
	c.waiters++
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		m.mu.Lock()
		c.waiters--
		last := c.waiters == 0 && c.users == 0
		if last {
			c.cancel(context.Cause(ctx))
			m.retireLocked(c)
		}
		m.mu.Unlock()
		if last {
			// A callback cannot be interrupted by closing TCP. Return a safe
			// checkpoint immediately; the worker reports its final result later.
			select {
			case <-c.ready:
				m.mu.Lock()
				err := c.err
				m.mu.Unlock()
				return nil, Diagnose(StageUnknown, errors.Join(err, safeError(ctx, ErrUnavailable)), time.Time{})
			default:
				return nil, c.progress.failure(c.dialCtx, ctx)
			}
		}
		return nil, safeError(ctx, ErrUnavailable)
	case <-c.ready:
	}
	m.mu.Lock()
	c.waiters--
	if c.err != nil || c.retired || ctx.Err() != nil {
		err := c.err
		if err == nil {
			err = safeError(ctx, ErrUnavailable)
		}
		if c.waiters == 0 && c.users == 0 {
			m.retireLocked(c)
		}
		m.mu.Unlock()
		return nil, err
	}
	if err := m.targetErrorLocked(target); err != nil {
		if c.waiters == 0 && c.users == 0 {
			m.retireLocked(c)
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
	return c, nil
}

func (m *Manager) dial(ctx context.Context, budgetCancel context.CancelFunc, c *connection) {
	defer budgetCancel()
	client, dialErr := dialSSHTracked(ctx, c.endpoint, c.progress)
	m.mu.Lock()
	c.client, c.err = client, dialErr
	if c.retired || m.closed {
		c.err = Diagnose(StageUnknown, errors.Join(dialErr, safeError(ctx, ErrUnavailable)), time.Time{})
	}
	if c.err != nil {
		m.retireLocked(c)
	} else {
		go m.closeChannels(c)
		go func() { _ = client.Wait(); m.retire(c) }()
	}
	close(c.ready)
	observer := m.failureObserver
	info, diagnostic := Diagnostic(c.err)
	m.mu.Unlock()
	if diagnostic && observer != nil {
		observer(c.target, info)
	}
}

// SetFailureObserver records safe acquisition diagnostics before callers such
// as File convert failures to their fixed external business errors. It never
// receives the SSH error, endpoint, command, credential, or terminal content.
func (m *Manager) SetFailureObserver(observer func(Target, FailureInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureObserver = observer
}

// OpenSession opens an independent PTY/SFTP channel on the shared transport.
func (m *Manager) OpenSession(ctx context.Context, target Target) (*Session, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeoutCause(ctx, m.config.SetupTimeout, errSetupTimeout)
	defer cancel()
	c, err := m.acquire(ctx, target)
	if err != nil {
		return nil, err
	}

	result, err := m.channelOperation(ctx, c, func() channelResult {
		s, err := c.client.NewSession()
		return channelResult{session: s, err: err}
	})
	sshSession := result.session
	if ctx.Err() != nil || err != nil || result.err != nil {
		m.closeChannel(c, result.closer())
		m.release(c)
		return nil, Diagnose(StageChannelOpen, errors.Join(m.connectionError(ctx, c), err, result.err), started)
	}
	m.mu.Lock()
	err = m.targetErrorLocked(target)
	if c.retired && err == nil {
		err = ErrUnavailable
	}
	m.mu.Unlock()
	if err != nil {
		m.closeChannel(c, result.closer())
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
	if !c.retired {
		close(c.done)
	}
	c.retired = true
	c.closing = nil
	c.cancel(errTransportRetired)
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
	m.releaseLocked(c)
}

func (m *Manager) releaseLocked(c *connection) {
	c.users--
	if c.users == 0 && c.waiters == 0 && !c.retired {
		var timer *time.Timer
		timer = time.AfterFunc(m.config.IdleTimeout, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if c.users == 0 && c.waiters == 0 && m.entries[c.target] == c && c.idle == timer {
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
	closed     bool // Protected by manager.mu.
}

func (s *Session) Close() error {
	s.once.Do(func() {
		s.manager.mu.Lock()
		defer s.manager.mu.Unlock()
		s.closed = true
		s.manager.closeChannelLocked(s.connection, s.Session)
		s.manager.releaseLocked(s.connection)
	})
	return nil
}

func (s *Session) Setup(ctx context.Context, setup func(*ssh.Session) error) error {
	started := time.Now()
	ctx, cancel := context.WithTimeoutCause(ctx, s.manager.config.SetupTimeout, errSetupTimeout)
	defer cancel()
	s.manager.mu.Lock()
	closed := s.closed
	s.manager.mu.Unlock()
	if closed {
		return safeError(ctx, ErrUnavailable)
	}
	result, err := s.manager.channelOperation(ctx, s.connection, func() channelResult { return channelResult{err: setup(s.Session)} })
	if ctx.Err() != nil || err != nil || result.err != nil {
		_ = s.Close()
		return Diagnose(StagePTYRequest, errors.Join(s.manager.connectionError(ctx, s.connection), err, result.err), started)
	}
	s.manager.mu.Lock()
	err = s.manager.targetErrorLocked(s.connection.target)
	if (s.connection.retired || s.closed) && err == nil {
		err = ErrUnavailable
	}
	s.manager.mu.Unlock()
	if err != nil {
		_ = s.Close()
		return err
	}
	return nil
}

type channelResult struct {
	session *ssh.Session
	conn    net.Conn
	err     error
}

func (r channelResult) closer() io.Closer {
	if r.session != nil {
		return r.session
	}
	if r.conn != nil {
		return r.conn
	}
	return nil
}

// The SSH API cannot cancel an open/request. Keep at most one such operation
// per transport; canceled waiters start no worker. The worker owns late results
// until the peer replies or idle expiry, invalidation or shutdown closes SSH.
func (m *Manager) channelOperation(ctx context.Context, c *connection, operation func() channelResult) (channelResult, error) {
	select {
	case c.opening <- struct{}{}:
	case <-ctx.Done():
		return channelResult{}, ctx.Err()
	case <-c.done:
		return channelResult{}, ErrUnavailable
	}
	if ctx.Err() != nil {
		<-c.opening
		return channelResult{}, ctx.Err()
	}
	select {
	case <-c.done:
		<-c.opening
		return channelResult{}, ErrUnavailable
	default:
	}
	result := make(chan channelResult)
	go func() {
		defer func() { <-c.opening }()
		r := operation()
		select {
		case result <- r: // Unbuffered delivery transfers ownership to the caller.
		case <-ctx.Done():
			m.discardResult(c, r)
		case <-c.done:
			m.discardResult(c, r)
		}
	}()
	select {
	case r := <-result:
		return r, nil
	case <-ctx.Done():
		return channelResult{}, ctx.Err()
	case <-c.done:
		return channelResult{}, ErrUnavailable
	}
}

func (m *Manager) discardResult(c *connection, result channelResult) {
	if result.conn != nil {
		// TCP results are managed pipes: close locally even if the transport's
		// raw SSH close writer is stalled on an earlier channel.
		_ = result.conn.Close()
		return
	}
	m.closeChannel(c, result.closer())
}

func (m *Manager) connectionError(ctx context.Context, c *connection) error {
	m.mu.Lock()
	err := m.targetErrorLocked(c.target)
	m.mu.Unlock()
	if err == nil {
		err = ErrUnavailable
	}
	return safeError(ctx, err)
}

func (m *Manager) closeChannel(c *connection, session io.Closer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeChannelLocked(c, session)
}

func (m *Manager) closeChannelLocked(c *connection, session io.Closer) {
	if session == nil || c.retired {
		return
	}
	c.closing = append(c.closing, session)
	select {
	case c.closeWake <- struct{}{}:
	default:
	}
}

// One cleanup worker per transport keeps a blocked channel-close write from
// blocking cancellation, reference release or idle retirement of the socket.
func (m *Manager) closeChannels(c *connection) {
	for {
		m.mu.Lock()
		if c.retired {
			m.mu.Unlock()
			return
		}
		var session io.Closer
		if len(c.closing) > 0 {
			session = c.closing[0]
			c.closing[0] = nil
			c.closing = c.closing[1:]
		}
		m.mu.Unlock()
		if session != nil {
			_ = session.Close()
			continue
		}
		select {
		case <-c.closeWake:
		case <-c.done:
			return
		}
	}
}

func safeError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		if errors.Is(context.Cause(ctx), errSetupTimeout) || errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			return errors.Join(fallback, context.DeadlineExceeded)
		}
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
	return dialSSHTracked(ctx, endpoint, &dialProgress{started: time.Now()})
}

type dialProgress struct {
	started       time.Time
	stage         atomic.Uint32
	handshake     atomic.Pointer[handshakeConn]
	contextClosed atomic.Bool
}

func (p *dialProgress) failure(dialCtx, callerCtx context.Context) error {
	d := Diagnose(FailureStage(p.stage.Load()), safeError(callerCtx, ErrUnavailable), p.started).(*diagnosticError)
	if h := p.handshake.Load(); h != nil {
		d.info.SSHPhase = SSHPhase(h.phase.Load())
	}
	d.info.ContextEnd = contextEnd(dialCtx)
	d.info.ContextClosedSocket = p.contextClosed.Load()
	return d
}

func dialSSHTracked(ctx context.Context, endpoint Endpoint, tracked *dialProgress) (*ssh.Client, error) {
	started := tracked.started
	tracked.stage.Store(uint32(StagePrivateKey))
	key, err := os.ReadFile(endpoint.PrivateKeyFile)
	if err != nil {
		return nil, Diagnose(StagePrivateKey, err, started)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, Diagnose(StagePrivateKey, err, started)
	}
	tracked.stage.Store(uint32(StageDial))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return nil, Diagnose(StageDial, errors.Join(err, safeError(ctx, ErrUnavailable)), started)
	}
	stop := context.AfterFunc(ctx, func() { tracked.contextClosed.Store(true); _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	pinFailed := false
	progress := &handshakeConn{Conn: conn}
	progress.phase.Store(uint32(SSHVersionExchange))
	tracked.handshake.Store(progress)
	tracked.stage.Store(uint32(StageHandshake))
	cc, channels, requests, err := ssh.NewClientConn(progress, endpoint.Address, &ssh.ClientConfig{
		User: endpoint.Username, Auth: []ssh.AuthMethod{ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			progress.phase.Store(uint32(SSHAuthentication))
			return []ssh.Signer{signer}, nil
		})}, HostKeyCallback: func(host string, remote net.Addr, key ssh.PublicKey) error {
			progress.phase.Store(uint32(SSHHostVerification))
			err := endpoint.HostKeyCallback(host, remote, key)
			pinFailed = err != nil
			return err
		},
	})
	if err == nil {
		progress.phase.Store(uint32(SSHComplete))
	}
	if !stop() || ctx.Err() != nil || err != nil {
		_ = conn.Close()
		stage := StageHandshake
		if pinFailed {
			stage = StageHostPin
		}
		d := Diagnose(stage, errors.Join(err, safeError(ctx, ErrUnavailable)), started).(*diagnosticError)
		d.info.SSHPhase = SSHPhase(progress.phase.Load())
		d.info.HandshakeReturnedError = err != nil
		d.info.ContextEnd = contextEnd(ctx)
		d.info.ContextClosedSocket = tracked.contextClosed.Load()
		var ne net.Error
		d.info.TransportTimeout = errors.As(err, &ne) && ne.Timeout()
		return nil, d
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, ErrUnavailable
	}
	return ssh.NewClient(cc, channels, requests), nil
}

// Observe only completion of the protocol identification line. No peer bytes
// are copied, retained, or logged. The encrypted transport remains untouched.
type handshakeConn struct {
	net.Conn
	phase         atomic.Uint32
	linePos       int
	versionPrefix bool
}

func (c *handshakeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if SSHPhase(c.phase.Load()) != SSHVersionExchange {
		return n, err
	}
	for _, b := range p[:n] {
		if c.linePos == 0 {
			c.versionPrefix = true
		}
		if c.linePos < 4 && b != "SSH-"[c.linePos] {
			c.versionPrefix = false
		}
		if b == '\n' {
			if c.versionPrefix && c.linePos >= 4 {
				c.phase.CompareAndSwap(uint32(SSHVersionExchange), uint32(SSHKeyExchange))
				break
			}
			c.linePos = 0
		} else if c.linePos < 4 {
			c.linePos++
		}
	}
	return n, err
}
