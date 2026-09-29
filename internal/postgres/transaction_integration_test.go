//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func newOrganizationInput(n int) repository.NewOrganization {
	return repository.NewOrganization{ID: testID(kindOrganization, n), Name: "Transaction Org"}
}

// assertNoLeakedConnections는 transaction이 끝난 뒤 connection이 열린 transaction과 함께 pool에 남지 않았음을 확인한다.
func assertNoLeakedConnections(t *testing.T, db *testDB) {
	t.Helper()
	if n := db.pool.Stat().AcquiredConns(); n != 0 {
		t.Errorf("AcquiredConns = %d, want 0", n)
	}
}

func TestWithinTransactionCommits(t *testing.T) {
	db := newTestDB(t)
	ctx := t.Context()
	org := newOrganizationInput(1)
	userID := testID(kindUser, 1)

	err := db.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
		if err := repos.CreateOrganization(ctx, org); err != nil {
			return err
		}
		if err := repos.CreateUser(ctx, repository.NewUser{ID: userID, OrganizationID: org.ID, OrganizationRole: repository.OrganizationRoleAdmin}); err != nil {
			return err
		}
		if err := repos.CreateLocalAccount(ctx, repository.NewLocalAccount{UserID: userID, Username: "tx-admin", PasswordHash: dummyPHC}); err != nil {
			return err
		}

		// transaction 안의 Repository는 자기 변경을 볼 수 있지만 다른 session에는 commit 전까지 보이지 않는다.
		if _, err := repos.LocalAccountByUsername(ctx, "tx-admin"); err != nil {
			t.Errorf("transaction 안에서 자기 변경을 읽지 못했습니다: %v", err)
		}
		if _, err := db.store.LocalAccountByUsername(ctx, "tx-admin"); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("commit 전 변경이 다른 session에 보입니다: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}

	account, err := db.store.LocalAccountByUsername(ctx, "tx-admin")
	if err != nil {
		t.Fatalf("commit 후 조회 실패: %v", err)
	}
	if account.User.ID != userID || account.User.OrganizationName != "Transaction Org" {
		t.Errorf("account = %+v", account)
	}
	assertNoLeakedConnections(t, db)
}

func TestWithinTransactionRollsBack(t *testing.T) {
	errApplication := errors.New("application decided to abort")
	panicValue := "boom"

	tests := []struct {
		name string
		// run은 transaction 안에서 Organization을 만든 뒤 각자의 방식으로 실패한다.
		run func(t *testing.T, db *testDB, org repository.NewOrganization) error
		// check는 WithinTransaction의 반환값을 검사한다.
		check func(t *testing.T, err error)
	}{
		{
			name: "callback 오류는 그대로 반환하고 rollback한다",
			run: func(t *testing.T, db *testDB, org repository.NewOrganization) error {
				return db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
					if err := repos.CreateOrganization(ctx, org); err != nil {
						return err
					}
					return errApplication
				})
			},
			check: func(t *testing.T, err error) {
				if err != errApplication {
					t.Errorf("error = %v, want callback 오류 그대로", err)
				}
			},
		},
		{
			name: "Repository 오류로 실패한 뒤 앞선 변경도 남지 않는다",
			run: func(t *testing.T, db *testDB, org repository.NewOrganization) error {
				return db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
					if err := repos.CreateOrganization(ctx, org); err != nil {
						return err
					}
					// 존재하지 않는 User의 Local Account는 FK 위반이다.
					return repos.CreateLocalAccount(ctx, repository.NewLocalAccount{UserID: testID(kindUser, 9999), Username: "ghost", PasswordHash: dummyPHC})
				})
			},
			check: func(t *testing.T, err error) {
				assertOnlyKind(t, err, repository.ErrConstraintViolation)
			},
		},
		{
			name: "DB 오류를 삼키고 nil을 반환해도 commit 실패로 처리한다",
			run: func(t *testing.T, db *testDB, org repository.NewOrganization) error {
				return db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
					if err := repos.CreateOrganization(ctx, org); err != nil {
						return err
					}
					if err := repos.CreateUser(ctx, repository.NewUser{ID: testID(kindUser, 1), OrganizationID: testID(kindOrganization, 9999), OrganizationRole: repository.OrganizationRoleMember}); !errors.Is(err, repository.ErrConstraintViolation) {
						t.Errorf("CreateUser() error = %v, want constraint violation", err)
					}
					// 실패한 statement 뒤의 transaction은 aborted 상태이므로 이후 statement도 성공할 수 없다.
					err := repos.CreateClass(ctx, repository.NewClass{ID: testID(kindClass, 1), OrganizationID: org.ID, Name: "after abort"})
					if !errors.Is(err, repository.ErrInternal) || repositoryError(t, err).SQLState != "25P02" {
						t.Errorf("aborted transaction의 다음 statement error = %v", err)
					}
					return nil
				})
			},
			check: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("commit 실패가 성공으로 처리되었습니다")
				}
				assertOnlyKind(t, err, repository.ErrInternal)
			},
		},
		{
			name: "요청 context가 취소되어도 rollback한다",
			run: func(t *testing.T, db *testDB, org repository.NewOrganization) error {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				return db.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
					if err := repos.CreateOrganization(ctx, org); err != nil {
						return err
					}
					cancel()
					return ctx.Err()
				})
			},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("error = %v, want context.Canceled", err)
				}
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			org := newOrganizationInput(i + 1)

			tt.check(t, tt.run(t, db, org))

			if n := countRows(t, db, `SELECT count(*) FROM organizations`); n != 0 {
				t.Errorf("rollback되지 않고 organizations %d개가 남았습니다", n)
			}
			assertNoLeakedConnections(t, db)
		})
	}

	t.Run("panic도 rollback하고 다시 전파한다", func(t *testing.T) {
		db := newTestDB(t)
		org := newOrganizationInput(1)

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = db.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
				if err := repos.CreateOrganization(ctx, org); err != nil {
					return err
				}
				panic(panicValue)
			})
		}()

		if recovered != panicValue {
			t.Errorf("recovered = %v, want %v", recovered, panicValue)
		}
		if n := countRows(t, db, `SELECT count(*) FROM organizations`); n != 0 {
			t.Errorf("panic 후 organizations %d개가 남았습니다", n)
		}
		assertNoLeakedConnections(t, db)
	})
}

