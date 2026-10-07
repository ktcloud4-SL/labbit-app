//go:build integration

package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestLiveSessionStoreIntegration(t *testing.T) {
	env := newTerminalEnv(t)
	ctx := t.Context()

	// 1. Create a source terminal session
	term := env.create(t)

	now := time.Now().UTC().Truncate(time.Microsecond)
	liveID := uuid.New()

	newLive := repository.NewLiveSession{
		ID:                      liveID,
		OrganizationID:          env.fixture.OrganizationID,
		ClassID:                 env.fixture.ClassID,
		SourceTerminalSessionID: term.ID,
		InstructorUserID:        env.fixture.InstructorID,
		CreatedAt:               now,
	}

	// 2. Create LiveSession
	if err := env.store.CreateLiveSession(ctx, newLive); err != nil {
		t.Fatalf("CreateLiveSession() error = %v", err)
	}

	// 3. Read by ID
	got, err := env.store.LiveSessionByID(ctx, liveID)
	if err != nil {
		t.Fatalf("LiveSessionByID() error = %v", err)
	}
	if got.ID != liveID || got.ClassID != env.fixture.ClassID || got.SourceTerminalSessionID != term.ID {
		t.Errorf("LiveSessionByID() got %+v, want ID %s", got, liveID)
	}
	if got.EndedAt != nil {
		t.Errorf("LiveSessionByID() endedAt = %v, want nil", got.EndedAt)
	}

	// 4. Read active by Class
	active, err := env.store.ActiveLiveSessionByClass(ctx, env.fixture.ClassID)
	if err != nil {
		t.Fatalf("ActiveLiveSessionByClass() error = %v", err)
	}
	if active.ID != liveID {
		t.Errorf("ActiveLiveSessionByClass() ID = %s, want %s", active.ID, liveID)
	}

	// 5. Duplicate active live session for the same class -> ErrConflict
	dupLive := repository.NewLiveSession{
		ID:                      uuid.New(),
		OrganizationID:          env.fixture.OrganizationID,
		ClassID:                 env.fixture.ClassID,
		SourceTerminalSessionID: term.ID,
		InstructorUserID:        env.fixture.InstructorID,
		CreatedAt:               now.Add(time.Second),
	}
	err = env.store.CreateLiveSession(ctx, dupLive)
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("CreateLiveSession() duplicate active error = %v, want ErrConflict", err)
	}

	// 6. End LiveSession
	endedAt := now.Add(10 * time.Second)
	changed, err := env.store.EndLiveSession(ctx, liveID, endedAt, "SESSION_CLOSED")
	if err != nil {
		t.Fatalf("EndLiveSession() error = %v", err)
	}
	if !changed {
		t.Errorf("EndLiveSession() changed = false, want true")
	}

	// 7. Second End LiveSession -> false (idempotent / already ended)
	changed2, err := env.store.EndLiveSession(ctx, liveID, endedAt, "SESSION_CLOSED")
	if err != nil {
		t.Fatalf("EndLiveSession() second call error = %v", err)
	}
	if changed2 {
		t.Errorf("EndLiveSession() second call changed = true, want false")
	}

	// 8. Active by class should now be ErrNotFound
	_, err = env.store.ActiveLiveSessionByClass(ctx, env.fixture.ClassID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("ActiveLiveSessionByClass() after end error = %v, want ErrNotFound", err)
	}

	// 9. After ended, creating a new active live session in same class succeeds!
	newActiveID := uuid.New()
	newActive := repository.NewLiveSession{
		ID:                      newActiveID,
		OrganizationID:          env.fixture.OrganizationID,
		ClassID:                 env.fixture.ClassID,
		SourceTerminalSessionID: term.ID,
		InstructorUserID:        env.fixture.InstructorID,
		CreatedAt:               endedAt.Add(time.Second),
	}
	if err := env.store.CreateLiveSession(ctx, newActive); err != nil {
		t.Fatalf("CreateLiveSession() after previous ended error = %v", err)
	}

	// 10. EndActiveLiveSessionBySourceTerminal
	termEndedAt := endedAt.Add(time.Minute)
	changedByTerm, err := env.store.EndActiveLiveSessionBySourceTerminal(ctx, term.ID, termEndedAt, "SOURCE_TERMINAL_ENDED")
	if err != nil {
		t.Fatalf("EndActiveLiveSessionBySourceTerminal() error = %v", err)
	}
	if !changedByTerm {
		t.Errorf("EndActiveLiveSessionBySourceTerminal() changed = false, want true")
	}

	// Verified ended
	endedLive, err := env.store.LiveSessionByID(ctx, newActiveID)
	if err != nil {
		t.Fatalf("LiveSessionByID() error = %v", err)
	}
	if endedLive.EndedAt == nil || endedLive.EndReason != "SOURCE_TERMINAL_ENDED" {
		t.Errorf("LiveSessionByID() ended = %+v, want reason SOURCE_TERMINAL_ENDED", endedLive)
	}

	// 11. Foreign key violations
	invalidClassLive := repository.NewLiveSession{
		ID:                      uuid.New(),
		OrganizationID:          env.fixture.OrganizationID,
		ClassID:                 uuid.New(), // non-existent class
		SourceTerminalSessionID: term.ID,
		InstructorUserID:        env.fixture.InstructorID,
		CreatedAt:               time.Now(),
	}
	err = env.store.CreateLiveSession(ctx, invalidClassLive)
	if !errors.Is(err, repository.ErrConstraintViolation) {
		t.Fatalf("CreateLiveSession() invalid class FK error = %v, want ErrConstraintViolation", err)
	}
}
