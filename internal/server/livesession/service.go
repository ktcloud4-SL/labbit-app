package livesession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// Store는 LiveSession use case가 필요로 하는 DB persistence 경계다.
type Store interface {
	repository.ClassRepository
	repository.LiveSessionRepository
	TerminalSessionByID(ctx context.Context, id uuid.UUID) (repository.TerminalSession, error)
	LabInstanceByID(ctx context.Context, id uuid.UUID) (repository.LabInstance, error)
}

// Clock은 시각을 주입하기 위한 경계다.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Authenticator는 학생의 Browser 세션 쿠키를 검증하는 경계다.
type Authenticator interface {
	Authenticate(ctx context.Context, token auth.SessionToken) (auth.Principal, error)
}

// LiveRelay는 실시간 WebSocket Live 세션 관리를 수행하는 Relay 경계다.
type LiveRelay interface {
	RegisterLive(liveSessionID string, sourceTerminalSessionID string, classID string) error
	TerminateLive(liveSessionID string, end realtime.End)
	SourceUsable(sourceTerminalSessionID string) bool
}

// Options는 Service 구성 옵션이다.
type Options struct {
	Store  Store
	Auth   Authenticator
	Relay  LiveRelay
	Clock  Clock
	Logger *slog.Logger
}

// Service는 LiveSession의 생성·조회·종료 및 WSS 권한 검증을 담당하는 use case다.
// realtime.LiveControl을 구현한다.
type Service struct {
	store  Store
	auth   Authenticator
	relay  LiveRelay
	clock  Clock
	logger *slog.Logger
}

// New는 LiveSession Service를 초기화한다.
func New(opts Options) *Service {
	clk := opts.Clock
	if clk == nil {
		clk = systemClock{}
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		store:  opts.Store,
		auth:   opts.Auth,
		relay:  opts.Relay,
		clock:  clk,
		logger: log,
	}
}

// Create는 강사의 source TerminalSession을 기반으로 새 LiveSession을 생성한다.
// - 강사 본인의 활성 TerminalSession 및 LabInstance여야 함
// - 해당 Class에서 INSTRUCTOR Membership을 가져야 함
// - Class당 active LiveSession은 최대 1개여야 함
func (s *Service) Create(ctx context.Context, user repository.User, sourceTerminalSessionID string) (repository.LiveSession, error) {
	termID, err := uuid.Parse(sourceTerminalSessionID)
	if err != nil {
		return repository.LiveSession{}, ErrNotFound
	}

	term, err := s.store.TerminalSessionByID(ctx, termID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, ErrNotFound
	}
	if err != nil {
		return repository.LiveSession{}, fmt.Errorf("terminal session 조회: %w", err)
	}

	if term.OrganizationID != user.OrganizationID || term.UserID != user.ID {
		return repository.LiveSession{}, ErrForbidden
	}

	if term.Status == repository.TerminalSessionEnded || term.Status == repository.TerminalSessionOpening {
		return repository.LiveSession{}, ErrSourceUnavailable
	}

	lab, err := s.store.LabInstanceByID(ctx, term.LabInstanceID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, ErrInconsistentData
	}
	if err != nil {
		return repository.LiveSession{}, fmt.Errorf("lab instance 조회: %w", err)
	}

	if lab.OrganizationID != user.OrganizationID || lab.UserID != user.ID {
		return repository.LiveSession{}, ErrForbidden
	}
	if lab.Generation != term.Generation || lab.Status != "READY" {
		return repository.LiveSession{}, ErrSourceUnavailable
	}

	membership, err := s.store.ClassMembership(ctx, lab.ClassID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, ErrForbidden
	}
	if err != nil {
		return repository.LiveSession{}, fmt.Errorf("class membership 조회: %w", err)
	}
	if membership.OrganizationID != user.OrganizationID || membership.Role != repository.ClassRoleInstructor {
		return repository.LiveSession{}, ErrForbidden
	}

	if s.relay == nil {
		return repository.LiveSession{}, ErrUnavailable
	}
	if !s.relay.SourceUsable(sourceTerminalSessionID) {
		return repository.LiveSession{}, ErrSourceUnavailable
	}

	// Class당 active LiveSession 존재 여부 확인
	_, err = s.store.ActiveLiveSessionByClass(ctx, lab.ClassID)
	if err == nil {
		return repository.LiveSession{}, ErrLiveActive
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, fmt.Errorf("active live session 확인: %w", err)
	}

	now := s.clock.Now()
	newLive := repository.NewLiveSession{
		ID:                      uuid.New(),
		OrganizationID:          user.OrganizationID,
		ClassID:                 lab.ClassID,
		SourceTerminalSessionID: term.ID,
		InstructorUserID:        user.ID,
		CreatedAt:               now,
	}
	if err := s.store.CreateLiveSession(ctx, newLive); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return repository.LiveSession{}, ErrLiveActive
		}
		return repository.LiveSession{}, fmt.Errorf("create live session: %w", err)
	}

	if err := s.relay.RegisterLive(newLive.ID.String(), term.ID.String(), lab.ClassID.String()); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.store.EndLiveSession(cleanupCtx, newLive.ID, s.clock.Now(), "SOURCE_UNAVAILABLE")
		return repository.LiveSession{}, ErrSourceUnavailable
	}

	return repository.LiveSession{
		ID:                      newLive.ID,
		OrganizationID:          newLive.OrganizationID,
		ClassID:                 newLive.ClassID,
		SourceTerminalSessionID: newLive.SourceTerminalSessionID,
		InstructorUserID:        newLive.InstructorUserID,
		CreatedAt:               newLive.CreatedAt,
	}, nil
}

