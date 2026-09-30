package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeTransactor는 저장 경계만 대신한다. fn을 실행하지 않고 err를 그대로 반환한다.
// bootstrap.Run은 spec 검증에 통과해야 WithinTransaction을 호출하므로 호출 횟수로 spec의 유효성도 확인할 수 있다.
type fakeTransactor struct {
	calls int
	err   error
}

func (f *fakeTransactor) WithinTransaction(context.Context, func(context.Context, repository.Repositories) error) error {
	f.calls++
	return f.err
}

// testPassword는 실제 credential이 아닌 test 실행마다 새로 만드는 값이다.
func testPassword(t *testing.T) string {
	t.Helper()
	cred, err := newCredentials()
	if err != nil {
		t.Fatalf("newCredentials() error = %v", err)
	}
	return cred.Password
}

func TestBuildSpecRoleMatrix(t *testing.T) {
	spec, err := buildSpec(testPassword(t))
	if err != nil {
		t.Fatalf("buildSpec() error = %v", err)
	}

	if len(spec.Users) != 3 || len(spec.Classes) != 2 || len(spec.Memberships) != 3 {
		t.Fatalf("users/classes/memberships = %d/%d/%d, want 3/2/3", len(spec.Users), len(spec.Classes), len(spec.Memberships))
	}
	if spec.Organization.Name != "Labbit Local Dev" {
		t.Errorf("Organization name = %q, want Labbit Local Dev", spec.Organization.Name)
	}

	wantRole := map[string]repository.OrganizationRole{
		"dev-admin":      repository.OrganizationRoleAdmin,
		"dev-instructor": repository.OrganizationRoleMember,
		"dev-student":    repository.OrganizationRoleMember,
	}
	usernames := map[uuid.UUID]string{}
	for _, user := range spec.Users {
		if want, ok := wantRole[user.Username]; !ok || user.OrganizationRole != want {
			t.Errorf("user %q organizationRole = %v, want %v", user.Username, user.OrganizationRole, want)
		}
		usernames[user.ID] = user.Username
	}
	if len(usernames) != 3 {
		t.Fatalf("distinct users = %d, want 3", len(usernames))
	}

	classNames := map[uuid.UUID]string{}
	for _, class := range spec.Classes {
		classNames[class.ID] = class.Name
	}
	if classNames[alphaID] != "Dev Alpha" || classNames[bravoID] != "Dev Bravo" {
		t.Errorf("classes = %v, want Dev Alpha/Dev Bravo", classNames)
	}

	// username -> class name -> role. ADMIN은 Class 역할이 없고, 같은 User도 Class별 역할이 다를 수 있다(D-11).
	got := map[string]map[string]repository.ClassRole{}
	for _, m := range spec.Memberships {
		username, class := usernames[m.UserID], classNames[m.ClassID]
		if username == "" || class == "" {
			t.Fatalf("membership %+v이 spec 밖의 User/Class를 참조합니다", m)
		}
		if got[username] == nil {
			got[username] = map[string]repository.ClassRole{}
		}
		got[username][class] = m.Role
	}
	want := map[string]map[string]repository.ClassRole{
		"dev-instructor": {"Dev Alpha": repository.ClassRoleInstructor, "Dev Bravo": repository.ClassRoleStudent},
		"dev-student":    {"Dev Alpha": repository.ClassRoleStudent},
	}
	if len(got) != len(want) {
		t.Errorf("memberships by user = %v, want %v (dev-admin은 Membership이 없어야 합니다)", got, want)
	}
	for username, classes := range want {
		if len(got[username]) != len(classes) {
			t.Errorf("%s memberships = %v, want %v", username, got[username], classes)
		}
		for class, role := range classes {
			if got[username][class] != role {
				t.Errorf("%s %s role = %v, want %v", username, class, got[username][class], role)
			}
		}
	}

	// 기존 bootstrap.Run 검증을 통과해야 한다. 통과하면 fake의 WithinTransaction까지 도달한다.
	fake := &fakeTransactor{}
	if err := bootstrap.Run(t.Context(), fake, spec); err != nil {
		t.Fatalf("bootstrap.Run() error = %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("WithinTransaction calls = %d, want 1", fake.calls)
	}
}

func TestBuildSpecStoresIndependentArgon2idHashes(t *testing.T) {
	password := testPassword(t)
	spec, err := buildSpec(password)
	if err != nil {
		t.Fatalf("buildSpec() error = %v", err)
	}

	seen := map[repository.PasswordHash]bool{}
	for _, user := range spec.Users {
		hash := string(user.PasswordHash)
		if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, password) {
			t.Errorf("%s password hash가 Argon2id PHC 형식이 아니거나 원문을 포함합니다", user.Username)
		}
		if ok, err := (auth.Argon2id{}).Verify(user.PasswordHash, password); err != nil || !ok {
			t.Errorf("%s hash가 password로 검증되지 않습니다: ok=%v err=%v", user.Username, ok, err)
		}
		seen[user.PasswordHash] = true
	}
	if len(seen) != len(spec.Users) {
		t.Errorf("distinct hashes = %d, want %d (계정마다 독립 salt)", len(seen), len(spec.Users))
	}
}

func TestInstallCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".local", "fixture.json")

	t.Run("새 credential file은 0600으로 만들고 성공 메시지에는 path만 남긴다", func(t *testing.T) {
		fake := &fakeTransactor{}
		var out bytes.Buffer
		if err := install(t.Context(), fake, path, &out); err != nil {
			t.Fatalf("install() error = %v", err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
		cred := readCredentials(t, path)
		if cred.Password == "" || cred.Accounts["admin"] != "dev-admin" || cred.Accounts["instructor"] != "dev-instructor" || cred.Accounts["student"] != "dev-student" {
			t.Errorf("credentials = %+v", cred)
		}
		if cred.Classes["alpha"] != alphaID.String() || cred.Classes["bravo"] != bravoID.String() {
			t.Errorf("classes = %v", cred.Classes)
		}
		if !strings.Contains(out.String(), path) || strings.Contains(out.String(), cred.Password) {
			t.Errorf("output = %q, want path만 포함", out.String())
		}
	})

	t.Run("기존 credential file은 password를 재사용하고 수정하지 않는다", func(t *testing.T) {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := install(t.Context(), &fakeTransactor{}, path, &bytes.Buffer{}); err != nil {
			t.Fatalf("install() error = %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("기존 credential file이 변경되었습니다")
		}
	})

	t.Run("기존 credential file은 저장이 실패해도 지우지 않는다", func(t *testing.T) {
		fake := &fakeTransactor{err: repository.ErrConflict}
		if err := install(t.Context(), fake, path, &bytes.Buffer{}); !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("기존 credential file이 사라졌습니다: %v", err)
		}
	})
}

func TestInstallRemovesNewCredentialFileOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	fake := &fakeTransactor{err: repository.ErrConflict}

	err := install(t.Context(), fake, path, &bytes.Buffer{})
	if !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want credential file path 안내", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("실패한 실행이 만든 credential file이 남았습니다: %v", statErr)
	}
}

func TestInstallRejectsMalformedCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(path, []byte(`{"password": ""}`), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &fakeTransactor{}
	if err := install(t.Context(), fake, path, &bytes.Buffer{}); err == nil {
		t.Fatal("error = nil, want malformed credential file 오류")
	}
	if fake.calls != 0 {
		t.Errorf("WithinTransaction calls = %d, want 0", fake.calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("기존 credential file이 사라졌습니다: %v", err)
	}
}

func TestRunRefusesOutsideDevelopment(t *testing.T) {
	// guard가 DB 설정을 읽기 전에 거부해야 하므로 DSN이 없어도 같은 오류여야 한다.
	t.Setenv("LABBIT_DATABASE_DSN", "")
	t.Setenv("LABBIT_DATABASE_DSN_FILE", "")

	for _, environment := range []string{"", "production", "staging", "Development"} {
		if err := run(t.Context(), environment, &bytes.Buffer{}); !errors.Is(err, errNotDevelopment) {
			t.Errorf("run(%q) error = %v, want errNotDevelopment", environment, err)
		}
	}
}

func readCredentials(t *testing.T, path string) credentials {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cred credentials
	if err := json.Unmarshal(content, &cred); err != nil {
		t.Fatalf("credential file parse error = %v", err)
	}
	return cred
}
