package terminal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func generateTestEd25519Key(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	return signer, priv
}

// startTestSSHServer는 테스트용 인프로세스 SSH 서버를 구동하여 실제 PTY, 윈도우 리사이즈, 셸 I/O를 시뮬레이션합니다.
func startTestSSHServer(t *testing.T) (address string, clientSigner ssh.Signer, clientPriv ed25519.PrivateKey, serverSigner ssh.Signer, cleanup func()) {
	t.Helper()

	var serverPriv ed25519.PrivateKey
	serverSigner, serverPriv = generateTestEd25519Key(t)
	_ = serverPriv
	clientSigner, clientPriv = generateTestEd25519Key(t)

	serverConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
				return &ssh.Permissions{}, nil
			}
			return nil, ssh.ErrNoAuth
		},
	}
	serverConfig.AddHostKey(serverSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on loopback: %v", err)
	}

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}

			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()

				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					return
				}
				defer sshConn.Close()

				go ssh.DiscardRequests(reqs)

				for newChannel := range chans {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
						continue
					}

					channel, requests, err := newChannel.Accept()
					if err != nil {
						return
					}

					go func(ch ssh.Channel, inReqs <-chan *ssh.Request) {
						defer ch.Close()

						for req := range inReqs {
							switch req.Type {
							case "pty-req":
								_ = req.Reply(true, nil)
							case "window-change":
								_ = req.Reply(true, nil)
							case "shell":
								_ = req.Reply(true, nil)
								// Echo 루프: 채널로 들어온 입력을 그대로 에코 회신
								buf := make([]byte, 1024)
								for {
									n, readErr := ch.Read(buf)
									if n > 0 {
										_, _ = ch.Write(buf[:n])
									}
									if readErr != nil {
										return
									}
								}
							default:
								_ = req.Reply(false, nil)
							}
						}
					}(channel, requests)
				}
			}(tcpConn)
		}
	}()

	cleanup = func() {
		cancel()
		_ = listener.Close()
		_ = ctx
	}

	return listener.Addr().String(), clientSigner, clientPriv, serverSigner, cleanup
}

func TestSSHPTY_RealSSH_EchoAndResize(t *testing.T) {
	addr, clientSigner, _, serverSigner, cleanup := startTestSSHServer(t)
	defer cleanup()

	clientCfg := &ssh.ClientConfig{
		User:            "testuser",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		HostKeyCallback: ssh.FixedHostKey(serverSigner.PublicKey()),
		Timeout:         5 * time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pty, err := DialSSHPTY(ctx, addr, clientCfg, 80, 24)
	if err != nil {
		t.Fatalf("DialSSHPTY failed: %v", err)
	}
	defer pty.Close()

	// 1. 입출력 검증 (Write -> Read Echo)
	testInput := []byte("hello ssh pty\n")
	n, err := pty.Write(testInput)
	if err != nil || n != len(testInput) {
		t.Fatalf("pty.Write() failed: n=%d, err=%v", n, err)
	}

	readBuf := make([]byte, 1024)
	readBytes, err := pty.Read(readBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("pty.Read() failed: %v", err)
	}

	if !bytes.Contains(readBuf[:readBytes], []byte("hello ssh pty")) {
		t.Fatalf("expected echo to contain 'hello ssh pty', got: %q", string(readBuf[:readBytes]))
	}

	// 2. 창 크기 변경 (Resize / window-change) 검증
	if err := pty.Resize(120, 40); err != nil {
		t.Fatalf("pty.Resize(120, 40) failed: %v", err)
	}

	// 3. 닫기 검증
	if err := pty.Close(); err != nil {
		t.Fatalf("pty.Close() failed: %v", err)
	}

	// 닫힌 후 쓰기 시 에러 반환 확인
	_, writeAfterCloseErr := pty.Write([]byte("after close"))
	if writeAfterCloseErr == nil {
		t.Fatalf("expected error on write after close, got nil")
	}
}

func TestSSHPTYFactory_ProductionKnownHosts_Success(t *testing.T) {
	addr, _, clientPriv, serverSigner, cleanup := startTestSSHServer(t)
	defer cleanup()

	// 1. Client Private Key PEM 직렬화
	block, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	privKeyBytes := pem.EncodeToMemory(block)

	// 2. Known_hosts 파일 생성
	knownHostsDir := t.TempDir()
	knownHostsPath := filepath.Join(knownHostsDir, "known_hosts")
	hostKeyLine := knownhosts.Line([]string{addr}, serverSigner.PublicKey()) + "\n"
	if err := os.WriteFile(knownHostsPath, []byte(hostKeyLine), 0600); err != nil {
		t.Fatalf("failed to write known_hosts: %v", err)
	}

	// 3. Factory 생성 및 호출 검증
	factory := NewSSHPTYFactory(SSHConfig{
		Username:       "testuser",
		PrivateKey:     privKeyBytes,
		KnownHostsFile: knownHostsPath,
		DialTimeout:    5 * time.Second,
		AddressResolver: func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			if serverID != "srv-1" {
				return "", fmt.Errorf("unknown server %s", serverID)
			}
			return addr, nil
		},
	})

	pty, err := factory("vm-1", "srv-1", 80, 24)
	if err != nil {
		t.Fatalf("factory call failed: %v", err)
	}
	defer pty.Close()

	// PTY I/O, Resize, Close 동작 검증
	_, err = pty.Write([]byte("factory echo test\n"))
	if err != nil {
		t.Fatalf("pty.Write failed: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := pty.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("pty.Read failed: %v", err)
	}
	if !bytes.Contains(buf[:n], []byte("factory echo test")) {
		t.Fatalf("expected echo, got: %q", string(buf[:n]))
	}

	if err := pty.Resize(100, 30); err != nil {
		t.Fatalf("pty.Resize failed: %v", err)
	}
}

func TestSSHPTYFactory_FailClosed_NoKnownHosts(t *testing.T) {
	addr, _, clientPriv, _, cleanup := startTestSSHServer(t)
	defer cleanup()

	block, _ := ssh.MarshalPrivateKey(clientPriv, "")
	privKeyBytes := pem.EncodeToMemory(block)

	// KnownHostsFile 이 없고 AllowInsecureHostKey 가 false 인 프로덕션 모드
	factory := NewSSHPTYFactory(SSHConfig{
		Username:             "testuser",
		PrivateKey:           privKeyBytes,
		KnownHostsFile:       "",
		AllowInsecureHostKey: false,
		DialTimeout:          2 * time.Second,
		AddressResolver: func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			return addr, nil
		},
	})

	_, err := factory("vm-1", "srv-1", 80, 24)
	if err == nil {
		t.Fatalf("expected error when known_hosts is not configured in production, got nil")
	}
}

