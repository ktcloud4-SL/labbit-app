//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

var terminalTime = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// terminalEnv는 migration이 적용된 database와 Fixture다.
type terminalEnv struct {
	*testDB
	fixture *terminaltest.Fixture
}

func newTerminalEnv(t *testing.T) *terminalEnv {
	t.Helper()
	db := newTestDB(t)
	conn := postgrestest.Connect(t, db.dsn)
	return &terminalEnv{testDB: db, fixture: terminaltest.New(t, conn)}
}

func (e *terminalEnv) newSession(t *testing.T, mods ...func(*repository.NewTerminalSession)) repository.NewTerminalSession {
	t.Helper()
	s := repository.NewTerminalSession{
		ID: uuid.New(), OrganizationID: e.fixture.OrganizationID, LabInstanceID: e.fixture.LabInstanceID, UserID: e.fixture.OwnerID,
		ProviderResourceID: e.fixture.Servers["workspace"].ResourceID, Generation: 1,
		AttachTokenHash: []byte("digest-" + uuid.NewString()), TokenExpiresAt: terminalTime.Add(8 * time.Hour), CreatedAt: terminalTime,
	}
	for _, mod := range mods {
		mod(&s)
	}
	return s
}

func (e *terminalEnv) create(t *testing.T, mods ...func(*repository.NewTerminalSession)) repository.NewTerminalSession {
	t.Helper()
	s := e.newSession(t, mods...)
	if err := e.store.CreateTerminalSession(t.Context(), s); err != nil {
		t.Fatalf("CreateTerminalSession() error = %v", err)
	}
	return s
}

func (e *terminalEnv) get(t *testing.T, id uuid.UUID) repository.TerminalSession {
	t.Helper()
	s, err := e.store.TerminalSessionByID(t.Context(), id)
	if err != nil {
		t.Fatalf("TerminalSessionByID() error = %v", err)
	}
	return s
}

func TestLabInstanceQueries(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	for name, read := range map[string]func(context.Context, uuid.UUID) (repository.LabInstance, error){
		"ByID": e.store.LabInstanceByID,
		"ForShare": func(ctx context.Context, id uuid.UUID) (repository.LabInstance, error) {
			var lab repository.LabInstance
			err := e.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
				var err error
				lab, err = repos.LabInstanceForShare(ctx, id)
				return err
			})
			return lab, err
		},
	} {
		lab, err := read(t.Context(), f.LabInstanceID)
		if err != nil {
			t.Fatalf("%s: error = %v", name, err)
		}
		want := repository.LabInstance{
			ID: f.LabInstanceID, OrganizationID: f.OrganizationID, LabExecutionID: f.LabExecutionID, ClassID: f.ClassID,
			UserID: f.OwnerID, Status: "READY", Generation: 1,
		}
		if lab != want {
			t.Fatalf("%s: LabInstance = %+v, want %+v", name, lab, want)
		}
		if _, err := read(t.Context(), uuid.New()); !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("%s: 없는 LabInstance error = %v, want ErrNotFound", name, err)
		}
	}
}

