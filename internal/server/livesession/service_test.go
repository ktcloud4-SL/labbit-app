package livesession_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/livesession"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type fakeStore struct {
	mu           sync.Mutex
	classes      map[uuid.UUID]repository.Class
	memberships  map[[2]uuid.UUID]repository.ClassMembership
	terminals    map[uuid.UUID]repository.TerminalSession
	labInstances map[uuid.UUID]repository.LabInstance
	liveSessions map[uuid.UUID]repository.LiveSession

	createLiveErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		classes:      make(map[uuid.UUID]repository.Class),
		memberships:  make(map[[2]uuid.UUID]repository.ClassMembership),
		terminals:    make(map[uuid.UUID]repository.TerminalSession),
		labInstances: make(map[uuid.UUID]repository.LabInstance),
		liveSessions: make(map[uuid.UUID]repository.LiveSession),
	}
}

func (s *fakeStore) ClassByID(_ context.Context, id uuid.UUID) (repository.Class, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.classes[id]
	if !ok {
		return repository.Class{}, repository.ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) ClassMembership(_ context.Context, classID, userID uuid.UUID) (repository.ClassMembership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.memberships[[2]uuid.UUID{classID, userID}]
	if !ok {
		return repository.ClassMembership{}, repository.ErrNotFound
	}
	return m, nil
}

func (s *fakeStore) ClassesByUser(_ context.Context, _ uuid.UUID) ([]repository.ClassWithRole, error) {
	return nil, nil
}

func (s *fakeStore) CreateClassMembership(_ context.Context, _ repository.NewClassMembership) error {
	return nil
}

func (s *fakeStore) TerminalSessionByID(_ context.Context, id uuid.UUID) (repository.TerminalSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.terminals[id]
	if !ok {
		return repository.TerminalSession{}, repository.ErrNotFound
	}
	return t, nil
}

func (s *fakeStore) LabInstanceByID(_ context.Context, id uuid.UUID) (repository.LabInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.labInstances[id]
	if !ok {
		return repository.LabInstance{}, repository.ErrNotFound
	}
	return l, nil
}

func (s *fakeStore) LiveSessionByID(_ context.Context, id uuid.UUID) (repository.LiveSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.liveSessions[id]
	if !ok {
		return repository.LiveSession{}, repository.ErrNotFound
	}
	return l, nil
}

func (s *fakeStore) ActiveLiveSessionByClass(_ context.Context, classID uuid.UUID) (repository.LiveSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.liveSessions {
		if l.ClassID == classID && l.EndedAt == nil {
			return l, nil
		}
	}
	return repository.LiveSession{}, repository.ErrNotFound
}

func (s *fakeStore) CreateLiveSession(_ context.Context, session repository.NewLiveSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createLiveErr != nil {
		return s.createLiveErr
	}
	// unique per class active
	for _, l := range s.liveSessions {
		if l.ClassID == session.ClassID && l.EndedAt == nil {
			return repository.ErrConflict
		}
	}
	s.liveSessions[session.ID] = repository.LiveSession{
		ID:                      session.ID,
		OrganizationID:          session.OrganizationID,
		ClassID:                 session.ClassID,
		SourceTerminalSessionID: session.SourceTerminalSessionID,
		InstructorUserID:        session.InstructorUserID,
		CreatedAt:               session.CreatedAt,
	}
	return nil
}

func (s *fakeStore) EndLiveSession(_ context.Context, id uuid.UUID, endedAt time.Time, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.liveSessions[id]
	if !ok || l.EndedAt != nil {
		return false, nil
	}
	l.EndedAt = &endedAt
	l.EndReason = reason
	s.liveSessions[id] = l
	return true, nil
}

func (s *fakeStore) EndActiveLiveSessionBySourceTerminal(_ context.Context, sourceTerminalSessionID uuid.UUID, endedAt time.Time, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var updated bool
	for k, l := range s.liveSessions {
		if l.SourceTerminalSessionID == sourceTerminalSessionID && l.EndedAt == nil {
			l.EndedAt = &endedAt
			l.EndReason = reason
			s.liveSessions[k] = l
			updated = true
		}
	}
	return updated, nil
}

type fakeAuthenticator struct {
	principals map[string]auth.Principal
}

func (a *fakeAuthenticator) Authenticate(_ context.Context, token auth.SessionToken) (auth.Principal, error) {
	p, ok := a.principals[string(token)]
	if !ok {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return p, nil
}

type fakeLiveRelay struct {
	mu           sync.Mutex
	registered   map[string]string // liveID -> sourceID
	terminated   map[string]realtime.End
	usableSource map[string]bool
	regErr       error
}

func newFakeLiveRelay() *fakeLiveRelay {
	return &fakeLiveRelay{
		registered:   make(map[string]string),
		terminated:   make(map[string]realtime.End),
		usableSource: make(map[string]bool),
	}
}

func (r *fakeLiveRelay) RegisterLive(liveSessionID string, sourceTerminalSessionID string, classID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.regErr != nil {
		return r.regErr
	}
	r.registered[liveSessionID] = sourceTerminalSessionID
	return nil
}

func (r *fakeLiveRelay) TerminateLive(liveSessionID string, end realtime.End) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.terminated[liveSessionID] = end
}

func (r *fakeLiveRelay) SourceUsable(sourceTerminalSessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usableSource[sourceTerminalSessionID]
}

type testClock struct {
	now time.Time
}

func (c testClock) Now() time.Time {
	return c.now
}

func TestLiveSessionCreate(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	studentID := uuid.New()
	classID := uuid.New()
	labID := uuid.New()
	termID := uuid.New()

	baseSetup := func() (*fakeStore, *fakeLiveRelay, *livesession.Service, repository.User) {
		store := newFakeStore()
		relay := newFakeLiveRelay()
		now := time.Now().Truncate(time.Millisecond)

		instructor := repository.User{
			ID:             instructorID,
			OrganizationID: orgID,
		}

		store.classes[classID] = repository.Class{ID: classID, OrganizationID: orgID, Name: "Cloud Lab"}
		store.memberships[[2]uuid.UUID{classID, instructorID}] = repository.ClassMembership{
			OrganizationID: orgID, ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor,
		}
		store.memberships[[2]uuid.UUID{classID, studentID}] = repository.ClassMembership{
			OrganizationID: orgID, ClassID: classID, UserID: studentID, Role: repository.ClassRoleStudent,
		}
		store.labInstances[labID] = repository.LabInstance{
			ID: labID, OrganizationID: orgID, UserID: instructorID, ClassID: classID, Generation: 1, Status: "READY",
		}
		store.terminals[termID] = repository.TerminalSession{
			ID: termID, OrganizationID: orgID, UserID: instructorID, LabInstanceID: labID, Generation: 1, Status: repository.TerminalSessionActive,
		}
		relay.usableSource[termID.String()] = true

		svc := livesession.New(livesession.Options{
			Store: store,
			Relay: relay,
			Clock: testClock{now: now},
		})
		return store, relay, svc, instructor
	}

	t.Run("성공: 강사 본인의 활성 터미널로 LiveSession 생성", func(t *testing.T) {
		store, relay, svc, instructor := baseSetup()
		live, err := svc.Create(ctx, instructor, termID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if live.ClassID != classID || live.SourceTerminalSessionID != termID || live.InstructorUserID != instructorID {
			t.Fatalf("unexpected live session payload: %+v", live)
		}
		if relay.registered[live.ID.String()] != termID.String() {
			t.Fatalf("relay not registered")
		}
		// DB 확인
		stored, err := store.ActiveLiveSessionByClass(ctx, classID)
		if err != nil {
			t.Fatalf("failed to query active live session: %v", err)
		}
		if stored.ID != live.ID {
			t.Fatalf("stored ID mismatch")
		}
	})

	t.Run("실패: 다른 사용자의 TerminalSession", func(t *testing.T) {
		_, _, svc, _ := baseSetup()
		otherUser := repository.User{ID: uuid.New(), OrganizationID: orgID}
		_, err := svc.Create(ctx, otherUser, termID.String())
		if !errors.Is(err, livesession.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: 다른 조직의 TerminalSession", func(t *testing.T) {
		_, _, svc, instructor := baseSetup()
		otherOrgInstructor := instructor
		otherOrgInstructor.OrganizationID = uuid.New()
		_, err := svc.Create(ctx, otherOrgInstructor, termID.String())
		if !errors.Is(err, livesession.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: TerminalSession이 ENDED 상태", func(t *testing.T) {
		store, _, svc, instructor := baseSetup()
		term := store.terminals[termID]
		term.Status = repository.TerminalSessionEnded
		store.terminals[termID] = term

		_, err := svc.Create(ctx, instructor, termID.String())
		if !errors.Is(err, livesession.ErrSourceUnavailable) {
			t.Fatalf("expected ErrSourceUnavailable, got %v", err)
		}
	})

	t.Run("실패: LabInstance generation 불일치", func(t *testing.T) {
		store, _, svc, instructor := baseSetup()
		lab := store.labInstances[labID]
		lab.Generation = 2
		store.labInstances[labID] = lab

		_, err := svc.Create(ctx, instructor, termID.String())
		if !errors.Is(err, livesession.ErrSourceUnavailable) {
			t.Fatalf("expected ErrSourceUnavailable, got %v", err)
		}
	})

	t.Run("실패: 학생은 LiveSession을 생성할 수 없음", func(t *testing.T) {
		_, _, svc, _ := baseSetup()
		student := repository.User{ID: studentID, OrganizationID: orgID}
		_, err := svc.Create(ctx, student, termID.String())
		if !errors.Is(err, livesession.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: Relay에서 source를 사용할 수 없음", func(t *testing.T) {
		_, relay, svc, instructor := baseSetup()
		relay.usableSource[termID.String()] = false

		_, err := svc.Create(ctx, instructor, termID.String())
		if !errors.Is(err, livesession.ErrSourceUnavailable) {
			t.Fatalf("expected ErrSourceUnavailable, got %v", err)
		}
	})

	t.Run("실패: Class당 active LiveSession은 최대 1개 (이미 존재)", func(t *testing.T) {
		_, _, svc, instructor := baseSetup()
		_, err := svc.Create(ctx, instructor, termID.String())
		if err != nil {
			t.Fatalf("first create failed: %v", err)
		}

		// 두 번째 생성 시도 -> ErrLiveActive
		_, err = svc.Create(ctx, instructor, termID.String())
		if !errors.Is(err, livesession.ErrLiveActive) {
			t.Fatalf("expected ErrLiveActive, got %v", err)
		}
	})

	t.Run("실패: Relay 등록 실패 시 DB 정리", func(t *testing.T) {
		store, relay, svc, instructor := baseSetup()
		relay.regErr = errors.New("relay register error")

		_, err := svc.Create(ctx, instructor, termID.String())
		if !errors.Is(err, livesession.ErrSourceUnavailable) {
			t.Fatalf("expected ErrSourceUnavailable, got %v", err)
		}

		// DB에는 active 세션이 없어야 함
		_, err = store.ActiveLiveSessionByClass(ctx, classID)
		if !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("expected no active live session in store, got %v", err)
		}
	})
}

func TestLiveSessionGetActive(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	studentID := uuid.New()
	classID := uuid.New()
	liveID := uuid.New()
	termID := uuid.New()

	store := newFakeStore()
	store.classes[classID] = repository.Class{ID: classID, OrganizationID: orgID, Name: "Cloud Lab"}
	store.memberships[[2]uuid.UUID{classID, instructorID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor,
	}
	store.memberships[[2]uuid.UUID{classID, studentID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: studentID, Role: repository.ClassRoleStudent,
	}
	store.liveSessions[liveID] = repository.LiveSession{
		ID:                      liveID,
		OrganizationID:          orgID,
		ClassID:                 classID,
		SourceTerminalSessionID: termID,
		InstructorUserID:        instructorID,
		CreatedAt:               time.Now(),
	}

	svc := livesession.New(livesession.Options{Store: store})

	t.Run("성공: 학생이 조회", func(t *testing.T) {
		student := repository.User{ID: studentID, OrganizationID: orgID}
		res, err := svc.GetActive(ctx, student, classID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ID != liveID {
			t.Fatalf("mismatched id")
		}
	})

	t.Run("성공: 강사가 조회", func(t *testing.T) {
		instructor := repository.User{ID: instructorID, OrganizationID: orgID}
		res, err := svc.GetActive(ctx, instructor, classID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.ID != liveID {
			t.Fatalf("mismatched id")
		}
	})

	t.Run("실패: 멤버십 없는 사용자 조회", func(t *testing.T) {
		other := repository.User{ID: uuid.New(), OrganizationID: orgID}
		_, err := svc.GetActive(ctx, other, classID.String())
		if !errors.Is(err, livesession.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: 활성 세션 없음", func(t *testing.T) {
		emptyClassID := uuid.New()
		store.memberships[[2]uuid.UUID{emptyClassID, studentID}] = repository.ClassMembership{
			OrganizationID: orgID, ClassID: emptyClassID, UserID: studentID, Role: repository.ClassRoleStudent,
		}
		student := repository.User{ID: studentID, OrganizationID: orgID}
		_, err := svc.GetActive(ctx, student, emptyClassID.String())
		if !errors.Is(err, livesession.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestLiveSessionClose(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	classID := uuid.New()
	liveID := uuid.New()
	termID := uuid.New()

	store := newFakeStore()
	relay := newFakeLiveRelay()
	store.memberships[[2]uuid.UUID{classID, instructorID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor,
	}
	store.liveSessions[liveID] = repository.LiveSession{
		ID:                      liveID,
		OrganizationID:          orgID,
		ClassID:                 classID,
		SourceTerminalSessionID: termID,
		InstructorUserID:        instructorID,
		CreatedAt:               time.Now(),
	}

	svc := livesession.New(livesession.Options{Store: store, Relay: relay})
	instructor := repository.User{ID: instructorID, OrganizationID: orgID}

	t.Run("성공: 강사가 LiveSession 종료", func(t *testing.T) {
		err := svc.Close(ctx, instructor, liveID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if relay.terminated[liveID.String()].Reason != realtime.EndReasonSessionClosed {
			t.Fatalf("relay not terminated")
		}
		stored, err := store.LiveSessionByID(ctx, liveID)
		if err != nil || stored.EndedAt == nil || stored.EndReason != "SESSION_CLOSED" {
			t.Fatalf("store not ended properly")
		}
	})

	t.Run("멱등성: 이미 종료된 LiveSession 재종료는 성공", func(t *testing.T) {
		err := svc.Close(ctx, instructor, liveID.String())
		if err != nil {
			t.Fatalf("expected idempotent success, got %v", err)
		}
	})

	t.Run("실패: 다른 강사는 종료 불가", func(t *testing.T) {
		otherInstructor := repository.User{ID: uuid.New(), OrganizationID: orgID}
		err := svc.Close(ctx, otherInstructor, liveID.String())
		if !errors.Is(err, livesession.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})
}

func TestAuthorizeLiveSubscribe(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	studentID := uuid.New()
	classID := uuid.New()
	liveID := uuid.New()
	termID := uuid.New()
	labID := uuid.New()

	store := newFakeStore()
	store.memberships[[2]uuid.UUID{classID, instructorID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor,
	}
	store.memberships[[2]uuid.UUID{classID, studentID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: studentID, Role: repository.ClassRoleStudent,
	}
	store.labInstances[labID] = repository.LabInstance{
		ID: labID, OrganizationID: orgID, UserID: instructorID, ClassID: classID, Generation: 1, Status: "READY",
	}
	store.terminals[termID] = repository.TerminalSession{
		ID: termID, OrganizationID: orgID, UserID: instructorID, LabInstanceID: labID, Generation: 1, Status: repository.TerminalSessionActive,
	}
	store.liveSessions[liveID] = repository.LiveSession{
		ID:                      liveID,
		OrganizationID:          orgID,
		ClassID:                 classID,
		SourceTerminalSessionID: termID,
		InstructorUserID:        instructorID,
		CreatedAt:               time.Now(),
	}

	authSvc := &fakeAuthenticator{
		principals: map[string]auth.Principal{
			"valid-student": {
				User: repository.User{ID: studentID, OrganizationID: orgID},
			},
			"valid-instructor": {
				User: repository.User{ID: instructorID, OrganizationID: orgID},
			},
			"other-org": {
				User: repository.User{ID: uuid.New(), OrganizationID: uuid.New()},
			},
		},
	}

	svc := livesession.New(livesession.Options{
		Store: store,
		Auth:  authSvc,
	})

	t.Run("성공: 학생이 유효한 토큰으로 구독 승인", func(t *testing.T) {
		grant, err := svc.AuthorizeLiveSubscribe(ctx, realtime.SessionToken("valid-student"), liveID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if grant.LiveSessionID != liveID.String() || grant.SourceTerminalSessionID != termID.String() {
			t.Fatalf("mismatched grant: %+v", grant)
		}
	})

	t.Run("실패: 강사 역할 사용자는 구독 불가 (학생 전용)", func(t *testing.T) {
		_, err := svc.AuthorizeLiveSubscribe(ctx, realtime.SessionToken("valid-instructor"), liveID.String())
		if !errors.Is(err, realtime.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: 다른 조직 사용자", func(t *testing.T) {
		_, err := svc.AuthorizeLiveSubscribe(ctx, realtime.SessionToken("other-org"), liveID.String())
		if !errors.Is(err, realtime.ErrForbidden) {
			t.Fatalf("expected ErrForbidden, got %v", err)
		}
	})

	t.Run("실패: 잘못된 인증 세션 토큰", func(t *testing.T) {
		_, err := svc.AuthorizeLiveSubscribe(ctx, realtime.SessionToken("invalid-token"), liveID.String())
		if !errors.Is(err, realtime.ErrUnauthenticated) {
			t.Fatalf("expected ErrUnauthenticated, got %v", err)
		}
	})

	t.Run("실패: Lab generation 변경됨", func(t *testing.T) {
		lab := store.labInstances[labID]
		lab.Generation = 99
		store.labInstances[labID] = lab

		_, err := svc.AuthorizeLiveSubscribe(ctx, realtime.SessionToken("valid-student"), liveID.String())
		if !errors.Is(err, realtime.ErrLabMutation) {
			t.Fatalf("expected ErrLabMutation, got %v", err)
		}
	})
}

func TestLiveSessionSourceTerminalEnded(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	classID := uuid.New()
	liveID := uuid.New()
	termID := uuid.New()

	store := newFakeStore()
	store.liveSessions[liveID] = repository.LiveSession{
		ID:                      liveID,
		OrganizationID:          orgID,
		ClassID:                 classID,
		SourceTerminalSessionID: termID,
		InstructorUserID:        instructorID,
		CreatedAt:               time.Now(),
	}

	svc := livesession.New(livesession.Options{Store: store})
	err := svc.SourceTerminalEnded(ctx, termID.String(), realtime.End{Reason: realtime.EndReasonSessionClosed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := store.LiveSessionByID(ctx, liveID)
	if err != nil || stored.EndedAt == nil || stored.EndReason != realtime.EndReasonSessionClosed {
		t.Fatalf("live session not ended by source terminal end")
	}
}

func TestLiveSessionCloseAndRecreateOnSameTerminal(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	instructorID := uuid.New()
	classID := uuid.New()
	labID := uuid.New()
	termID := uuid.New()

	store := newFakeStore()
	relay := newFakeLiveRelay()

	instructor := repository.User{
		ID:             instructorID,
		OrganizationID: orgID,
	}

	store.classes[classID] = repository.Class{ID: classID, OrganizationID: orgID, Name: "Cloud Lab"}
	store.memberships[[2]uuid.UUID{classID, instructorID}] = repository.ClassMembership{
		OrganizationID: orgID, ClassID: classID, UserID: instructorID, Role: repository.ClassRoleInstructor,
	}
	store.labInstances[labID] = repository.LabInstance{
		ID: labID, OrganizationID: orgID, UserID: instructorID, ClassID: classID, Generation: 1, Status: "READY",
	}
	store.terminals[termID] = repository.TerminalSession{
		ID: termID, OrganizationID: orgID, UserID: instructorID, LabInstanceID: labID, Generation: 1, Status: repository.TerminalSessionActive,
	}
	relay.usableSource[termID.String()] = true

	svc := livesession.New(livesession.Options{Store: store, Relay: relay})

	// 1. Create Live 1
	live1, err := svc.Create(ctx, instructor, termID.String())
	if err != nil {
		t.Fatalf("Create(1) error = %v", err)
	}

	// 2. Explicitly Close Live 1
	err = svc.Close(ctx, instructor, live1.ID.String())
	if err != nil {
		t.Fatalf("Close(1) error = %v", err)
	}

	// Verify Live 1 is ended in store
	stored1, err := store.LiveSessionByID(ctx, live1.ID)
	if err != nil || stored1.EndedAt == nil {
		t.Fatalf("Live 1 not ended in store")
	}

	// 3. Immediately Create Live 2 on the same source terminal
	live2, err := svc.Create(ctx, instructor, termID.String())
	if err != nil {
		t.Fatalf("Create(2) error = %v", err)
	}

	// Verify Live 2 is active in store
	activeLive, err := svc.GetActive(ctx, instructor, classID.String())
	if err != nil {
		t.Fatalf("GetActive error = %v", err)
	}
	if activeLive.ID != live2.ID {
		t.Fatalf("active live ID = %v, want %v", activeLive.ID, live2.ID)
	}
	if activeLive.EndedAt != nil {
		t.Fatalf("Live 2 should be active, but EndedAt is %v", activeLive.EndedAt)
	}
}
