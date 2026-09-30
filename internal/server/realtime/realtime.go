// Package realtime은 Browser Terminal WSS(contracts/realtime/)와 Connector Terminal Data WSS(contracts/connector/)를
// 중계하는 Terminal Relay의 transport와 ephemeral 상태다.
//
// 이 package는 PostgreSQL, repository, auth/class use case를 import하지 않는다(Runtime Contract의 realtime role은
// DB를 application dependency로 요구하지 않는다). Browser 인증, 현재 권한, TerminalSession lifecycle, Connector credential 인증처럼
// DB-backed authority는 이 package가 정의한 좁은 interface(Control, ConnectorAuthenticator)로만 받는다. v0.1에서는
// 같은 process의 api role이 그 구현을 제공한다(runtime/contract.yaml saas.realtime).
//
// Relay가 가진 것은 process 안의 ephemeral 상태다. TerminalSession별 Browser attachment 하나, Connector data channel 하나,
// 60초 grace timer, 그리고 attachment마다의 bounded 전송 queue다. queue는 전송 중인 byte를 잠시 담을 뿐 history가 아니며
// attachment가 끝나면 버린다. Terminal INPUT/OUTPUT, transcript, ANSI 화면은 저장·재생·log하지 않는다.
package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// 계약에서 정한 endpoint, subprotocol, cookie 이름이다.
const (
	// BrowserPath는 Browser Terminal WSS endpoint다(contracts/realtime/README.md §1).
	BrowserPath = "/realtime/v1/terminal"
	// DataPath는 Connector Terminal Data WSS endpoint다(contracts/connector/README.md §7).
	DataPath = "/connector/v1/terminal-data"

	BrowserSubprotocol = "labbit.terminal.v1"
	DataSubprotocol    = "labbit.connector-terminal.v1"

	// SessionCookieName은 Browser 로그인 세션 Cookie 이름이다(contracts/http/openapi.yaml sessionCookie).
	SessionCookieName = "__Host-labbit-session"
)

// DefaultGrace는 Browser 단절 후 같은 TerminalSession에 재접속할 수 있는 기본 시간이다(D-21).
const DefaultGrace = 60 * time.Second

// 계약이 정한 JSON Text application message 한도(contracts/connector/README.md §3)와 같은 값이다.
// Binary PTY stream에는 적용하지 않는다.
const maxJSONTextBytes = 1 << 20

const redacted = "[REDACTED]"

// SessionToken은 Browser가 Cookie로 제시한 로그인 Session token이다. log나 오류 문자열에 실수로 출력되지 않도록 값을 가린다.
type SessionToken string

func (SessionToken) String() string               { return redacted }
func (SessionToken) GoString() string             { return redacted }
func (SessionToken) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (SessionToken) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// AttachToken은 TerminalSession에 scope된 opaque attach token이다. URL, log, trace, DB 일반 column에 기록하지 않으므로
// 실수로 출력되지 않도록 값을 가린다. 원문이 필요한 경계(HTTP 응답 본문, 검증을 위한 digest 계산)에서만 string(token)으로 변환한다.
type AttachToken string

func (AttachToken) String() string               { return redacted }
func (AttachToken) GoString() string             { return redacted }
func (AttachToken) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (AttachToken) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// ConnectorCredential은 Connector가 Authorization: Bearer로 제시한 Credential 원문이다. 값을 가린다.
type ConnectorCredential string

func (ConnectorCredential) String() string               { return redacted }
func (ConnectorCredential) GoString() string             { return redacted }
func (ConnectorCredential) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (ConnectorCredential) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Control이 반환하는 오류다. Relay는 이 값으로 Browser에 보낼 ERROR code와 close code를 결정한다.
var (
	// ErrUnauthenticated는 Browser 로그인 세션이나 Connector credential이 유효하지 않음이다.
	ErrUnauthenticated = errors.New("realtime: 인증되지 않음")
	// ErrInvalidToken은 attach token이 이 TerminalSession의 것이 아니거나 형식이 올바르지 않음이다.
	ErrInvalidToken = errors.New("realtime: attach token이 유효하지 않음")
	// ErrTokenExpired는 attach token의 절대 만료 시간이 지남이다. ErrInvalidToken이기도 하다.
	ErrTokenExpired = fmt.Errorf("%w: 만료", ErrInvalidToken)
	// ErrForbidden은 현재 사용자가 이 TerminalSession을 사용할 권한이 없음이다.
	ErrForbidden = errors.New("realtime: 권한 없음")
	// ErrSessionNotFound는 TerminalSession이 없거나 attach할 수 있는 상태가 아님이다.
	ErrSessionNotFound = errors.New("realtime: TerminalSession을 찾을 수 없음")
	// ErrSessionEnded는 TerminalSession이 이미 종료되었음이다(grace 만료, 명시적 종료, PTY 종료 등).
	ErrSessionEnded = errors.New("realtime: TerminalSession이 종료됨")
	// ErrLabMutation은 Reset 등으로 LabInstance generation이 바뀌어 이 TerminalSession을 더 이상 사용할 수 없음이다.
	ErrLabMutation = errors.New("realtime: LabInstance가 바뀌어 TerminalSession을 사용할 수 없음")
	// ErrDependencyUnavailable은 저장소 같은 의존성 장애다. 권한 판정 결과가 아니다.
	ErrDependencyUnavailable = errors.New("realtime: 의존성을 사용할 수 없음")
)

// AttachRequest는 Browser의 TERMINAL_ATTACH에서 온 값이다. 모두 검증되지 않은 입력이다.
type AttachRequest struct {
	TerminalSessionID string
	Token             AttachToken
}

