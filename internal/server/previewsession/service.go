// Package previewsession은 본인 LabInstance의 Workspace VM application port에 대한 PreviewSession의 생성·종료 use case다(LBT-101).
//
// Browser가 보낸 targetPort도, 대상 VM도 신뢰하지 않는다. 현재 User, LabInstance 소유, 현재 ClassMembership, READY, 그 LabExecution의
// immutable CreationSnapshot의 workspaceVmKey, 현재 generation의 PRESENT SERVER ProviderResource, Connector를 PostgreSQL 상태로 결정하고,
// targetPort는 Backend가 명시적으로 설정한 허용 목록(Policy)에 있는 정확한 값만 승인한다. Browser가 보낸 VM key, Provider Server ID,
// Connector ID, generation은 받지 않는다.
//
// 이 package는 DB-backed authority(api role)다. Preview Origin의 Browser 인증, Gateway proxy, Connector Preview Data tunnel은
// internal/server/preview(preview role, DB 비의존)가 하며 이 package가 판정한 결과를 preview.Expected로만 넘긴다. PreviewSession 자체는
// process 안의 ephemeral 상태이며 PostgreSQL에 저장하지 않는다.
//
// 권한과 대상은 하나의 짧은 transaction에서 결정해 commit한 뒤에만 Connector Control(PREVIEW_OPEN)을 시작하고, 성공을 돌려주기 전에
// LabInstance의 generation과 READY가 그대로인지 다시 확인한다. Preview 본문, 경로, credential은 이 package가 알지 못한다.
// HTTP status와 Problem Details는 알지 못한다.
package previewsession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	// LabInstanceStatusReady는 PreviewSession을 만들 수 있는 유일한 LabInstance 상태다.
	LabInstanceStatusReady = "READY"
	// ProviderResourcePresent는 대상 VM이 지금 존재하는 provider_resources.lifecycle_status다.
	ProviderResourcePresent = "PRESENT"

	// 구현의 보수적인 기본값이다. 계약 수치가 아니며 Options로 주입할 수 있다.
	defaultOpenTimeout = 30 * time.Second
	// cleanupTimeout은 요청이 취소된 뒤에도 끝내야 하는 정리(PREVIEW_CLOSE)의 시간 상한이다.
	cleanupTimeout = 5 * time.Second
)

// 종료 원인(PREVIEW_CLOSE reason)이다. 생성 실패 정리에만 쓰는 값이며 Gateway의 종료 사유(preview.End*)와 같은 집합의 일부다.
const (
	closeReasonOpenTimeout = "OPEN_TIMEOUT"
	closeReasonCanceled    = "REQUEST_CANCELED"
	closeReasonOpenFailed  = "OPEN_FAILED"
)

// Connector가 PREVIEW_OPEN_RESULT FAILED의 error.code에 쓰는 값이다(preview-control.schema.json).
const (
	codeAppNotRunning = "APP_NOT_RUNNING"
	codePortRejected  = "PORT_REJECTED"
	codeVMUnreachable = "VM_UNREACHABLE"
)

// Store는 Service가 사용하는 persistence 경계다. postgres.Store가 구현한다.
type Store interface {
	repository.Transactor
	// LabInstanceByID는 LabInstance를 잠그지 않고 읽는다. PreviewSession을 돌려주기 전에 대상이 그대로인지 확인하는 데 쓴다.
	LabInstanceByID(ctx context.Context, id uuid.UUID) (repository.LabInstance, error)
}

// Connectors는 Connector Control connection으로 PreviewSession lifecycle message를 보내는 경계다. *connector.Router가 구현한다.
type Connectors interface {
	SendPreviewOpen(ctx context.Context, open connector.PreviewOpen) (connector.SentMessage, error)
	SendPreviewClose(ctx context.Context, cl connector.PreviewClose) (connector.SentMessage, error)
	ForgetPreviewOpen(connectorID uuid.UUID, previewSessionID string) bool
}

// Gateway는 Service가 사용하는 Preview Gateway 경계다. *preview.Gateway가 구현한다.
type Gateway interface {
	Expect(preview.Expected) error
	Bind(id string, b preview.Binding) error
	Pending(id string) (attached, ended <-chan struct{}, ok bool)
	PrepareTunnel(id string, openMessageID string) (<-chan struct{}, error)
	CancelTunnel(id string, openMessageID string)
	Activate(id string) (preview.Activation, error)
	Forget(id string)
	Terminate(id string, end preview.End) bool
	Info(id string) (preview.Info, bool)
	SessionsForLab(labInstanceID string) []string
	SetTunnelOpener(opener preview.TunnelOpener)
}

