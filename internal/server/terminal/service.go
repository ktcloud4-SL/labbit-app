// Package terminal은 Terminal 대상 VM 조회(Targets)와 TerminalSession의 생성·종료·attach 권한 판정 use case, Connector lifecycle Control이다.
//
// Browser/Connector의 WebSocket byte stream은 realtime.Relay가 중계하고, 이 package는 그 Relay가 필요로 하는
// DB-backed authority(realtime.Control)를 제공한다. 현재 User, Class 권한, LabInstance 소유, 현재 generation, 대상 VM은
// 이 package가 PostgreSQL 상태로 판단하며 Browser가 보낸 Provider Server ID나 Connector ID, generation은 신뢰하지 않는다.
//
// Terminal INPUT/OUTPUT, transcript, exit code는 저장하지 않는다. 저장하는 것은 lifecycle metadata뿐이다.
// HTTP status와 Problem Details는 알지 못한다.
package terminal

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/observability"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	// LabInstanceStatusReady는 TerminalSession을 만들 수 있는 유일한 LabInstance 상태다.
	LabInstanceStatusReady = "READY"
	// ProviderResourcePresent는 대상 VM이 지금 존재하는 provider_resources.lifecycle_status다.
	ProviderResourcePresent = "PRESENT"

	defaultOpenTimeout = 30 * time.Second
	// cleanupTimeout은 요청이 취소된 뒤에도 끝내야 하는 정리(종료 기록, Connector CLOSE)의 시간 상한이다.
	cleanupTimeout = 10 * time.Second
)

// EndReasonOpenFailed는 생성에 실패한 TerminalSession을 기록하는 종료 원인이다. Browser가 attach한 적이 없으므로 wire에는 나가지 않는다.
const EndReasonOpenFailed = "OPEN_FAILED"

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.ClassRepository
	repository.TerminalRepository
	repository.Transactor
}

// Authenticator는 Browser 로그인 Session을 검증하는 use case다. *auth.Service가 구현한다.
type Authenticator interface {
	Authenticate(ctx context.Context, token auth.SessionToken) (auth.Principal, error)
}

// Connectors는 Connector Control connection으로 TerminalSession lifecycle message를 보내는 경계다. *connector.Router가 구현한다.
type Connectors interface {
	SendTerminalOpen(ctx context.Context, open connector.TerminalOpen) (connector.SentMessage, error)
	SendTerminalClose(ctx context.Context, cl connector.TerminalClose) (connector.SentMessage, error)
	ForgetTerminalOpen(connectorID uuid.UUID, terminalSessionID string) bool
}

// Relay는 Service가 사용하는 Relay 경계다. *realtime.Relay가 구현한다.
type Relay interface {
	Expect(e realtime.Expected) error
	Activate(id string, graceExpiresAt time.Time) error
	Forget(id string)
	Terminate(id string, end realtime.End)
}

// Options는 Service 구성이다.
type Options struct {
	Store      Store
	Auth       Authenticator
	Connectors Connectors
	Relay      Relay
	// Clock이 nil이면 realtime.SystemClock이다.
	Clock realtime.Clock
	// Logger가 nil이면 로그를 남기지 않는다. token과 Terminal 본문은 어떤 경우에도 기록하지 않는다.
	Logger *slog.Logger
	// OpenTimeout은 Connector의 TERMINAL_OPEN_RESULT를 기다리는 시간이다. 0이면 30초다.
	OpenTimeout time.Duration
	// Grace는 Browser attach를 기다리는 시간(첫 attach 전과 Browser 단절 뒤)이다. 0이면 realtime.DefaultGrace다.
	// Relay의 Grace와 같은 값이어야 한다.
	Grace time.Duration
}

// Service는 TerminalSession use case이며 realtime.Control과 connector.TerminalSink를 구현한다.
type Service struct {
	store      Store
	auth       Authenticator
	connectors Connectors
	relay      Relay
	clock      realtime.Clock
	logger     *slog.Logger

	openTimeout time.Duration
	grace       time.Duration

	mu     sync.Mutex
	opens  map[string]chan openOutcome
	closed bool
	wg     sync.WaitGroup
}

var (
	_ realtime.Control       = (*Service)(nil)
	_ connector.TerminalSink = (*Service)(nil)
)

