//go:build integration

package postgres_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestRepositoryReadsBootstrappedIdentity(t *testing.T) {
	db := newTestDB(t)
	s := bootstrapSample(t, db, 1)
	ctx := t.Context()

	wantInstructor := repository.User{
		ID:               s.InstructorID,
		OrganizationID:   s.OrganizationID,
		OrganizationName: "Labbit Academy 1",
		OrganizationRole: repository.OrganizationRoleMember,
		Username:         s.instructorUsername(),
	}

	t.Run("LocalAccountByUsername이 NULL timestamp를 nil로 매핑한다", func(t *testing.T) {
		account, err := db.store.LocalAccountByUsername(ctx, s.instructorUsername())
		if err != nil {
			t.Fatalf("LocalAccountByUsername() error = %v", err)
		}
		if !reflect.DeepEqual(account.User, wantInstructor) {
			t.Errorf("User = %+v, want %+v", account.User, wantInstructor)
		}
		if account.User.DisabledAt != nil || account.User.PasswordChangedAt != nil {
			t.Errorf("NULL timestamp가 nil이 아닙니다: %+v", account.User)
		}
		if string(account.PasswordHash) != dummyPHC {
			t.Error("저장된 password hash가 그대로 반환되지 않았습니다")
		}
	})

	t.Run("ADMIN organizationRole을 그대로 전달한다", func(t *testing.T) {
		account, err := db.store.LocalAccountByUsername(ctx, s.adminUsername())
		if err != nil {
			t.Fatalf("LocalAccountByUsername() error = %v", err)
		}
		if account.User.ID != s.AdminID || account.User.OrganizationRole != repository.OrganizationRoleAdmin {
			t.Errorf("User = %+v", account.User)
		}
	})

	t.Run("없는 username은 typed Not Found다", func(t *testing.T) {
		_, err := db.store.LocalAccountByUsername(ctx, "no-such-user")
		assertOnlyKind(t, err, repository.ErrNotFound)
		if got := repositoryError(t, err).Op; got != "LocalAccountByUsername" {
			t.Errorf("Op = %q", got)
		}
	})

	t.Run("username은 정확히 일치해야 한다", func(t *testing.T) {
		// 대소문자 정규화 같은 정책은 Repository가 임의로 정하지 않는다.
		_, err := db.store.LocalAccountByUsername(ctx, "INSTRUCTOR-1")
		assertOnlyKind(t, err, repository.ErrNotFound)
	})

	t.Run("disabled와 Password 변경 시각을 그대로 전달하고 필터링하지 않는다", func(t *testing.T) {
		mustExec(t, db, `UPDATE users SET disabled_at = $2 WHERE id = $1`, s.StudentID, at(10))
		mustExec(t, db, `UPDATE local_accounts SET password_changed_at = $2 WHERE user_id = $1`, s.StudentID, at(11))

		account, err := db.store.LocalAccountByUsername(ctx, s.studentUsername())
		if err != nil {
			t.Fatalf("disabled 계정도 조회되어야 합니다: %v", err)
		}
		if account.User.DisabledAt == nil || !account.User.DisabledAt.Equal(at(10)) {
			t.Errorf("DisabledAt = %v, want %v", account.User.DisabledAt, at(10))
		}
		if account.User.PasswordChangedAt == nil || !account.User.PasswordChangedAt.Equal(at(11)) {
			t.Errorf("PasswordChangedAt = %v, want %v", account.User.PasswordChangedAt, at(11))
		}
	})

	t.Run("UserByID", func(t *testing.T) {
		user, err := db.store.UserByID(ctx, s.InstructorID)
		if err != nil {
			t.Fatalf("UserByID() error = %v", err)
		}
		if !reflect.DeepEqual(user, wantInstructor) {
			t.Errorf("User = %+v, want %+v", user, wantInstructor)
		}

		_, err = db.store.UserByID(ctx, testID(kindUser, 9999))
		assertOnlyKind(t, err, repository.ErrNotFound)
	})

	t.Run("Local Account가 없는 User도 조회된다", func(t *testing.T) {
		orphanID := testID(kindUser, 9001)
		if err := db.store.CreateUser(ctx, repository.NewUser{ID: orphanID, OrganizationID: s.OrganizationID, OrganizationRole: repository.OrganizationRoleMember}); err != nil {
			t.Fatal(err)
		}
		user, err := db.store.UserByID(ctx, orphanID)
		if err != nil {
			t.Fatalf("UserByID() error = %v", err)
		}
		if user.Username != "" || user.PasswordChangedAt != nil || user.OrganizationName != "Labbit Academy 1" {
			t.Errorf("User = %+v", user)
		}
	})
}