// FOR SHARE는 그 LabInstance의 generation을 바꾸는 UPDATE(Reset)를 transaction이 끝날 때까지 기다리게 한다.
// 그래서 같은 transaction에서 만드는 TerminalSession은 읽은 generation과 항상 일치한다.
func TestLabInstanceForShareBlocksGenerationChangeUntilCommit(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	other := postgrestest.Connect(t, e.dsn)

	locked := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- e.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
			if _, err := repos.LabInstanceForShare(ctx, f.LabInstanceID); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	updated := make(chan error, 1)
	go func() {
		_, err := other.Exec(t.Context(), `UPDATE lab_instances SET generation = generation + 1 WHERE id = $1`, f.LabInstanceID)
		updated <- err
	}()
	select {
	case err := <-updated:
		t.Fatalf("generation UPDATE가 FOR SHARE를 기다리지 않고 끝남: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("transaction error = %v", err)
	}
	select {
	case err := <-updated:
		if err != nil {
			t.Fatalf("UPDATE error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("commit 뒤에도 UPDATE가 끝나지 않음")
	}

	// 다른 행의 읽기와 같은 행의 다른 FOR SHARE는 서로 막지 않는다.
	if _, err := e.store.LabInstanceByID(t.Context(), f.PeerLabInstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorIDForLabInstance(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	got, err := e.store.ConnectorIDForLabInstance(t.Context(), f.LabInstanceID)
	if err != nil || got != f.ConnectorID {
		t.Fatalf("ConnectorIDForLabInstance() = %v, %v, want %v", got, err, f.ConnectorID)
	}
	if _, err := e.store.ConnectorIDForLabInstance(t.Context(), uuid.New()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("없는 LabInstance error = %v, want ErrNotFound", err)
	}

	// CreationSnapshot이 없는 LabExecution의 LabInstance는 Connector를 결정할 수 없다(ErrNotFound).
	conn := postgrestest.Connect(t, e.dsn)
	execution, instance := uuid.New(), uuid.New()
	terminaltest.Exec(t, conn, `UPDATE lab_executions SET finished_at = now() WHERE id = $1`, f.LabExecutionID)
	terminaltest.Exec(t, conn, `INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status)
		VALUES ($1, $2, $3, $4, $5, 'ACTIVE')`, execution, f.OrganizationID, f.ClassID, f.LabSpecID, f.InstructorID)
	terminaltest.Exec(t, conn, `INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		VALUES ($1, $2, $3, $4, 'STUDENT', 'READY', 1)`, instance, f.OrganizationID, execution, f.OwnerID)
	if _, err := e.store.ConnectorIDForLabInstance(t.Context(), instance); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("snapshot 없는 LabInstance error = %v, want ErrNotFound", err)
	}
}

func TestProviderServersFiltersByGenerationTypeAndName(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	conn := postgrestest.Connect(t, e.dsn)

	servers, err := e.store.ProviderServers(t.Context(), f.LabInstanceID, 1, "workspace")
	if err != nil || len(servers) != 1 || servers[0].ID != f.Servers["workspace"].ResourceID ||
		servers[0].ProviderID != f.Servers["workspace"].ProviderID || servers[0].LifecycleStatus != "PRESENT" {
		t.Fatalf("workspace = %+v, %v (같은 logical_name의 NETWORK는 포함하면 안 됨)", servers, err)
	}
	// 모든 lifecycle을 돌려준다. 사용 가능 여부는 Application이 판단한다.
	for name, lifecycle := range map[string]string{"retired": "DELETED", "ghost": "MISSING"} {
		got, err := e.store.ProviderServers(t.Context(), f.LabInstanceID, 1, name)
		if err != nil || len(got) != 1 || got[0].LifecycleStatus != lifecycle {
			t.Fatalf("%s = %+v, %v, want lifecycle %s", name, got, err, lifecycle)
		}
	}
	// 없는 이름, 다른 generation, 다른 LabInstance는 빈 목록이다(오류가 아니다).
	for name, args := range map[string]struct {
		lab  uuid.UUID
		gen  int64
		name string
	}{
		"unknown name":     {f.LabInstanceID, 1, "nope"},
		"other generation": {f.LabInstanceID, 2, "workspace"},
		"network only":     {f.LabInstanceID, 1, "net"},
		"other instance":   {f.PeerLabInstanceID, 1, "db"},
	} {
		got, err := e.store.ProviderServers(t.Context(), args.lab, args.gen, args.name)
		if err != nil || len(got) != 0 {
			t.Fatalf("%s = %+v, %v, want empty", name, got, err)
		}
	}

	// Reset 뒤에는 새 generation의 리소스만 그 generation에서 보인다.
	generation, workspace := f.BumpGeneration(t, conn, f.LabInstanceID)
	got, err := e.store.ProviderServers(t.Context(), f.LabInstanceID, generation, "workspace")
	if err != nil || len(got) != 1 || got[0].ID != workspace.ResourceID {
		t.Fatalf("새 generation workspace = %+v, %v", got, err)
	}
	if old, _ := e.store.ProviderServers(t.Context(), f.LabInstanceID, 1, "workspace"); len(old) != 1 || old[0].ID == workspace.ResourceID {
		t.Fatalf("이전 generation의 리소스가 섞임: %+v", old)
	}
}

// 제약은 경쟁 조건에서도 최종 안전망이다.
func TestCreateTerminalSessionConstraints(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture

	t.Run("stores only the digest and lifecycle metadata", func(t *testing.T) {
		s := e.create(t)
		got := e.get(t, s.ID)
		if got.Status != repository.TerminalSessionOpening || string(got.AttachTokenHash) != string(s.AttachTokenHash) || got.Generation != 1 ||
			got.OrganizationID != f.OrganizationID || got.LabInstanceID != f.LabInstanceID || got.UserID != f.OwnerID ||
			got.ProviderResourceID != s.ProviderResourceID || !got.TokenExpiresAt.Equal(s.TokenExpiresAt) || !got.CreatedAt.Equal(s.CreatedAt) ||
			got.AttachedAt != nil || got.DetachedAt != nil || got.GraceExpiresAt != nil || got.EndedAt != nil || got.EndReason != "" {
			t.Fatalf("TerminalSession = %+v", got)
		}
	})
	t.Run("duplicate token digest", func(t *testing.T) {
		first := e.create(t)
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.AttachTokenHash = first.AttachTokenHash }))
		if !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
	})
	t.Run("provider resource of another lab instance", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.ProviderResourceID = f.PeerServer.ResourceID }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
	t.Run("provider resource of another generation", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.Generation = 2 }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.UserID = uuid.New() }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
	t.Run("user of another organization", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.UserID = f.ForeignID }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
	t.Run("token expiry not after creation", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.TokenExpiresAt = s.CreatedAt }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
	t.Run("empty digest", func(t *testing.T) {
		err := e.store.CreateTerminalSession(t.Context(), e.newSession(t, func(s *repository.NewTerminalSession) { s.AttachTokenHash = []byte{} }))
		if !errors.Is(err, repository.ErrConstraintViolation) {
			t.Fatalf("error = %v, want ErrConstraintViolation", err)
		}
	})
}