// NewService는 Service를 만든다. Relay와 Service는 서로를 필요로 하므로 조립하는 쪽이 둘 중 하나를 forwarder로 넘겨
// 순환을 끊는다(internal/server/app).
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Auth == nil || opts.Connectors == nil || opts.Relay == nil {
		return nil, errors.New("terminal: Store, Auth, Connectors, Relay가 필요합니다")
	}
	s := &Service{
		store:       opts.Store,
		auth:        opts.Auth,
		connectors:  opts.Connectors,
		relay:       opts.Relay,
		clock:       opts.Clock,
		logger:      opts.Logger,
		openTimeout: opts.OpenTimeout,
		grace:       opts.Grace,
		opens:       make(map[string]chan openOutcome),
	}
	if s.clock == nil {
		s.clock = realtime.SystemClock{}
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.openTimeout == 0 {
		s.openTimeout = defaultOpenTimeout
	}
	if s.grace == 0 {
		s.grace = realtime.DefaultGrace
	}
	if s.openTimeout < 0 || s.grace < 0 {
		return nil, errors.New("terminal: 시간 설정은 0보다 커야 합니다")
	}
	return s, nil
}

// Shutdown은 새 background 작업을 받지 않고 진행 중인 것이 끝나거나 ctx가 끝날 때까지 기다린다.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// spawn은 Connector read loop의 fence 안에서 호출되는 handler가 오래 걸리는 일을 넘기는 곳이다. 종료 후에는 받지 않는다.
func (s *Service) spawn(fn func()) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		fn()
	}()
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다. 하나의 리소스가 여러 ID 표기로 보이지 않게 한다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}

// CreateInput은 Create의 입력이다. Provider Server ID, Connector ID, generation은 받지 않는다.
type CreateInput struct {
	// LabInstanceID는 요청 경로의 값이다. 검증되지 않았다.
	LabInstanceID string
	TargetVMKey   string
	// Cols와 Rows는 이미 검증된 양의 정수 JSON number 원문이다.
	Cols, Rows json.RawMessage
	// RequestID는 원본 HTTP request의 correlation이다(선택).
	RequestID string
}

// Created는 만들어진 TerminalSession이다.
type Created struct {
	ID             uuid.UUID
	Generation     int64
	Token          realtime.AttachToken
	TokenExpiresAt time.Time
}

// openOutcome은 TERMINAL_OPEN에 대한 Connector의 결과다.
type openOutcome struct {
	succeeded bool
	// ended이면 OPEN_RESULT 전에 Connector가 TERMINAL_ENDED를 알렸다.
	ended bool
}

// target은 Create가 transaction 안에서 결정한 값이다.
type target struct {
	lab         repository.LabInstance
	connectorID uuid.UUID
	serverID    uuid.UUID
	providerID  string
}