// AttachGrant는 Control이 현재 권한을 모두 확인한 뒤 허용한 attach다.
type AttachGrant struct {
	TerminalSessionID string
	LabInstanceID     string
	Generation        int64
}

// End는 TerminalSession 종료의 원인이다.
type End struct {
	// Reason은 PTY_EXITED, SESSION_CLOSED, SESSION_EXPIRED, LAB_RESET, LAB_CLEANUP, SERVICE_RESTARTING 같은 종료 원인이다.
	Reason string
	// ExitCode는 Connector가 알려 준 Shell/PTY 종료 code다. 알 수 없으면 nil이며 0으로 만들어 내지 않는다.
	ExitCode *int64
	// FromConnector는 Connector가 종료를 먼저 알렸음을 뜻한다. 그러면 Relay는 Connector에 TERMINAL_DATA_CLOSE를 다시 보내지 않는다.
	FromConnector bool
}

// 종료 원인 상수다. 계약이 예시로 든 값과 Labbit이 추가로 쓰는 값이다.
const (
	EndReasonSessionClosed     = "SESSION_CLOSED"
	EndReasonSessionExpired    = "SESSION_EXPIRED"
	EndReasonPTYExited         = "PTY_EXITED"
	EndReasonLabReset          = "LAB_RESET"
	EndReasonLabCleanup        = "LAB_CLEANUP"
	EndReasonServiceRestarting = "SERVICE_RESTARTING"
)

// Control은 Relay가 사용하는 DB-backed authority다. v0.1에서는 같은 process의 api role(terminal.Service)이 구현한다.
// Relay는 이 interface만 알고 저장소를 직접 조회하지 않는다.
//
// 구현은 Relay를 다시 호출하지 않는다. Relay가 session 전이를 직렬화하는 잠금을 잡은 채 호출하므로
// 구현이 Relay.Terminate 등을 호출하면 교착한다. 종료 후의 ephemeral 정리는 Relay가 스스로 한다.
type Control interface {
	// AuthenticateBrowser는 Upgrade 전에 Cookie의 로그인 Session이 지금 유효한지 확인한다. 유효하지 않으면 ErrUnauthenticated다.
	AuthenticateBrowser(ctx context.Context, session SessionToken) error
	// AuthorizeAttach는 모든 attach/re-attach에서 현재 로그인 세션, 사용자, Class 권한, LabInstance 소유, 현재 generation,
	// TerminalSession lifecycle, attach token digest를 다시 확인한다. 위의 Err* 중 하나 또는 ErrDependencyUnavailable을 반환한다.
	AuthorizeAttach(ctx context.Context, session SessionToken, req AttachRequest) (AttachGrant, error)
	// RecordAttached는 Browser가 attach했음을 기록한다(DETACHED/ACTIVE → ACTIVE). 이미 종료되었다면 ErrSessionEnded다.
	RecordAttached(ctx context.Context, terminalSessionID string, at time.Time) error
	// RecordDetached는 Browser attachment가 끝나 grace가 시작되었음을 기록한다(ACTIVE → DETACHED).
	RecordDetached(ctx context.Context, terminalSessionID string, at, graceExpiresAt time.Time) error
	// CloseSession은 Labbit이 TerminalSession을 종료한다. Connector에 TERMINAL_CLOSE를 요청하고 종료를 기록한다.
	// 이미 종료되었다면 아무것도 하지 않고 성공한다(멱등). grace 만료와 서비스 종료에 사용한다.
	CloseSession(ctx context.Context, terminalSessionID, reason string) error
	// SessionEnded는 Connector가 알려 준 종료(TERMINAL_DATA_ENDED)를 기록한다. Connector에 다시 CLOSE를 보내지 않는다. 멱등이다.
	SessionEnded(ctx context.Context, terminalSessionID string, end End) error
}

// ConnectorIdentity는 인증된 Connector다. 메시지가 주장한 값이 아니라 credential에서 결정한 값이다.
type ConnectorIdentity struct {
	// ConnectorID는 Labbit의 canonical Connector ID 문자열이다.
	ConnectorID string
}

// ConnectorAuthenticator는 Terminal Data WSS Upgrade의 Connector credential을 인증한다.
// 유효하지 않으면 ErrUnauthenticated, 저장소 장애는 ErrDependencyUnavailable이다.
type ConnectorAuthenticator interface {
	AuthenticateConnector(ctx context.Context, credential ConnectorCredential) (ConnectorIdentity, error)
}

// Expected는 Control이 TerminalSession 생성 중에 Relay에 미리 등록하는 correlation이다.
// Connector의 Terminal Data attach는 이 값과 모두 정확히 일치해야 bind된다.
type Expected struct {
	TerminalSessionID string
	// ConnectorID는 이 TerminalSession의 PTY를 소유하는 Connector다. 인증된 Connector identity와 같아야 한다.
	ConnectorID   string
	LabInstanceID string
	Generation    int64
}

// Clock은 grace timer와 시각을 주입하기 위한 경계다. 60초 grace를 test에서 실제로 기다리지 않는다.
type Clock interface {
	Now() time.Time
	// AfterFunc는 d 뒤에 f를 별도 goroutine에서 실행하는 Timer를 만든다.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer는 취소 가능한 지연 실행이다.
type Timer interface {
	// Stop은 아직 실행되지 않은 timer를 취소한다. 이미 실행되었거나 취소되었다면 false다.
	Stop() bool
}

// SystemClock은 실제 시간을 사용하는 Clock이다.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