// Options는 Service 구성이다.
type Options struct {
	Store      Store
	Connectors Connectors
	Gateway    Gateway
	// Policy는 Backend가 명시적으로 설정한 허용 port 목록이다(LABBIT_PREVIEW_ALLOWED_PORTS). 비어 있으면 어떤 port도 승인하지 않는다.
	Policy Policy
	// TTL은 PreviewSession의 절대 TTL이다(LABBIT_PREVIEW_SESSION_TTL). 0보다 커야 한다. 기본값이 없다.
	TTL time.Duration
	// Clock이 nil이면 preview.SystemClock이다.
	Clock preview.Clock
	// Logger가 nil이면 로그를 남기지 않는다. 경로, query, Cookie, credential, 본문은 어떤 경우에도 기록하지 않는다.
	Logger *slog.Logger
	// OpenTimeout은 PREVIEW_OPEN을 보낸 뒤 Connector의 Preview Data attach를 기다리는 시간이다. 0이면 30초다.
	OpenTimeout time.Duration
}

// Service는 PreviewSession use case이며 preview.Lifecycle과 connector.PreviewSink를 구현한다.
type Service struct {
	store      Store
	connectors Connectors
	gateway    Gateway
	policy     Policy
	ttl        time.Duration
	clock      preview.Clock
	logger     *slog.Logger

	openTimeout time.Duration

	mu     sync.Mutex
	opens  map[string]chan openOutcome
	closed bool
	wg     sync.WaitGroup
}

var (
	_ preview.Lifecycle     = (*Service)(nil)
	_ connector.PreviewSink = (*Service)(nil)
	_ preview.TunnelOpener  = (*Service)(nil)
)

// NewService는 Service를 만든다. Gateway와 Service는 서로를 필요로 하므로(Gateway가 종료를 Service에 알리고 Service가 Gateway를 쓴다)
// 조립하는 쪽이 preview.Gateway.SetLifecycle로 순환을 끊는다(internal/server/app).
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Connectors == nil || opts.Gateway == nil {
		return nil, errors.New("previewsession: Store, Connectors, Gateway가 필요합니다")
	}
	if opts.TTL <= 0 {
		return nil, errors.New("previewsession: TTL은 0보다 커야 합니다")
	}
	if opts.OpenTimeout < 0 {
		return nil, errors.New("previewsession: OpenTimeout은 0 이상이어야 합니다")
	}
	s := &Service{
		store: opts.Store, connectors: opts.Connectors, gateway: opts.Gateway, policy: opts.Policy, ttl: opts.TTL,
		clock: opts.Clock, logger: opts.Logger, openTimeout: opts.OpenTimeout,
		opens: make(map[string]chan openOutcome),
	}
	if s.clock == nil {
		s.clock = preview.SystemClock{}
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.openTimeout == 0 {
		s.openTimeout = defaultOpenTimeout
	}
	opts.Gateway.SetTunnelOpener(s)
	return s, nil
}

// Shutdown은 새 PreviewSession 생성을 거절하고 진행 중인 생성이 끝나거나 ctx가 끝날 때까지 기다린다.
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

// begin은 생성 하나를 시작한다. 종료 뒤에는 false다. true이면 호출자가 s.wg.Done을 호출해야 한다.
func (s *Service) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	return true
}

// parseID는 이 서버가 발급하는 canonical UUID 문자열만 받아들인다. 하나의 리소스가 여러 ID 표기로 보이지 않게 한다.
func parseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.UUID{}, false
	}
	return id, true
}

// CreateInput은 Create의 입력이다. VM, Provider Server ID, Connector ID, generation은 받지 않는다.
type CreateInput struct {
	// LabInstanceID는 요청 경로의 값이다. 검증되지 않았다.
	LabInstanceID string
	// TargetPort는 요청 body의 값이다. 1~65535 정수 검증은 HTTP layer가 하지만 Service도 다시 확인한다.
	TargetPort int
	// RequestID는 원본 HTTP request의 correlation이다(선택).
	RequestID string
}

// Created는 만들어진 PreviewSession이다.
type Created struct {
	ID         string
	TargetPort int
	// PreviewURL은 Preview Origin의 bootstrap 경로이며 fragment에 일회용 bootstrap credential이 있다. 이 반환에서만 원문으로 나간다.
	PreviewURL string
	ExpiresAt  time.Time
}

// openOutcome은 PREVIEW_OPEN에 대한 Connector의 결과다.
type openOutcome struct {
	succeeded bool
	code      string
}

// target은 Create가 transaction 안에서 결정하고 승인한 값이다.
type target struct {
	lab         repository.LabInstance
	connectorID uuid.UUID
	vmKey       string
	providerID  string
	port        int
}