// Create는 현재 사용자의 LabInstance VM에 TerminalSession을 만든다.
//
//  1. 현재 DB 상태에서 LabInstance 소유, 현재 ClassMembership, READY, targetVmKey가 immutable CreationSnapshot의 VM인지,
//     현재 generation의 대상 VM을 결정한다. 하나라도 맞지 않으면 side effect 없이 거절한다. Targets가 돌려준 목록을 근거로 신뢰하지 않고
//     매번 다시 판정한다. transaction은 LabInstance를 FOR SHARE로 잠가 그 사이 generation이 바뀌지 않게 한다.
//  2. OPENING TerminalSession과 attach token digest를 저장한다(transaction은 외부 I/O 전에 끝낸다).
//  3. Relay에 예상 correlation을 등록한 뒤 Connector에 TERMINAL_OPEN을 보낸다.
//  4. TERMINAL_OPEN_RESULT SUCCEEDED를 받고 같은 TerminalSession의 Terminal Data WSS가 실제로 bind되었을 때만 성공한다.
//
// 3~4의 어떤 실패에서도 TerminalSession을 성공 상태로 남기지 않는다. ENDED로 기록하고 Relay 등록을 지우며 PTY가 만들어졌을 수
// 있으면 TERMINAL_CLOSE를 요청한다. 요청이 취소되어도 이 정리는 끝까지 수행한다.
func (s *Service) Create(ctx context.Context, user repository.User, in CreateInput) (Created, error) {
	relay := s.relay
	labID, ok := parseID(in.LabInstanceID)
	if !ok {
		return Created{}, ErrNotFound
	}

	now := s.clock.Now()
	token, digest := newAttachToken()
	sessionID := uuid.New()
	tokenExpiresAt := now.Add(AttachTokenLifetime)

	var resolved target
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		var err error
		resolved, err = s.resolveTarget(ctx, repos, user, labID, in.TargetVMKey)
		if err != nil {
			return err
		}
		err = repos.CreateTerminalSession(ctx, repository.NewTerminalSession{
			ID:                 sessionID,
			OrganizationID:     resolved.lab.OrganizationID,
			LabInstanceID:      resolved.lab.ID,
			UserID:             user.ID,
			ProviderResourceID: resolved.serverID,
			Generation:         resolved.lab.Generation,
			AttachTokenHash:    digest[:],
			TokenExpiresAt:     tokenExpiresAt,
			CreatedAt:          now,
		})
		if err != nil {
			return fmt.Errorf("terminal: TerminalSession 저장: %w", err)
		}
		return nil
	})
	if err != nil {
		return Created{}, err
	}

	id := sessionID.String()
	corr := connector.TerminalCorrelation{TerminalSessionID: id, LabInstanceID: resolved.lab.ID.String(), Generation: resolved.lab.Generation}
	log := s.logger.With(
		"terminal_session_id", id,
		"lab_instance_id", corr.LabInstanceID,
		"connector_id", resolved.connectorID.String(),
		"generation", corr.Generation,
	)
	log = withControlCorrelation(log, in.RequestID, connector.TraceFromContext(ctx))

	// 정리는 요청 context가 취소되어도 끝까지 수행한다.
	abort := func(sentOpen bool) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		s.abortCreate(cleanupCtx, log, relay, resolved.connectorID, sessionID, corr, in.RequestID, sentOpen)
	}

	// Connector의 Data attach보다 먼저 예상 correlation을 등록한다.
	if err := relay.Expect(realtime.Expected{
		TerminalSessionID: id, ConnectorID: resolved.connectorID.String(), LabInstanceID: corr.LabInstanceID, Generation: corr.Generation,
	}); err != nil {
		log.Error("Relay 등록 실패", "error_code", "RELAY_UNAVAILABLE")
		abort(false)
		return Created{}, ErrUnavailable
	}
	waiter := s.addOpenWaiter(id)
	defer s.removeOpenWaiter(id)

	_, err = s.connectors.SendTerminalOpen(ctx, connector.TerminalOpen{
		ConnectorID:      resolved.connectorID,
		RequestID:        in.RequestID,
		Correlation:      corr,
		TargetVMKey:      in.TargetVMKey,
		ProviderServerID: resolved.providerID,
		Cols:             in.Cols,
		Rows:             in.Rows,
		Trace:            connector.TraceFromContext(ctx),
	})
	switch {
	case err == nil:
	case errors.Is(err, connector.ErrConnectorUnavailable):
		// 아무것도 쓰지 않았고 pending도 없다. Connector가 PTY를 만들었을 수 없다.
		log.Warn("TerminalSession 생성 실패", "reason", "connector_unavailable")
		abort(false)
		return Created{}, ErrConnectorUnavailable
	case errors.Is(err, connector.ErrSendFailed):
		// 전송 여부가 불명확하다. Connector가 OPEN을 받았을 수 있으므로 CLOSE를 요청한다.
		log.Warn("TerminalSession 생성 실패", "reason", "open_send_failed")
		abort(true)
		return Created{}, ErrConnectorUnavailable
	default:
		log.Error("TERMINAL_OPEN을 만들지 못함", "error_code", "INTERNAL_ERROR")
		abort(false)
		return Created{}, fmt.Errorf("terminal: TERMINAL_OPEN 전송: %w", err)
	}

	timedOut := make(chan struct{})
	timer := s.clock.AfterFunc(s.openTimeout, func() { close(timedOut) })
	defer timer.Stop()

	select {
	case outcome := <-waiter:
		if !outcome.succeeded {
			reason := "open_failed"
			if outcome.ended {
				reason = "ended_before_open_result"
			}
			log.Warn("TerminalSession 생성 실패", "reason", reason)
			abort(true)
			return Created{}, ErrOpenFailed
		}
	case <-timedOut:
		log.Warn("TerminalSession 생성 실패", "reason", "open_timeout")
		abort(true)
		return Created{}, ErrOpenFailed
	case <-ctx.Done():
		log.Warn("TerminalSession 생성 취소", "reason", "request_canceled")
		abort(true)
		return Created{}, ctx.Err()
	}

	// OPEN_RESULT SUCCEEDED라는 주장만 믿지 않는다. 같은 TerminalSession의 valid Terminal Data WSS가 실제로 bind되었어야 한다.
	graceExpiresAt := s.clock.Now().Add(s.grace)
	if err := relay.Activate(id, graceExpiresAt); err != nil {
		log.Warn("TerminalSession 생성 실패", "reason", "data_not_bound")
		abort(true)
		return Created{}, ErrOpenFailed
	}
	changed, err := s.store.MarkTerminalSessionOpened(ctx, sessionID, s.clock.Now(), graceExpiresAt)
	if err != nil || !changed {
		// 그 사이 종료되었거나 저장하지 못했다. 성공으로 남기지 않는다.
		log.Warn("TerminalSession 생성 실패", "reason", "open_not_recorded")
		abort(true)
		return Created{}, ErrOpenFailed
	}
	log.Info("TerminalSession 생성")
	return Created{ID: sessionID, Generation: corr.Generation, Token: token, TokenExpiresAt: tokenExpiresAt}, nil
}

