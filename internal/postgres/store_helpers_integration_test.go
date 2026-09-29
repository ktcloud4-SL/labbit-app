//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// dummyPHC는 형식만 흉내 낸 폐기용 문자열이다. 실제 Password에서 만들지 않았고 검증 대상도 아니다.
const dummyPHC = "$argon2id$v=19$m=19456,t=2,p=1$dGVzdC1zYWx0LWR1bW15$ZHVtbXktaGFzaC1ub3QtYS1yZWFsLXNlY3JldA"

// ID 종류 prefix다. testID는 고정 UUID를 만들어 실패 시 같은 row를 바로 가리킬 수 있게 한다.
const (
	kindOrganization = iota + 1
	kindUser
	kindClass
	kindSession
	kindLabSpec
	kindLabExecution
)

func testID(kind, n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("%08d-0000-4000-8000-%012d", kind, n))
}

// at은 microsecond 정밀도의 PostgreSQL timestamptz 왕복에서 값이 바뀌지 않는 고정 시각이다.
func at(hour int) time.Time {
	return time.Date(2026, 9, 29, hour, 0, 0, 0, time.UTC)
}

// testDB는 migration이 적용된 폐기 가능한 database와 그 위의 Store다.
type testDB struct {
	dsn   string
	pool  *pgxpool.Pool
	store *postgres.Store
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, embeddedMigrations(t))

	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatalf("OpenPool() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return &testDB{dsn: dsn, pool: pool, store: postgres.NewStore(pool)}
}

// sample은 sampleSpec이 만드는 데이터의 ID다.
type sample struct {
	N              int // sampleSpec에 전달한 번호
	OrganizationID uuid.UUID
	AdminID        uuid.UUID // ADMIN, ClassMembership 없음
	InstructorID   uuid.UUID // MEMBER, Alpha INSTRUCTOR, Bravo STUDENT
	StudentID      uuid.UUID // MEMBER, Alpha STUDENT
	AlphaID        uuid.UUID
	BravoID        uuid.UUID
}

func (s sample) adminUsername() string      { return fmt.Sprintf("admin-%d", s.N) }
func (s sample) instructorUsername() string { return fmt.Sprintf("instructor-%d", s.N) }
func (s sample) studentUsername() string    { return fmt.Sprintf("student-%d", s.N) }

// sampleSpec은 번호 n마다 서로 겹치지 않는 Organization 하나와 소속 데이터를 만든다.
// ADMIN이 Class 운영 권한을 갖지 않는 D-11의 분리를 확인할 수 있도록 ADMIN에는 Membership이 없다.
func sampleSpec(n int) (bootstrap.Spec, sample) {
	base := n * 10
	s := sample{
		N:              n,
		OrganizationID: testID(kindOrganization, n),
		AdminID:        testID(kindUser, base+1),
		InstructorID:   testID(kindUser, base+2),
		StudentID:      testID(kindUser, base+3),
		AlphaID:        testID(kindClass, base+1),
		BravoID:        testID(kindClass, base+2),
	}
	spec := bootstrap.Spec{
		Organization: bootstrap.Organization{ID: s.OrganizationID, Name: fmt.Sprintf("Labbit Academy %d", n)},
		Users: []bootstrap.User{
			{ID: s.AdminID, Username: s.adminUsername(), PasswordHash: dummyPHC, OrganizationRole: repository.OrganizationRoleAdmin},
			{ID: s.InstructorID, Username: s.instructorUsername(), PasswordHash: dummyPHC, OrganizationRole: repository.OrganizationRoleMember},
			{ID: s.StudentID, Username: s.studentUsername(), PasswordHash: dummyPHC, OrganizationRole: repository.OrganizationRoleMember},
		},
		Classes: []bootstrap.Class{
			{ID: s.AlphaID, Name: "Alpha"},
			{ID: s.BravoID, Name: "Bravo"},
		},
		Memberships: []bootstrap.Membership{
			{ClassID: s.AlphaID, UserID: s.InstructorID, Role: repository.ClassRoleInstructor},
			{ClassID: s.BravoID, UserID: s.InstructorID, Role: repository.ClassRoleStudent},
			{ClassID: s.AlphaID, UserID: s.StudentID, Role: repository.ClassRoleStudent},
		},
	}
	return spec, s
}

// bootstrapSample은 운영 Bootstrap 경로로 sampleSpec(n)을 저장한다. fixture도 test 전용 우회 없이 같은 경로를 쓴다.
func bootstrapSample(t *testing.T, db *testDB, n int) sample {
	t.Helper()
	spec, s := sampleSpec(n)
	if err := bootstrap.Run(t.Context(), db.store, spec); err != nil {
		t.Fatalf("bootstrap.Run(sample %d) error = %v", n, err)
	}
	return s
}

func mustExec(t *testing.T, db *testDB, sql string, args ...any) {
	t.Helper()
	if _, err := db.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("Exec(%q) error = %v", sql, err)
	}
}

func countRows(t *testing.T, db *testDB, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("QueryRow(%q) error = %v", sql, err)
	}
	return n
}

// orgRows는 한 Organization에 속한 운영 Bootstrap 데이터의 row 수다.
type orgRows struct {
	Organizations, Users, LocalAccounts, Classes, Memberships int
}

func countOrganizationRows(t *testing.T, db *testDB, organizationID uuid.UUID) orgRows {
	t.Helper()
	var rows orgRows
	err := db.pool.QueryRow(t.Context(), `
		SELECT
			(SELECT count(*) FROM organizations WHERE id = $1),
			(SELECT count(*) FROM users WHERE organization_id = $1),
			(SELECT count(*) FROM local_accounts a JOIN users u ON u.id = a.user_id WHERE u.organization_id = $1),
			(SELECT count(*) FROM classes WHERE organization_id = $1),
			(SELECT count(*) FROM class_memberships WHERE organization_id = $1)`,
		organizationID,
	).Scan(&rows.Organizations, &rows.Users, &rows.LocalAccounts, &rows.Classes, &rows.Memberships)
	if err != nil {
		t.Fatalf("countOrganizationRows() error = %v", err)
	}
	return rows
}

// repositoryError는 err가 정규화된 repository.Error인지 확인하고 반환한다.
func repositoryError(t *testing.T, err error) *repository.Error {
	t.Helper()
	var repoErr *repository.Error
	if !errors.As(err, &repoErr) {
		t.Fatalf("error = %T(%v), want *repository.Error", err, err)
	}
	return repoErr
}

// assertOnlyKind는 err가 want 의미 하나에만 해당하고 PostgreSQL 세부 정보가 노출되지 않는지 확인한다.
func assertOnlyKind(t *testing.T, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %v", want)
	}
	for _, sentinel := range []error{repository.ErrNotFound, repository.ErrConflict, repository.ErrConstraintViolation, repository.ErrInternal} {
		if got, wantIs := errors.Is(err, sentinel), sentinel == want; got != wantIs {
			t.Errorf("errors.Is(%v, %v) = %v, want %v", err, sentinel, got, wantIs)
		}
	}
	assertNoRawDatabaseError(t, err)
}

func assertNoRawDatabaseError(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		t.Errorf("PostgreSQL 오류가 노출되었습니다: %v", pgErr)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		t.Error("pgx.ErrNoRows가 노출되었습니다")
	}
	for _, leaked := range []string{"SQLSTATE", "violates", "duplicate key", "null value", "pgx", "constraint \""} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("오류 문자열에 %q가 노출되었습니다: %q", leaked, err.Error())
		}
	}
}

// execer는 Pool, Conn, Tx가 공통으로 제공하는 Exec다.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