func TestSSHPTYFactory_FailClosed_MismatchedKnownHosts(t *testing.T) {
	addr, _, clientPriv, _, cleanup := startTestSSHServer(t)
	defer cleanup()

	block, _ := ssh.MarshalPrivateKey(clientPriv, "")
	privKeyBytes := pem.EncodeToMemory(block)

	// 다른 공개키를 가진 bogus known_hosts 파일 생성
	_, bogusPriv := generateTestEd25519Key(t)
	bogusSigner, _ := ssh.NewSignerFromKey(bogusPriv)

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	hostKeyLine := knownhosts.Line([]string{addr}, bogusSigner.PublicKey()) + "\n"
	_ = os.WriteFile(knownHostsPath, []byte(hostKeyLine), 0600)

	factory := NewSSHPTYFactory(SSHConfig{
		Username:       "testuser",
		PrivateKey:     privKeyBytes,
		KnownHostsFile: knownHostsPath,
		DialTimeout:    2 * time.Second,
		AddressResolver: func(ctx context.Context, targetVmKey, serverID string) (string, error) {
			return addr, nil
		},
	})

	_, err := factory("vm-1", "srv-1", 80, 24)
	if err == nil {
		t.Fatalf("expected host key verification error on mismatched known_hosts, got nil")
	}
}

func TestSSHPTYFactory_Fail_AddressResolver(t *testing.T) {
	t.Run("resolver returns error", func(t *testing.T) {
		factory := NewSSHPTYFactory(SSHConfig{
			Username: "testuser",
			AddressResolver: func(ctx context.Context, targetVmKey, serverID string) (string, error) {
				return "", fmt.Errorf("server %s not found in provider", serverID)
			},
		})
		_, err := factory("vm-1", "unknown-srv", 80, 24)
		if err == nil {
			t.Fatalf("expected resolver error, got nil")
		}
	})

	t.Run("missing resolver rejects targetVmKey fallback", func(t *testing.T) {
		factory := NewSSHPTYFactory(SSHConfig{
			Username:        "testuser",
			AddressResolver: nil,
		})
		_, err := factory("vm-logical-key", "srv-1", 80, 24)
		if err == nil {
			t.Fatalf("expected error when address resolver is nil, got nil")
		}
	})
}
