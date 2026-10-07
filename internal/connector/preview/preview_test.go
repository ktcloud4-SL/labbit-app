package preview

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
		TargetPort:       port,
	}
	env := protocol.BaseEnvelope{
		MessageID:        "msg-open-001",
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

	if err := client.DialAndAttach(ctx, session, env.MessageID); err != nil {
		t.Fatalf("DialAndAttach failed: %v", err)
	}

	// Gateway 측 ATTACH 수신 확인 (LBT-101: replyToMessageId = openMessageId)
	attachMsg, err := gateway.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("WaitForAttach failed: %v", err)
	}
	if attachMsg.PreviewSessionID != sessID || attachMsg.Payload.TargetPort != port {
		t.Fatalf("unexpected attach msg: %+v", attachMsg)
	}
	if attachMsg.ReplyToMessageID != "msg-open-001" {
		t.Fatalf("expected replyToMessageId msg-open-001, got %q", attachMsg.ReplyToMessageID)
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
		TargetPort: port,
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
	if !errors.Is(err, ErrAuthenticationFailed) {
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
		TargetPort: freePort,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-refused",
	})
	if err == nil {
		t.Fatalf("expected error for closed port %d, got nil", freePort)
	}
	if !errors.Is(err, ErrAppNotRunning) {
		t.Fatalf("expected ErrAppNotRunning, got %v", err)
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
		TargetPort: port,
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

	// Gateway가 Close 프레임 전송
	if err := gateway.SendClose(websocket.CloseNormalClosure, protocol.PreviewReasonSessionExpired); err != nil {
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
		TargetPort: port,
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

	// Gateway 측에서 연결 종료 수신 확인
	if err := gateway.WaitForClose(3 * time.Second); err != nil {
		t.Fatalf("WaitForClose failed: %v", err)
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
		TargetPort: port,
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

func TestPreview_TextFrameRejected(t *testing.T) {
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
		TargetPort: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-text-reject",
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

	// LBT-101: ATTACH 완료 후 텍스트 프레임 전송 시 프로토콜 에러 (1008) 및 세션 종료
	if err := gateway.SendText([]byte("invalid-text-message")); err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	select {
	case reason := <-endedCh:
		if reason != protocol.PreviewErrProtocolError {
			t.Fatalf("expected ended reason %s, got %s", protocol.PreviewErrProtocolError, reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for protocol error handling")
	}

	if session.GetStatus() != StatusClosed {
		t.Fatalf("expected session status CLOSED, got %s", session.GetStatus())
	}
}

func TestPreview_AttachTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	gateway.SetSimulateAttachTimeout(true)

	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-timeout",
		LabInstanceID:    "inst-1",
		Generation:       1,
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer session.Close("cleanup")

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   2 * time.Second,
		AttachTimeout: 100 * time.Millisecond,
		AllowInsecure: true,
	})

	err = client.DialAndAttach(context.Background(), session)
	if err == nil {
		t.Fatalf("expected attach timeout error, got nil")
	}
	if !errors.Is(err, ErrAttachTimeout) {
		t.Fatalf("expected ErrAttachTimeout, got %v", err)
	}
}

func TestPreview_CorrelationMismatch(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()
	gateway.SetSimulateCorrelationMismatch(true)

	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-mismatch",
		LabInstanceID:    "inst-1",
		Generation:       1,
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer session.Close("cleanup")

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   2 * time.Second,
		AttachTimeout: 2 * time.Second,
		AllowInsecure: true,
	})

	err = client.DialAndAttach(context.Background(), session)
	if err == nil {
		t.Fatalf("expected correlation mismatch error, got nil")
	}
	if !errors.Is(err, ErrCorrelationMismatch) {
		t.Fatalf("expected ErrCorrelationMismatch, got %v", err)
	}
}

func TestPreview_SSHPort22Rejected(t *testing.T) {
	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	_, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: 22,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-ssh-blocked",
	})
	if err == nil {
		t.Fatalf("expected error for port 22, got nil")
	}
	if !errors.Is(err, ErrPortRejected) {
		t.Fatalf("expected ErrPortRejected, got %v", err)
	}

	// 잘못된 범위 포트 검증 (0, 70000)
	_, _, err = mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: 0,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-port-0",
	})
	if !errors.Is(err, ErrPortRejected) {
		t.Fatalf("expected ErrPortRejected for port 0, got %v", err)
	}

	_, _, err = mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: 70000,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-port-70000",
	})
	if !errors.Is(err, ErrPortRejected) {
		t.Fatalf("expected ErrPortRejected for port 70000, got %v", err)
	}
}