// resolveTarget은 transaction 안에서 현재 DB 상태로 대상 LabInstance, Connector, VM을 결정한다. side effect는 없다.
func (s *Service) resolveTarget(ctx context.Context, repos repository.Repositories, user repository.User, labID uuid.UUID, targetVMKey string) (target, error) {
	// 소유, 현재 ClassMembership, READY 판정은 target 조회(Targets)와 같은 경계를 쓴다.
	lab, err := authorizeLabInstance(ctx, repos, user, labID)
	if err != nil {
		return target{}, err
	}

	// Browser가 보낸 targetVmKey는 target 조회가 돌려준 immutable CreationSnapshot의 VM이어야 한다. 현재 generation에 우연히 같은 이름의
	// SERVER ProviderResource가 있어도 snapshot에 없는 key는 target이 아니다. 최신 LabSpec이 아니라 snapshot 기준이다.
	catalog, err := snapshotTargets(ctx, repos, lab)
	if err != nil {
		return target{}, err
	}
	if !catalog.hasTarget(targetVMKey) {
		return target{}, ErrTargetNotFound
	}

	connectorID, err := repos.ConnectorIDForLabInstance(ctx, lab.ID)
	if errors.Is(err, repository.ErrNotFound) {
		// CreationSnapshot → ProviderConnection → Connector 관계는 DB 제약상 있어야 한다.
		return target{}, ErrInconsistentData
	}
	if err != nil {
		return target{}, fmt.Errorf("terminal: Connector 조회: %w", err)
	}

	servers, err := repos.ProviderServers(ctx, lab.ID, lab.Generation, targetVMKey)
	if err != nil {
		return target{}, fmt.Errorf("terminal: 대상 VM 조회: %w", err)
	}
	if len(servers) == 0 {
		// snapshot에는 있는 VM인데 현재 generation에 SERVER ProviderResource가 없다(Reset 진행·Provider drift 등). Browser가 잘못 보낸
		// 값이 아니라 지금 사용할 수 없는 것이다.
		return target{}, ErrTargetUnavailable
	}
	var present []repository.ProviderServer
	for _, server := range servers {
		if server.LifecycleStatus == ProviderResourcePresent {
			present = append(present, server)
		}
	}
	switch len(present) {
	case 0:
		return target{}, ErrTargetUnavailable
	case 1:
	default:
		// 같은 generation에 같은 논리 이름의 PRESENT VM이 둘 이상이면 어느 쪽인지 알 수 없다. 임의로 고르지 않는다.
		return target{}, ErrInconsistentData
	}
	return target{lab: lab, connectorID: connectorID, serverID: present[0].ID, providerID: present[0].ProviderID}, nil
}

// abortCreate는 실패한 생성을 정리한다. 성공 상태로 남은 TerminalSession이 없도록 기록을 ENDED로 바꾸고 Relay 등록과
// pending을 지운다. sentOpen이면 Connector가 PTY를 만들었을 수 있으므로 TERMINAL_CLOSE를 요청한다(실패해도 기록은 ENDED다).
func (s *Service) abortCreate(ctx context.Context, log *slog.Logger, relay Relay, connectorID, sessionID uuid.UUID, corr connector.TerminalCorrelation, requestID string, sentOpen bool) {
	s.connectors.ForgetTerminalOpen(connectorID, corr.TerminalSessionID)
	relay.Forget(corr.TerminalSessionID)
	if _, err := s.store.EndTerminalSession(ctx, sessionID, s.clock.Now(), EndReasonOpenFailed); err != nil {
		log.Error("실패한 TerminalSession 종료 기록 실패", "error_code", classify(err))
	}
	if sentOpen {
		s.sendClose(ctx, log, connectorID, corr, requestID, "", "SESSION_CLOSED")
	}
}

