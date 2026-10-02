package terminal_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
		EndpointURL:   relay.URL(),
		RuntimeID:     "runtime-test-1",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
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
		EndpointURL:   relay.URL(),
		RuntimeID:     "runtime-test-2",
		AllowInsecure: true,
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

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-1",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("initial connect failed: %v", err)
	}
	initialAttach, err := relay.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("initial attach failed: %v", err)
	}
	if initialAttach.TerminalSessionID != "sess-grace-1" {
		t.Fatalf("unexpected session ID in attach: %s", initialAttach.TerminalSessionID)
	}

	// 1. 브라우저 탭 닫힘 / 일시적 네트워크 단절 시뮬레이션
	relay.DisconnectConnection()

	// 2. Connector 프로덕션 자동 재연결 오너에 의해 백그라운드에서 동일 세션으로 re-dial/attach 요청 도착 대기
	reconnectedAttach, err := relay.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("expected auto-reconnect attach from client, but got error: %v", err)
	}
	if reconnectedAttach.TerminalSessionID != "sess-grace-1" {
		t.Fatalf("expected same session ID %q on reconnect, got %q", "sess-grace-1", reconnectedAttach.TerminalSessionID)
	}

	// 3. 재연결 완료 후 세션 상태가 StatusActive 로 복원되었는지 확인
	var active bool
	for i := 0; i < 20; i++ {
		if session.Status == terminal.StatusActive {
			active = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !active {
		t.Fatalf("expected session status to resume to StatusActive, got %s", session.Status)
	}

	// 4. 재연결 후에도 동일 PTY 입출력이 유지되는지 확인
	testBytes := []byte("still alive after auto-reconnect\n")
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
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-1",
		AllowInsecure: true,
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
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-ended",
		AllowInsecure: true,
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
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{protocol.SubprotocolTerminalData},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read attach
		_, _, _ = conn.ReadMessage()

		f := false
		// Write malformed replyTo
		badResp := protocol.TerminalDataAttachedMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalDataAttached,
				MessageID:         "msg-resp",
				SentAt:            time.Now().UTC(),
				ReplyToMessageID:  "WRONG-REPLY-TO-ID", // mismatch
				TerminalSessionID: "sess-bad-attach",
				LabInstanceID:     "inst-1",
				Generation:        1,
			},
			Payload: protocol.TerminalDataAttachedPayload{
				Resumed:          &f,
				HistoryAvailable: &f,
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
		EndpointURL:   wsURL,
		DialTimeout:   1 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	attachErr := client.DialAndAttach(context.Background())
	if attachErr == nil {
		t.Fatal("expected DialAndAttach to fail on replyTo mismatch, got nil")
	}
}

func TestSession_SingleWriter_Concurrency(t *testing.T) {
	// 단일 WebSocket connection에 대해 여러 고루틴이 동시 쓰기(WriteMessage, WriteJSON)를 호출할 때
	// 데이터 레이스나 패닉 없이 정상적으로 직렬화되어 처리되는지 검증
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-concurrent-write", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-concurrency",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}
	_, _ = relay.WaitForAttach(2 * time.Second)

	var wg sync.WaitGroup
	const goroutines = 20
	const iterations = 50

	// 20개의 goroutine이 WriteMessage 호출
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				data := []byte(fmt.Sprintf("msg-%d-%d\n", id, j))
				_ = session.WriteMessage(websocket.BinaryMessage, data)
			}
		}(i)
	}

	// 20개의 goroutine이 WriteJSON 호출
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				jsonMsg := map[string]interface{}{
					"id":   id,
					"seq":  j,
					"time": time.Now().UnixNano(),
				}
				_ = session.WriteJSON(jsonMsg)
			}
		}(i)
	}

	wg.Wait()
}