// Create는 현재 사용자의 Workspace VM application port에 PreviewSession을 만든다.
//
//  1. 현재 DB 상태에서 LabInstance 소유, 현재 ClassMembership, READY, Workspace VM, Connector를 결정하고 targetPort를 Backend 허용 목록에 대해
//     승인한다. 하나라도 맞지 않으면 side effect 없이 거절한다(Connector에 어떤 message도 보내지 않고 Gateway에도 등록하지 않는다).
//     transaction은 LabInstance를 FOR SHARE로 잠가 그 사이 generation이 바뀌지 않게 하며 외부 I/O 전에 끝낸다.
//  2. Gateway에 예상 correlation을 등록한 뒤 Connector에 PREVIEW_OPEN을 보낸다. PREVIEW_OPEN을 받을 Control Session에 PreviewSession을
//     Connector가 message를 받기 전에 묶는다.
//  3. Connector의 Preview Data WSS가 실제로 attach되어야 한다. PREVIEW_OPEN_RESULT SUCCEEDED라는 주장만 믿지 않는다.
//  4. 성공을 돌려주기 전에 LabInstance의 generation과 READY가 그대로인지 다시 확인하고 PreviewSession을 활성화한다.
//
// 2~4의 어떤 실패에서도 PreviewSession을 성공 상태로 남기지 않는다. Gateway 등록과 Router pending을 지우고 Connector가 열었을 수 있으면
// PREVIEW_CLOSE를 요청한다. 요청이 취소되어도 이 정리는 끝까지 수행한다.
func (s *Service) Create(ctx context.Context, user repository.User, in CreateInput) (Created, error) {
	if !s.begin() {
		return Created{}, ErrUnavailable
	}
	defer s.wg.Done()

	labID, ok := parseID(in.LabInstanceID)
	if !ok {
		return Created{}, ErrNotFound
	}
	if in.TargetPort < 1 || in.TargetPort > 65535 {
		return Created{}, ErrInvalidPort
	}

	resolved, err := s.resolve(ctx, user, labID, in.TargetPort)
	if err != nil {
		return Created{}, err
	}

	id := uuid.NewString()
	corr := connector.PreviewCorrelation{PreviewSessionID: id, LabInstanceID: resolved.lab.ID.String(), Generation: resolved.lab.Generation}
	log := withControlCorrelation(s.logger.With(
		"preview_session_id", id,
		"lab_instance_id", corr.LabInstanceID,
		"connector_id", resolved.connectorID.String(),
		"generation", corr.Generation,
	), in.RequestID, connector.TraceFromContext(ctx))

	// 정리는 요청 context가 취소되어도 끝까지 수행한다.
	abort := func(sentOpen bool, reason string) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		s.gateway.Forget(id)
		if sentOpen {
			s.sendClose(cleanupCtx, log, resolved.connectorID, corr, in.RequestID, reason)
		}
	}

	openMessageID := uuid.NewString()

	// Connector의 Data attach보다 먼저 예상 correlation을 등록한다.
	err = s.gateway.Expect(preview.Expected{
		SessionID: id, OwnerID: user.ID.String(), OrganizationID: user.OrganizationID.String(),
		ConnectorID: resolved.connectorID, LabInstanceID: corr.LabInstanceID, Generation: corr.Generation,
		TargetVMKey: resolved.vmKey, ProviderServerID: resolved.providerID, TargetPort: resolved.port,
		TTL: s.ttl, RequestID: in.RequestID, OpenMessageID: openMessageID,
	})
	switch {
	case err == nil:
	case errors.Is(err, preview.ErrClosed):
		return Created{}, ErrUnavailable
	default:
		log.Error("Gateway 등록 실패", "error_code", "GATEWAY_REGISTER_FAILED")
		return Created{}, fmt.Errorf("previewsession: Gateway 등록: %w", err)
	}
	attached, ended, ok := s.gateway.Pending(id)
	if !ok {
		abort(false, closeReasonOpenFailed)
		return Created{}, ErrOpenFailed
	}
	waiter := s.addOpenWaiter(id)
	defer s.removeOpenWaiter(id)

	_, err = s.connectors.SendPreviewOpen(ctx, connector.PreviewOpen{
		MessageID:        openMessageID,
		ConnectorID:      resolved.connectorID,
		RequestID:        in.RequestID,
		Correlation:      corr,
		TargetVMKey:      resolved.vmKey,
		ProviderServerID: resolved.providerID,
		TargetPort:       resolved.port,
		Trace:            connector.TraceFromContext(ctx),
		// PREVIEW_OPEN을 받을 Control Session에 PreviewSession을 묶는다. Connector가 message를 받기 전이므로 Data WSS가 붙는 순간에는 이미 있다.
		OnRoute: func(session connector.Session) error {
			return s.gateway.Bind(id, preview.Binding{
				ConnectorID: session.ConnectorID, ControlSessionID: session.ID, CredentialID: session.CredentialID,
			})
		},
	})
	switch {
	case err == nil:
	case errors.Is(err, connector.ErrCapabilityUnsupported):
		// Connector는 연결되어 있지만 preview-v1을 선언하지 않았다. PREVIEW_OPEN을 보내지 않았다. 버전으로 추론하지 않는다.
		log.Warn("PreviewSession 생성 실패", "reason", "capability_unsupported")
		abort(false, closeReasonOpenFailed)
		return Created{}, ErrTransportUnsupported
	case errors.Is(err, connector.ErrConnectorUnavailable):
		// 아무것도 쓰지 않았고 pending도 없다. Connector가 TCP forwarding을 열었을 수 없다.
		log.Warn("PreviewSession 생성 실패", "reason", "connector_unavailable")
		abort(false, closeReasonOpenFailed)
		return Created{}, ErrConnectorUnavailable
	case errors.Is(err, connector.ErrSendFailed):
		// 전송 여부가 불명확하다. Connector가 OPEN을 받았을 수 있으므로 CLOSE를 요청한다.
		log.Warn("PreviewSession 생성 실패", "reason", "open_send_failed")
		abort(true, closeReasonOpenFailed)
		return Created{}, ErrConnectorUnavailable
	case ctx.Err() != nil:
		abort(false, closeReasonCanceled)
		return Created{}, ctx.Err()
	case errors.Is(err, preview.ErrControlMismatch), errors.Is(err, preview.ErrSessionEnded), errors.Is(err, preview.ErrUnknownSession):
		// PreviewSession을 이 Control Session에 묶지 못했다. 아무것도 쓰지 않았다.
		log.Warn("PreviewSession 생성 실패", "reason", "control_not_bound")
		abort(false, closeReasonOpenFailed)
		return Created{}, ErrOpenFailed
	default:
		log.Error("PREVIEW_OPEN을 만들지 못함", "error_code", "INTERNAL_ERROR")
		abort(false, closeReasonOpenFailed)
		return Created{}, fmt.Errorf("previewsession: PREVIEW_OPEN 전송: %w", err)
	}
	defer s.connectors.ForgetPreviewOpen(resolved.connectorID, id)

	timedOut := make(chan struct{})
	timer := s.clock.AfterFunc(s.openTimeout, func() { close(timedOut) })
	defer timer.Stop()

