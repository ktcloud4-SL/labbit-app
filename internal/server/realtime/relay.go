package realtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/observability"
)

// Relay가 Control과 주고받는 I/O 하나의 상한이다. 저장소 호출이 이 시간을 넘으면 실패로 처리한다.
const controlTimeout = 10 * time.Second

// 기본 설정이다. 정확한 byte/message 한도는 부하 테스트로 조정할 운영 값이며 wire 계약의 숫자가 아니다.
const (
	defaultAttachTimeout    = 10 * time.Second
	defaultWriteTimeout     = 10 * time.Second
	defaultCloseGrace       = 2 * time.Second
	defaultQueueBytes       = 1 << 20
	defaultQueueMessages    = 256
	defaultMaxBinaryMessage = 1 << 20
)

// contracts/realtime/README.md §9의 close code와 표준 code다.
const (
	closeNormal         = websocket.CloseNormalClosure
	closePolicy         = websocket.ClosePolicyViolation
	closeTooBig         = websocket.CloseMessageTooBig
	closeInternal       = websocket.CloseInternalServerErr
	closeServiceRestart = websocket.CloseServiceRestart
	closeAuth           = 4001
	closeForbidden      = 4002
	closeNotFound       = 4003
	closeReplaced       = 4004
	closeSlowConsumer   = 4005
	closeLifecycle      = 4006

	// closeCredentialRevoked는 Connector Terminal Data WSS가 인증에 쓴 Credential(또는 Connector)이 revoke되어 더 이상 신뢰할 수 없을 때다.
	// contracts/connector/README.md §15의 4001(Connector Credential revoke)과 같은 의미다. TerminalSession의 종료가 아니라
	// 이 transport의 trust 상실이다.
	closeCredentialRevoked = 4001
)

var (
	// ErrRelayClosed는 Relay가 종료 중이라 새 TerminalSession을 받지 않음이다.
	ErrRelayClosed = errors.New("realtime: Relay가 종료 중")
	// ErrDuplicateSession은 같은 TerminalSession이 이미 등록되어 있음이다.
	ErrDuplicateSession = errors.New("realtime: 이미 등록된 TerminalSession")
	// ErrInvalidExpected는 Expected의 필수 field가 비어 있음이다.
	ErrInvalidExpected = errors.New("realtime: Expected가 올바르지 않음")
	// ErrDataNotBound는 Connector Terminal Data WSS가 아직 bind되지 않았음이다.
	ErrDataNotBound = errors.New("realtime: Terminal Data WSS가 bind되지 않음")

	// errDataRevoked는 bind하려던 Data WSS의 Credential/Connector trust를 잃었음이다.
	errDataRevoked = errors.New("realtime: Terminal Data WSS의 trust를 잃음")
)

// Options는 Relay 구성이다.
type Options struct {
	// Control은 DB-backed authority다. 필수다.
	Control Control
	// Connectors는 Terminal Data WSS의 Connector credential을 인증한다. 필수다.
	Connectors ConnectorAuthenticator
	// AllowOrigin은 Browser WSS Upgrade의 Origin header 값 하나가 trusted origin과 정확히 일치하는지 판정한다. 필수다.
	// Origin이 없거나 둘 이상이면 호출하지 않고 거절한다.
	AllowOrigin func(origin string) bool

	// Clock이 nil이면 SystemClock이다.
	Clock Clock
	// Logger가 nil이면 로그를 남기지 않는다. Terminal 본문과 token은 어떤 경우에도 기록하지 않는다.
	Logger  *slog.Logger
	Metrics *observability.RealtimeMetrics

	// Grace는 Browser 단절(또는 첫 attach 전) 뒤 같은 TerminalSession에 attach할 수 있는 시간이다. 0이면 DefaultGrace다.
	Grace time.Duration
	// AttachTimeout은 Upgrade 후 첫 application message(attach)를 기다리는 시간이다. 0이면 10초다.
	AttachTimeout time.Duration
	// WriteTimeout은 WebSocket write 하나의 시간 상한이다. 0이면 10초다.
	WriteTimeout time.Duration
	// CloseGrace는 close frame을 보낸 뒤 상대의 응답을 기다리는 시간이다. 0이면 2초다.
	CloseGrace time.Duration

	// BrowserQueueBytes/BrowserQueueMessages는 Browser attachment마다의 전송 queue 한도다. 넘으면 그 attachment만 종료한다.
	// DataQueueBytes/DataQueueMessages는 Connector data channel의 INPUT/control queue 한도다. 0이면 각각 1 MiB와 256개다.
	BrowserQueueBytes    int
	BrowserQueueMessages int
	DataQueueBytes       int
	DataQueueMessages    int
	// MaxBinaryMessage는 Binary PTY message 하나의 읽기 상한이다. 0이면 1 MiB다. JSON Text 한도(1 MiB)와 무관하다.
	MaxBinaryMessage int64
}