func TestSession_Close_ConcurrentWrite_NoDeadlock(t *testing.T) {
	// Binary output, JSON error, ENDED, and Close concurrent execution stress test
	// Verifies no lock-order inversion deadlock between s.mu and s.writeMu
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-deadlock-test", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-deadlock",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}
	_, _ = relay.WaitForAttach(2 * time.Second)

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		const writers = 20
		const iterations = 100

		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					_ = session.WriteMessage(websocket.BinaryMessage, []byte(fmt.Sprintf("bin-%d-%d", id, j)))
				}
			}(i)
		}

		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					_ = session.WriteJSON(map[string]int{"writer": id, "iter": j})
				}
			}(i)
		}

		// Close concurrently during active writes
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(5 * time.Millisecond)
			session.Close("CONCURRENT_STRESS_CLOSE", nil, nil)
		}()

		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded with no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("DEADLOCK DETECTED: timeout waiting for concurrent writes and Close to complete")
	}

	if session.Status != terminal.StatusClosed {
		t.Fatalf("expected StatusClosed, got %s", session.Status)
	}
}

func TestDataWSSClient_SubprotocolMismatch_Fails(t *testing.T) {
	// 하위 프로토콜 협상이 안 되거나 다른 프로토콜로 회신하는 경우 DialAndAttach 실패 검증
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
		// 올바른 subprotocol 대신 다른 것을 반환
		Subprotocols: []string{"wrong.subprotocol.v1"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
	}))
	defer server.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-subproto-mismatch", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	wsURL := "ws" + server.URL[len("http"):]
	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   wsURL,
		DialTimeout:   1 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	err = client.DialAndAttach(context.Background())
	if err == nil {
		t.Fatal("expected DialAndAttach to fail on subprotocol mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "negotiated subprotocol") {
		t.Fatalf("expected error mentioning negotiated subprotocol, got: %v", err)
	}
}

func TestDataWSSClient_HistoryAvailableTrue_Fails(t *testing.T) {
	// Phase 1에서 historyAvailable: true 응답 수신 시 거부 검증
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{protocol.SubprotocolTerminalData},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var attachMsg protocol.TerminalDataAttachMessage
		_ = json.Unmarshal(data, &attachMsg)

		f := false
		tr := true
		resp := protocol.TerminalDataAttachedMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalDataAttached,
				MessageID:         "msg-resp",
				SentAt:            time.Now().UTC(),
				ReplyToMessageID:  attachMsg.MessageID,
				TerminalSessionID: attachMsg.TerminalSessionID,
				LabInstanceID:     attachMsg.LabInstanceID,
				Generation:        attachMsg.Generation,
			},
			Payload: protocol.TerminalDataAttachedPayload{
				Resumed:          &f,
				HistoryAvailable: &tr, // Phase 1 forbidden
			},
		}
		_ = conn.WriteJSON(resp)
	}))
	defer server.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-history-unsupported", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	wsURL := "ws" + server.URL[len("http"):]
	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   wsURL,
		DialTimeout:   1 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	err = client.DialAndAttach(context.Background())
	if err == nil {
		t.Fatal("expected DialAndAttach to fail on historyAvailable: true, got nil")
	}
	if !strings.Contains(err.Error(), "historyAvailable must be false") {
		t.Fatalf("expected error mentioning historyAvailable must be false, got: %v", err)
	}
}

func TestDataWSSClient_InsecureScheme_RejectedInProduction(t *testing.T) {
	// 프로덕션 모드 (AllowInsecure = false) 일 때 비-루프백 ws:// 연결 거부 검증
	mgr := terminal.NewSessionManager(5*time.Second, nil)
	session, _, _ := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-insecure-scheme", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
	)

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   "ws://relay.production.domain/connector/v1/terminal-data",
		DialTimeout:   1 * time.Second,
		AllowInsecure: false, // production
	}, session)
	defer client.Close()

	err := client.DialAndAttach(context.Background())
	if err == nil {
		t.Fatal("expected DialAndAttach to fail for ws:// in production, got nil")
	}
	if !strings.Contains(err.Error(), "prohibited in production") {
		t.Fatalf("expected error mentioning prohibited in production, got: %v", err)
	}
}

