package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SSHConfig는 대상 Workspace VM 접속을 위한 SSH 클라이언트 설정입니다.
type SSHConfig struct {
	Username             string
	PrivateKey           []byte
	PrivateKeyFile       string
	KnownHostsFile       string
	HostKeyCallback      ssh.HostKeyCallback
	AllowInsecureHostKey bool // 테스트 전용: 명시적 설정 시에만 비보안 호스트키 허용
	DialTimeout          time.Duration
	AddressResolver      func(ctx context.Context, targetVmKey, serverID string) (string, error)
}

// SSHPTY는 실제 SSH 세션과 PTY 채널을 래핑하여 PTYChannel 인터페이스를 구현합니다.
type SSHPTY struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	mu       sync.Mutex
	cols     int
	rows     int
	closed   bool
	waitDone chan struct{}
	exitCode *int
	waitErr  error
}

// NewSSHPTY는 기존 활성 SSH 클라이언트로부터 PTY 세션을 생성하고 셸을 시작합니다.
func NewSSHPTY(client *ssh.Client, cols, rows int) (*SSHPTY, error) {
	if client == nil {
		return nil, errors.New("ssh client cannot be nil")
	}

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}

	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	if err := session.Shell(); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("failed to start shell: %w", err)
	}

	pty := &SSHPTY{
		client:   client,
		session:  session,
		stdin:    stdin,
		stdout:   stdout,
		cols:     cols,
		rows:     rows,
		waitDone: make(chan struct{}),
	}

	go func() {
		defer close(pty.waitDone)
		waitErr := session.Wait()
		pty.mu.Lock()
		defer pty.mu.Unlock()
		pty.waitErr = waitErr
		if waitErr == nil {
			zero := 0
			pty.exitCode = &zero
		} else {
			var exitErr *ssh.ExitError
			if errors.As(waitErr, &exitErr) {
				code := exitErr.ExitStatus()
				pty.exitCode = &code
			}
		}
	}()

	return pty, nil
}

// ExitStatus 는 관측된 원격 프로세스의 종료 코드를 반환합니다.
func (p *SSHPTY) ExitStatus() (exitCode *int, exited bool) {
	select {
	case <-p.waitDone:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.exitCode, true
	default:
		return nil, false
	}
}

// Read는 PTY stdout 출력을 읽습니다.
func (p *SSHPTY) Read(buf []byte) (int, error) {
	return p.stdout.Read(buf)
}

// Write는 PTY stdin으로 입력을 전달합니다.
func (p *SSHPTY) Write(buf []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	return p.stdin.Write(buf)
}

// Resize는 터미널 창 크기(cols, rows)를 변경하는 RFC 4254 window-change 요청을 보냅니다.
func (p *SSHPTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return io.ErrClosedPipe
	}
	p.cols = cols
	p.rows = rows
	if err := p.session.WindowChange(rows, cols); err != nil {
		return fmt.Errorf("window-change failed: %w", err)
	}
	return nil
}

// Close는 SSH 세션 및 연결을 닫습니다.
func (p *SSHPTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true

	var errs []error
	if p.stdin != nil {
		if err := p.stdin.Close(); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, err)
		}
	}
	if p.session != nil {
		if err := p.session.Close(); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, err)
		}
	}
	if p.client != nil {
		if err := p.client.Close(); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DialSSHPTY는 지정된 주소로 SSH를 연결하고 새 SSHPTY를 반환합니다.
func DialSSHPTY(ctx context.Context, address string, sshConfig *ssh.ClientConfig, cols, rows int) (*SSHPTY, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial ssh target %s: %w", address, err)
	}

	clientConn, channels, reqs, err := ssh.NewClientConn(conn, address, sshConfig)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake failed with %s: %w", address, err)
	}

	client := ssh.NewClient(clientConn, channels, reqs)
	pty, err := NewSSHPTY(client, cols, rows)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return pty, nil
}

// NewSSHPTYFactory는 SSHConfig를 기반으로 targetVmKey/serverId를 SSH PTY로 변환하는 팩토리 함수를 생성합니다.
func NewSSHPTYFactory(cfg SSHConfig) func(targetVmKey string, serverID string, cols, rows int) (PTYChannel, error) {
	return func(targetVmKey string, serverID string, cols, rows int) (PTYChannel, error) {
		if cfg.AddressResolver == nil {
			return nil, fmt.Errorf("management address resolver is required: cannot resolve target VM %q without resolver", targetVmKey)
		}

		timeout := cfg.DialTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		address, err := cfg.AddressResolver(ctx, targetVmKey, serverID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve management address for target %s (server %s): %w", targetVmKey, serverID, err)
		}
		address = strings.TrimSpace(address)
		if address == "" {
			return nil, fmt.Errorf("resolved management address is empty for target %s (server %s)", targetVmKey, serverID)
		}

		if _, _, err := net.SplitHostPort(address); err != nil {
			address = net.JoinHostPort(address, "22")
		}

		var authMethods []ssh.AuthMethod
		var keyBytes []byte
		if len(cfg.PrivateKey) > 0 {
			keyBytes = cfg.PrivateKey
		} else if cfg.PrivateKeyFile != "" {
			var err error
			keyBytes, err = os.ReadFile(cfg.PrivateKeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read ssh private key file: %w", err)
			}
		}

		if len(keyBytes) > 0 {
			signer, err := ssh.ParsePrivateKey(keyBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse ssh private key: %w", err)
			}
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}

		var hostKeyCallback ssh.HostKeyCallback
		if cfg.HostKeyCallback != nil {
			hostKeyCallback = cfg.HostKeyCallback
		} else if cfg.KnownHostsFile != "" {
			cb, err := knownhosts.New(cfg.KnownHostsFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load known_hosts file: %w", err)
			}
			hostKeyCallback = cb
		} else if cfg.AllowInsecureHostKey {
			hostKeyCallback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				return nil
			}
		} else {
			return nil, fmt.Errorf("ssh host key verification required: known_hosts file must be configured in production")
		}

		clientCfg := &ssh.ClientConfig{
			User:            cfg.Username,
			Auth:            authMethods,
			HostKeyCallback: hostKeyCallback,
			Timeout:         timeout,
		}

		return DialSSHPTY(ctx, address, clientCfg, cols, rows)
	}
}