waiting:
	for {
		select {
		case outcome := <-waiter:
			if outcome.succeeded {
				// SUCCEEDED는 통지일 뿐이다. 실제 Data WSS attach를 계속 기다린다.
				waiter = nil
				continue
			}
			// Connector가 이미 실패를 알렸다. 알고 있으므로 PREVIEW_CLOSE를 보내지 않는다.
			log.Warn("Connector PREVIEW_OPEN 실패 보고", "error_code", safeCode(outcome.code))
			abort(false, closeReasonOpenFailed)
			return Created{}, mapOpenFailure(outcome.code)
		case <-attached:
			break waiting
		case <-ended:
			// attach 전에 PreviewSession이 끝났다(Credential revoke, Control Session 교체, 서비스 종료 등).
			log.Warn("PreviewSession 생성 실패", "reason", "ended_before_attach")
			abort(true, closeReasonOpenFailed)
			return Created{}, ErrOpenFailed
		case <-timedOut:
			log.Warn("PreviewSession 생성 실패", "reason", "open_timeout")
			abort(true, closeReasonOpenTimeout)
			return Created{}, ErrOpenTimeout
		case <-ctx.Done():
			log.Warn("PreviewSession 생성 취소", "reason", "request_canceled")
			abort(true, closeReasonCanceled)
			return Created{}, ctx.Err()
		}
	}

	// 성공을 돌려주기 전에 대상이 그대로인지 다시 확인한다. Reset이 그 사이에 generation을 바꿨거나 LabInstance가 READY를 벗어났다면
	// 오래된 target을 성공으로 돌려주지 않는다.
	if err := s.stillCurrent(ctx, resolved); err != nil {
		reason := "reset_race"
		if !errors.Is(err, ErrTargetChanged) {
			reason = "recheck_failed"
		}
		log.Warn("PreviewSession 생성 실패", "reason", reason)
		abort(true, "LAB_RESET")
		return Created{}, err
	}
	activation, err := s.gateway.Activate(id)
	if err != nil {
		// attach한 tunnel이 그 사이 끝났다(revoke 등). 성공으로 남기지 않는다.
		log.Warn("PreviewSession 생성 실패", "reason", "activate_failed")
		abort(true, closeReasonOpenFailed)
		return Created{}, ErrOpenFailed
	}
	log.Info("PreviewSession 생성")
	return Created{ID: id, TargetPort: resolved.port, PreviewURL: activation.URL, ExpiresAt: activation.ExpiresAt}, nil
}