// Relay는 TerminalSession별 Browser attachment와 Connector data channel을 이어 주는 ephemeral hub다.
type Relay struct {
	control     Control
	connectors  ConnectorAuthenticator
	allowOrigin func(string) bool
	clock       Clock
	logger      *slog.Logger
	metrics     *observability.RealtimeMetrics

	grace         time.Duration
	attachTimeout time.Duration
	writeTimeout  time.Duration
	closeGrace    time.Duration

	browserQueueBytes, browserQueueMessages int
	dataQueueBytes, dataQueueMessages       int
	readLimit                               int64

	browserUpgrader websocket.Upgrader
	dataUpgrader    websocket.Upgrader

	// trust는 열린 Terminal Data WSS를 인증한 Connector trust별로 추적한다. revoke 통지가 오면 해당 connection만 종료한다.
	trust *dataTrust

	// mu는 sessions, closed, wg에 요청을 더하는 시점을 보호한다.
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
	done     chan struct{}
	// wg는 처리 중인 Upgrade 요청과 열린 connection을 센다.
	wg sync.WaitGroup
}

// New는 Relay를 만든다.
func New(opts Options) (*Relay, error) {
	if opts.Control == nil {
		return nil, errors.New("realtime: Control이 필요합니다")
	}
	if opts.Connectors == nil {
		return nil, errors.New("realtime: ConnectorAuthenticator가 필요합니다")
	}
	if opts.AllowOrigin == nil {
		return nil, errors.New("realtime: AllowOrigin이 필요합니다")
	}
	r := &Relay{
		control:              opts.Control,
		connectors:           opts.Connectors,
		allowOrigin:          opts.AllowOrigin,
		clock:                opts.Clock,
		logger:               opts.Logger,
		metrics:              opts.Metrics,
		grace:                durationOr(opts.Grace, DefaultGrace),
		attachTimeout:        durationOr(opts.AttachTimeout, defaultAttachTimeout),
		writeTimeout:         durationOr(opts.WriteTimeout, defaultWriteTimeout),
		closeGrace:           durationOr(opts.CloseGrace, defaultCloseGrace),
		browserQueueBytes:    intOr(opts.BrowserQueueBytes, defaultQueueBytes),
		browserQueueMessages: intOr(opts.BrowserQueueMessages, defaultQueueMessages),
		dataQueueBytes:       intOr(opts.DataQueueBytes, defaultQueueBytes),
		dataQueueMessages:    intOr(opts.DataQueueMessages, defaultQueueMessages),
		sessions:             make(map[string]*session),
		trust:                newDataTrust(),
		done:                 make(chan struct{}),
	}
	if r.clock == nil {
		r.clock = SystemClock{}
	}
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	if r.grace <= 0 || r.attachTimeout <= 0 || r.writeTimeout <= 0 || r.closeGrace <= 0 {
		return nil, errors.New("realtime: 시간 설정은 0보다 커야 합니다")
	}
	maxBinary := int64(opts.MaxBinaryMessage)
	if maxBinary <= 0 {
		maxBinary = defaultMaxBinaryMessage
	}
	// gorilla의 read limit은 Text와 Binary 모두에 적용된다. JSON Text 한도보다 작아지지 않게 한다.
	r.readLimit = max(maxBinary, maxJSONTextBytes)

	// Browser WSS는 위에서 strict Origin을 직접 검증하므로 gorilla의 기본 Origin 검사(Host와 비교)를 쓰지 않는다.
	r.browserUpgrader = websocket.Upgrader{
		Subprotocols:     []string{BrowserSubprotocol},
		HandshakeTimeout: r.writeTimeout,
		CheckOrigin:      func(*http.Request) bool { return true },
		Error:            r.upgradeRejected,
	}
	// Connector는 Browser가 아니므로 Origin을 보내지 않는다. 기본 검사를 유지해 cross-origin Browser의 Upgrade는 거절한다.
	r.dataUpgrader = websocket.Upgrader{
		Subprotocols:     []string{DataSubprotocol},
		HandshakeTimeout: r.writeTimeout,
		Error:            r.upgradeRejected,
	}
	return r, nil
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

func intOr(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// BrowserHandler는 Browser Terminal WSS(BrowserPath)를 처리한다.
func (r *Relay) BrowserHandler() http.Handler { return http.HandlerFunc(r.serveBrowserHTTP) }

// DataHandler는 Connector Terminal Data WSS(DataPath)를 처리한다.
func (r *Relay) DataHandler() http.Handler { return http.HandlerFunc(r.serveDataHTTP) }

// enter는 요청 하나의 처리를 시작한다. Close 이후에는 false다. true이면 호출자가 leave를 호출해야 한다.
func (r *Relay) enter() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.wg.Add(1)
	return true
}

func (r *Relay) leave() { r.wg.Done() }

// Close는 새 Upgrade와 새 TerminalSession 등록을 거절한다. 이미 attach된 connection은 Shutdown이 정리한다.
// 여러 번 호출해도 안전하고 기다리지 않는다.
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		if r.metrics != nil {
			r.metrics.Draining.Set(1)
		}
		close(r.done)
	}
}