func TestDataWSSClient_OversizedFrame_Fails(t *testing.T) {
	// JSON Text 1 MiB 상한과 PTY Binary 상한(4 MiB)이 정상 분리 동작하는지 검증 (Reviewer 3번 지적 사항)
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-oversized", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-oversized",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}
	_, _ = relay.WaitForAttach(2 * time.Second)

	// 1. JSON Text 1 MiB 초과 전송 -> Bounded Read로 감지되어 1009(Message Too Big) Close 및 자동 재연결
	oversizedJSON := make([]byte, protocol.MaxJSONMessageSize+1024)
	_ = relay.SendText(oversizedJSON)

	// Close code 1009 수신 확인
	closeCode, err := relay.WaitForClose(2 * time.Second)
	if err != nil {
		t.Fatalf("expected 1009 close for oversized JSON text, got err: %v", err)
	}
	if closeCode != websocket.CloseMessageTooBig {
		t.Fatalf("expected close code %d (1009 Message Too Big), got %d", websocket.CloseMessageTooBig, closeCode)
	}

	// 1009 로 끊긴 후 백그라운드 재연결(re-attach) 확인
	_, err = relay.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("expected auto-reconnect after 1009 close, got: %v", err)
	}

	// 2. Binary 1.1 MiB 전송 (JSON 1 MiB 한도를 넘지만 Binary 4 MiB 한도 이내) -> 정상 수신 및 Echo 성공
	binaryOver1MB := make([]byte, protocol.MaxJSONMessageSize+64*1024)
	copy(binaryOver1MB, "large-binary-ok")
	if err := relay.SendBinary(binaryOver1MB); err != nil {
		t.Fatalf("SendBinary > 1MB failed: %v", err)
	}
	output, err := relay.ReadBinary(3 * time.Second)
	if err != nil {
		t.Fatalf("ReadBinary for binary frame > 1 MiB failed: %v", err)
	}
	if len(output) == 0 {
		t.Fatalf("expected non-empty binary echo, got %d", len(output))
	}
	if session.Status != terminal.StatusActive {
		t.Fatalf("session should remain StatusActive after binary frame > 1 MiB, got %s", session.Status)
	}

	// 3. Binary 4 MiB 초과 전송 (MaxBinaryMessageSize 초과) -> Connection ReadLimit 초과로 소켓 단절
	oversizedBinary := make([]byte, terminal.MaxBinaryMessageSize+1024)
	_ = relay.SendBinary(oversizedBinary)

	// ReadLimit 초과로 소켓이 끊기면, 클라이언트는 세션을 detach하고 백그라운드에서 자동 재연결(re-attach)을 수행함
	reconnectedAttach, err := relay.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("expected client connection to drop and auto-reconnect on >4 MiB frame, got error: %v", err)
	}
	if reconnectedAttach.TerminalSessionID != "sess-oversized" {
		t.Fatalf("expected same session ID on reconnect, got %s", reconnectedAttach.TerminalSessionID)
	}
}

func TestDataWSSClient_InvalidResizeDimensions_Rejected(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-invalid-resize", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-resize-bad",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}
	_, _ = relay.WaitForAttach(2 * time.Second)

	// 유효하지 않은 크기 전송 (cols: 0, rows: -5)
	_ = relay.SendResize(0, -5)

	time.Sleep(100 * time.Millisecond)

	// 원래 크기 유지 확인
	if session.Cols != 80 || session.Rows != 24 {
		t.Fatalf("session dimensions should remain 80x24, got %dx%d", session.Cols, session.Rows)
	}
}