// sendClose는 TERMINAL_CLOSE를 요청한다. Connector를 사용할 수 없어도 호출자의 종료 기록은 유지하므로 실패는 log만 남긴다.
func (s *Service) sendClose(ctx context.Context, log *slog.Logger, connectorID uuid.UUID, corr connector.TerminalCorrelation, requestID, operationID, reason string) {
	_, err := s.connectors.SendTerminalClose(ctx, connector.TerminalClose{
		ConnectorID: connectorID, RequestID: requestID, OperationID: operationID,
		Correlation: corr, Reason: reason, Trace: connector.TraceFromContext(ctx),
	})
	if err != nil {
		log.Warn("Connector TERMINAL_CLOSE 전달 못 함", "reason", closeFailureReason(err))
	}
}

func closeFailureReason(err error) string {
	switch {
	case errors.Is(err, connector.ErrConnectorUnavailable):
		return "connector_unavailable"
	case errors.Is(err, connector.ErrSendFailed):
		return "send_failed"
	default:
		return "invalid_close"
	}
}

// classify는 오류를 log에 남겨도 안전한 고정 분류로 바꾼다. repository.Error의 원문이나 Cause는 남기지 않는다.
func classify(err error) string {
	var repoErr *repository.Error
	switch {
	case errors.As(err, &repoErr):
		return "REPOSITORY_" + repoErr.Kind.String()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "CONTEXT"
	default:
		return "UNCLASSIFIED"
	}
}

// addOpenWaiter는 TERMINAL_OPEN_RESULT를 기다리는 채널을 등록한다. OPEN을 보내기 전에 등록해 즉시 온 응답을 놓치지 않는다.
func (s *Service) addOpenWaiter(id string) chan openOutcome {
	ch := make(chan openOutcome, 1)
	s.mu.Lock()
	s.opens[id] = ch
	s.mu.Unlock()
	return ch
}

func (s *Service) removeOpenWaiter(id string) {
	s.mu.Lock()
	delete(s.opens, id)
	s.mu.Unlock()
}

// notifyOpen은 기다리는 Create가 있으면 결과를 전달한다. 기다리지 않는다. 없으면(시간 초과 뒤 늦게 온 결과 등) 아무것도 하지 않는다.
func (s *Service) notifyOpen(id string, outcome openOutcome) bool {
	s.mu.Lock()
	ch := s.opens[id]
	s.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- outcome:
		return true
	default:
		return false
	}
}

// HandleTerminalEvent는 Router가 Connector Session의 read loop fence 안에서 호출한다. 짧게 반환한다.
func (s *Service) HandleTerminalEvent(event connector.TerminalEvent) {
	switch e := event.(type) {
	case connector.TerminalOpenResultEvent:
		// Router가 인증된 ConnectorID, terminalSessionId, labInstanceId, generation, replyToMessageId를 모두 대조했다.
		log := withControlCorrelation(s.logger.With(
			"connector_id", e.ConnectorID.String(),
			"terminal_session_id", e.Correlation.TerminalSessionID,
			"lab_instance_id", e.Correlation.LabInstanceID,
			"generation", e.Correlation.Generation,
		), e.RequestID, e.Trace)
		if e.Payload.Error != nil {
			log.Warn("Connector TERMINAL_OPEN 실패 보고", "error_code", safeCode(e.Payload.Error.Code))
		}
		if !s.notifyOpen(e.Correlation.TerminalSessionID, openOutcome{succeeded: e.Payload.Outcome == connector.TerminalOutcomeSucceeded}) {
			log.Debug("기다리는 Create가 없는 TERMINAL_OPEN_RESULT")
		}
	case connector.TerminalEndedEvent:
		s.spawn(func() { s.handleConnectorEnded(e) })
	case connector.TerminalUnmatchedEvent:
		// Router가 이미 기록했다. 어떤 TerminalSession에도 연결하지 않는다.
	}
}

// safeCode는 Connector가 보낸 오류 code를 log에 남길 수 있는 형태로 제한한다.
func safeCode(code string) string { return realtime.SanitizeReason(code) }