func TestTerminalSessionByIDNotFound(t *testing.T) {
	e := newTerminalEnv(t)
	if _, err := e.store.TerminalSessionByID(t.Context(), uuid.New()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// 전이는 모두 조건부 UPDATE다. 조건을 만족하지 않으면 아무것도 바꾸지 않고 false이며 ENDED는 어떤 전이로도 되살아나지 않는다.
func TestTerminalSessionTransitionsAreConditional(t *testing.T) {
	e := newTerminalEnv(t)
	ctx := t.Context()
	at := func(minutes int) time.Time { return terminalTime.Add(time.Duration(minutes) * time.Minute) }

	expect := func(what string, got bool, err error, want bool) {
		t.Helper()
		if err != nil || got != want {
			t.Fatalf("%s = %v, %v, want %v", what, got, err, want)
		}
	}
	status := func(s repository.NewTerminalSession) repository.TerminalSessionStatus { return e.get(t, s.ID).Status }

	s := e.create(t)

	// OPENING에서는 attach/detach할 수 없고 Opened만 가능하다.
	ok, err := e.store.MarkTerminalSessionAttached(ctx, s.ID, at(1))
	expect("OPENING attach", ok, err, false)
	ok, err = e.store.MarkTerminalSessionDetached(ctx, s.ID, at(1), at(2))
	expect("OPENING detach", ok, err, false)
	if status(s) != repository.TerminalSessionOpening {
		t.Fatalf("status = %s, want OPENING", status(s))
	}

	ok, err = e.store.MarkTerminalSessionOpened(ctx, s.ID, at(1), at(2))
	expect("Opened", ok, err, true)
	got := e.get(t, s.ID)
	if got.Status != repository.TerminalSessionDetached || got.DetachedAt == nil || !got.DetachedAt.Equal(at(1)) || got.GraceExpiresAt == nil || !got.GraceExpiresAt.Equal(at(2)) || got.AttachedAt != nil {
		t.Fatalf("Opened 뒤 = %+v", got)
	}
	ok, err = e.store.MarkTerminalSessionOpened(ctx, s.ID, at(3), at(4))
	expect("Opened again", ok, err, false)
	ok, err = e.store.MarkTerminalSessionDetached(ctx, s.ID, at(3), at(4))
	expect("DETACHED detach", ok, err, false)
	if e.get(t, s.ID).GraceExpiresAt.Equal(at(4)) {
		t.Fatal("거절된 전이가 grace를 바꿈")
	}

	// DETACHED → ACTIVE: grace를 비우고 attached_at을 기록한다. ACTIVE → ACTIVE(replace)도 가능하다.
	ok, err = e.store.MarkTerminalSessionAttached(ctx, s.ID, at(5))
	expect("attach", ok, err, true)
	got = e.get(t, s.ID)
	if got.Status != repository.TerminalSessionActive || got.AttachedAt == nil || !got.AttachedAt.Equal(at(5)) || got.DetachedAt != nil || got.GraceExpiresAt != nil {
		t.Fatalf("attach 뒤 = %+v", got)
	}
	ok, err = e.store.MarkTerminalSessionAttached(ctx, s.ID, at(6))
	expect("re-attach while ACTIVE", ok, err, true)
	if !e.get(t, s.ID).AttachedAt.Equal(at(6)) {
		t.Fatal("ACTIVE 재attach가 attached_at을 갱신하지 않음")
	}

	// ACTIVE → DETACHED
	ok, err = e.store.MarkTerminalSessionDetached(ctx, s.ID, at(7), at(8))
	expect("detach", ok, err, true)
	got = e.get(t, s.ID)
	if got.Status != repository.TerminalSessionDetached || !got.DetachedAt.Equal(at(7)) || !got.GraceExpiresAt.Equal(at(8)) || !got.AttachedAt.Equal(at(6)) {
		t.Fatalf("detach 뒤 = %+v", got)
	}

	// 어느 상태에서도 ENDED로 갈 수 있다. 처음 기록한 시각과 이유를 유지하고 되살아나지 않는다.
	ok, err = e.store.EndTerminalSession(ctx, s.ID, at(9), "SESSION_EXPIRED")
	expect("end", ok, err, true)
	got = e.get(t, s.ID)
	if got.Status != repository.TerminalSessionEnded || got.EndedAt == nil || !got.EndedAt.Equal(at(9)) || got.EndReason != "SESSION_EXPIRED" || got.GraceExpiresAt != nil {
		t.Fatalf("end 뒤 = %+v", got)
	}
	ok, err = e.store.EndTerminalSession(ctx, s.ID, at(10), "SESSION_CLOSED")
	expect("end again", ok, err, false)
	if got := e.get(t, s.ID); !got.EndedAt.Equal(at(9)) || got.EndReason != "SESSION_EXPIRED" {
		t.Fatalf("반복 종료가 처음 기록을 바꿈: %+v", got)
	}
	for name, transition := range map[string]func() (bool, error){
		"opened":   func() (bool, error) { return e.store.MarkTerminalSessionOpened(ctx, s.ID, at(11), at(12)) },
		"attached": func() (bool, error) { return e.store.MarkTerminalSessionAttached(ctx, s.ID, at(11)) },
		"detached": func() (bool, error) { return e.store.MarkTerminalSessionDetached(ctx, s.ID, at(11), at(12)) },
	} {
		ok, err := transition()
		expect("ENDED "+name, ok, err, false)
	}
	if status(s) != repository.TerminalSessionEnded {
		t.Fatalf("ENDED가 되살아남: %s", status(s))
	}

	// 없는 TerminalSession은 오류가 아니라 false다.
	ok, err = e.store.EndTerminalSession(ctx, uuid.New(), at(1), "X")
	expect("end unknown", ok, err, false)

	// OPENING에서 바로 종료할 수 있다(생성 실패).
	failed := e.create(t)
	ok, err = e.store.EndTerminalSession(ctx, failed.ID, at(1), "OPEN_FAILED")
	expect("end OPENING", ok, err, true)
}

// 동시에 같은 전이를 시도해도 정확히 하나만 성공한다(조건부 UPDATE의 원자성).
func TestConcurrentEndTerminalSessionSucceedsExactlyOnce(t *testing.T) {
	e := newTerminalEnv(t)
	s := e.create(t)
	if ok, err := e.store.MarkTerminalSessionOpened(t.Context(), s.ID, terminalTime, terminalTime.Add(time.Minute)); !ok || err != nil {
		t.Fatal(ok, err)
	}

	const workers = 16
	results := make(chan bool, workers)
	for i := 0; i < workers; i++ {
		go func() {
			ok, err := e.store.EndTerminalSession(t.Context(), s.ID, terminalTime.Add(time.Hour), "SESSION_CLOSED")
			if err != nil {
				t.Errorf("EndTerminalSession() error = %v", err)
			}
			results <- ok
		}()
	}
	wins := 0
	for i := 0; i < workers; i++ {
		if <-results {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("종료에 성공한 호출 = %d, want 정확히 1", wins)
	}
}

func TestUnendedTerminalSessionsByLabInstance(t *testing.T) {
	e := newTerminalEnv(t)
	f := e.fixture
	ctx := t.Context()

	if got, err := e.store.UnendedTerminalSessionsByLabInstance(ctx, f.LabInstanceID); err != nil || len(got) != 0 {
		t.Fatalf("빈 LabInstance = %v, %v, want empty", got, err)
	}
	opening := e.create(t, func(s *repository.NewTerminalSession) { s.CreatedAt = terminalTime })
	active := e.create(t, func(s *repository.NewTerminalSession) { s.CreatedAt = terminalTime.Add(time.Minute) })
	ended := e.create(t, func(s *repository.NewTerminalSession) { s.CreatedAt = terminalTime.Add(2 * time.Minute) })
	peer := e.create(t, func(s *repository.NewTerminalSession) {
		s.LabInstanceID, s.UserID, s.ProviderResourceID = f.PeerLabInstanceID, f.PeerID, f.PeerServer.ResourceID
	})
	_, _ = e.store.MarkTerminalSessionOpened(ctx, active.ID, terminalTime, terminalTime.Add(time.Minute))
	_, _ = e.store.MarkTerminalSessionAttached(ctx, active.ID, terminalTime)
	_, _ = e.store.EndTerminalSession(ctx, ended.ID, terminalTime, "SESSION_CLOSED")

	got, err := e.store.UnendedTerminalSessionsByLabInstance(ctx, f.LabInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != opening.ID || got[1].ID != active.ID {
		t.Fatalf("ENDED가 아닌 TerminalSession = %+v, want [opening active] in creation order (peer %s 제외)", got, peer.ID)
	}
}

// DB에는 lifecycle metadata만 있다. Terminal INPUT/OUTPUT, transcript, exit code를 저장할 column이 없다.
func TestTerminalSessionsTableHasOnlyLifecycleColumns(t *testing.T) {
	e := newTerminalEnv(t)
	rows, err := e.pool.Query(t.Context(),
		`SELECT column_name FROM information_schema.columns WHERE table_name = 'terminal_sessions' ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{
		"attach_token_hash", "attached_at", "created_at", "detached_at", "end_reason", "ended_at", "generation", "grace_expires_at",
		"id", "lab_instance_id", "organization_id", "provider_resource_id", "status", "token_expires_at", "user_id",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("terminal_sessions columns = %v, want %v", got, want)
	}
}