func TestDataWSSClient_EmptyCloseReason_Rejected(t *testing.T) {
	// Empty close reason in TERMINAL_DATA_CLOSE should be rejected without closing the session
	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{protocol.SubprotocolTerminalData},
	}
	var serverConn *websocket.Conn
	var connMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connMu.Lock()
		serverConn = conn
		connMu.Unlock()
		defer conn.Close()

		_, data, _ := conn.ReadMessage()
		var attachMsg protocol.TerminalDataAttachMessage
		_ = json.Unmarshal(data, &attachMsg)

		f := false
		resp := protocol.TerminalDataAttachedMessage{
			BaseEnvelope: protocol.BaseEnvelope{
				Type:              protocol.MessageTypeTerminalDataAttached,
				MessageID:         "msg-resp",
				SentAt:            time.Now().UTC(),
				ReplyToMessageID:  attachMsg.MessageID,
				TerminalSessionID: attachMsg.TerminalSessionID,
				LabInstanceID:     attachMsg.LabInstanceID,
				Generation:        attachMsg.Generation,
			},
			Payload: protocol.TerminalDataAttachedPayload{
				Resumed:          &f,
				HistoryAvailable: &f,
			},
		}
		_ = conn.WriteJSON(resp)

		// Keep connection open
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	mgr := terminal.NewSessionManager(5*time.Second, nil)
	pty := terminal.NewMockEchoPTY(80, 24)

	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-empty-close", LabInstanceID: "inst-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	wsURL := "ws" + server.URL[len("http"):]
	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   wsURL,
		DialTimeout:   1 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Send TERMINAL_DATA_CLOSE with empty reason
	badCloseMsg := protocol.TerminalDataCloseMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:              protocol.MessageTypeTerminalDataClose,
			MessageID:         "msg-bad-close",
			SentAt:            time.Now().UTC(),
			TerminalSessionID: "sess-empty-close",
			LabInstanceID:     "inst-1",
			Generation:        1,
		},
		Payload: protocol.TerminalDataClosePayload{
			Reason: "", // invalid empty reason
		},
	}
	connMu.Lock()
	if serverConn != nil {
		_ = serverConn.WriteJSON(badCloseMsg)
	}
	connMu.Unlock()

	time.Sleep(100 * time.Millisecond)

	// Session should NOT be closed
	if session.Status == terminal.StatusClosed {
		t.Fatal("session should not be closed on invalid empty close reason")
	}
}