// handleConnectorEnded는 Connector가 알린 TerminalSession 종료(PTY 종료, SSH 끊김 등)를 처리한다.
//
// 종료 통지는 요청에 대한 응답이 아니므로 Router가 pending에 연결하지 않는다. 그래서 여기서 권위 있는 저장소 상태와 대조한다.
// 인증된 Connector가 이 TerminalSession을 소유한 Connector이고 labInstanceId와 generation이 모두 같을 때만 종료하며,
// 하나라도 다르면 다른 TerminalSession으로 fallback하지 않고 무시한다. 같은 종료가 Control과 Data 양쪽으로 와도 멱등이다.
func (s *Service) handleConnectorEnded(e connector.TerminalEndedEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	log := s.logger.With(
		"connector_id", e.ConnectorID.String(),
		"terminal_session_id", e.Correlation.TerminalSessionID,
		"lab_instance_id", e.Correlation.LabInstanceID,
		"generation", e.Correlation.Generation,
	)
	log = withControlCorrelation(log, "", e.Trace)

	id, ok := parseID(e.Correlation.TerminalSessionID)
	if !ok {
		log.Warn("TERMINAL_ENDED 무시", "reason", "unknown_session")
		return
	}
	rec, err := s.store.TerminalSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		log.Warn("TERMINAL_ENDED 무시", "reason", "unknown_session")
		return
	}
	if err != nil {
		log.Error("TERMINAL_ENDED 처리 실패", "error_code", classify(err))
		return
	}
	owner, err := s.store.ConnectorIDForLabInstance(ctx, rec.LabInstanceID)
	if err != nil {
		log.Error("TERMINAL_ENDED 처리 실패", "error_code", classify(err))
		return
	}
	if owner != e.ConnectorID || e.Correlation.LabInstanceID != rec.LabInstanceID.String() || e.Correlation.Generation != rec.Generation {
		log.Warn("TERMINAL_ENDED 무시", "reason", "correlation_mismatch")
		return
	}

	// 아직 OPEN_RESULT를 기다리는 Create가 있으면 그 Create를 실패시킨다.
	s.notifyOpen(e.Correlation.TerminalSessionID, openOutcome{ended: true})

	// Connector가 종료를 알린 Control message의 유효한 Trace Context를 Browser의 종료 통지까지 잇는다.
	end := realtime.End{Reason: realtime.SanitizeReason(e.Payload.Reason), ExitCode: e.Payload.ExitCode, FromConnector: true, Trace: e.Trace}
	if _, err := s.store.EndTerminalSession(ctx, id, s.clock.Now(), end.Reason); err != nil {
		log.Error("TerminalSession 종료 기록 실패", "error_code", classify(err))
		return
	}
	s.relay.Terminate(e.Correlation.TerminalSessionID, end)
	log.Info("TerminalSession 종료 수신", "reason", end.Reason)
}

// Close는 사용자의 명시적 종료다. Browser WSS의 단절과 달리 60초 grace를 시작하지 않고 즉시 ENDED로 만든다.
// 이미 종료된 TerminalSession은 성공한다(멱등). 소유자가 아니면 ErrForbidden, 없으면 ErrNotFound다.
func (s *Service) Close(ctx context.Context, user repository.User, terminalSessionID string) error {
	id, ok := parseID(terminalSessionID)
	if !ok {
		return ErrNotFound
	}
	rec, err := s.store.TerminalSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("terminal: TerminalSession 조회: %w", err)
	}
	if rec.OrganizationID != user.OrganizationID || rec.UserID != user.ID {
		return ErrForbidden
	}
	if rec.Status == repository.TerminalSessionEnded {
		return nil
	}
	if err := s.closeLifecycle(ctx, rec, realtime.EndReasonSessionClosed, ""); err != nil {
		return err
	}
	s.relay.Terminate(rec.ID.String(), realtime.End{Reason: realtime.EndReasonSessionClosed, Trace: connector.TraceFromContext(ctx)})
	return nil
}

// LabMutation은 Reset/Cleanup 같은 Lab mutation이 LabInstance의 TerminalSession을 종료하라고 요청하는 입력이다.
type LabMutation struct {
	LabInstanceID uuid.UUID
	// Reason은 realtime.EndReasonLabReset 또는 realtime.EndReasonLabCleanup이다.
	Reason string
	// OperationID는 종료를 일으킨 Operation의 ID다(선택). Connector 메시지의 상관관계에 쓴다.
	OperationID string
}