// GetActive는 Class에 활성화된 LiveSession을 반환한다.
// 요청자는 해당 Class의 INSTRUCTOR 또는 STUDENT Membership이 있어야 한다.
func (s *Service) GetActive(ctx context.Context, user repository.User, classID string) (repository.LiveSession, error) {
	cID, err := uuid.Parse(classID)
	if err != nil {
		return repository.LiveSession{}, ErrNotFound
	}

	membership, err := s.store.ClassMembership(ctx, cID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, ErrForbidden
	}
	if err != nil {
		return repository.LiveSession{}, fmt.Errorf("class membership 조회: %w", err)
	}
	if membership.OrganizationID != user.OrganizationID || !membership.Role.Valid() {
		return repository.LiveSession{}, ErrForbidden
	}

	active, err := s.store.ActiveLiveSessionByClass(ctx, cID)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.LiveSession{}, ErrNotFound
	}
	if err != nil {
		return repository.LiveSession{}, fmt.Errorf("active live session 조회: %w", err)
	}

	return active, nil
}

// Close는 강사가 LiveSession을 명시적으로 종료한다.
// source TerminalSession은 종료되지 않고 유지된다.
func (s *Service) Close(ctx context.Context, user repository.User, liveSessionID string) error {
	id, err := uuid.Parse(liveSessionID)
	if err != nil {
		return ErrNotFound
	}

	live, err := s.store.LiveSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("live session 조회: %w", err)
	}

	if live.OrganizationID != user.OrganizationID || live.InstructorUserID != user.ID {
		return ErrForbidden
	}

	membership, err := s.store.ClassMembership(ctx, live.ClassID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("class membership 조회: %w", err)
	}
	if membership.Role != repository.ClassRoleInstructor {
		return ErrForbidden
	}

	if live.EndedAt != nil || live.EndReason != "" {
		return nil
	}

	now := s.clock.Now()
	if _, err := s.store.EndLiveSession(ctx, live.ID, now, "SESSION_CLOSED"); err != nil {
		return fmt.Errorf("end live session: %w", err)
	}

	if s.relay != nil {
		s.relay.TerminateLive(live.ID.String(), realtime.End{Reason: realtime.EndReasonSessionClosed})
	}

	return nil
}

// AuthorizeLiveSubscribe는 realtime.LiveControl 구현으로 학생의 Live 구독 권한을 검증한다.
func (s *Service) AuthorizeLiveSubscribe(ctx context.Context, session realtime.SessionToken, liveSessionID string) (realtime.LiveGrant, error) {
	id, err := uuid.Parse(liveSessionID)
	if err != nil {
		return realtime.LiveGrant{}, realtime.ErrSessionNotFound
	}

	if s.auth == nil {
		return realtime.LiveGrant{}, realtime.ErrDependencyUnavailable
	}

	principal, err := s.auth.Authenticate(ctx, auth.SessionToken(session))
	if errors.Is(err, auth.ErrUnauthenticated) {
		return realtime.LiveGrant{}, realtime.ErrUnauthenticated
	}
	if err != nil {
		return realtime.LiveGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}

	user := principal.User

	live, err := s.store.LiveSessionByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.LiveGrant{}, realtime.ErrSessionNotFound
	}
	if err != nil {
		return realtime.LiveGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}

	if live.EndedAt != nil || live.EndReason != "" {
		return realtime.LiveGrant{}, realtime.ErrSessionEnded
	}

	if live.OrganizationID != user.OrganizationID {
		return realtime.LiveGrant{}, realtime.ErrForbidden
	}

	membership, err := s.store.ClassMembership(ctx, live.ClassID, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.LiveGrant{}, realtime.ErrForbidden
	}
	if err != nil {
		return realtime.LiveGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if membership.Role != repository.ClassRoleStudent {
		return realtime.LiveGrant{}, realtime.ErrForbidden
	}

	term, err := s.store.TerminalSessionByID(ctx, live.SourceTerminalSessionID)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.LiveGrant{}, realtime.ErrSessionNotFound
	}
	if err != nil {
		return realtime.LiveGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if term.Status == repository.TerminalSessionEnded {
		return realtime.LiveGrant{}, realtime.ErrSessionEnded
	}

	lab, err := s.store.LabInstanceByID(ctx, term.LabInstanceID)
	if errors.Is(err, repository.ErrNotFound) {
		return realtime.LiveGrant{}, realtime.ErrSessionNotFound
	}
	if err != nil {
		return realtime.LiveGrant{}, fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	if lab.Generation != term.Generation {
		return realtime.LiveGrant{}, realtime.ErrLabMutation
	}

	return realtime.LiveGrant{
		LiveSessionID:           live.ID.String(),
		SourceTerminalSessionID: live.SourceTerminalSessionID.String(),
		ClassID:                 live.ClassID.String(),
	}, nil
}

// SourceTerminalEnded는 realtime.LiveControl 구현으로 source TerminalSession 종료 시 연계된 LiveSession을 종료한다.
func (s *Service) SourceTerminalEnded(ctx context.Context, sourceTerminalSessionID string, end realtime.End) error {
	id, err := uuid.Parse(sourceTerminalSessionID)
	if err != nil {
		return nil
	}
	_, err = s.store.EndActiveLiveSessionBySourceTerminal(ctx, id, s.clock.Now(), end.Reason)
	if err != nil {
		return fmt.Errorf("%w: %w", realtime.ErrDependencyUnavailable, err)
	}
	return nil
}
