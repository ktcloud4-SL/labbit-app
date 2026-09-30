// dev-auth-class-fixture는 Local Browser 검증용 Auth/Class fixture를 개발 DB에 저장하는 dev-only 실행기다.
//
// production Bootstrap 경로가 아니다. 기존 bootstrap.Run과 auth.HashPassword를 그대로 재사용하며,
// 기존 row를 삭제·갱신하지 않는다. 같은 데이터가 이미 있으면 bootstrap.Run이 conflict로 실패한다.
// Migration은 실행하지 않는다. 먼저 `make dev-db-migrate`로 schema를 적용한다.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// credentialPath는 repository root 기준 상대 경로다. .local/은 gitignore 대상이다.
const credentialPath = ".local/dev-auth-class-fixture.json"

const (
	organizationName   = "Labbit Local Dev"
	adminUsername      = "dev-admin"
	instructorUsername = "dev-instructor"
	studentUsername    = "dev-student"
	alphaName          = "Dev Alpha"
	bravoName          = "Dev Bravo"
)

// dev fixture 전용 stable ID다. Secret이 아니며 실행할 때마다 새로 만들지 않는다.
var (
	organizationID = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000001")
	adminID        = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000011")
	instructorID   = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000012")
	studentID      = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000013")
	alphaID        = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000021")
	bravoID        = uuid.MustParse("d0d0d0d0-0000-4000-8000-000000000022")
)

var errNotDevelopment = errors.New("LABBIT_ENVIRONMENT=development에서만 실행할 수 있는 dev-only 도구입니다")

// credentials는 Local 검증자가 읽는 파일이다. password 원문이 들어 있으므로 Git에 commit하지 않는다.
type credentials struct {
	Password string            `json:"password"`
	Accounts map[string]string `json:"accounts"`
	Classes  map[string]string `json:"classes"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := run(ctx, os.Getenv("LABBIT_ENVIRONMENT"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dev-auth-class-fixture:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, environment string, out io.Writer) error {
	environment = strings.TrimSpace(environment)
	if environment != "development" {
		return errNotDevelopment
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dsn, err := postgres.LoadDSN(environment)
	if err != nil {
		return err
	}
	pool, err := postgres.OpenPool(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	return install(ctx, postgres.NewStore(pool), credentialPath, out)
}

// install은 credential file을 준비하고 fixture를 저장한다.
// 이번 실행에서 새로 만든 credential file은 저장에 실패하면 지워서 DB와 맞지 않는 password만 남지 않게 한다.
func install(ctx context.Context, tx repository.Transactor, path string, out io.Writer) (err error) {
	cred, created, err := loadOrCreateCredentials(path)
	if err != nil {
		return err
	}
	if created {
		defer func() {
			if err != nil {
				_ = os.Remove(path)
			}
		}()
	}

	spec, err := buildSpec(cred.Password)
	if err != nil {
		return err
	}
	if err := bootstrap.Run(ctx, tx, spec); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return fmt.Errorf("같은 fixture 또는 충돌하는 데이터가 이미 있어 아무것도 저장하지 않았습니다. "+
				"기존 row는 삭제·갱신하지 않습니다. 개발 DB 상태와 %s를 확인하세요: %w", path, err)
		}
		return fmt.Errorf("fixture 저장에 실패했습니다. make dev-db-up, make dev-db-migrate가 적용되었는지 확인하세요: %w", err)
	}

	fmt.Fprintf(out, "dev Auth/Class fixture를 저장했습니다. credential file: %s\n", path)
	return nil
}

// buildSpec은 role matrix를 bootstrap.Spec으로 만든다. 계정마다 HashPassword를 따로 호출해 독립 salt를 갖게 한다.
//
//	dev-admin      ADMIN,  Class Membership 없음
//	dev-instructor MEMBER, Dev Alpha INSTRUCTOR, Dev Bravo STUDENT
//	dev-student    MEMBER, Dev Alpha STUDENT
func buildSpec(password string) (bootstrap.Spec, error) {
	users := []struct {
		id       uuid.UUID
		username string
		role     repository.OrganizationRole
	}{
		{adminID, adminUsername, repository.OrganizationRoleAdmin},
		{instructorID, instructorUsername, repository.OrganizationRoleMember},
		{studentID, studentUsername, repository.OrganizationRoleMember},
	}

	spec := bootstrap.Spec{
		Organization: bootstrap.Organization{ID: organizationID, Name: organizationName},
		Classes: []bootstrap.Class{
			{ID: alphaID, Name: alphaName},
			{ID: bravoID, Name: bravoName},
		},
		Memberships: []bootstrap.Membership{
			{ClassID: alphaID, UserID: instructorID, Role: repository.ClassRoleInstructor},
			{ClassID: bravoID, UserID: instructorID, Role: repository.ClassRoleStudent},
			{ClassID: alphaID, UserID: studentID, Role: repository.ClassRoleStudent},
		},
	}
	for _, user := range users {
		hash, err := auth.HashPassword(password)
		if err != nil {
			return bootstrap.Spec{}, err
		}
		spec.Users = append(spec.Users, bootstrap.User{
			ID:               user.id,
			Username:         user.username,
			PasswordHash:     hash,
			OrganizationRole: user.role,
		})
	}
	return spec, nil
}

// loadOrCreateCredentials는 credential file이 있으면 그 password를 재사용하고, 없으면 새로 만든다.
// 기존 file은 수정하지 않는다.
func loadOrCreateCredentials(path string) (cred credentials, created bool, err error) {
	content, err := os.ReadFile(path)
	switch {
	case err == nil:
		// 파싱 오류에 file 내용이 섞이지 않도록 원인은 버리고 고정 문구만 반환한다.
		if json.Unmarshal(content, &cred) != nil || cred.Password == "" {
			return credentials{}, false, fmt.Errorf("credential file 형식이 올바르지 않습니다. 확인 후 삭제하고 다시 실행하세요: %s", path)
		}
		return cred, false, nil
	case !errors.Is(err, fs.ErrNotExist):
		return credentials{}, false, fmt.Errorf("credential file을 읽을 수 없습니다: %s: %w", path, err)
	}

	cred, err = newCredentials()
	if err != nil {
		return credentials{}, false, err
	}
	if err := writeCredentials(path, cred); err != nil {
		return credentials{}, false, err
	}
	return cred, true, nil
}

func newCredentials() (credentials, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return credentials{}, fmt.Errorf("password 생성 실패: %w", err)
	}
	return credentials{
		Password: base64.RawURLEncoding.EncodeToString(raw),
		Accounts: map[string]string{
			"admin":      adminUsername,
			"instructor": instructorUsername,
			"student":    studentUsername,
		},
		Classes: map[string]string{
			"alpha": alphaID.String(),
			"bravo": bravoID.String(),
		},
	}, nil
}

// writeCredentials는 0600 file을 새로 만든다. 이미 있으면 덮어쓰지 않고 실패한다.
func writeCredentials(path string, cred credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("credential 디렉터리를 만들 수 없습니다: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("credential file을 만들 수 없습니다: %s: %w", path, err)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cred); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("credential file을 쓸 수 없습니다: %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("credential file을 쓸 수 없습니다: %s: %w", path, err)
	}
	return nil
}
