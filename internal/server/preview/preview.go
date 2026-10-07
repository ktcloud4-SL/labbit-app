// Package preview는 Preview Gateway다(LBT-101, runtime/contract.yaml `saas.preview`). 사용자 코드가 실행되는 별도 Preview Origin에서
// Browser 요청을 인증하고, PreviewSession별 Connector Preview Data tunnel(contracts/connector/README.md §7b) 위로 Workspace VM
// application에 HTTP byte stream을 전달한다.
//
//	Browser → Preview Origin(Gateway) → Preview Data WSS → Connector → Workspace VM SSH TCP forwarding → application port
//
// 이 package는 PostgreSQL을 application dependency로 요구하지 않는다(Runtime Contract의 preview role). 현재 User, LabInstance 소유,
// Class 권한, 대상 VM, 허용 port 같은 DB-backed authority는 PreviewSession use case(internal/server/previewsession)가 생성 시점에 판정해
// Expected로 등록하며, Connector Credential 인증은 이 package가 정의한 좁은 interface(ConnectorAuthenticator)로만 받는다.
// 경계 test(boundary_test.go)가 DB 관련 package를 import하지 않음을 전이 의존까지 검사한다.
//
// PreviewSession은 process 안의 ephemeral 상태다. session, bootstrap credential/Cookie의 digest, tunnel을 PostgreSQL에 저장하지 않으며
// process가 비정상 종료되면 복구하지 않는다. Preview HTTP 요청/응답 본문, 경로, query, Cookie, Authorization은 log, trace, metric에
// 남기지 않는다. 관측 metadata는 preview_session_id, request_id, lab_instance_id, connector_id, generation, 안전한 error_code, duration_ms뿐이다.
package preview

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// DataPath는 Connector Preview Data WSS endpoint다.
	DataPath = "/connector/v1/preview-data"

	// ReservedPrefix는 Gateway가 직접 처리하는 Preview Origin 경로 prefix다. 이 prefix의 요청은 Workspace application으로 전달하지 않는다.
	ReservedPrefix = "/__labbit"
	// BootstrapPath와 ExchangePath는 contracts/http/openapi.yaml의 Preview Origin 경로다.
	BootstrapPath = ReservedPrefix + "/bootstrap"
	ExchangePath  = ReservedPrefix + "/exchange"

	// 아래 시간과 크기는 구현의 보수적인 기본값이다. 계약 수치가 아니며 Options로 주입할 수 있다.
	defaultAttachTimeout   = 10 * time.Second
	defaultBootstrapTTL    = 2 * time.Minute
	defaultUpstreamTimeout = 30 * time.Second
	defaultMaxFrameBytes   = 1 << 20

	// writeTimeout은 Data WSS frame 하나를 쓰는 데 쓸 수 있는 시간이다.
	writeTimeout = 10 * time.Second
	// controlWriteTimeout은 close frame 같은 control frame을 쓰는 데 쓸 수 있는 시간이다.
	controlWriteTimeout = time.Second
	// authTimeout은 Connector Credential 인증 한 번에 쓸 수 있는 시간이다.
	authTimeout = 5 * time.Second
)

// PREVIEW_CLOSE의 종료 원인이자 PreviewSession의 종료 사유다(contracts/connector/preview-control.schema.json).
// 앞의 값들은 SaaS가 시작한 종료라 Connector에 PREVIEW_CLOSE를 보낸다. 뒤의 값들은 Connector가 이미 알고 있거나 Control이 끊겨 보낼 수 없는 종료다.
const (
	EndSessionClosed     = "SESSION_CLOSED"
	EndSessionExpired    = "SESSION_EXPIRED"
	EndLabReset          = "LAB_RESET"
	EndLabCleanup        = "LAB_CLEANUP"
	EndServiceRestarting = "SERVICE_RESTARTING"
	EndProtocolViolation = "PROTOCOL_VIOLATION"

	EndTunnelClosed      = "TUNNEL_CLOSED"
	EndCredentialRevoked = "CREDENTIAL_REVOKED"
	EndControlReplaced   = "CONTROL_REPLACED"
)