// resolve는 하나의 짧은 transaction에서 현재 DB 상태로 대상 Workspace VM과 Connector를 결정하고 targetPort를 승인한다.
// side effect는 없고 외부 I/O도 없다. LabInstance를 FOR SHARE로 잠가 읽는 동안 generation이 바뀌지 않게 하며 commit 뒤에는 잠금을 잡지 않는다.
func (s *Service) resolve(ctx context.Context, user repository.User, labID uuid.UUID, port int) (target, error) {
	var resolved target
	err := s.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		lab, err := repos.LabInstanceForShare(ctx, labID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("previewsession: LabInstance 조회: %w", err)
		}
		// 같은 Organization이고 LabInstance.user_id가 현재 사용자다. 강사나 Organization ADMIN도 다른 사용자의 Workspace를 Preview할 수 없다.
		if lab.OrganizationID != user.OrganizationID || lab.UserID != user.ID {
			return ErrForbidden
		}

		membership, err := repos.ClassMembership(ctx, lab.ClassID, user.ID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("previewsession: Membership 조회: %w", err)
		}
		if membership.ClassID != lab.ClassID || membership.UserID != user.ID ||
			membership.OrganizationID != lab.OrganizationID || !membership.Role.Valid() {
			return ErrInconsistentData
		}

		if lab.Status != LabInstanceStatusReady {
			return ErrLabInstanceNotReady
		}
		if lab.Generation < 1 {
			return fmt.Errorf("%w: generation이 1 미만", ErrInconsistentData)
		}

		// Browser가 아니라 immutable CreationSnapshot이 Workspace VM을 정한다. 최신 LabSpec을 다시 해석하지 않는다.
		snapshot, err := repos.CreationSnapshotTargets(ctx, lab.ID)
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("%w: CreationSnapshot 없음", ErrInconsistentData)
		}
		if err != nil {
			return fmt.Errorf("previewsession: CreationSnapshot 조회: %w", err)
		}
		vmKey, err := workspaceVMKey(snapshot)
		if err != nil {
			return err
		}

		connectorID, err := repos.ConnectorIDForLabInstance(ctx, lab.ID)
		if errors.Is(err, repository.ErrNotFound) {
			// CreationSnapshot → ProviderConnection → Connector 관계는 DB 제약상 있어야 한다.
			return fmt.Errorf("%w: Connector 관계 없음", ErrInconsistentData)
		}
		if err != nil {
			return fmt.Errorf("previewsession: Connector 조회: %w", err)
		}

		servers, err := repos.ProviderServers(ctx, lab.ID, lab.Generation, vmKey)
		if err != nil {
			return fmt.Errorf("previewsession: Workspace VM 조회: %w", err)
		}
		var present []repository.ProviderServer
		for _, server := range servers {
			if server.LifecycleStatus == ProviderResourcePresent {
				present = append(present, server)
			}
		}
		switch len(present) {
		case 0:
			// snapshot에는 있는 VM인데 현재 generation에 PRESENT SERVER ProviderResource가 없다(Reset 진행·Provider drift 등).
			// Browser가 잘못 보낸 값이 아니라 지금 사용할 수 없는 것이다.
			return ErrTargetUnavailable
		case 1:
		default:
			// 같은 generation에 같은 논리 이름의 PRESENT VM이 둘 이상이면 어느 쪽인지 알 수 없다. 임의로 고르지 않는다.
			return fmt.Errorf("%w: PRESENT Workspace VM이 둘 이상", ErrInconsistentData)
		}
		if present[0].ProviderID == "" {
			return fmt.Errorf("%w: Workspace VM의 Provider ID가 비어 있음", ErrInconsistentData)
		}

		// targetPort 승인은 권한과 대상 판정 뒤에 한다. Backend의 명시적 허용 목록에 있는 정확한 값만 승인하며 숫자 범위로 승인하지 않는다.
		if err := s.policy.Approve(port); err != nil {
			return err
		}

		resolved = target{lab: lab, connectorID: connectorID, vmKey: vmKey, providerID: present[0].ProviderID, port: port}
		return nil
	})
	if err != nil {
		return target{}, err
	}
	return resolved, nil
}

