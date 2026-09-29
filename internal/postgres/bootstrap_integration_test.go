//go:build integration

package postgres_test

import (
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func TestBootstrapCreatesEveryEntityAndRepositoryReadsIt(t *testing.T) {
	db := newTestDB(t)
	spec, s := sampleSpec(1)
	ctx := t.Context()

	if err := bootstrap.Run(ctx, db.store, spec); err != nil {
		t.Fatalf("bootstrap.Run() error = %v", err)
	}

	got := countOrganizationRows(t, db, s.OrganizationID)
	want := orgRows{Organizations: 1, Users: 3, LocalAccounts: 3, Classes: 2, Memberships: 3}
	if got != want {
		t.Fatalf("row 수 = %+v, want %+v", got, want)
	}

	// Bootstrap 결과를 로그인·Class 목록 query가 그대로 사용할 수 있다.
	account, err := db.store.LocalAccountByUsername(ctx, s.instructorUsername())
	if err != nil {
		t.Fatalf("Bootstrap한 계정을 조회하지 못했습니다: %v", err)
	}
	classes, err := db.store.ClassesByUser(ctx, account.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(classes) != 2 || classes[0].Role != repository.ClassRoleInstructor {
		t.Errorf("Class 목록 = %+v", classes)
	}
}

// Organization과 User가 이미 저장된 뒤 LocalAccount 단계가 실패해도 일부 운영 데이터만 남지 않아야 한다.
func TestBootstrapIsAtomicWhenLocalAccountFails(t *testing.T) {
	tests := []struct {
		name string
		// failAt은 이미 사용 중인 username을 갖게 할 spec 안의 User 위치다.
		failAt int
	}{
		{name: "첫 번째 User의 Local Account에서 실패", failAt: 0},
		{name: "마지막 User의 Local Account에서 실패", failAt: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			existing := bootstrapSample(t, db, 1)
			before := countOrganizationRows(t, db, existing.OrganizationID)

			// 두 번째 Organization의 한 User가 이미 사용 중인 username을 요구한다.
			spec, second := sampleSpec(2)
			spec.Users[tt.failAt].Username = existing.instructorUsername()

			err := bootstrap.Run(t.Context(), db.store, spec)

			assertOnlyKind(t, err, repository.ErrConflict)
			if got := countOrganizationRows(t, db, second.OrganizationID); got != (orgRows{}) {
				t.Errorf("실패한 Bootstrap의 일부 데이터가 남았습니다: %+v", got)
			}
			if got := countOrganizationRows(t, db, existing.OrganizationID); got != before {
				t.Errorf("기존 Organization 데이터가 변경되었습니다: %+v, want %+v", got, before)
			}
			assertNoLeakedConnections(t, db)

			// 원인을 고친 뒤 같은 ID로 다시 실행하면 성공한다. 실패가 재실행을 막지 않는다.
			spec.Users[tt.failAt].Username = "fixed-username"
			if err := bootstrap.Run(t.Context(), db.store, spec); err != nil {
				t.Fatalf("수정 후 재실행 error = %v", err)
			}
			want := orgRows{Organizations: 1, Users: 3, LocalAccounts: 3, Classes: 2, Memberships: 3}
			if got := countOrganizationRows(t, db, second.OrganizationID); got != want {
				t.Errorf("재실행 후 row 수 = %+v, want %+v", got, want)
			}
		})
	}
}

func TestBootstrapRerunWithSameIDsConflictsWithoutChangingData(t *testing.T) {
	db := newTestDB(t)
	spec, s := sampleSpec(1)
	if err := bootstrap.Run(t.Context(), db.store, spec); err != nil {
		t.Fatal(err)
	}
	before := countOrganizationRows(t, db, s.OrganizationID)

	err := bootstrap.Run(t.Context(), db.store, spec)

	assertOnlyKind(t, err, repository.ErrConflict)
	if got := countOrganizationRows(t, db, s.OrganizationID); got != before {
		t.Errorf("재실행 실패 후 row 수 = %+v, want %+v", got, before)
	}
}

// 서로 다른 Organization의 Bootstrap 데이터는 Repository query에서도 섞이지 않는다.
func TestBootstrapKeepsOrganizationsSeparate(t *testing.T) {
	db := newTestDB(t)
	first := bootstrapSample(t, db, 1)
	second := bootstrapSample(t, db, 2)

	items, err := db.store.ClassesByUser(t.Context(), second.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Class.ID != second.AlphaID || items[0].Class.OrganizationID != second.OrganizationID {
		t.Errorf("Class 목록 = %+v", items)
	}
	if items[0].Class.ID == first.AlphaID {
		t.Error("다른 Organization의 Class가 섞였습니다")
	}
}