var (
	// ErrClosed는 Gateway가 종료 중이라 새 PreviewSession과 요청을 받지 않음이다.
	ErrClosed = errors.New("preview: Gateway가 종료 중")
	// ErrInvalidSession은 Expected의 값이 올바르지 않음이다.
	ErrInvalidSession = errors.New("preview: PreviewSession 값이 올바르지 않음")
	// ErrDuplicateSession은 같은 ID의 PreviewSession이 이미 있음이다.
	ErrDuplicateSession = errors.New("preview: 같은 ID의 PreviewSession이 이미 있음")
	// ErrUnknownSession은 그 ID의 PreviewSession이 없음이다(정리되었거나 만든 적 없음).
	ErrUnknownSession = errors.New("preview: PreviewSession을 찾을 수 없음")
	// ErrSessionEnded는 PreviewSession이 이미 끝났음이다.
	ErrSessionEnded = errors.New("preview: PreviewSession이 이미 끝남")
	// ErrNotAttached는 Connector의 Preview Data WSS가 아직 attach되지 않았음이다.
	ErrNotAttached = errors.New("preview: Preview Data WSS가 attach되지 않음")
	// ErrAlreadyActive는 PreviewSession이 이미 활성화되었음이다.
	ErrAlreadyActive = errors.New("preview: PreviewSession이 이미 활성화됨")
	// ErrControlMismatch는 PREVIEW_OPEN을 전달한 Control Session의 Connector가 기대한 Connector와 다름이다.
	ErrControlMismatch = errors.New("preview: Control Session의 Connector가 PreviewSession의 Connector와 다름")

	// ErrUnauthenticated는 Connector Credential이 유효하지 않음이다(revoke된 Credential/Connector 포함).
	ErrUnauthenticated = errors.New("preview: Connector 인증 실패")
	// ErrDependencyUnavailable은 인증 의존성(저장소 등)을 사용할 수 없음이다. 인증 실패가 아니다.
	ErrDependencyUnavailable = errors.New("preview: 인증 의존성을 사용할 수 없음")
)

// ConnectorIdentity는 Preview Data WSS Upgrade의 Connector Credential 인증 결과다. message가 주장한 값이 아니다.
type ConnectorIdentity struct {
	ConnectorID uuid.UUID
	// CredentialID는 Credential 원문이 아니라 식별자다. PREVIEW_OPEN을 전달한 Control Session을 인증한 Credential과 같아야 attach된다.
	CredentialID uuid.UUID
}

// ConnectorAuthenticator는 Preview Data WSS Upgrade의 Connector Credential을 인증한다. 유효하지 않으면 ErrUnauthenticated,
// 저장소 장애는 ErrDependencyUnavailable이다. Control WSS와 같은 인증 의미(revoke된 Credential/Connector 거절)를 써야 한다.
type ConnectorAuthenticator interface {
	AuthenticateConnector(ctx context.Context, credential string) (ConnectorIdentity, error)
}

// Ended는 활성화된 PreviewSession이 끝났음을 Lifecycle에 알리는 값이다.
type Ended struct {
	SessionID        string
	ConnectorID      uuid.UUID
	LabInstanceID    string
	Generation       int64
	RequestID        string
	Reason           string
	NotifyConnector  bool
	ControlSessionID uuid.UUID
}

// Lifecycle은 Gateway가 PreviewSession의 종료를 알리는 경계다. PreviewSession use case가 구현해 Connector에 PREVIEW_CLOSE를 보낸다.
// 활성화되지 않고 끝난 PreviewSession(생성 중 실패)은 알리지 않는다. 생성을 기다리던 호출자가 직접 정리한다.
//
// SessionEnded는 종료를 일으킨 goroutine에서 호출되며(timer, tunnel, Registry observer, 호출자) Gateway의 lock 밖이다. 짧게 반환해야 한다.
type Lifecycle interface {
	SessionEnded(Ended)
}

// TunnelOpener는 활성화된 PreviewSession에 새 Workspace TCP tunnel을 확보하는 경계다. PreviewSession use case가 구현한다.
type TunnelOpener interface {
	OpenTunnel(ctx context.Context, sessionID string) error
}

