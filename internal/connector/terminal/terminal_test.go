package terminal_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ktcloud4-SL/rabbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/rabbit-app/internal/connector/terminal"
)

func TestTerminal_OpenAndEchoStreaming(t *testing.T) {
	// 1. Mock Terminal Relay 기동
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	// 2. SessionManager 생성
	mgr := terminal.NewSessionManager(5*time.Second, nil)

	// 3. 세션 생성 및 Mock Echo PTY 바인딩
	openPayload := protocol.TerminalOpenPayload{
		TargetVmKey:      "vm-1",
		ProviderServerID: "srv-uuid-1",
		Cols:             80,
		Rows:             24,
	}
	env := protocol.BaseEnvelope{
		TerminalSessionID: "sess-echo-1",
		LabInstanceID:     "inst-1",
		Generation:        1,
	}

	session, reused, err := mgr.GetOrCreateSession(openPayload, env, func() (terminal.PTYChannel, error) {
		return terminal.NewMockEchoPTY(80, 24), nil
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if reused {
		t.Fatalf("expected new session, but got reused")
	}

	// 4. Data WSS Client 연결 및 스트리밍 시작
	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "runtime-test-1",
		DialTimeout: 2 * time.Second,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}

	// 5. Relay 측에서 ATTACH 수신 확인
	attachMsg, err := relay.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive attach: %v", err)
	}
	if attachMsg.TerminalSessionID != "sess-echo-1" {
		t.Fatalf("expected session ID sess-echo-1, got %s", attachMsg.TerminalSessionID)
	}

	// 6. 브라우저 키보드 입력 시뮬레이션: "ls -la\n"
	inputData := []byte("ls -la\n")
	if err := relay.SendBinary(inputData); err != nil {
		t.Fatalf("SendBinary failed: %v", err)
	}

	// 7. PTY Echo 출력 수신 검증
	output, err := relay.ReadBinary(2 * time.Second)
	if err != nil {
		t.Fatalf("ReadBinary failed: %v", err)
	}
	if !bytes.Equal(output, inputData) {
		t.Fatalf("expected output %q, got %q", inputData, output)
	}
}

func TestTerminal_Resize(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-resize-1", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "runtime-test-2",
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}

	_, _ = relay.WaitForAttach(2 * time.Second)

	// 브라우저 창 크기 변경 제어 프레임 전송 (120x40)
	if err := relay.SendResize(120, 40); err != nil {
		t.Fatalf("SendResize failed: %v", err)
	}

	// 약간의 반영 시간 대기
	time.Sleep(100 * time.Millisecond)

	if session.Cols != 120 || session.Rows != 40 {
		t.Fatalf("expected 120x40 in session, got %dx%d", session.Cols, session.Rows)
	}
	if pty.Cols != 120 || pty.Rows != 40 {
		t.Fatalf("expected 120x40 in PTY, got %dx%d", pty.Cols, pty.Rows)
	}
}

func TestTerminal_GracePeriod_Resume(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	// 5초 Grace Period
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-grace-1", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	client1 := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "rt-1",
	}, session)

	if err := client1.ConnectAndStream(); err != nil {
		t.Fatalf("initial connect failed: %v", err)
	}
	_, _ = relay.WaitForAttach(2 * time.Second)

	// 브라우저 탭 닫힘 / 일시적 단절 시뮬레이션
	relay.DisconnectConnection()
	client1.Close()

	// Detached 상태 확인
	time.Sleep(50 * time.Millisecond)
	if session.Status != terminal.StatusDetached {
		t.Fatalf("expected StatusDetached, got %s", session.Status)
	}

	// 60초 만료 전에 브라우저 새로고침으로 재접속 시뮬레이션
	client2 := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "rt-1",
	}, session)
	defer client2.Close()

	if err := client2.ConnectAndStream(); err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}

	if session.Status != terminal.StatusActive {
		t.Fatalf("expected session to resume to StatusActive, got %s", session.Status)
	}

	// 재연결 후에도 PTY 입출력이 살아있는지 확인
	testBytes := []byte("still alive\n")
	if err := relay.SendBinary(testBytes); err != nil {
		t.Fatalf("SendBinary after resume failed: %v", err)
	}
	output, err := relay.ReadBinary(2 * time.Second)
	if err != nil {
		t.Fatalf("ReadBinary after resume failed: %v", err)
	}
	if !bytes.Equal(output, testBytes) {
		t.Fatalf("expected %q, got %q", testBytes, output)
	}
}

func TestTerminal_GracePeriod_Timeout(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	endedChan := make(chan string, 1)
	// 빠른 타임아웃 테스트를 위해 150ms Grace Period 설정
	mgr := terminal.NewSessionManager(150*time.Millisecond, func(s *terminal.Session, reason string, exitCode *int, err error) {
		endedChan <- reason
	})

	pty := terminal.NewMockEchoPTY(80, 24)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-timeout-1", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "rt-1",
	}, session)

	_ = client.ConnectAndStream()
	_, _ = relay.WaitForAttach(2 * time.Second)

	// 단절 발생
	relay.DisconnectConnection()
	client.Close()

	// 150ms 초과 대기 후 Grace Period 만료 확인
	select {
	case reason := <-endedChan:
		if reason != protocol.TerminalReasonGraceTimeout {
			t.Fatalf("expected reason %s, got %s", protocol.TerminalReasonGraceTimeout, reason)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for grace period expiration callback")
	}

	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected StatusClosed, got %s", session.Status)
	}
	if mgr.ActiveCount() != 0 {
		t.Fatalf("expected 0 active sessions, got %d", mgr.ActiveCount())
	}
}

func TestTerminal_Close_Idempotent(t *testing.T) {
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-close-1", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// 1차 Close
	if err := mgr.CloseSession(session.SessionID, protocol.TerminalReasonSessionClosed); err != nil {
		t.Fatalf("first CloseSession failed: %v", err)
	}

	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected StatusClosed, got %s", session.Status)
	}

	// 2차 Close (이미 정리된 세션에 대한 닫기 요청 - 에러 없이 멱등성 유지)
	_ = mgr.CloseSession(session.SessionID, protocol.TerminalReasonSessionClosed)

	if mgr.ActiveCount() != 0 {
		t.Fatalf("expected 0 active sessions, got %d", mgr.ActiveCount())
	}
}