// CloseForLabMutation은 LabInstance의 ENDED가 아닌 모든 TerminalSession을 Reset/Cleanup 사유로 종료한다.
// 이 종료는 단순 Browser disconnect가 아니므로 reconnect grace 대상이 아니다. Reset/Cleanup 자체는 이 package가 구현하지 않으며
// Operation 처리 쪽이 호출할 경계만 제공한다. 일부가 실패해도 나머지를 계속 종료하고 오류를 합쳐 반환한다.
func (s *Service) CloseForLabMutation(ctx context.Context, m LabMutation) error {
	if m.Reason != realtime.EndReasonLabReset && m.Reason != realtime.EndReasonLabCleanup {
		return ErrInvalidReason
	}
	recs, err := s.store.UnendedTerminalSessionsByLabInstance(ctx, m.LabInstanceID)
	if err != nil {
		return fmt.Errorf("terminal: TerminalSession 조회: %w", err)
	}
	var errs []error
	for _, rec := range recs {
		if err := s.closeLifecycle(ctx, rec, m.Reason, m.OperationID); err != nil {
			errs = append(errs, err)
			continue
		}
		s.relay.Terminate(rec.ID.String(), realtime.End{Reason: m.Reason, Trace: connector.TraceFromContext(ctx)})
	}
	return errors.Join(errs...)
}

// closeLifecycle은 Labbit이 TerminalSession을 종료한다. 종료를 먼저 기록하고(그래서 이후 attach할 수 없다) Connector에
// TERMINAL_CLOSE를 요청한다. Connector를 사용할 수 없어 CLOSE를 전달하지 못해도 기록은 유지한다. 이미 종료되었다면 CLOSE를
// 다시 보내지 않는다. Relay는 호출하지 않는다.
func (s *Service) closeLifecycle(ctx context.Context, rec repository.TerminalSession, reason, operationID string) error {
	changed, err := s.store.EndTerminalSession(ctx, rec.ID, s.clock.Now(), reason)
	if err != nil {
		return fmt.Errorf("terminal: 종료 기록: %w", err)
	}
	if !changed {
		return nil
	}
	log := s.logger.With(
		"terminal_session_id", rec.ID.String(),
		"lab_instance_id", rec.LabInstanceID.String(),
		"generation", rec.Generation,
	)
	requestID := observability.RequestIDFromContext(ctx)
	log = withControlCorrelation(log, requestID, connector.TraceFromContext(ctx))
	if operationID != "" {
		log = log.With("operation_id", operationID)
	}
	connectorID, err := s.store.ConnectorIDForLabInstance(ctx, rec.LabInstanceID)
	if err != nil {
		log.Warn("TERMINAL_CLOSE 대상 Connector를 찾지 못함", "error_code", classify(err))
		return nil
	}
	log = log.With("connector_id", connectorID.String())
	s.sendClose(ctx, log, connectorID, connector.TerminalCorrelation{
		TerminalSessionID: rec.ID.String(), LabInstanceID: rec.LabInstanceID.String(), Generation: rec.Generation,
	}, "", operationID, reason)
	log.Info("TerminalSession 종료", "reason", reason)
	return nil
}

// withControlCorrelation은 이 control event가 실제로 가진 metadata만 기록한다.
func withControlCorrelation(log *slog.Logger, requestID string, trace connector.TraceContext) *slog.Logger {
	if requestID != "" {
		log = log.With("request_id", requestID)
	}
	trace = connector.NormalizeTrace(trace.Traceparent, trace.Tracestate)
	if id := trace.TraceID(); id != "" {
		log = log.With("trace_id", id)
	}
	return log
}

// AuthenticateBrowser는 realtime.Control의 구현이다.
func (s *Service) AuthenticateBrowser(ctx context.Context, session realtime.SessionToken) error {
	_, err := s.auth.Authenticate(ctx, auth.SessionToken(session))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, auth.ErrUnauthenticated):
		return realtime.ErrUnauthenticated
	default:
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
}