func TestTerminalData_MissingRequiredFields_Fails(t *testing.T) {
	// BaseEnvelope 필수 필드(messageId, sentAt, terminalSessionId, labInstanceId, generation) 및
	// ATTACHED payload(resumed, historyAvailable) 누락 시 검증 실패 확인 (Reviewer 5번 지적 사항)

	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{protocol.SubprotocolTerminalData},
	}

	testCases := []struct {
		name        string
		attachedMsg func(attachMsg protocol.TerminalDataAttachMessage) interface{}
		wantErr     string
	}{
		{
			name: "missing messageId in BaseEnvelope",
			attachedMsg: func(attachMsg protocol.TerminalDataAttachMessage) interface{} {
				f := false
				return map[string]interface{}{
					"type":              protocol.MessageTypeTerminalDataAttached,
					"sentAt":            time.Now().UTC().Format(time.RFC3339),
					"replyToMessageId":  attachMsg.MessageID,
					"terminalSessionId": attachMsg.TerminalSessionID,
					"labInstanceId":     attachMsg.LabInstanceID,
					"generation":        attachMsg.Generation,
					"payload": map[string]interface{}{
						"resumed":          f,
						"historyAvailable": f,
					},
				}
			},
			wantErr: "missing required field: messageId",
		},
		{
			name: "missing sentAt in BaseEnvelope",
			attachedMsg: func(attachMsg protocol.TerminalDataAttachMessage) interface{} {
				f := false
				return map[string]interface{}{
					"type":              protocol.MessageTypeTerminalDataAttached,
					"messageId":         "msg-1",
					"replyToMessageId":  attachMsg.MessageID,
					"terminalSessionId": attachMsg.TerminalSessionID,
					"labInstanceId":     attachMsg.LabInstanceID,
					"generation":        attachMsg.Generation,
					"payload": map[string]interface{}{
						"resumed":          f,
						"historyAvailable": f,
					},
				}
			},
			wantErr: "missing required field: sentAt",
		},
		{
			name: "missing resumed in AttachedPayload",
			attachedMsg: func(attachMsg protocol.TerminalDataAttachMessage) interface{} {
				f := false
				return map[string]interface{}{
					"type":              protocol.MessageTypeTerminalDataAttached,
					"messageId":         "msg-1",
					"sentAt":            time.Now().UTC().Format(time.RFC3339),
					"replyToMessageId":  attachMsg.MessageID,
					"terminalSessionId": attachMsg.TerminalSessionID,
					"labInstanceId":     attachMsg.LabInstanceID,
					"generation":        attachMsg.Generation,
					"payload": map[string]interface{}{
						"historyAvailable": f,
					},
				}
			},
			wantErr: "missing required field in attached payload: resumed",
		},
		{
			name: "missing historyAvailable in AttachedPayload",
			attachedMsg: func(attachMsg protocol.TerminalDataAttachMessage) interface{} {
				f := false
				return map[string]interface{}{
					"type":              protocol.MessageTypeTerminalDataAttached,
					"messageId":         "msg-1",
					"sentAt":            time.Now().UTC().Format(time.RFC3339),
					"replyToMessageId":  attachMsg.MessageID,
					"terminalSessionId": attachMsg.TerminalSessionID,
					"labInstanceId":     attachMsg.LabInstanceID,
					"generation":        attachMsg.Generation,
					"payload": map[string]interface{}{
						"resumed": f,
					},
				}
			},
			wantErr: "missing required field in attached payload: historyAvailable",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()

				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var attachMsg protocol.TerminalDataAttachMessage
				_ = json.Unmarshal(data, &attachMsg)

				resp := tc.attachedMsg(attachMsg)
				_ = conn.WriteJSON(resp)
			}))
			defer server.Close()

			mgr := terminal.NewSessionManager(5*time.Second, nil)
			session, _, err := mgr.GetOrCreateSession(
				protocol.TerminalOpenPayload{TargetVmKey: "vm-1", ProviderServerID: "srv-1", Cols: 80, Rows: 24},
				protocol.BaseEnvelope{TerminalSessionID: "sess-schema-test", LabInstanceID: "inst-1", Generation: 1},
				func() (terminal.PTYChannel, error) { return terminal.NewMockEchoPTY(80, 24), nil },
			)
			if err != nil {
				t.Fatalf("GetOrCreateSession failed: %v", err)
			}

			wsURL := "ws" + server.URL[len("http"):]
			client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
				EndpointURL:   wsURL,
				DialTimeout:   1 * time.Second,
				AllowInsecure: true,
			}, session)
			defer client.Close()

			err = client.DialAndAttach(context.Background())
			if err == nil {
				t.Fatalf("expected DialAndAttach to fail with error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// TestTerminal_CredentialRevoke_ReattachPreservesPTY 는 Data WSS 연결이 Credential revoke로 인해 4001로 종료되고,
// 재접속 시 이전 토큰이 401로 거부될 때, PTY와 세션이 종료되지 않고 Grace Period 안에서 유지되며,
// Credential 갱신 후 동일한 terminalSessionId로 reattach(resumed=true)하여 동일 PTY 세션이 유지되는지 검증합니다.
func TestTerminal_CredentialRevoke_ReattachPreservesPTY(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	var credMu sync.Mutex
	validToken := "token-initial"
	relay.SetAuthValidator(func(req *http.Request) int {
		auth := req.Header.Get("Authorization")
		credMu.Lock()
		cur := validToken
		credMu.Unlock()
		if auth == "Bearer "+cur {
			return http.StatusOK
		}
		return http.StatusUnauthorized
	})

	pty := terminal.NewMockEchoPTY(80, 24)
	mgr := terminal.NewSessionManager(10*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-revoked", ProviderServerID: "srv-revoked", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-revoked-test", LabInstanceID: "lab-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// 임시 Credential 파일 생성
	tmpCredFile := filepath.Join(t.TempDir(), "connector_cred.txt")
	if err := os.WriteFile(tmpCredFile, []byte("token-initial"), 0600); err != nil {
		t.Fatalf("failed to write tmp cred file: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:    relay.URL(),
		CredentialFile: tmpCredFile,
		RuntimeID:      "rt-revoke-test",
		DialTimeout:    2 * time.Second,
		AllowInsecure:  true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}

	// 1. 초기 Attach 성공 확인
	firstAttach, err := relay.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}
	if firstAttach.TerminalSessionID != "sess-revoked-test" {
		t.Fatalf("expected session ID sess-revoked-test, got %s", firstAttach.TerminalSessionID)
	}

	// PTY 입력 및 에코 검증
	if err := relay.SendBinary([]byte("echo-before-revoke")); err != nil {
		t.Fatalf("SendBinary failed: %v", err)
	}
	out, err := relay.ReadBinary(2 * time.Second)
	if err != nil || string(out) != "echo-before-revoke" {
		t.Fatalf("expected initial echo, got %s, err: %v", string(out), err)
	}

	// 2. Data connection 4001 종료 및 토큰 revoke (이전 토큰 무효화)
	credMu.Lock()
	validToken = "token-renewed-v2"
	credMu.Unlock()

	const closeCredentialRevoked = 4001
	relay.CloseWithCode(closeCredentialRevoked, "credential revoked")

	// 3. reconnect()가 이전 토큰(token-initial)으로 재접속을 시도하여 실제 401로 거절되는 시점을 deterministic하게 대기
	rejectedAuth, err := relay.WaitForAuthReject(3 * time.Second)
	if err != nil {
		t.Fatalf("expected reconnect attempt with old credential to be rejected with 401: %v", err)
	}
	if rejectedAuth != "Bearer token-initial" {
		t.Fatalf("expected 401 rejection for 'Bearer token-initial', got %q", rejectedAuth)
	}

	// 4. 검증: 실제 401을 수신한 후에도 세션이 StatusClosed가 아니라 StatusDetached로 유지되어야 함!
	status := session.GetStatus()
	if status != terminal.StatusDetached {
		t.Fatalf("session should NOT be closed on 401 auth failure, must remain StatusDetached, got %s", status)
	}

	// 5. Credential 파일 갱신 (새로운 유효 토큰 주입)
	if err := os.WriteFile(tmpCredFile, []byte("token-renewed-v2"), 0600); err != nil {
		t.Fatalf("failed to update cred file: %v", err)
	}

	// 6. 클라이언트가 파일 갱신을 감지하고 새 토큰으로 reattach 성공하는지 대기
	secondAttach, err := relay.WaitForAttach(4 * time.Second)
	if err != nil {
		t.Fatalf("expected successful reattach with renewed credential, got: %v", err)
	}
	if secondAttach.TerminalSessionID != "sess-revoked-test" {
		t.Fatalf("expected same session ID on reattach, got %s", secondAttach.TerminalSessionID)
	}

	// 재연결 완료 후 세션 상태가 StatusActive 로 복원되었는지 확인
	var active bool
	for i := 0; i < 20; i++ {
		if session.GetStatus() == terminal.StatusActive {
			active = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !active {
		t.Fatalf("expected session status to resume to StatusActive, got %s", session.GetStatus())
	}

	// 7. 동일 PTY 유지 검증: 재연결된 소켓으로 키 입력 전달 시 동일한 PTY가 계속 에코 회신
	if err := relay.SendBinary([]byte("echo-after-renew")); err != nil {
		t.Fatalf("SendBinary after reattach failed: %v", err)
	}
	outAfter, err := relay.ReadBinary(2 * time.Second)
	if err != nil || string(outAfter) != "echo-after-renew" {
		t.Fatalf("expected PTY to remain alive and echo, got %s, err: %v", string(outAfter), err)
	}
}

// TestTerminal_Reconnect_RaceWithSessionClose_NoOrphanConn 은 Reconnect 핸드셰이크 중
// Grace Timeout 또는 Session.Close()가 인터리빙되어 세션이 CLOSED 될 때,
// 새로 맺어진 커넥션이 바인딩되지 않고 즉시 회수(Close)되어 orphan WebSocket이 남지 않는지 검증합니다.
func TestTerminal_Reconnect_RaceWithSessionClose_NoOrphanConn(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	pty := terminal.NewMockEchoPTY(80, 24)
	mgr := terminal.NewSessionManager(10*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-race", ProviderServerID: "srv-race", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-race-test", LabInstanceID: "lab-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	// ATTACH 메시지 수신 직후 (클라이언트가 ATTACHED 응답을 받기 직전),
	// 다른 고루틴에서 session.Close()가 실행되는 경쟁 상황을 인터리빙
	relay.SetBeforeAttached(func(attachMsg protocol.TerminalDataAttachMessage) {
		session.Close("shutdown-race", nil, nil)
	})

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-race-test",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	// DialAndAttach 실행 -> ATTACHED 수신 후 AttachDataConn 호출 시 session이 이미 CLOSED 상태
	err = client.DialAndAttach(context.Background())
	if err == nil {
		t.Fatalf("expected DialAndAttach to fail when session was closed concurrently, got nil")
	}
	if !errors.Is(err, terminal.ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got: %v", err)
	}

	// 세션이 계속 CLOSED 상태인지 확인
	status := session.GetStatus()
	dataConn := session.GetDataConn()
	if status != terminal.StatusClosed {
		t.Fatalf("session status must remain StatusClosed, got %s", status)
	}
	if dataConn != nil {
		t.Fatalf("session.DataConn must be nil, but orphan conn found: %v", dataConn)
	}
}

// TestTerminal_JSONText_BoundedRead_RejectsOversized 는 1 MiB를 초과하는 JSON Text 프레임이
// 메모리에 전체 적재되지 않고 pre-decode bounded read 단계에서 1009로 차단되는지 검증합니다.
func TestTerminal_JSONText_BoundedRead_RejectsOversized(t *testing.T) {
	relay := mock.NewTerminalRelay()
	defer relay.Close()

	pty := terminal.NewMockEchoPTY(80, 24)
	mgr := terminal.NewSessionManager(10*time.Second, nil)
	session, _, err := mgr.GetOrCreateSession(
		protocol.TerminalOpenPayload{TargetVmKey: "vm-bounded", ProviderServerID: "srv-bounded", Cols: 80, Rows: 24},
		protocol.BaseEnvelope{TerminalSessionID: "sess-bounded-test", LabInstanceID: "lab-1", Generation: 1},
		func() (terminal.PTYChannel, error) { return pty, nil },
	)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := terminal.NewDataWSSClient(terminal.DataWSSClientConfig{
		EndpointURL:   relay.URL(),
		RuntimeID:     "rt-bounded-test",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	}, session)
	defer client.Close()

	if err := client.ConnectAndStream(); err != nil {
		t.Fatalf("ConnectAndStream failed: %v", err)
	}
	_, err = relay.WaitForAttach(2 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}

	// 1.5 MiB Text frame 전송 (1 MiB 상한 초과)
	oversizedPayload := make([]byte, 1500*1024)
	for i := range oversizedPayload {
		oversizedPayload[i] = 'A'
	}
	_ = relay.SendText(oversizedPayload)

	// Close 1009 수신 확인
	closeCode, err := relay.WaitForClose(3 * time.Second)
	if err != nil {
		t.Fatalf("expected close code 1009 from client bounded read, got error: %v", err)
	}
	if closeCode != websocket.CloseMessageTooBig {
		t.Fatalf("expected close code %d (1009 Message Too Big), got %d", websocket.CloseMessageTooBig, closeCode)
	}
}
