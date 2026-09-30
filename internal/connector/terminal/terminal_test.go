package terminal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/terminal"
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

func TestSessionManager_GenerationMismatch_Rejects(t *testing.T) {
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	// 초기 세션 생성: generation = 1
	_, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-mismatch", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("first GetOrCreateSession failed: %v", err)
	}

	// 1. Generation N+1 (2 > 1) 요청 시 기존 세션 재사용 거부 및 ErrSessionConflict 반환 검증 (Reviewer 5번 지적 사항)
	_, _, err = mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-mismatch", LabInstanceID: "inst-1", Generation: 2},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if !errors.Is(err, terminal.ErrSessionConflict) {
		t.Fatalf("expected ErrSessionConflict for higher generation, got: %v", err)
	}

	// 2. Generation N-1 (0 < 1) 요청 시 ErrStaleGeneration 반환 검증
	_, _, err = mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-mismatch", LabInstanceID: "inst-1", Generation: 0},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if !errors.Is(err, terminal.ErrStaleGeneration) {
		t.Fatalf("expected ErrStaleGeneration for lower generation, got: %v", err)
	}

	// 3. TargetVmKey 또는 ServerId 불일치 요청 시 ErrSessionConflict 반환 검증
	_, _, err = mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-DIFFERENT", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-mismatch", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if !errors.Is(err, terminal.ErrSessionConflict) {
		t.Fatalf("expected ErrSessionConflict for targetVmKey mismatch, got: %v", err)
	}

	// 4. 동일한 Generation/Target 요청 시 안전하게 재사용(reused = true) 검증
	existing, reused, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-mismatch", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if err != nil || !reused || existing == nil {
		t.Fatalf("expected session reuse, got reused=%v, err=%v", reused, err)
	}
}

func TestSession_Close_SendsTerminalDataEnded(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-ended-1", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: relay.URL(),
		RuntimeID:   "rt-ended",
	}, session)
	defer client.Close()

	if err := client.DialAndAttach(context.Background()); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}
	client.StartStreaming()

	// 세션 닫기 호출 -> TERMINAL_DATA_ENDED 프레임 전송 검증 (Reviewer 4번 지적 사항)
	session.Close(protocol.TerminalReasonSessionClosed, nil, nil)

	data, err := relay.ReadText(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive TERMINAL_DATA_ENDED text frame: %v", err)
	}

	var endedMsg protocol.TerminalDataEndedMessage
	if err := json.Unmarshal(data, &endedMsg); err != nil {
		t.Fatalf("failed to unmarshal TERMINAL_DATA_ENDED: %v", err)
	}
	if endedMsg.Type != protocol.MessageTypeTerminalDataEnded {
		t.Fatalf("expected type TERMINAL_DATA_ENDED, got %s", endedMsg.Type)
	}
	if endedMsg.Payload.Reason != protocol.TerminalReasonSessionClosed {
		t.Fatalf("expected reason %s, got %s", protocol.TerminalReasonSessionClosed, endedMsg.Payload.Reason)
	}
}

func TestDataWSSClient_AttachCorrelationMismatch_Fails(t *testing.T) {
	// 잘못된 replyToMessageId 를 회신하는 Mock 서버
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read attach
		_, _, _ = conn.ReadMessage()

		// Write malformed replyTo
		badResp := protocol.TerminalDataAttachedMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalDataAttached,
				MessageID:         "msg-resp",
				ReplyToMessageID:  "WRONG-REPLY-TO-ID", // mismatch
				TerminalSessionID: "sess-bad-attach",
				LabInstanceID:     "inst-1",
				Generation:        1,
			},
		}
		_ = conn.WriteJSON(badResp)
	}))
	defer server.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-bad-attach", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	wsURL := "ws" + server.URL[len("http"):]
	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL: wsURL,
		DialTimeout: 1 * time.Second,
	}, session)
	defer client.Close()

	attachErr := client.DialAndAttach(context.Background())
	if attachErr == nil {
		t.Fatal("expected DialAndAttach to fail on replyTo mismatch, got nil")
	}
}