// Shutdown은 Close를 호출하고 active TerminalSession을 SERVICE_RESTARTING으로 종료한 뒤(Connector에 CLOSE를 요청하고
// Browser에 TERMINAL_SESSION_ENDED를 보낸다) 열린 connection이 모두 끝나거나 ctx가 끝날 때까지 기다린다.
func (r *Relay) Shutdown(ctx context.Context) error {
	r.Close()

	r.mu.Lock()
	sessions := make([]*session, 0, len(r.sessions))
	for _, s := range r.sessions {
		sessions = append(sessions, s)
	}
	r.mu.Unlock()

	for _, s := range sessions {
		r.shutdownSession(ctx, s)
	}

	drained := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Relay) shutdownSession(ctx context.Context, s *session) {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	if s.ended() {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	if err := r.control.CloseSession(callCtx, s.corr.TerminalSessionID, EndReasonServiceRestarting); err != nil {
		s.log.Warn("서비스 종료 중 TerminalSession 종료 기록 실패", "error_code", errorCode(err))
	}
	r.finish(s, End{Reason: EndReasonServiceRestarting}, true, true)
}

// RevokeConnectorCredential은 credentialID로 인증된 Terminal Data WSS를 더 이상 신뢰하지 않고 종료한다. 종료한 connection 수를 반환한다.
// Credential 저장소 상태가 revoke로 바뀐 뒤에만 호출한다. 그래서 이 호출 뒤에 시작하는 Upgrade는 인증에서 거절된다.
//
// 다른 Credential이나 다른 Connector의 connection은 건드리지 않는다. 종료는 TerminalSession의 종료가 아니라 이 transport의
// trust 상실이다. PTY는 그대로이고 TerminalSession은 data channel 없이 남으며, Connector가 유효한 Credential로 다시 attach하면
// 같은 TerminalSession의 새 data channel이 된다(resumed=true). 이 호출이 반환한 뒤에는 종료된 connection으로 Binary frame이
// 오가지 않는다.
func (r *Relay) RevokeConnectorCredential(credentialID string) int {
	if credentialID == "" {
		return 0
	}
	return r.terminateRevoked(r.trust.revokeCredential(credentialID), "credential")
}

// RevokeConnector는 connectorID의 모든 Terminal Data WSS를 더 이상 신뢰하지 않고 종료한다. Connector 자체가 revoke되었을 때
// 그 Connector의 어떤 Credential로 인증한 connection이든 같다. 그 밖의 의미는 RevokeConnectorCredential과 같다.
func (r *Relay) RevokeConnector(connectorID string) int {
	if connectorID == "" {
		return 0
	}
	return r.terminateRevoked(r.trust.revokeConnector(connectorID), "connector")
}

// terminateRevoked는 trust를 잃은 connection들을 data channel에서 내리고 4001로 종료한다. 기다리지 않는다.
func (r *Relay) terminateRevoked(conns []*dataConn, scope string) int {
	for _, d := range conns {
		// 먼저 세션의 current data channel에서 내려 이후 OUTPUT/INPUT/resize가 이 connection을 지나지 않게 한다.
		// dataLoop의 읽기 반복이 끝나면 dataGone이 호출되지만 이미 s.data가 이 connection이 아니라 아무것도 하지 않는다.
		if s := d.session.Load(); s != nil {
			s.mu.Lock()
			if s.data == d {
				s.data = nil
				s.log.Warn("Connector Terminal Data trust 상실로 data channel 종료", "scope", scope)
			}
			s.mu.Unlock()
		}
		// 아직 보내지 않은 frame은 버린다. 신뢰하지 않는 connection으로 INPUT을 더 흘리지 않는다.
		d.p.close(closeCredentialRevoked, "credential revoked", false)
	}
	return len(conns)
}

// Sessions는 Relay가 추적 중인 TerminalSession 수다. 종료된 TerminalSession은 즉시 제거한다.
func (r *Relay) Sessions() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

func (r *Relay) lookup(id string) *session {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

func (r *Relay) remove(id string, s *session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions[id] == s {
		delete(r.sessions, id)
	}
}

// Expect는 TerminalSession 생성 중에 호출하며 Connector의 Terminal Data attach보다 먼저 예상 correlation을 등록한다.
// 등록된 TerminalSession은 Activate 전까지 Browser attach를 받지 않는다.
func (r *Relay) Expect(e Expected) error {
	if e.TerminalSessionID == "" || e.ConnectorID == "" || e.LabInstanceID == "" || e.Generation < 1 {
		return ErrInvalidExpected
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrRelayClosed
	}
	if _, exists := r.sessions[e.TerminalSessionID]; exists {
		return ErrDuplicateSession
	}
	r.sessions[e.TerminalSessionID] = &session{
		corr:        correlation{TerminalSessionID: e.TerminalSessionID, LabInstanceID: e.LabInstanceID, Generation: e.Generation},
		connectorID: e.ConnectorID,
		log: r.logger.With(
			"terminal_session_id", e.TerminalSessionID,
			"lab_instance_id", e.LabInstanceID,
			"connector_id", e.ConnectorID,
			"generation", e.Generation,
		),
	}
	return nil
}

// Activate는 PTY가 준비되어 Browser attach를 받을 수 있게 하고 첫 attach를 기다리는 grace를 graceExpiresAt까지로 시작한다.
// Connector의 valid Terminal Data WSS가 bind되어 있어야 하며, 아니면 ErrDataNotBound다.
// 등록되지 않았거나 이미 종료된 TerminalSession이면 ErrSessionNotFound/ErrSessionEnded다.
func (r *Relay) Activate(id string, graceExpiresAt time.Time) error {
	s := r.lookup(id)
	if s == nil {
		return ErrSessionNotFound
	}
	s.tmu.Lock()
	defer s.tmu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.state == stateEnded:
		return ErrSessionEnded
	case s.data == nil:
		return ErrDataNotBound
	}
	s.state = stateLive
	r.armGraceLocked(s, graceExpiresAt)
	return nil
}

// DataBound는 Connector의 valid Terminal Data WSS가 이 TerminalSession에 지금 bind되어 있는지 반환한다.
func (r *Relay) DataBound(id string) bool {
	s := r.lookup(id)
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data != nil && s.state != stateEnded
}

// Forget은 TerminalSession 생성에 실패했을 때 등록을 없애고 bind된 data channel을 닫는다. Browser에는 알리지 않는다.
func (r *Relay) Forget(id string) {
	s := r.lookup(id)
	if s == nil {
		return
	}
	s.tmu.Lock()
	defer s.tmu.Unlock()
	r.finish(s, End{}, false, false)
}

// Terminate는 Control이 종료를 결정한 TerminalSession을 정리한다. 열려 있는 Browser에 TERMINAL_SESSION_ENDED를 보내고
// data channel을 닫는다. end.FromConnector가 아니면 Connector에도 TERMINAL_DATA_CLOSE를 보낸다. 없는 TerminalSession이면 아무것도 하지 않는다.
// Control 구현이 Relay 안에서(CloseSession, SessionEnded 호출 중에) 호출해서는 안 된다.
func (r *Relay) Terminate(id string, end End) {
	s := r.lookup(id)
	if s == nil {
		return
	}
	s.tmu.Lock()
	defer s.tmu.Unlock()
	r.finish(s, end, true, !end.FromConnector)
}

// finish는 TerminalSession의 ephemeral 상태를 모두 정리한다. s.tmu를 잡고 호출한다. 이미 종료된 TerminalSession이면 아무것도 하지 않는다.
func (r *Relay) finish(s *session, end End, notifyBrowser, notifyData bool) {
	s.mu.Lock()
	if s.state == stateEnded {
		s.mu.Unlock()
		return
	}
	s.state = stateEnded
	b, d := s.browser, s.data
	s.browser, s.data = nil, nil
	s.stopGraceLocked()
	s.mu.Unlock()

	r.remove(s.corr.TerminalSessionID, s)
	s.log.Info("TerminalSession Relay 정리", "reason", end.Reason)

	if b != nil {
		code := closeCodeFor(end.Reason)
		if notifyBrowser {
			if err := b.p.send(websocket.TextMessage, browserEnded(s.corr.TerminalSessionID, end)); err != nil {
				// 느린 수신자라 queue가 가득 찼다. 쌓인 OUTPUT을 버리고 close code로 종료를 알린다.
				b.close(code, "session ended", false)
			} else {
				b.close(code, "session ended", true)
			}
		} else {
			b.close(code, "session ended", true)
		}
	}
	if d != nil {
		if notifyData {
			_ = d.p.send(websocket.TextMessage, dataClose(s.corr, end.Reason, end.Trace))
		}
		d.p.close(closeNormal, "session ended", true)
	}
}

// closeCodeFor는 종료 원인을 Browser WSS close code로 바꾼다(contracts/realtime/README.md §9).
func closeCodeFor(reason string) int {
	switch reason {
	case EndReasonSessionExpired:
		return closeNotFound
	case EndReasonLabReset, EndReasonLabCleanup, "SOURCE_TERMINAL_ENDED":
		return closeLifecycle
	case EndReasonServiceRestarting:
		return closeServiceRestart
	default:
		// SESSION_CLOSED, PTY_EXITED, SSH_DISCONNECTED와 알 수 없는 값은 정상 종료다.
		return closeNormal
	}
}

var safeReason = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// SanitizeReason은 Connector가 보낸 종료 원인을 Browser와 DB에 전달해도 되는 형태로 제한한다.
// 기계 판독용 상수 형태가 아니면 UNKNOWN이다. 원문을 log나 응답에 복사하지 않는다.
func SanitizeReason(reason string) string {
	if safeReason.MatchString(reason) {
		return reason
	}
	return "UNKNOWN"
}

// errorCode는 log에 남겨도 안전한 고정 분류다. 오류 원문은 남기지 않는다.
func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return codeAuthRequired
	case errors.Is(err, ErrInvalidToken):
		return codeInvalidSessionTok
	case errors.Is(err, ErrForbidden):
		return codeForbidden
	case errors.Is(err, ErrSessionNotFound):
		return codeSessionNotFound
	case errors.Is(err, ErrSessionEnded):
		return codeSessionExpired
	case errors.Is(err, ErrLabMutation):
		return codeLabMutation
	default:
		return codeInternalError
	}
}