// workspaceVMKey는 CreationSnapshot에서 Workspace VM key를 읽는다. 저장된 값을 신뢰하지 않는다.
// connector.schema.json의 CreationSnapshot처럼 workspaceVmKey는 비어 있지 않고 vms 중 정확히 하나를 가리켜야 한다.
// 해석할 수 없거나 모순이면 값을 만들어 채우지 않고 ErrInconsistentData로 fail closed한다. 오류 문구에는 값을 넣지 않는다.
func workspaceVMKey(snapshot repository.CreationSnapshotTargets) (string, error) {
	if snapshot.WorkspaceVMKey == "" {
		return "", fmt.Errorf("%w: CreationSnapshot에 workspaceVmKey가 없음", ErrInconsistentData)
	}
	matches := 0
	for _, vm := range snapshot.VMs {
		if vm.VMKey == snapshot.WorkspaceVMKey {
			matches++
		}
	}
	switch matches {
	case 0:
		return "", fmt.Errorf("%w: workspaceVmKey가 vms에 없음", ErrInconsistentData)
	case 1:
		return snapshot.WorkspaceVMKey, nil
	default:
		return "", fmt.Errorf("%w: workspaceVmKey가 vms에 중복됨", ErrInconsistentData)
	}
}

// stillCurrent는 Connector와의 교환이 끝난 뒤 target을 결정한 시점의 LabInstance가 그대로인지 확인한다. Reset이 그 사이에 generation을
// 바꿨거나 LabInstance가 READY를 벗어났다면 결과를 성공으로 처리하지 않는다. 짧은 단일 조회이며 분산 lock을 쓰지 않는다.
func (s *Service) stillCurrent(ctx context.Context, t target) error {
	lab, err := s.store.LabInstanceByID(ctx, t.lab.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrTargetChanged
	}
	if err != nil {
		return fmt.Errorf("previewsession: LabInstance 재확인: %w", err)
	}
	if lab.Generation != t.lab.Generation || lab.Status != LabInstanceStatusReady {
		s.logger.Warn("PreviewSession 처리 중 대상이 바뀜",
			"lab_instance_id", t.lab.ID.String(), "generation", t.lab.Generation, "error_code", "WORKSPACE_TARGET_CHANGED")
		return ErrTargetChanged
	}
	return nil
}

// mapOpenFailure는 Connector가 FAILED 결과에 실은 error.code를 application error로 바꾼다. 알 수 없는 code는 일반 열기 실패로 취급한다.
// 오류 문구(message)는 사용하지 않는다.
func mapOpenFailure(code string) error {
	switch code {
	case codeAppNotRunning:
		return ErrAppNotRunning
	case codePortRejected:
		return ErrPortRejected
	case codeVMUnreachable:
		return ErrTargetUnreachable
	default:
		return ErrOpenFailed
	}
}

// safeCode는 Connector가 보낸 오류 code를 log에 남길 수 있는 형태로 제한한다. 알려진 code만 그대로 남기고 나머지는 고정 값이다.
func safeCode(code string) string {
	switch code {
	case codeAppNotRunning, codePortRejected, codeVMUnreachable, "UNAVAILABLE", "INTERNAL_ERROR":
		return code
	case "":
		return "NONE"
	default:
		return "UNKNOWN"
	}
}

// sendClose는 PREVIEW_CLOSE를 요청한다. Connector를 사용할 수 없어도 PreviewSession 종료는 유지하므로 실패는 log만 남긴다.
func (s *Service) sendClose(ctx context.Context, log *slog.Logger, connectorID uuid.UUID, corr connector.PreviewCorrelation, requestID, reason string) {
	_, err := s.connectors.SendPreviewClose(ctx, connector.PreviewClose{
		ConnectorID: connectorID, RequestID: requestID, Correlation: corr, Reason: reason, Trace: connector.TraceFromContext(ctx),
	})
	if err != nil {
		log.Warn("Connector PREVIEW_CLOSE 전달 못 함", "reason", closeFailureReason(err))
	}
}