func TestPreview_DuplicateAttachRejected(t *testing.T) {
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

	session, _, err := mgr.GetOrCreateSession(context.Background(), protocol.PreviewOpenPayload{
		TargetPort: port,
	}, protocol.BaseEnvelope{
		PreviewSessionID: "sess-dup-attach",
	})
	if err != nil {
		t.Fatalf("GetOrCreateSession failed: %v", err)
	}
	defer session.Close("cleanup")

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "token",
		RuntimeID:     "runtime-01",
		DialTimeout:   2 * time.Second,
		AllowInsecure: true,
	})

	if err := client.DialAndAttach(context.Background(), session); err != nil {
		t.Fatalf("first DialAndAttach failed: %v", err)
	}

	// 두 번째 DialAndAttach 시도 -> ErrSessionAlreadyAttached 반환
	err = client.DialAndAttach(context.Background(), session)
	if err == nil {
		t.Fatalf("expected error for duplicate attach, got nil")
	}
	if !errors.Is(err, ErrSessionAlreadyAttached) {
		t.Fatalf("expected ErrSessionAlreadyAttached, got %v", err)
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
		TargetPort: port,
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

func TestPreview_SequentialTunnels_SameSession(t *testing.T) {
	// 1. VM 테스트 서버 시작 (HTTP echo)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte("ok:" + r.URL.Path))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url failed: %v", err)
	}
	var port int
	_, _ = fmt.Sscanf(u.Host, "127.0.0.1:%d", &port)
	if port == 0 {
		_, _ = fmt.Sscanf(u.Host, "[::1]:%d", &port)
	}

	gateway := mock.NewPreviewGateway()
	defer gateway.Close()

	forwarder := NewDirectTCPForwarder(nil)
	mgr := NewSessionManager(forwarder, nil)

	sessID := "preview-sess-seq"
	openPayload := protocol.PreviewOpenPayload{
		TargetVmKey:      "vm-web-1",
		ProviderServerID: "srv-01",
		TargetPort:       port,
	}

	client := NewDataWSSClient(DataWSSClientConfig{
		EndpointURL:   gateway.URL(),
		Credential:    "test-token",
		RuntimeID:     "runtime-01",
		DialTimeout:   5 * time.Second,
		AllowInsecure: true,
	})

	// 1. 첫 번째 터널 (openMessageID = msg-1)
	env1 := protocol.BaseEnvelope{
		MessageID:        "msg-1",
		PreviewSessionID: sessID,
		LabInstanceID:    "lab-1",
		Generation:       1,
	}
	s1, resumed1, err := mgr.GetOrCreateSession(context.Background(), openPayload, env1)
	if err != nil {
		t.Fatalf("first GetOrCreateSession failed: %v", err)
	}
	if resumed1 {
		t.Fatalf("expected resumed=false for first tunnel")
	}

	if err := client.DialAndAttach(context.Background(), s1, env1.MessageID); err != nil {
		t.Fatalf("first DialAndAttach failed: %v", err)
	}

	attach1, err := gateway.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("first WaitForAttach failed: %v", err)
	}
	if attach1.ReplyToMessageID != "msg-1" {
		t.Fatalf("expected replyToMessageId msg-1, got %s", attach1.ReplyToMessageID)
	}

	// 첫 번째 터널을 통해 HTTP 요청 전송
	if err := gateway.SendBinary([]byte("GET /req1 HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("first SendBinary failed: %v", err)
	}
	resp1, err := gateway.WaitForBinary(3 * time.Second)
	if err != nil {
		t.Fatalf("first WaitForBinary failed: %v", err)
	}
	if !strings.Contains(string(resp1), "ok:/req1") {
		t.Fatalf("unexpected first response: %s", string(resp1))
	}

	// 첫 터널 종료 후에도 logical PreviewSession 은 manager 에 유지되어야 함 (§7b)
	time.Sleep(50 * time.Millisecond)
	sess, exists := mgr.GetSession(sessID)
	if !exists {
		t.Fatalf("logical session must exist after tunnel closed")
	}
	if sess.IsDestroyed() {
		t.Fatalf("logical session must not be destroyed after tunnel closed")
	}

	// 2. 두 번째 순차 터널 (openMessageID = msg-2)
	env2 := protocol.BaseEnvelope{
		MessageID:        "msg-2",
		PreviewSessionID: sessID,
		LabInstanceID:    "lab-1",
		Generation:       1,
	}
	s2, resumed2, err := mgr.GetOrCreateSession(context.Background(), openPayload, env2)
	if err != nil {
		t.Fatalf("second GetOrCreateSession failed: %v", err)
	}
	if resumed2 {
		t.Fatalf("expected resumed=false for new openMessageId")
	}
	if s1 != s2 {
		t.Fatalf("expected same logical session instance")
	}

	if err := client.DialAndAttach(context.Background(), s2, env2.MessageID); err != nil {
		t.Fatalf("second DialAndAttach failed: %v", err)
	}

	attach2, err := gateway.WaitForAttach(3 * time.Second)
	if err != nil {
		t.Fatalf("second WaitForAttach failed: %v", err)
	}
	if attach2.ReplyToMessageID != "msg-2" {
		t.Fatalf("expected replyToMessageId msg-2, got %s", attach2.ReplyToMessageID)
	}

	// 3. 동일 openMessageId (msg-2) 재전송 시 멱등성 (resumed=true, 재다이얼 없음)
	s2Retry, resumedRetry, err := mgr.GetOrCreateSession(context.Background(), openPayload, env2)
	if err != nil {
		t.Fatalf("retry GetOrCreateSession failed: %v", err)
	}
	if !resumedRetry {
		t.Fatalf("expected resumed=true for duplicate openMessageId retry")
	}
	if s2Retry != s2 {
		t.Fatalf("expected same session instance on retry")
	}

	// 4. 명시적 CloseSession 으로 세션 종료
	if err := mgr.CloseSession(sessID, "explicit-close"); err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}
	if _, exists := mgr.GetSession(sessID); exists {
		t.Fatalf("session must be removed after CloseSession")
	}
}
