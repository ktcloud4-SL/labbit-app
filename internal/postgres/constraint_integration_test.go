//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// DB 제약은 경쟁 조건에서 최종 안전망이다. 대표 FK/Unique/Check 위반이 Repository를 통과하면서
// 올바른 의미로 정규화되고, 어느 제약이 발동했는지는 진단 필드로만 확인한다.
func TestRepositoryNormalizesConstraintViolations(t *testing.T) {
	db := newTestDB(t)
	a := bootstrapSample(t, db, 1)
	b := bootstrapSample(t, db, 2)
	ctx := t.Context()

	tokenHash := []byte("0123456789abcdef0123456789abcdef")
	if err := db.store.CreateAuthSession(ctx, repository.AuthSession{
		ID: testID(kindSession, 1), UserID: a.InstructorID, TokenHash: tokenHash, CreatedAt: at(9), ExpiresAt: at(17),
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
		want error
		// constraint는 명시적으로 이름을 붙인 제약만 확인한다. 비어 있으면 종류만 확인한다.
		constraint string
	}{
		// Unique
		{
			name: "Unique: 중복 username",
			run: func() error {
				userID := testID(kindUser, 9001)
				if err := db.store.CreateUser(ctx, repository.NewUser{ID: userID, OrganizationID: a.OrganizationID, OrganizationRole: repository.OrganizationRoleMember}); err != nil {
					return err
				}
				return db.store.CreateLocalAccount(ctx, repository.NewLocalAccount{UserID: userID, Username: a.instructorUsername(), PasswordHash: dummyPHC})
			},
			want: repository.ErrConflict,
		},
		{
			name: "Unique: 같은 (class, user) ClassMembership",
			run: func() error {
				return db.store.CreateClassMembership(ctx, repository.NewClassMembership{
					OrganizationID: a.OrganizationID, ClassID: a.AlphaID, UserID: a.StudentID, Role: repository.ClassRoleInstructor,
				})
			},
			want: repository.ErrConflict,
		},
		{
			name: "Unique: 같은 Organization ID",
			run: func() error {
				return db.store.CreateOrganization(ctx, repository.NewOrganization{ID: a.OrganizationID, Name: "duplicate"})
			},
			want: repository.ErrConflict,
		},
		{
			name: "Unique: 같은 Session token digest",
			run: func() error {
				return db.store.CreateAuthSession(ctx, repository.AuthSession{
					ID: testID(kindSession, 2), UserID: a.StudentID, TokenHash: tokenHash, CreatedAt: at(9), ExpiresAt: at(17),
				})
			},
			want: repository.ErrConflict,
		},

		// FK: Organization 경계를 넘는 관계
		{
			name: "FK: 다른 Organization의 Class에 참여",
			run: func() error {
				return db.store.CreateClassMembership(ctx, repository.NewClassMembership{
					OrganizationID: a.OrganizationID, ClassID: b.AlphaID, UserID: a.StudentID, Role: repository.ClassRoleStudent,
				})
			},
			want:       repository.ErrConstraintViolation,
			constraint: "fk_class_memberships_class",
		},
		{
			name: "FK: 다른 Organization의 User를 Class에 참여",
			run: func() error {
				return db.store.CreateClassMembership(ctx, repository.NewClassMembership{
					OrganizationID: b.OrganizationID, ClassID: b.AlphaID, UserID: a.StudentID, Role: repository.ClassRoleStudent,
				})
			},
			want:       repository.ErrConstraintViolation,
			constraint: "fk_class_memberships_user",
		},
		{
			name: "FK: 없는 Organization의 User",
			run: func() error {
				return db.store.CreateUser(ctx, repository.NewUser{ID: testID(kindUser, 9002), OrganizationID: testID(kindOrganization, 9999), OrganizationRole: repository.OrganizationRoleMember})
			},
			want:       repository.ErrConstraintViolation,
			constraint: "fk_users_organization",
		},
		{
			name: "FK: 없는 User의 Local Account",
			run: func() error {
				return db.store.CreateLocalAccount(ctx, repository.NewLocalAccount{UserID: testID(kindUser, 9999), Username: "ghost", PasswordHash: dummyPHC})
			},
			want:       repository.ErrConstraintViolation,
			constraint: "fk_local_accounts_user",
		},

		// Check
		{
			name: "Check: Session 만료 시각이 생성 시각 이하",
			run: func() error {
				return db.store.CreateAuthSession(ctx, repository.AuthSession{
					ID: testID(kindSession, 3), UserID: a.StudentID, TokenHash: []byte("check-expiry-digest"), CreatedAt: at(9), ExpiresAt: at(9),
				})
			},
			want:       repository.ErrConstraintViolation,
			constraint: "ck_auth_sessions_expiry",
		},
		{
			name: "Check: 허용되지 않은 organizationRole",
			run: func() error {
				return db.store.CreateUser(ctx, repository.NewUser{ID: testID(kindUser, 9003), OrganizationID: a.OrganizationID, OrganizationRole: "OWNER"})
			},
			want: repository.ErrConstraintViolation,
		},
		{
			name: "Check: 허용되지 않은 Class role",
			run: func() error {
				return db.store.CreateClassMembership(ctx, repository.NewClassMembership{
					OrganizationID: a.OrganizationID, ClassID: a.BravoID, UserID: a.StudentID, Role: "ADMIN",
				})
			},
			want: repository.ErrConstraintViolation,
		},
		{
			name: "Check: 공백뿐인 Organization 이름",
			run: func() error {
				return db.store.CreateOrganization(ctx, repository.NewOrganization{ID: testID(kindOrganization, 9004), Name: "  "})
			},
			want: repository.ErrConstraintViolation,
		},
		{
			name: "Not Null: token digest 없음",
			run: func() error {
				return db.store.CreateAuthSession(ctx, repository.AuthSession{ID: testID(kindSession, 4), UserID: a.StudentID, CreatedAt: at(9), ExpiresAt: at(17)})
			},
			want: repository.ErrConstraintViolation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()

			assertOnlyKind(t, err, tt.want)
			repoErr := repositoryError(t, err)
			if repoErr.SQLState == "" {
				t.Error("진단용 SQLState가 비어 있습니다")
			}
			if tt.constraint != "" && repoErr.Constraint != tt.constraint {
				t.Errorf("Constraint = %q, want %q", repoErr.Constraint, tt.constraint)
			}
			if repoErr.Cause != nil {
				t.Errorf("제약 위반이 원인 오류를 보존합니다: %v", repoErr.Cause)
			}
		})
	}
}

