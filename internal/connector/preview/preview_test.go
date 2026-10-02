package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestPreview_AttachAndProxyHTTP(t *testing.T) {
	// 1. Mock Target HTTP Web Server (학생 VM 내 웹 애플리케이션 시뮬레이션)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local port: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Hello from VM preview web app!"))
		}),
	}
	go func() {
		_ = httpServer.Serve(listener)
	}()
	defer func() {
		_ = httpServer.Close()
	}()

	// 2. Mock SaaS Preview Gateway 시작
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	// 3. Forwarder 및 SessionManager 설정
	forwarder := NewDirectTCPForwarder(nil)
	endedCh := make(chan string, 1)
	mgr := NewSessionManager(forwarder, func(s *PreviewSession, reason string, err error) {
		endedCh <- reason
	})

	sessID := "preview-sess-001"
	openPayload := protocol.PreviewOpenPayload{
		TargetVmKey:      "vm-web-1",
		ProviderServerID: "srv-01",
		Port:             port,
	}
	env := protocol.BaseEnvelope{
		PreviewSessionID: sessID,
		LabInstanceID:    "lab-instance-1",
		Generation:       1,
	}

	session, resumed, err := mgr.GetOrCreateSession(context.Background(), openPayload, env)
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	if resumed {
		t.Fatalf("expected resumed=false for newly created session")
	}

	// 4. DataWSSClient 로 Gateway에 연결 및 바인딩
	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.DialAndAttach(ctx, session); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	// Gateway 측 ATTACH 수신 확인
	attachMsg, err := gateway.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}
	if attachMsg.PreviewSessionID != sessID || attachMsg.Payload.Port != port {
		t.Fatalf("unexpected attach msg: %+v", attachMsg)
	}

	if session.GetStatus() != StatusActive {
		t.Fatalf("expected session status ACTIVE, got %s", session.GetStatus())
	}

	// 5. 브라우저/Gateway에서 HTTP Request 전송 (WSS Binary Frame)
	httpRequest := "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: keep-alive\r\n\r\n"
	if err := gateway.SendBinary([]byte(httpRequest)); err != nil {
		t.Fatalf("SendBinary failed: %v", err)
	}

	// 6. VM 앱 응답 수신 대기 및 검증
	respBytes, err := gateway.WaitForBinary(3 * time.Second)
	if err != nil {
		t.Fatalf("WaitForBinary failed: %v", err)
	}

	respStr := string(respBytes)
	if !strings.Contains(respStr, "Hello from VM preview web app!") {
		t.Fatalf("unexpected response from VM: %s", respStr)
	}

	// 7. 세션 닫기
	if err := mgr.CloseSession(sessID, protocol.PreviewReasonSessionClosed); err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}

	select {
	case reason := <-endedCh:
		if reason != protocol.PreviewReasonSessionClosed {
			t.Fatalf("unexpected ended reason: %s", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for onEnded callback")
	}

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestPreview_UnauthorizedCredential(t *testing.T) {
	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	// 인증 실패 설정 (401 Unauthorized)
	gateway.SetAuthValidator(func(req *http.Request) int {
		if req.Header.Get("Authorization") != "Bearer valid-token" {
			return http.StatusUnauthorized
		}
		return http.StatusOK
	})

	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	// 가짜 TCP Listener 생성
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-unauth",
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer session.Close("test-cleanup")

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "wrong-token",
		RuntimeID:     "runtime-01",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	err = client.DialAndAttach(context.Background(), session)
	if err == nil {
		t.Fatalf("expected error for unauthorized token, got nil")
	}
	if err != ErrAuthenticationFailed {
		t.Fatalf("expected ErrAuthenticationFailed, got %v", err)
	}

	rej, err := gateway.WaitForAuthReject(2 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAuthReject failed: %v", err)
	}
	if rej != "Bearer wrong-token" {
		t.Fatalf("unexpected rejected token: %s", rej)
	}
}

func TestPreview_TargetConnectionRefused(t *testing.T) {
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	// 사용되지 않는 포트 (Connection Refused 기대)
	freePort := 59876

	_, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: freePort,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-refused",
	})
	if err == nil {
		t.Fatalf("expected error for closed port %d, got nil", freePort)
	}
	if !strings.Contains(err.Error(), "failed to dial target VM port") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPreview_GatewayCloseFrame(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	endedCh := make(chan string, 1)
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, func(s *PreviewSession, reason string, err error) {
		endedCh <- reason
	})

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-close-test",
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	if err := client.DialAndAttach(context.Background(), session); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	_, _ = gateway.WaitForAttach(3 * time.Second)

	// Gateway가 PREVIEW_DATA_CLOSE 송신
	if err := gateway.SendClose(protocol.PreviewReasonSessionExpired); err != nil {
		t.Fatalf("SendClose failed: %v", err)
	}

	select {
	case reason := <-endedCh:
		if reason != protocol.PreviewReasonSessionExpired {
			t.Fatalf("expected reason %s, got %s", protocol.PreviewReasonSessionExpired, reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for session close")
	}

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestPreview_GracefulShutdown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	sessID := "sess-restart"
	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: sessID,
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	if err := client.DialAndAttach(context.Background(), session); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	_, _ = gateway.WaitForAttach(3 * time.Second)

	// CloseAll 호출 (서비스 재시작)
	mgr.CloseAll(protocol.PreviewReasonServiceRestart)

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}

	// Gateway 측에서 PREVIEW_DATA_ENDED 수신 확인
	textData, err := gateway.WaitForText(3 * time.Second)
	if err != nil {
		t.Fatalf("WaitForText failed: %v", err)
	}

	var endedMsg protocol.PreviewDataEndedMessage
	if err := json.Unmarshal(textData, &endedMsg); err != nil {
		t.Fatalf("failed to unmarshal ended msg: %v", err)
	}

	if endedMsg.Payload.Reason != protocol.PreviewReasonServiceRestart {
		t.Fatalf("expected ended reason %s, got %s", protocol.PreviewReasonServiceRestart, endedMsg.Payload.Reason)
	}

	if mgr.Count() != 0 {
		t.Fatalf("expected 0 active sessions after CloseAll, got %d", mgr.Count())
	}
}

func TestPreview_IdempotentGetOrCreate(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	sessID := "sess-idempotent"
	openPayload := protocol.PreviewOpenPayload{
		Port: port,
	}
	env := protocol.BaseEnvelope{
		PreviewSessionID: sessID,
	}

	s1, resumed1, err := mgr.GetOrCreateSession(context.Background(), openPayload, env)
	if err != nil {
		t.Fatalf("first GetOrCreateSession failed: %v", err)
	}
	if resumed1 {
		t.Fatalf("expected resumed=false for first call")
	}

	s2, resumed2, err := mgr.GetOrCreateSession(context.Background(), openPayload, env)
	if err != nil {
		t.Fatalf("second GetOrCreateSession failed: %v", err)
	}
	if !resumed2 {
		t.Fatalf("expected resumed=true for second call")
	}
	if s1 != s2 {
		t.Fatalf("expected same session instance returned")
	}

	mgr.CloseAll("cleanup")
}

func TestPreview_BoundedJSONRead(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	endedCh := make(chan string, 1)
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, func(s *PreviewSession, reason string, err error) {
		endedCh <- reason
	})

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-oversized",
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	if err := client.DialAndAttach(context.Background(), session); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	_, _ = gateway.WaitForAttach(3 * time.Second)

	// 1.1 MiB 크기의 거대 JSON Text 메시지 생성 및 송신
	oversized := bytes.Repeat([]byte("x"), 1100000)
	if err := gateway.SendText(oversized); err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	select {
	case reason := <-endedCh:
		if reason != protocol.PreviewErrProtocolError {
			t.Fatalf("expected ended reason %s, got %s", protocol.PreviewErrProtocolError, reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for oversized message handling")
	}

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestPreview_TargetClosedMidStream(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	port := l.Addr().(*net.TCPAddr).Port

	var serverConn net.Conn
	var connMu sync.Mutex
	go func() {
		conn, err := l.Accept()
		if err == nil {
			connMu.Lock()
			serverConn = conn
			connMu.Unlock()
		}
	}()
	defer l.Close()

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	endedCh := make(chan string, 1)
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, func(s *PreviewSession, reason string, err error) {
		endedCh <- reason
	})

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		Port: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-midstream-close",
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	if err := client.DialAndAttach(context.Background(), session); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	_, _ = gateway.WaitForAttach(3 * time.Second)

	// 서버가 연결을 닫음 (VM 프로세스 종료 시뮬레이션)
	time.Sleep(50 * time.Millisecond)
	connMu.Lock()
	if serverConn != nil {
		_ = serverConn.Close()
	}
	connMu.Unlock()

	select {
	case reason := <-endedCh:
		if reason != protocol.PreviewReasonTargetClosed {
			t.Fatalf("expected ended reason %s, got %s", protocol.PreviewReasonTargetClosed, reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for target closed handling")
	}

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestPreview_DynamicCredentialFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "preview-token-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString("initial-token\n"); err != nil {
		t.Fatalf("failed to write initial token: %v", err)
	}
	tmpFile.Close()

	cfg := DataWSSClientConfig{
		CredentialFile: tmpFile.Name(),
	}

	token1, err := cfg.GetCredential()
	if err != nil {
		t.Fatalf("GetCredential failed: %v", err)
	}
	if token1 != "initial-token" {
		t.Fatalf("expected initial-token, got %s", token1)
	}

	// 파일 내용 동적 갱신
	if err := os.WriteFile(tmpFile.Name(), []byte("updated-token\n"), 0600); err != nil {
		t.Fatalf("failed to update token file: %v", err)
	}

	token2, err := cfg.GetCredential()
	if err != nil {
		t.Fatalf("GetCredential after update failed: %v", err)
	}
	if token2 != "updated-token" {
		t.Fatalf("expected updated-token, got %s", token2)
	}
}