func TestRepositoryReadsClassesAndMemberships(t *testing.T) {
	db := newTestDB(t)
	s := bootstrapSample(t, db, 1)
	other := bootstrapSample(t, db, 2)
	ctx := t.Context()

	t.Run("ClassesByUser는 참여 Class와 역할을 이름 순서로 반환한다", func(t *testing.T) {
		items, err := db.store.ClassesByUser(ctx, s.InstructorID)
		if err != nil {
			t.Fatalf("ClassesByUser() error = %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("len(items) = %d, want 2: %+v", len(items), items)
		}
		want := []struct {
			id   uuid.UUID
			name string
			role repository.ClassRole
		}{
			{s.AlphaID, "Alpha", repository.ClassRoleInstructor},
			{s.BravoID, "Bravo", repository.ClassRoleStudent},
		}
		for i, w := range want {
			got := items[i]
			if got.Class.ID != w.id || got.Class.Name != w.name || got.Role != w.role {
				t.Errorf("items[%d] = %+v, want %+v", i, got, w)
			}
			if got.Class.OrganizationID != s.OrganizationID || got.Class.CreatedAt.IsZero() {
				t.Errorf("items[%d].Class = %+v", i, got.Class)
			}
		}
	})

	t.Run("ClassesByUser는 다른 Organization의 Class를 포함하지 않는다", func(t *testing.T) {
		items, err := db.store.ClassesByUser(ctx, s.StudentID)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].Class.ID != s.AlphaID || items[0].Role != repository.ClassRoleStudent {
			t.Errorf("items = %+v", items)
		}
	})

	t.Run("Membership이 없는 ADMIN은 빈 목록이며 오류가 아니다", func(t *testing.T) {
		// ADMIN이라는 이유로 Class를 추가해 주는 것은 Repository가 아니라 Application의 권한 판단이다.
		items, err := db.store.ClassesByUser(ctx, s.AdminID)
		if err != nil {
			t.Fatalf("ClassesByUser() error = %v", err)
		}
		if len(items) != 0 {
			t.Errorf("items = %+v, want empty", items)
		}
	})

	t.Run("ClassByID", func(t *testing.T) {
		class, err := db.store.ClassByID(ctx, s.AlphaID)
		if err != nil {
			t.Fatalf("ClassByID() error = %v", err)
		}
		if class.ID != s.AlphaID || class.OrganizationID != s.OrganizationID || class.Name != "Alpha" || class.CreatedAt.IsZero() {
			t.Errorf("Class = %+v", class)
		}

		// 다른 Organization 사용자가 요청해도 Repository는 Class를 숨기지 않는다. 403/404 판단은 Application 책임이다.
		if _, err := db.store.ClassByID(ctx, other.AlphaID); err != nil {
			t.Errorf("다른 Organization의 Class 조회 error = %v", err)
		}

		_, err = db.store.ClassByID(ctx, testID(kindClass, 9999))
		assertOnlyKind(t, err, repository.ErrNotFound)
	})

	t.Run("ClassMembership", func(t *testing.T) {
		membership, err := db.store.ClassMembership(ctx, s.AlphaID, s.StudentID)
		if err != nil {
			t.Fatalf("ClassMembership() error = %v", err)
		}
		if membership.OrganizationID != s.OrganizationID || membership.ClassID != s.AlphaID ||
			membership.UserID != s.StudentID || membership.Role != repository.ClassRoleStudent || membership.CreatedAt.IsZero() {
			t.Errorf("ClassMembership = %+v", membership)
		}

		// 같은 User라도 Class마다 역할이 다르다.
		bravo, err := db.store.ClassMembership(ctx, s.BravoID, s.InstructorID)
		if err != nil || bravo.Role != repository.ClassRoleStudent {
			t.Errorf("Bravo membership = %+v, %v", bravo, err)
		}

		// 참여 관계가 없는 조합은 Not Found이며 Repository가 403/404를 정하지 않는다.
		for name, ids := range map[string][2]uuid.UUID{
			"참여하지 않은 Class":   {s.BravoID, s.StudentID},
			"ADMIN":           {s.AlphaID, s.AdminID},
			"다른 Organization": {other.AlphaID, s.StudentID},
		} {
			_, err := db.store.ClassMembership(ctx, ids[0], ids[1])
			if !errors.Is(err, repository.ErrNotFound) {
				t.Errorf("%s: error = %v, want Not Found", name, err)
			}
		}
	})
}

