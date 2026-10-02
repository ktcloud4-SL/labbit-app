package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// DataWSSClientConfig 는 Preview Data WSS 클라이언트 설정입니다.
type DataWSSClientConfig struct {
	EndpointURL        string
	Credential         string
	CredentialFile     string                 // Bearer 토큰 파일 경로 (주입/갱신 동적 로딩)
	CredentialProvider func() (string, error) // 동적 토큰 제공자 (테스트/커스텀)
	RuntimeID          string
	DialTimeout        time.Duration
	AllowInsecure      bool // Test 전용: localhost 및 비보안 ws:// 연결 허용
}

// GetCredential 은 CredentialProvider, CredentialFile, 또는 Credential 순으로 최신 유효 Bearer 토큰을 가져옵니다.
func (cfg *DataWSSClientConfig) GetCredential() (string, error) {
	if cfg.CredentialProvider != nil {
		return cfg.CredentialProvider()
	}
	if cfg.CredentialFile != "" {
		data, err := os.ReadFile(cfg.CredentialFile)
		if err != nil {
			return "", fmt.Errorf("failed to read credential file %s: %w", cfg.CredentialFile, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if cfg.Credential != "" {
		return strings.TrimSpace(cfg.Credential), nil
	}
	return "", nil
}

// DataWSSClient 는 개별 프리뷰 세션에 대해 SaaS Preview Gateway와의 WebSocket 터널을 수립합니다.
type DataWSSClient struct {
	config DataWSSClientConfig
}

// NewDataWSSClient 는 설정을 기반으로 DataWSSClient 를 생성합니다.
func NewDataWSSClient(cfg DataWSSClientConfig) *DataWSSClient {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	return &DataWSSClient{
		config: cfg,
	}
}

var (
	ErrAuthenticationFailed = errors.New("preview data authentication failed")
	errJSONTooLarge         = errors.New("json text message exceeds 1 MiB limit")
)

// readBoundedMessage 는 WebSocket 메시지 1건을 읽고, JSON Text 메시지는 1 MiB 상한을 강제합니다.
func readBoundedMessage(ws *websocket.Conn, maxText int64) (kind int, data []byte, err error) {
	kind, r, err := ws.NextReader()
	if err != nil {
		return 0, nil, err
	}
	if kind != websocket.TextMessage {
		data, err = io.ReadAll(r)
		return kind, data, err
	}
	data, err = io.ReadAll(io.LimitReader(r, maxText+1))
	if err != nil {
		return kind, nil, err
	}
	if int64(len(data)) > maxText {
		return kind, nil, errJSONTooLarge
	}
	return kind, data, nil
}

// DialAndAttach 는 SaaS Preview Gateway로 아웃바운드 WSS 연결을 맺고,
// ATTACH 핸드셰이크를 완료한 후 세션에 WebSocket 연결을 바인딩합니다.
func (c *DataWSSClient) DialAndAttach(ctx context.Context, session *PreviewSession) error {
	if session == nil {
		return errors.New("preview session is nil")
	}
	if c.config.EndpointURL == "" {
		return errors.New("preview data wss endpoint URL is empty")
	}

	u, err := url.Parse(c.config.EndpointURL)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	host := u.Hostname()
	isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	switch u.Scheme {
	case "wss":
		// 보안 WebSocket 허용
	case "ws":
		if !c.config.AllowInsecure || !isLoopback {
			return fmt.Errorf("insecure scheme %q is prohibited in production: TLS (wss://) is required", u.Scheme)
		}
	default:
		return fmt.Errorf("unsupported URL scheme %q: only wss is allowed", u.Scheme)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout: c.config.DialTimeout,
		Subprotocols:     []string{protocol.SubprotocolPreviewData},
	}

	cred, err := c.config.GetCredential()
	if err != nil {
		return fmt.Errorf("failed to get credential: %w", err)
	}

	header := http.Header{}
	if cred != "" {
		header.Set("Authorization", "Bearer "+cred)
	}

	conn, resp, err := dialer.DialContext(ctx, c.config.EndpointURL, header)
	if err != nil {
		if resp != nil {
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return ErrAuthenticationFailed
			}
			return fmt.Errorf("dial failed with status %d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("dial failed: %w", err)
	}

	// 1. Subprotocol 검증
	negotiated := conn.Subprotocol()
	if negotiated != protocol.SubprotocolPreviewData {
		_ = conn.Close()
		return fmt.Errorf("unexpected subprotocol %q: expected %q", negotiated, protocol.SubprotocolPreviewData)
	}

	// 2. PREVIEW_DATA_ATTACH 전송
	attachMsg := protocol.PreviewDataAttachMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:             protocol.MessageTypePreviewDataAttach,
			MessageID:        uuid.NewString(),
			SentAt:           time.Now().UTC(),
			PreviewSessionID: session.SessionID,
			LabInstanceID:    session.LabInstanceID,
			Generation:       session.Generation,
		},
		Payload: protocol.PreviewDataAttachPayload{
			RuntimeID:        c.config.RuntimeID,
			TargetVmKey:      session.TargetVmKey,
			ProviderServerID: session.ProviderServerID,
			Port:             session.Port,
		},
	}

	attachBytes, err := json.Marshal(attachMsg)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to marshal attach message: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, attachBytes); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to send attach message: %w", err)
	}

	// 3. PREVIEW_DATA_ATTACHED 응답 대기 (1 MiB bounded)
	msgType, replyData, err := readBoundedMessage(conn, protocol.MaxJSONMessageSize)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to read attach response: %w", err)
	}

	if msgType != websocket.TextMessage {
		_ = conn.Close()
		return fmt.Errorf("expected text frame for attach response, got %d", msgType)
	}

	var replyEnv protocol.BaseEnvelope
	if err := json.Unmarshal(replyData, &replyEnv); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to unmarshal attach response envelope: %w", err)
	}

	if replyEnv.Type == protocol.MessageTypePreviewDataError {
		var errReply protocol.PreviewDataErrorMessage
		_ = json.Unmarshal(replyData, &errReply)
		_ = conn.Close()
		return fmt.Errorf("gateway returned error [%s]: %s", errReply.Payload.Code, errReply.Payload.Message)
	}

	if replyEnv.Type != protocol.MessageTypePreviewDataAttached {
		_ = conn.Close()
		return fmt.Errorf("unexpected message type %q: expected %q", replyEnv.Type, protocol.MessageTypePreviewDataAttached)
	}

	if replyEnv.PreviewSessionID != session.SessionID {
		_ = conn.Close()
		return fmt.Errorf("session ID mismatch: expected %q, got %q", session.SessionID, replyEnv.PreviewSessionID)
	}

	// 4. 세션에 활성 WebSocket 연결 바인딩
	if err := session.AttachDataConn(conn); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to attach connection to session: %w", err)
	}

	return nil
}