const activeLabExecutionIndex = "uq_lab_executions_active_per_class"

// labExecutionFixture는 LabExecution Repository 없이 partial unique index만 검증하기 위한 최소 fixture다.
type labExecutionFixture struct {
	s      sample
	specID uuid.UUID
	nextID int
}

func newLabExecutionFixture(t *testing.T, db *testDB) *labExecutionFixture {
	t.Helper()
	s := bootstrapSample(t, db, 1)
	specID := testID(kindLabSpec, 1)
	mustExec(t, db, `
		INSERT INTO lab_specs (id, organization_id, owner_user_id, name, internet_outbound, workspace_role, workspace_instance_index)
		VALUES ($1, $2, $3, 'fixture spec', false, 'workspace', 0)`,
		specID, s.OrganizationID, s.InstructorID)
	return &labExecutionFixture{s: s, specID: specID, nextID: 1}
}

// newClass는 다른 Class의 실행에 영향받지 않는 독립된 Class를 만든다.
func (f *labExecutionFixture) newClass(t *testing.T, db *testDB) uuid.UUID {
	t.Helper()
	id := testID(kindClass, 1000+f.nextID)
	f.nextID++
	if err := db.store.CreateClass(t.Context(), repository.NewClass{ID: id, OrganizationID: f.s.OrganizationID, Name: "extra"}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *labExecutionFixture) insertExecution(ctx context.Context, exec execer, classID, executionID uuid.UUID, finished bool) error {
	finishedAt := "NULL"
	if finished {
		finishedAt = "now()"
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status, finished_at)
		VALUES ($1, $2, $3, $4, $5, 'RUNNING', `+finishedAt+`)`,
		executionID, f.s.OrganizationID, classID, f.specID, f.s.InstructorID)
	return err
}

func (f *labExecutionFixture) executionID() uuid.UUID {
	id := testID(kindLabExecution, f.nextID)
	f.nextID++
	return id
}

func activeExecutions(t *testing.T, db *testDB, classID uuid.UUID) int {
	t.Helper()
	return countRows(t, db, `SELECT count(*) FROM lab_executions WHERE class_id = $1 AND finished_at IS NULL`, classID)
}

func assertActiveExecutionConflict(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != activeLabExecutionIndex {
		t.Fatalf("error = %v, want unique violation of %s", err, activeLabExecutionIndex)
	}
}

// 같은 Class에는 finished_at IS NULL인 LabExecution이 동시에 하나만 있을 수 있다.
func TestActiveLabExecutionPerClassIsEnforcedByPartialUniqueIndex(t *testing.T) {
	db := newTestDB(t)
	f := newLabExecutionFixture(t, db)
	ctx := t.Context()

	t.Run("종료된 실행은 partial 조건에서 제외되어 여러 개 있을 수 있다", func(t *testing.T) {
		classID := f.newClass(t, db)

		first, second := f.executionID(), f.executionID()
		if err := f.insertExecution(ctx, db.pool, classID, first, true); err != nil {
			t.Fatal(err)
		}
		if err := f.insertExecution(ctx, db.pool, classID, second, true); err != nil {
			t.Fatalf("종료된 두 번째 실행 INSERT error = %v", err)
		}
	})

	t.Run("활성 실행이 있으면 두 번째 활성 실행은 거부되고 종료 후 다시 시작할 수 있다", func(t *testing.T) {
		classID := f.newClass(t, db)

		active := f.executionID()
		if err := f.insertExecution(ctx, db.pool, classID, active, false); err != nil {
			t.Fatal(err)
		}
		assertActiveExecutionConflict(t, f.insertExecution(ctx, db.pool, classID, f.executionID(), false))

		mustExec(t, db, `UPDATE lab_executions SET finished_at = now() WHERE id = $1`, active)
		if err := f.insertExecution(ctx, db.pool, classID, f.executionID(), false); err != nil {
			t.Fatalf("종료 후 새 활성 실행 INSERT error = %v", err)
		}
		if n := activeExecutions(t, db, classID); n != 1 {
			t.Errorf("활성 실행 = %d, want 1", n)
		}
	})

	t.Run("다른 Class의 활성 실행과는 독립이다", func(t *testing.T) {
		for range 2 {
			if err := f.insertExecution(ctx, db.pool, f.newClass(t, db), f.executionID(), false); err != nil {
				t.Fatalf("서로 다른 Class의 활성 실행 INSERT error = %v", err)
			}
		}
	})
}

// 순차 INSERT가 아니라 두 transaction이 실제로 경쟁하는 상황이다.
// tx1이 아직 commit하지 않은 활성 실행 때문에 tx2의 INSERT가 index lock에서 대기하고,
// tx1의 결과에 따라 tx2가 거부되거나 성공한다. 어느 쪽이든 Class의 활성 실행은 최대 하나다.
func TestActiveLabExecutionRaceBetweenTwoTransactions(t *testing.T) {
	tests := []struct {
		name string
		// finish는 먼저 시작한 transaction의 최종 결과다.
		finish func(ctx context.Context, tx pgx.Tx) error
		// assertSecond는 대기하던 두 번째 INSERT의 결과를 확인한다.
		assertSecond func(t *testing.T, err error)
	}{
		{
			name:         "먼저 commit하면 두 번째 INSERT가 unique violation으로 거부된다",
			finish:       func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
			assertSecond: assertActiveExecutionConflict,
		},
		{
			name:   "먼저 rollback하면 두 번째 INSERT가 성공한다",
			finish: func(ctx context.Context, tx pgx.Tx) error { return tx.Rollback(ctx) },
			assertSecond: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("첫 transaction이 rollback되었는데 두 번째 INSERT error = %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			f := newLabExecutionFixture(t, db)
			ctx := t.Context()
			classID := f.newClass(t, db)

			conn1, conn2 := postgrestest.Connect(t, db.dsn), postgrestest.Connect(t, db.dsn)
			tx1, err := conn1.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tx2, err := conn2.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = tx1.Rollback(context.Background())
				_ = tx2.Rollback(context.Background())
			})

			if err := f.insertExecution(ctx, tx1, classID, f.executionID(), false); err != nil {
				t.Fatalf("첫 transaction INSERT error = %v", err)
			}

			second := make(chan error, 1)
			secondID := f.executionID()
			go func() { second <- f.insertExecution(ctx, tx2, classID, secondID, false) }()

			// 두 번째 INSERT가 첫 transaction의 미확정 index entry를 기다리는 상태를 실제로 관측한다.
			waitForLockWait(t, db.pool)
			select {
			case err := <-second:
				t.Fatalf("첫 transaction이 끝나기 전에 두 번째 INSERT가 끝났습니다: %v", err)
			default:
			}

			if err := tt.finish(ctx, tx1); err != nil {
				t.Fatalf("첫 transaction 종료 error = %v", err)
			}
			select {
			case err := <-second:
				tt.assertSecond(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("두 번째 INSERT가 첫 transaction 종료 후에도 끝나지 않았습니다")
			}

			// 두 번째 transaction의 최종 결과와 무관하게 활성 실행은 최대 하나다.
			_ = tx2.Commit(ctx)
			if n := activeExecutions(t, db, classID); n != 1 {
				t.Errorf("활성 실행 = %d, want 1", n)
			}
		})
	}
}

// 여러 connection이 동시에 같은 Class의 활성 실행을 만들려 할 때 정확히 하나만 성공한다.
func TestActiveLabExecutionRaceAmongManyConnections(t *testing.T) {
	db := newTestDB(t)
	f := newLabExecutionFixture(t, db)
	classID := f.newClass(t, db)
	const workers = 8

	ids := make([]uuid.UUID, workers)
	for i := range ids {
		ids[i] = f.executionID()
	}
	start := make(chan struct{})
	results := make(chan error, workers)
	for _, id := range ids {
		go func() {
			<-start
			results <- f.insertExecution(t.Context(), db.pool, classID, id, false)
		}()
	}
	close(start)

	succeeded, conflicted := 0, 0
	for range workers {
		err := <-results
		var pgErr *pgconn.PgError
		switch {
		case err == nil:
			succeeded++
		case errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == activeLabExecutionIndex:
			conflicted++
		default:
			t.Errorf("예상하지 못한 오류: %v", err)
		}
	}
	if succeeded != 1 || conflicted != workers-1 {
		t.Errorf("성공 %d, unique violation %d, want 1/%d", succeeded, conflicted, workers-1)
	}
	if n := activeExecutions(t, db, classID); n != 1 {
		t.Errorf("활성 실행 = %d, want 1", n)
	}
}

// waitForLockWait는 어떤 backend가 다른 transaction의 lock을 기다리는 상태가 될 때까지 기다린다.
// sleep으로 timing을 가정하지 않고 PostgreSQL의 대기 상태를 직접 관측한다.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		err := pool.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&waiting)
		if err != nil {
			t.Fatalf("pg_stat_activity 조회 실패: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("두 번째 transaction이 lock을 기다리는 경쟁 상태를 재현하지 못했습니다")
}