// End는 Terminate의 입력이다.
type End struct {
	// Reason은 End* 상수 중 하나다.
	Reason string
	// NotifyConnector이면 Lifecycle이 Connector에 PREVIEW_CLOSE를 보내야 한다.
	NotifyConnector bool
}

// Clock은 시간과 timer의 원본이다. test에서 가짜 시간을 주입한다.
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

// Options는 Gateway 구성이다.
type Options struct {
	// Origin은 Preview Origin template이다(ParseOriginTemplate). 필수다.
	Origin OriginTemplate
	// Connectors는 Preview Data WSS의 Connector Credential 인증이다. 필수다.
	Connectors ConnectorAuthenticator
	// Clock이 nil이면 SystemClock이다.
	Clock Clock
	// Logger가 nil이면 로그를 남기지 않는다. 경로, query, Cookie, Authorization, 본문, credential은 어떤 경우에도 기록하지 않는다.
	Logger *slog.Logger
	// AttachTimeout은 Data WSS Upgrade 뒤 PREVIEW_ATTACH를 기다리는 시간이다. 0이면 10초다.
	AttachTimeout time.Duration
	// BootstrapTTL은 일회용 bootstrap credential을 교환할 수 있는 시간이다(PreviewSession 만료보다 길지 않다). 0이면 2분이다.
	BootstrapTTL time.Duration
	// UpstreamTimeout은 Workspace application이 응답 header를 보내기까지 기다리는 시간이다. 0이면 30초다.
	UpstreamTimeout time.Duration
	// MaxFrameBytes는 Data WSS Binary frame 하나의 최대 크기다. 0이면 1 MiB다. 구현 기본값이며 계약 수치가 아니다.
	MaxFrameBytes int64
}

// Gateway는 Preview Gateway의 ephemeral 상태(PreviewSession)와 Connector Preview Data tunnel, Preview Origin HTTP handler를 가진다.
type Gateway struct {
	origin     OriginTemplate
	connectors ConnectorAuthenticator
	clock      Clock
	logger     *slog.Logger

	attachTimeout   time.Duration
	bootstrapTTL    time.Duration
	upstreamTimeout time.Duration
	maxFrame        int64

	mu        sync.Mutex
	lifecycle Lifecycle
	opener    TunnelOpener
	closed    bool
	sessions  map[string]*session
	done      chan struct{}
	// wg는 처리 중인 Data WSS와 Preview HTTP 요청을 센다.
	wg sync.WaitGroup
}

// New는 Gateway를 만든다. PreviewSession use case와 Gateway는 서로를 필요로 하므로 조립하는 쪽이 SetLifecycle로 순환을 끊는다.
func New(opts Options) (*Gateway, error) {
	if opts.Origin.IsZero() || opts.Connectors == nil {
		return nil, errors.New("preview: Origin과 Connectors가 필요합니다")
	}
	g := &Gateway{
		origin:          opts.Origin,
		connectors:      opts.Connectors,
		clock:           opts.Clock,
		logger:          opts.Logger,
		attachTimeout:   opts.AttachTimeout,
		bootstrapTTL:    opts.BootstrapTTL,
		upstreamTimeout: opts.UpstreamTimeout,
		maxFrame:        opts.MaxFrameBytes,
		sessions:        make(map[string]*session),
		done:            make(chan struct{}),
	}
	if g.clock == nil {
		g.clock = SystemClock{}
	}
	if g.logger == nil {
		g.logger = slog.New(slog.DiscardHandler)
	}
	if g.attachTimeout == 0 {
		g.attachTimeout = defaultAttachTimeout
	}
	if g.bootstrapTTL == 0 {
		g.bootstrapTTL = defaultBootstrapTTL
	}
	if g.upstreamTimeout == 0 {
		g.upstreamTimeout = defaultUpstreamTimeout
	}
	if g.maxFrame == 0 {
		g.maxFrame = defaultMaxFrameBytes
	}
	if g.attachTimeout < 0 || g.bootstrapTTL < 0 || g.upstreamTimeout < 0 || g.maxFrame < 0 {
		return nil, errors.New("preview: 시간과 크기 설정은 0보다 커야 합니다")
	}
	return g, nil
}