func TestRepositoryAuthSessions(t *testing.T) {
	db := newTestDB(t)
	s := bootstrapSample(t, db, 1)
	ctx := t.Context()

	tokenHash := bytes.Repeat([]byte{0xa1}, 32)
	session := repository.AuthSession{
		ID:        testID(kindSession, 1),
		UserID:    s.InstructorID,
		TokenHash: tokenHash,
		CreatedAt: at(9),
		ExpiresAt: at(17),
	}
	if err := db.store.CreateAuthSession(ctx, session); err != nil {
		t.Fatalf("CreateAuthSession() error = %v", err)
	}

	t.Run("token digest로 Session과 User 상태를 조회한다", func(t *testing.T) {
		found, err := db.store.AuthSessionByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatalf("AuthSessionByTokenHash() error = %v", err)
		}
		got := found.Session
		if got.ID != session.ID || got.UserID != s.InstructorID || !bytes.Equal(got.TokenHash, tokenHash) ||
			!got.CreatedAt.Equal(at(9)) || !got.ExpiresAt.Equal(at(17)) {
			t.Errorf("Session = %+v", got)
		}
		if got.LastSeenAt != nil || got.RevokedAt != nil {
			t.Errorf("NULL timestamp가 nil이 아닙니다: %+v", got)
		}
		if found.User.ID != s.InstructorID || found.User.OrganizationID != s.OrganizationID ||
			found.User.Username != s.instructorUsername() || found.User.OrganizationName != "Labbit Academy 1" {
			t.Errorf("User = %+v", found.User)
		}
	})

	t.Run("없는 token digest는 typed Not Found다", func(t *testing.T) {
		_, err := db.store.AuthSessionByTokenHash(ctx, bytes.Repeat([]byte{0x00}, 32))
		assertOnlyKind(t, err, repository.ErrNotFound)
	})

	t.Run("만료된 Session도 필터링하지 않는다", func(t *testing.T) {
		// 유효성 판정은 LBT-10 Application이 시각·revoke·User 상태로 수행한다.
		expiredHash := bytes.Repeat([]byte{0xb2}, 32)
		expired := repository.AuthSession{ID: testID(kindSession, 2), UserID: s.StudentID, TokenHash: expiredHash, CreatedAt: at(1), ExpiresAt: at(2)}
		if err := db.store.CreateAuthSession(ctx, expired); err != nil {
			t.Fatal(err)
		}
		found, err := db.store.AuthSessionByTokenHash(ctx, expiredHash)
		if err != nil {
			t.Fatalf("만료된 Session도 조회되어야 합니다: %v", err)
		}
		if !found.Session.ExpiresAt.Equal(at(2)) {
			t.Errorf("ExpiresAt = %v", found.Session.ExpiresAt)
		}
	})

	t.Run("User의 disabled와 Password 변경 시각이 Session 조회에 포함된다", func(t *testing.T) {
		mustExec(t, db, `UPDATE users SET disabled_at = $2 WHERE id = $1`, s.InstructorID, at(12))
		mustExec(t, db, `UPDATE local_accounts SET password_changed_at = $2 WHERE user_id = $1`, s.InstructorID, at(13))

		found, err := db.store.AuthSessionByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if found.User.DisabledAt == nil || !found.User.DisabledAt.Equal(at(12)) ||
			found.User.PasswordChangedAt == nil || !found.User.PasswordChangedAt.Equal(at(13)) {
			t.Errorf("User = %+v", found.User)
		}
	})

	t.Run("RevokeAuthSession은 처음 기록한 시각을 유지한다", func(t *testing.T) {
		if err := db.store.RevokeAuthSession(ctx, session.ID, at(14)); err != nil {
			t.Fatalf("RevokeAuthSession() error = %v", err)
		}
		if err := db.store.RevokeAuthSession(ctx, session.ID, at(15)); err != nil {
			t.Fatalf("이미 revoke된 Session의 재요청도 성공해야 합니다: %v", err)
		}

		found, err := db.store.AuthSessionByTokenHash(ctx, tokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if found.Session.RevokedAt == nil || !found.Session.RevokedAt.Equal(at(14)) {
			t.Errorf("RevokedAt = %v, want %v", found.Session.RevokedAt, at(14))
		}
	})

	t.Run("없는 Session revoke는 typed Not Found다", func(t *testing.T) {
		err := db.store.RevokeAuthSession(ctx, testID(kindSession, 9999), at(14))
		assertOnlyKind(t, err, repository.ErrNotFound)
	})
}