func TestWithinTransactionDoesNotStartWithCanceledContext(t *testing.T) {
	db := newTestDB(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	called := false
	err := db.store.WithinTransaction(ctx, func(context.Context, repository.Repositories) error {
		called = true
		return nil
	})

	if called {
		t.Error("transaction을 시작하지 못했는데 callback이 실행되었습니다")
	}
	if !errors.Is(err, context.Canceled) || !errors.Is(err, repository.ErrInternal) {
		t.Errorf("error = %v, want context.Canceled로 판별 가능한 internal 오류", err)
	}
	assertNoRawDatabaseError(t, err)
}

// 로그인 시 presented Session 교체는 새 Session 발급과 기존 Session revoke가 함께 성공하거나 함께 실패해야 한다.
// Application이 원자성 범위를 정하는 대표 사례다.
func TestWithinTransactionReplacesSessionAtomically(t *testing.T) {
	db := newTestDB(t)
	s := bootstrapSample(t, db, 1)
	ctx := t.Context()

	oldSession := func(n int) repository.AuthSession {
		return repository.AuthSession{
			ID: testID(kindSession, n), UserID: s.InstructorID,
			TokenHash: bytes.Repeat([]byte{byte(n)}, 32), CreatedAt: at(9), ExpiresAt: at(17),
		}
	}
	replace := func(old, fresh repository.AuthSession) error {
		return db.store.WithinTransaction(ctx, func(ctx context.Context, repos repository.Repositories) error {
			// 실패 사례에서 revoke가 먼저 반영된 뒤 INSERT가 실패해도 revoke가 남지 않아야 하므로 revoke를 먼저 실행한다.
			if err := repos.RevokeAuthSession(ctx, old.ID, at(10)); err != nil {
				return err
			}
			return repos.CreateAuthSession(ctx, fresh)
		})
	}
	revokedAt := func(t *testing.T, session repository.AuthSession) bool {
		t.Helper()
		found, err := db.store.AuthSessionByTokenHash(ctx, session.TokenHash)
		if err != nil {
			t.Fatalf("AuthSessionByTokenHash() error = %v", err)
		}
		return found.Session.RevokedAt != nil
	}

	t.Run("성공하면 교체가 함께 반영된다", func(t *testing.T) {
		old, fresh := oldSession(1), oldSession(2)
		if err := db.store.CreateAuthSession(ctx, old); err != nil {
			t.Fatal(err)
		}
		if err := replace(old, fresh); err != nil {
			t.Fatalf("replace() error = %v", err)
		}
		if !revokedAt(t, old) {
			t.Error("기존 Session이 revoke되지 않았습니다")
		}
		if revokedAt(t, fresh) {
			t.Error("새 Session이 revoke되었습니다")
		}
	})

	t.Run("새 Session 저장이 실패하면 기존 Session revoke도 취소된다", func(t *testing.T) {
		old, existing := oldSession(3), oldSession(4)
		for _, session := range []repository.AuthSession{old, existing} {
			if err := db.store.CreateAuthSession(ctx, session); err != nil {
				t.Fatal(err)
			}
		}
		// 이미 저장된 token digest와 충돌하는 새 Session이다.
		conflicting := oldSession(5)
		conflicting.TokenHash = existing.TokenHash

		err := replace(old, conflicting)

		assertOnlyKind(t, err, repository.ErrConflict)
		if revokedAt(t, old) {
			t.Error("실패한 교체에서 기존 Session revoke가 남았습니다")
		}
	})
}