func closeFailureReason(err error) string {
	switch {
	case errors.Is(err, connector.ErrCapabilityUnsupported):
		return "capability_unsupported"
	case errors.Is(err, connector.ErrConnectorUnavailable):
		return "connector_unavailable"
	case errors.Is(err, connector.ErrSendFailed):
		return "send_failed"
	default:
		return "invalid_close"
	}
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

// addOpenWaiter는 PREVIEW_OPEN_RESULT를 기다리는 채널을 등록한다. OPEN을 보내기 전에 등록해 즉시 온 응답을 놓치지 않는다.
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

// HandlePreviewEvent는 connector.PreviewSink의 구현이다. Router가 Connector Session의 read loop fence 안에서 호출하므로 짧게 반환한다.
func (s *Service) HandlePreviewEvent(event connector.PreviewEvent) {
	switch e := event.(type) {
	case connector.PreviewOpenResultEvent:
		// Router가 인증된 ConnectorID, previewSessionId, labInstanceId, generation, replyToMessageId를 모두 대조했다.
		log := withControlCorrelation(s.logger.With(
			"connector_id", e.ConnectorID.String(),
			"preview_session_id", e.Correlation.PreviewSessionID,
			"lab_instance_id", e.Correlation.LabInstanceID,
			"generation", e.Correlation.Generation,
		), e.RequestID, e.Trace)
		outcome := openOutcome{succeeded: e.Payload.Outcome == connector.PreviewOutcomeSucceeded}
		if e.Payload.Error != nil {
			outcome.code = e.Payload.Error.Code
		}
		if !s.notifyOpen(e.Correlation.PreviewSessionID, outcome) {
			log.Debug("기다리는 Create가 없는 PREVIEW_OPEN_RESULT")
		}
	case connector.PreviewUnmatchedEvent:
		// Router가 이미 기록했다. 어떤 PreviewSession에도 연결하지 않는다.
	}
}

// SessionEnded는 preview.Lifecycle의 구현이다. Gateway가 활성화된 PreviewSession의 종료를 알린다. SaaS가 시작한 종료(명시적 종료, 만료,
// Reset/Cleanup, 서비스 재시작, 프로토콜 위반)에서만 Connector에 PREVIEW_CLOSE를 보낸다. Connector가 이미 아는 종료(tunnel 종료)나 Control이
// 끊겨 보낼 수 없는 종료(Credential revoke, Control Session 교체)는 보내지 않는다. Connector는 Data WSS 종료로도 정리한다.
func (s *Service) SessionEnded(e preview.Ended) {
	log := withControlCorrelation(s.logger.With(
		"preview_session_id", e.SessionID,
		"lab_instance_id", e.LabInstanceID,
		"connector_id", e.ConnectorID.String(),
		"generation", e.Generation,
		"reason", e.Reason,
	), e.RequestID, connector.TraceContext{})
	if !e.NotifyConnector {
		log.Debug("PreviewSession 종료(Connector에 알리지 않음)")
		return
	}
	// 종료를 일으킨 요청이나 timer와 무관하게 짧은 시간 안에 끝낸다.
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	s.sendClose(ctx, log, e.ConnectorID, connector.PreviewCorrelation{
		PreviewSessionID: e.SessionID, LabInstanceID: e.LabInstanceID, Generation: e.Generation,
	}, e.RequestID, e.Reason)
}

// Close는 사용자의 명시적 종료다. 즉시 Preview Cookie 인증이 무효가 되고 tunnel이 닫힌다. 이미 종료되었거나 만료된 PreviewSession은
// 성공한다(멱등). 소유자가 아니면 ErrForbidden, 서버가 기억하지 못하는 ID면 ErrNotFound다.
func (s *Service) Close(_ context.Context, user repository.User, previewSessionID string) error {
	if _, ok := parseID(previewSessionID); !ok {
		return ErrNotFound
	}
	info, ok := s.gateway.Info(previewSessionID)
	if !ok {
		return ErrNotFound
	}
	if info.OrganizationID != user.OrganizationID.String() || info.OwnerID != user.ID.String() {
		return ErrForbidden
	}
	if info.Ended {
		return nil
	}
	// Connector에 PREVIEW_CLOSE를 보내는 일은 Gateway가 알리는 Lifecycle(SessionEnded)이 한다.
	s.gateway.Terminate(previewSessionID, preview.End{Reason: preview.EndSessionClosed, NotifyConnector: true})
	return nil
}

// LabMutation은 Reset/Cleanup 같은 Lab mutation이 LabInstance의 PreviewSession을 종료하라고 요청하는 입력이다.
type LabMutation struct {
	LabInstanceID uuid.UUID
	// Reason은 preview.EndLabReset 또는 preview.EndLabCleanup이다.
	Reason string
}

// CloseForLabMutation은 LabInstance의 끝나지 않은 모든 PreviewSession을 Reset/Cleanup 사유로 종료한다. Reset/Cleanup 자체는 이 package가
// 구현하지 않으며 Operation 처리 쪽이 호출할 경계만 제공한다. 종료한 PreviewSession 수를 반환한다.
func (s *Service) CloseForLabMutation(_ context.Context, m LabMutation) (int, error) {
	if m.Reason != preview.EndLabReset && m.Reason != preview.EndLabCleanup {
		return 0, ErrInvalidReason
	}
	closed := 0
	for _, id := range s.gateway.SessionsForLab(m.LabInstanceID.String()) {
		if s.gateway.Terminate(id, preview.End{Reason: m.Reason, NotifyConnector: true}) {
			closed++
		}
	}
	return closed, nil
}

// OpenTunnel은 preview.TunnelOpener의 구현이다. 활성화된 PreviewSession의 후속 Workspace TCP tunnel을 준비한다.
func (s *Service) OpenTunnel(ctx context.Context, sessionID string) error {
	if !s.begin() {
		return ErrUnavailable
	}
	defer s.wg.Done()

	info, ok := s.gateway.Info(sessionID)
	if !ok || info.Ended {
		return ErrNotFound
	}
	now := s.clock.Now()
	if !info.ExpiresAt.IsZero() && !now.Before(info.ExpiresAt) {
		return ErrNotFound
	}

	labID, ok := parseID(info.LabInstanceID)
	if !ok {
		return ErrNotFound
	}

	// 대상 LabInstance가 여전히 같은 generation이고 READY인지 재확인한다.
	lab, err := s.store.LabInstanceByID(ctx, labID)
	if errors.Is(err, repository.ErrNotFound) {
		s.gateway.Terminate(sessionID, preview.End{Reason: preview.EndLabReset, NotifyConnector: true})
		return ErrTargetChanged
	}
	if err != nil {
		return fmt.Errorf("previewsession: LabInstance 재확인: %w", err)
	}
	if lab.Generation != info.Generation || lab.Status != LabInstanceStatusReady {
		s.logger.Warn("새 tunnel 준비 중 대상이 바뀜",
			"lab_instance_id", lab.ID.String(), "generation", lab.Generation, "error_code", "WORKSPACE_TARGET_CHANGED")
		s.gateway.Terminate(sessionID, preview.End{Reason: preview.EndLabReset, NotifyConnector: true})
		return ErrTargetChanged
	}

	openMessageID := uuid.NewString()
	readyCh, err := s.gateway.PrepareTunnel(sessionID, openMessageID)
	if err != nil {
		return err
	}

	corr := connector.PreviewCorrelation{
		PreviewSessionID: sessionID,
		LabInstanceID:    info.LabInstanceID,
		Generation:       info.Generation,
	}
	log := withControlCorrelation(s.logger.With(
		"preview_session_id", sessionID,
		"lab_instance_id", corr.LabInstanceID,
		"connector_id", info.ConnectorID.String(),
		"generation", corr.Generation,
		"open_message_id", openMessageID,
	), "", connector.TraceFromContext(ctx))

	waiter := s.addOpenWaiter(sessionID)
	defer s.removeOpenWaiter(sessionID)

	_, err = s.connectors.SendPreviewOpen(ctx, connector.PreviewOpen{
		MessageID:        openMessageID,
		ConnectorID:      info.ConnectorID,
		Correlation:      corr,
		TargetVMKey:      info.TargetVMKey,
		ProviderServerID: info.ProviderServerID,
		TargetPort:       info.TargetPort,
		Trace:            connector.TraceFromContext(ctx),
		OnRoute: func(session connector.Session) error {
			return s.gateway.Bind(sessionID, preview.Binding{
				ConnectorID:      session.ConnectorID,
				ControlSessionID: session.ID,
				CredentialID:     session.CredentialID,
			})
		},
	})
	if err != nil {
		s.gateway.CancelTunnel(sessionID, openMessageID)
		switch {
		case errors.Is(err, connector.ErrCapabilityUnsupported):
			log.Warn("tunnel 준비 실패", "reason", "capability_unsupported")
			return ErrTransportUnsupported
		case errors.Is(err, connector.ErrConnectorUnavailable), errors.Is(err, connector.ErrSendFailed):
			log.Warn("tunnel 준비 실패", "reason", "connector_unavailable")
			return ErrConnectorUnavailable
		case errors.Is(err, preview.ErrControlMismatch), errors.Is(err, preview.ErrSessionEnded), errors.Is(err, preview.ErrUnknownSession):
			log.Warn("tunnel 준비 실패", "reason", "control_not_bound")
			return ErrOpenFailed
		default:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Error("PREVIEW_OPEN 전송 실패", "error_code", "INTERNAL_ERROR")
			return fmt.Errorf("previewsession: PREVIEW_OPEN 전송: %w", err)
		}
	}
	defer s.connectors.ForgetPreviewOpen(info.ConnectorID, sessionID)

	timedOut := make(chan struct{})
	timer := s.clock.AfterFunc(s.openTimeout, func() { close(timedOut) })
	defer timer.Stop()

	for {
		select {
		case outcome := <-waiter:
			if outcome.succeeded {
				continue
			}
			s.gateway.CancelTunnel(sessionID, openMessageID)
			log.Warn("Connector PREVIEW_OPEN 실패 보고", "error_code", safeCode(outcome.code))
			return mapOpenFailure(outcome.code)
		case <-readyCh:
			return nil
		case <-timedOut:
			s.gateway.CancelTunnel(sessionID, openMessageID)
			log.Warn("tunnel 준비 시간 초과", "reason", "open_timeout")
			return ErrOpenTimeout
		case <-ctx.Done():
			s.gateway.CancelTunnel(sessionID, openMessageID)
			log.Warn("tunnel 준비 취소", "reason", "request_canceled")
			return ctx.Err()
		}
	}
}
