package wss

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Config 는 Connector WSS Client 연결 설정입니다.
type Config struct {
	BaseURL          string   // SaaS 기본 엔드포인트 URL (e.g. "https://saas.example.com")
	Credential       string   // Bearer 토큰 문자열
	CredentialFile   string   // Bearer 토큰 파일 경로 (*_FILE 보안 주입 패턴)
	ConnectorVersion string   // Connector 애플리케이션 버전 (기본값: "0.1.0")
	RuntimeID        string   // 프로세스 런타임 인스턴스 식별자
	Capabilities     []string // 선택 지원 capability 목록
	ReadLimit        int64    // WebSocket 메시지 최대 수신 크기 바이트 (기본값: 1 MiB)
	AllowInsecure    bool     // Test 전용: localhost 및 비보안 ws:// 연결 허용 플래그 (런타임에서는 항상 wss 강제)
}

// ResolveEndpoint 는 BaseURL을 WebSocket 제어 엔드포인트(wss://.../connector/v1/control)로 변환합니다.
// 런타임 보안 규칙:
// - 실제 런타임 환경에서는 평문 HTTP/WS를 전면 차단하고 HTTPS/WSS(TLS 1.2+)만 허용하여 Bearer Credential 유출을 방지합니다.
// - ws://localhost 및 ws://127.0.0.1 형태의 비보안 연결은 AllowInsecure=true 설정 시에만 Test-only 경계로 허용됩니다.
func (c *Config) ResolveEndpoint() (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("baseURL is required")
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid baseURL: %w", err)
	}

	host := u.Hostname()
	isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"

	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		if !c.AllowInsecure {
			return "", fmt.Errorf("insecure scheme %q is prohibited in runtime: TLS (https/wss) is required to protect Bearer credentials", u.Scheme)
		}
		if !isLoopback {
			return "", fmt.Errorf("insecure scheme %q is prohibited for non-localhost endpoints: only test loopback (localhost/127.0.0.1) is permitted", u.Scheme)
		}
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported URL scheme: %s", u.Scheme)
	}

	// 표준 Control 엔드포인트 path 확인 및 보정
	if u.Path == "" || u.Path == "/" {
		u.Path = "/connector/v1/control"
	} else if !strings.HasSuffix(u.Path, "/connector/v1/control") {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/connector/v1/control"
	}

	return u.String(), nil
}

// GetCredential 은 직접 입력된 토큰 또는 CredentialFile 경로에서 Bearer 토큰을 가져옵니다.
func (c *Config) GetCredential() (string, error) {
	if c.Credential != "" {
		return strings.TrimSpace(c.Credential), nil
	}
	if c.CredentialFile != "" {
		data, err := os.ReadFile(c.CredentialFile)
		if err != nil {
			return "", fmt.Errorf("failed to read credential file %s: %w", c.CredentialFile, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", fmt.Errorf("no credential provided (either Credential or CredentialFile required)")
}

// EnsureDefaults 는 누락된 기본 설정값을 보충합니다.
func (c *Config) EnsureDefaults() {
	if c.ConnectorVersion == "" {
		c.ConnectorVersion = "0.1.0"
	}
	if c.ReadLimit <= 0 {
		c.ReadLimit = protocol.MaxJSONMessageSize
	}
}