// SetLifecycle은 PreviewSession 종료를 받을 Lifecycle을 정한다. 조립 시점에 서비스를 시작하기 전에 한 번 호출한다.
func (g *Gateway) SetLifecycle(l Lifecycle) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lifecycle = l
}

// SetTunnelOpener는 PreviewSession의 새 tunnel을 열 TunnelOpener를 정한다. 조립 시점에 서비스를 시작하기 전에 한 번 호출한다.
func (g *Gateway) SetTunnelOpener(o TunnelOpener) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opener = o
}

func (g *Gateway) openTunnel(ctx context.Context, s *session) error {
	g.mu.Lock()
	opener := g.opener
	closed := g.closed
	g.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if opener == nil {
		return errors.New("preview: tunnel opener가 설정되지 않음")
	}
	return opener.OpenTunnel(ctx, s.id)
}

// Origin은 이 Gateway의 Preview Origin template이다.
func (g *Gateway) Origin() OriginTemplate { return g.origin }

func (g *Gateway) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

func (g *Gateway) lookup(id string) *session {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sessions[id]
}

// enter는 처리를 시작한다. Close 이후에는 false다. true이면 호출자가 g.wg.Done을 호출해야 한다.
func (g *Gateway) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

// Expected는 PreviewSession use case가 권한 검증과 port 승인을 마친 뒤 Gateway에 미리 등록하는 correlation이다.
// Connector의 Preview Data attach는 이 값과 모두 정확히 일치해야 bind된다.
type Expected struct {
	// SessionID는 PreviewSession ID다. Preview Origin host의 첫 label이므로 DNS label이어야 한다(소문자 영숫자와 '-', 63자 이하).
	SessionID string
	// OwnerID와 OrganizationID는 PreviewSession을 만든 사용자와 그 Organization의 opaque 식별자다. 종료 권한 판정에 쓴다.
	OwnerID        string
	OrganizationID string
	// ConnectorID는 이 PreviewSession의 TCP forwarding을 소유하는 Connector다. 인증된 Connector identity와 같아야 한다.
	ConnectorID      uuid.UUID
	LabInstanceID    string
	Generation       int64
	TargetVMKey      string
	ProviderServerID string
	// TargetPort는 SaaS가 승인한 정확한 Workspace application port다. attach의 targetPort와 같아야 한다.
	TargetPort int
	// TTL은 활성화된 시점부터의 PreviewSession 절대 수명이다. 0보다 커야 한다.
	TTL time.Duration
	// RequestID는 원본 HTTP request의 correlation이다(선택).
	RequestID string
	// OpenMessageID는 이 PreviewSession의 첫 tunnel open attempt(PREVIEW_OPEN)의 correlation이다.
	OpenMessageID string
}

func (e Expected) valid() bool {
	return validLabel(e.SessionID) && e.OwnerID != "" && e.OrganizationID != "" && e.ConnectorID != uuid.Nil &&
		e.LabInstanceID != "" && e.Generation >= 1 && e.TargetVMKey != "" && e.ProviderServerID != "" &&
		e.TargetPort >= 1 && e.TargetPort <= 65535 && e.TTL > 0
}

// Expect는 Connector의 Data attach보다 먼저 호출해 PreviewSession을 등록한다. 등록된 PreviewSession은 pending이며 Bind와 Connector의
// attach, Activate를 거쳐 활성화된다. 어떤 실패에서도 호출자가 Forget이나 Terminate로 정리해야 한다.
func (g *Gateway) Expect(e Expected) error {
	if !e.valid() {
		return ErrInvalidSession
	}
	s := newSession(e, g, g.logger)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return ErrClosed
	}
	if _, exists := g.sessions[e.SessionID]; exists {
		return ErrDuplicateSession
	}
	g.sessions[e.SessionID] = s
	return nil
}

// Binding은 PREVIEW_OPEN을 전달한 Control Session이다. Connector가 message를 받기 전에 기록해야 한다(connector.PreviewOpen.OnRoute).
type Binding struct {
	ConnectorID      uuid.UUID
	ControlSessionID uuid.UUID
	CredentialID     uuid.UUID
}

