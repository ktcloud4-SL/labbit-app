package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"golang.org/x/crypto/ssh"
)

type finalBytesPTY struct {
	PTYChannel
	ready chan struct{}
	code  *int
	err   error
}

func (p *finalBytesPTY) Read(buf []byte) (int, error) {
	<-p.ready
	return copy(buf, "final-output"), io.EOF
}
func (p *finalBytesPTY) WaitTermination() (*int, error) { return p.code, p.err }

func TestFinalOutputAndRemoteTermination(t *testing.T) {
	zero, seven := 0, 7
	for _, tc := range []struct {
		name   string
		code   *int
		err    error
		reason string
	}{
		{"normal", &zero, nil, protocol.TerminalReasonPtyExited},
		{"nonzero", &seven, errors.New("remote exit"), protocol.TerminalReasonPtyExited},
		{"disconnect", nil, errors.New("private SSH target detail"), protocol.TerminalReasonSSHDisconnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relay := mock.NewTerminalRelay()
			defer relay.Close()
			manager := NewSessionManager(time.Second, nil)
			defer manager.CloseAll(protocol.TerminalReasonServiceRestart)
			pty := &finalBytesPTY{PTYChannel: NewMockEchoPTY(80, 24), ready: make(chan struct{}), code: tc.code, err: tc.err}
			session, _, err := manager.GetOrCreateSession(protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}, protocol.BaseEnvelope{TerminalSessionID: "terminal-1", LabInstanceID: "lab-1", Generation: 1}, func() (PTYChannel, error) { return pty, nil })
			if err != nil {
				t.Fatal(err)
			}
			client := NewDataWSSClient(DataWSSClientConfig{EndpointURL: relay.URL(), AllowInsecure: true}, session)
			defer client.Close()
			if err := client.ConnectAndStream(); err != nil {
				t.Fatal(err)
			}
			close(pty.ready)
			output, err := relay.ReadBinary(time.Second)
			if err != nil || string(output) != "final-output" {
				t.Fatal("final bytes were lost before ENDED")
			}
			raw, err := relay.ReadText(time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var ended protocol.TerminalDataEndedMessage
			if err := json.Unmarshal(raw, &ended); err != nil {
				t.Fatal(err)
			}
			if ended.Payload.Reason != tc.reason {
				t.Fatalf("reason=%s", ended.Payload.Reason)
			}
			if tc.code != nil && (ended.Payload.ExitCode == nil || *ended.Payload.ExitCode != *tc.code) {
				t.Fatal("remote exit code lost")
			}
			if tc.code == nil && (ended.Payload.Error == nil || strings.Contains(string(raw), tc.err.Error())) {
				t.Fatal("disconnect error not safely classified")
			}
		})
	}
}

func TestSSHPTYDrainsBothStreamsBeforeExitStatus(t *testing.T) {
	host, _ := generateTestEd25519Key(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		config := &ssh.ServerConfig{NoClientAuth: true}
		config.AddHostKey(host)
		server, channels, requests, err := ssh.NewServerConn(conn, config)
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
			for request := range requests {
				_ = request.Reply(true, nil)
				if request.Type == "shell" {
					_, _ = channel.Write([]byte("stdout-final"))
					_, _ = channel.Stderr().Write([]byte("stderr-final"))
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Code uint32 }{7}))
					_ = channel.Close()
					break
				}
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pty, err := DialSSHPTY(ctx, listener.Addr().String(), &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.FixedHostKey(host.PublicKey())}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer pty.Close()
	data, err := io.ReadAll(pty)
	if err != nil || !strings.Contains(string(data), "stdout-final") || !strings.Contains(string(data), "stderr-final") {
		t.Fatal("terminal output stream was truncated")
	}
	code, err := pty.WaitTermination()
	if code == nil || *code != 7 || err == nil {
		t.Fatal("remote shell status was not observed")
	}
}

func TestCompletedTerminalCannotAllocateAnotherPTY(t *testing.T) {
	manager := NewSessionManager(time.Second, nil)
	payload := protocol.TerminalOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server-1", Cols: 80, Rows: 24}
	envelope := protocol.BaseEnvelope{TerminalSessionID: "terminal-1", LabInstanceID: "lab-1", Generation: 1}
	allocations := 0
	factory := func() (PTYChannel, error) { allocations++; return NewMockEchoPTY(80, 24), nil }
	session, _, err := manager.GetOrCreateSession(payload, envelope, factory)
	if err != nil {
		t.Fatal(err)
	}
	session.Close(protocol.TerminalReasonSessionClosed, nil, nil)
	if _, _, err := manager.GetOrCreateSession(payload, envelope, factory); !errors.Is(err, ErrSessionClosed) {
		t.Fatal("completed TerminalSession was resurrected")
	}
	if allocations != 1 || manager.ActiveCount() != 0 {
		t.Fatal("PTY leaked or was reallocated")
	}
}
