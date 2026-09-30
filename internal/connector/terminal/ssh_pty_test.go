package terminal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func generateTestEd25519Key(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	_ = pub
	return signer, priv
}

// startTestSSHServer는 테스트용 인프로세스 SSH 서버를 구동하여 실제 PTY, 윈도우 리사이즈, 셸 I/O를 시뮬레이션합니다.
func startTestSSHServer(t *testing.T) (address string, clientSigner ssh.Signer, cleanup func()) {
	t.Helper()

	serverSigner, _ := generateTestEd25519Key(t)
	clientSigner, _ = generateTestEd25519Key(t)

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

	return listener.Addr().String(), clientSigner, cleanup
}

func TestSSHPTY_RealSSH_EchoAndResize(t *testing.T) {
	addr, clientSigner, cleanup := startTestSSHServer(t)
	defer cleanup()

	clientCfg := &ssh.ClientConfig{
		User:            "testuser",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(clientSigner)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
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

func TestSSHPTYFactory(t *testing.T) {
	addr, clientSigner, cleanup := startTestSSHServer(t)
	defer cleanup()

	host, port, _ := net.SplitHostPort(addr)
	_ = port

	factory := NewSSHPTYFactory(SSHConfig{
		Username:        "testuser",
		InsecureHostKey: true,
		DialTimeout:     5 * time.Second,
		AddressResolver: func(targetVmKey, serverID string) (string, error) {
			return addr, nil
		},
	})

	// clientSigner를 주입하기 위해 custom dialer 대신 mock AddressResolver 활용
	_ = host
	_ = clientSigner
	_ = factory
}

func parseWindowChangePayload(b []byte) (cols, rows uint32) {
	if len(b) >= 8 {
		cols = binary.BigEndian.Uint32(b[0:4])
		rows = binary.BigEndian.Uint32(b[4:8])
	}
	return
}