// Bind는 pending PreviewSession을 PREVIEW_OPEN을 전달한 Control Session에 묶는다. 이후 attach는 그 Control Session을 인증한 Credential로
// 인증된 Data WSS여야 하며, 그 Control Session이 교체·revoke되면 PreviewSession은 끝난다.
func (g *Gateway) Bind(id string, b Binding) error {
	s := g.lookup(id)
	if s == nil {
		return ErrUnknownSession
	}
	return s.bind(b)
}

// Pending은 생성 중인 PreviewSession의 상태 channel이다. attached는 Data WSS가 attach되고 tunnel이 준비되면, ended는 PreviewSession이
// 끝나면 닫힌다. 없으면 ok가 false다.
func (g *Gateway) Pending(id string) (attached, ended <-chan struct{}, ok bool) {
	s := g.lookup(id)
	if s == nil {
		return nil, nil, false
	}
	return s.attached, s.ended, true
}

// PrepareTunnel은 sessionID에 새로운 tunnel open attempt(openMessageID)를 등록한다.
func (g *Gateway) PrepareTunnel(id string, openMessageID string) (<-chan struct{}, error) {
	s := g.lookup(id)
	if s == nil {
		return nil, ErrUnknownSession
	}
	return s.prepareTunnel(openMessageID)
}

// CancelTunnel은 실패하거나 취소된 tunnel open attempt를 정리한다.
func (g *Gateway) CancelTunnel(id string, openMessageID string) {
	s := g.lookup(id)
	if s == nil {
		return
	}
	s.cancelTunnel(openMessageID)
}

// Activation은 PreviewSession을 활성화한 결과다.
type Activation struct {
	// URL은 Preview Origin의 bootstrap 경로이며 fragment에 일회용 bootstrap credential이 있다. 이 반환에서만 원문으로 나간다.
	URL       string
	ExpiresAt time.Time
}

// Activate는 attach된 PreviewSession을 활성화한다. 그 시점부터 TTL이 흐르고 Preview Origin이 이 PreviewSession의 요청을 받는다.
// 일회용 bootstrap credential을 만들어 URL의 fragment로 돌려준다. Gateway에는 digest만 남는다.
func (g *Gateway) Activate(id string) (Activation, error) {
	s := g.lookup(id)
	if s == nil {
		return Activation{}, ErrUnknownSession
	}
	credential, expiresAt, err := s.activate(g)
	if err != nil {
		return Activation{}, err
	}
	s.log.Info("PreviewSession 활성화")
	return Activation{URL: g.origin.Origin(id) + BootstrapPath + "#" + credential, ExpiresAt: expiresAt}, nil
}

// Info는 PreviewSession의 식별 정보다. credential이나 tunnel 상태는 담지 않는다.
type Info struct {
	SessionID        string
	OwnerID          string
	OrganizationID   string
	ConnectorID      uuid.UUID
	LabInstanceID    string
	Generation       int64
	TargetVMKey      string
	ProviderServerID string
	TargetPort       int
	Ended            bool
	EndReason        string
	ExpiresAt        time.Time
	ControlSessionID uuid.UUID
}

// Info는 id의 PreviewSession을 반환한다. 끝난 PreviewSession도 보존 기간 동안은 반환한다(Ended가 true). 없으면 false다.
func (g *Gateway) Info(id string) (Info, bool) {
	s := g.lookup(id)
	if s == nil {
		return Info{}, false
	}
	return s.info(), true
}

// SessionsForLab은 labInstanceID의 끝나지 않은 PreviewSession ID들이다. Reset/Cleanup이 종료할 대상을 찾는 데 쓴다.
func (g *Gateway) SessionsForLab(labInstanceID string) []string {
	g.mu.Lock()
	all := make([]*session, 0, len(g.sessions))
	for _, s := range g.sessions {
		all = append(all, s)
	}
	g.mu.Unlock()

	var ids []string
	for _, s := range all {
		if info := s.info(); info.LabInstanceID == labInstanceID && !info.Ended {
			ids = append(ids, info.SessionID)
		}
	}
	return ids
}