// AuthorizeAttach는 realtime.Control의 구현이다. 첫 attach와 모든 re-attach에서 현재 로그인 세션, 현재 사용자, 현재 Class 권한,
// LabInstance 소유, 현재 generation, TerminalSession lifecycle, token digest를 다시 확인한다. token 하나만으로 attach할 수 없다.
func (s *Service) AuthorizeAttach(ctx context.Context, session realtime.SessionToken, req realtime.AttachRequest) (realtime.AttachGrant, error) {
	principal, err := s.auth.Authenticate(ctx, auth.SessionToken(session))
	switch {
	case err == nil:
	case errors.Is(err, auth.ErrUnauthenticated):
		return realtime.AttachGrant{}, realtime.ErrUnauthenticated
	default:
		return realtime.AttachGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	user := principal.User

	id, ok := parseID(req.TerminalSessionID)
	if !ok {
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}
	rec, err := s.store.TerminalSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}
	if err != nil {
		return realtime.AttachGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if rec.OrganizationID != user.OrganizationID || rec.UserID != user.ID {
		return realtime.AttachGrant{}, realtime.ErrForbidden
	}

	// token은 이 TerminalSession의 digest와 일치해야 한다. 형식이 틀리면 비교하지 않는다. 비교는 constant time이다.
	digest, ok := attachTokenDigest(req.Token)
	if !ok || subtle.ConstantTimeCompare(digest[:], rec.AttachTokenHash) != 1 {
		return realtime.AttachGrant{}, realtime.ErrInvalidToken
	}
	if !rec.TokenExpiresAt.After(s.clock.Now()) {
		return realtime.AttachGrant{}, realtime.ErrTokenExpired
	}
	switch rec.Status {
	case repository.TerminalSessionEnded:
		return realtime.AttachGrant{}, realtime.ErrSessionEnded
	case repository.TerminalSessionOpening:
		// 아직 PTY가 준비되지 않았다. 생성이 끝나기 전에는 token을 받은 Browser가 있을 수 없다.
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}

	lab, err := s.store.LabInstanceByID(ctx, rec.LabInstanceID)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.AttachGrant{}, realtime.ErrSessionNotFound
	}
	if err != nil {
		return realtime.AttachGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if lab.UserID != user.ID || lab.OrganizationID != user.OrganizationID {
		return realtime.AttachGrant{}, realtime.ErrForbidden
	}
	if lab.Generation != rec.Generation {
		// Reset 등으로 generation이 바뀌었다. 이 TerminalSession의 PTY는 이전 generation의 것이므로 더 이상 사용할 수 없다.
		// 권한 판정만 하고 끝내지 않고 이 TerminalSession을 종료한다(다시 시도해도 같은 결과이므로 정리한다).
		if err := s.closeLifecycle(ctx, rec, realtime.EndReasonLabReset, ""); err != nil {
			withControlCorrelation(s.logger.With(
				"terminal_session_id", rec.ID.String(), "lab_instance_id", rec.LabInstanceID.String(), "generation", rec.Generation,
			), observability.RequestIDFromContext(ctx), connector.TraceFromContext(ctx)).Error("stale TerminalSession 종료 실패", "error_code", classify(err))
		}
		return realtime.AttachGrant{}, realtime.ErrLabMutation
	}
	if _, err := s.store.ClassMembership(ctx, lab.ClassID, user.ID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return realtime.AttachGrant{}, realtime.ErrForbidden
		}
		return realtime.AttachGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	return realtime.AttachGrant{
		TerminalSessionID: rec.ID.String(),
		LabInstanceID:     rec.LabInstanceID.String(),
		Generation:        rec.Generation,
	}, nil
}

// RecordAttached는 realtime.Control의 구현이다.
func (s *Service) RecordAttached(ctx context.Context, terminalSessionID string, at time.Time) error {
	id, ok := parseID(terminalSessionID)
	if !ok {
		return realtime.ErrSessionNotFound
	}
	changed, err := s.store.MarkTerminalSessionAttached(ctx, id, at)
	if err != nil {
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if !changed {
		return realtime.ErrSessionEnded
	}
	return nil
}

// RecordDetached는 realtime.Control의 구현이다.
func (s *Service) RecordDetached(ctx context.Context, terminalSessionID string, at, graceExpiresAt time.Time) error {
	id, ok := parseID(terminalSessionID)
	if !ok {
		return realtime.ErrSessionNotFound
	}
	changed, err := s.store.MarkTerminalSessionDetached(ctx, id, at, graceExpiresAt)
	if err != nil {
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if !changed {
		return realtime.ErrSessionEnded
	}
	return nil
}

// CloseSession은 realtime.Control의 구현이다. Relay가 grace 만료와 서비스 종료에서 호출하므로 Relay를 다시 호출하지 않는다.
func (s *Service) CloseSession(ctx context.Context, terminalSessionID, reason string) error {
	id, ok := parseID(terminalSessionID)
	if !ok {
		return realtime.ErrSessionNotFound
	}
	rec, err := s.store.TerminalSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	return s.closeLifecycle(ctx, rec, reason, "")
}

// SessionEnded는 realtime.Control의 구현이다. Connector가 이미 종료를 알렸으므로 CLOSE를 다시 보내지 않는다.
func (s *Service) SessionEnded(ctx context.Context, terminalSessionID string, end realtime.End) error {
	id, ok := parseID(terminalSessionID)
	if !ok {
		return realtime.ErrSessionNotFound
	}
	if _, err := s.store.EndTerminalSession(ctx, id, s.clock.Now(), end.Reason); err != nil {
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	return nil
}
