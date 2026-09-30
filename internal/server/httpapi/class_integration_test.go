//go:build integration

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/server/bootstrap"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	nameA1 = "A1 Algorithms"
	nameA2 = "A2 Databases"
	nameA3 = "A3 Networks"
	nameB1 = "B1 Security"
)

// classFixture는 실제 PostgreSQL 위의 두 Organization이다.
//
//	Organization A
//	  alice  (MEMBER): A1 INSTRUCTOR, A2 STUDENT, A3 Membership 없음
//	  admin  (ADMIN):  A2 STUDENT만. A1·A3는 같은 Organization이지만 Membership 없음
//	  loner  (MEMBER): Membership 없음
//	Organization B
//	  bob    (MEMBER): B1 INSTRUCTOR
type classFixture struct {
	*stack
	orgA, orgB     uuid.UUID
	a1, a2, a3, b1 uuid.UUID
	alice, admin   uuid.UUID
	loner, bob     uuid.UUID
}

func newClassFixture(t *testing.T) *classFixture {
	t.Helper()
	f := &classFixture{
		orgA: uuid.New(), orgB: uuid.New(),
		a1: uuid.New(), a2: uuid.New(), a3: uuid.New(), b1: uuid.New(),
		alice: uuid.New(), admin: uuid.New(), loner: uuid.New(), bob: uuid.New(),
	}
	f.stack = newStackWith(t, func(hash repository.PasswordHash) []bootstrap.Spec {
		user := func(id uuid.UUID, username string, role repository.OrganizationRole) bootstrap.User {
			return bootstrap.User{ID: id, Username: username, PasswordHash: hash, OrganizationRole: role}
		}
		return []bootstrap.Spec{
			{
				Organization: bootstrap.Organization{ID: f.orgA, Name: "Org A"},
				Users: []bootstrap.User{
					user(f.alice, "alice", repository.OrganizationRoleMember),
					user(f.admin, "admin", repository.OrganizationRoleAdmin),
					user(f.loner, "loner", repository.OrganizationRoleMember),
				},
				Classes: []bootstrap.Class{{ID: f.a1, Name: nameA1}, {ID: f.a2, Name: nameA2}, {ID: f.a3, Name: nameA3}},
				Memberships: []bootstrap.Membership{
					{ClassID: f.a1, UserID: f.alice, Role: repository.ClassRoleInstructor},
					{ClassID: f.a2, UserID: f.alice, Role: repository.ClassRoleStudent},
					{ClassID: f.a2, UserID: f.admin, Role: repository.ClassRoleStudent},
				},
			},
			{
				Organization: bootstrap.Organization{ID: f.orgB, Name: "Org B"},
				Users:        []bootstrap.User{user(f.bob, "bob", repository.OrganizationRoleMember)},
				Classes:      []bootstrap.Class{{ID: f.b1, Name: nameB1}},
				Memberships:  []bootstrap.Membership{{ClassID: f.b1, UserID: f.bob, Role: repository.ClassRoleInstructor}},
			},
		}
	})
	return f
}

// loginAs는 실제 Login으로 Session token을 발급받는다.
func (s *stack) loginAs(t *testing.T, username string) string {
	t.Helper()
	return issuedToken(t, s.login(username, password))
}

func (s *stack) classes(token string) *httptest.ResponseRecorder {
	return s.send(http.MethodGet, "/api/v1/classes", "", withCookie(token))
}

func (s *stack) class(token, classID string) *httptest.ResponseRecorder {
	return s.send(http.MethodGet, "/api/v1/classes/"+classID, "", withCookie(token))
}

// classBody는 OpenAPI ClassSummary/ClassDetail의 필수 필드다. 알 수 없는 필드가 있으면 decode가 실패한다.
type classBody struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	MyRole string `json:"myRole"`
}

func decodeStrict(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	decoder := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		t.Fatalf("응답 JSON이 OpenAPI 형태와 다릅니다: %s (%v)", rec.Body.String(), err)
	}
}

func decodeClassList(t *testing.T, rec *httptest.ResponseRecorder) []classBody {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /classes = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []classBody `json:"items"`
	}
	decodeStrict(t, rec, &list)
	if list.Items == nil {
		t.Fatalf("items가 null입니다: %s", rec.Body.String())
	}
	return list.Items
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", got)
	}
	var problem struct {
		Status    int    `json:"status"`
		Code      string `json:"code"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Status != rec.Code || problem.RequestID == "" {
		t.Fatalf("Problem Details = %s (%v)", rec.Body.String(), err)
	}
	return problem.Code
}

func TestListClassesAgainstPostgres(t *testing.T) {
	f := newClassFixture(t)
	a1 := classBody{ID: f.a1.String(), Name: nameA1, MyRole: "INSTRUCTOR"}
	a2 := classBody{ID: f.a2.String(), Name: nameA2, MyRole: "STUDENT"}

	tests := []struct {
		username string
		want     []classBody
	}{
		// Membership이 있는 Class만, 그 Class에서의 실제 역할로 반환한다. A3(Membership 없음)와 B1(다른 Organization)은 없다.
		{username: "alice", want: []classBody{a1, a2}},
		// ADMIN은 Membership이 있는 A2만 본다. 같은 Organization의 A1·A3는 목록에 없다.
		{username: "admin", want: []classBody{a2}},
		// Membership이 0개면 오류가 아니라 빈 목록이다.
		{username: "loner", want: []classBody{}},
		{username: "bob", want: []classBody{{ID: f.b1.String(), Name: nameB1, MyRole: "INSTRUCTOR"}}},
	}
	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			rec := f.classes(f.loginAs(t, tt.username))

			got := decodeClassList(t, rec)

			if len(got) != len(tt.want) {
				t.Fatalf("items = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("items[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
			if tt.username == "loner" && strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
				t.Fatalf("빈 목록 body = %s, want {\"items\":[]}", rec.Body.String())
			}
		})
	}
}

// 목록은 Repository의 ORDER BY c.name, c.id 의미를 따른다. 이름이 같으면 ID 순서다.
func TestListClassesOrdersByNameThenID(t *testing.T) {
	var (
		orgID  = uuid.New()
		userID = uuid.New()
		beta   = uuid.MustParse("ffffffff-0000-4000-8000-000000000001")
		alphaL = uuid.MustParse("11111111-0000-4000-8000-000000000002")
		alphaH = uuid.MustParse("eeeeeeee-0000-4000-8000-000000000003")
	)
	s := newStackWith(t, func(hash repository.PasswordHash) []bootstrap.Spec {
		// 저장 순서(beta, alphaH, alphaL)와 기대 순서가 다르도록 일부러 섞어 넣는다.
		return []bootstrap.Spec{{
			Organization: bootstrap.Organization{ID: orgID, Name: "Order Org"},
			Users: []bootstrap.User{{
				ID: userID, Username: "alice", PasswordHash: hash, OrganizationRole: repository.OrganizationRoleMember,
			}},
			Classes: []bootstrap.Class{{ID: beta, Name: "Beta"}, {ID: alphaH, Name: "Alpha"}, {ID: alphaL, Name: "Alpha"}},
			Memberships: []bootstrap.Membership{
				{ClassID: beta, UserID: userID, Role: repository.ClassRoleStudent},
				{ClassID: alphaH, UserID: userID, Role: repository.ClassRoleStudent},
				{ClassID: alphaL, UserID: userID, Role: repository.ClassRoleInstructor},
			},
		}}
	})

	got := decodeClassList(t, s.classes(s.loginAs(t, "alice")))

	want := []string{alphaL.String(), alphaH.String(), beta.String()}
	if len(got) != len(want) {
		t.Fatalf("items = %+v", got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("items[%d].id = %s, want %s (전체: %+v)", i, got[i].ID, id, got)
		}
	}
}

// 접근 가능하면 200과 실제 Membership 역할, 존재하지만 접근할 수 없으면 403, 존재하지 않으면 404다.
func TestGetClassAuthorizationMatrixAgainstPostgres(t *testing.T) {
	f := newClassFixture(t)
	missing := uuid.NewString()

	type expectation struct {
		status int
		role   string // 200일 때 myRole
		name   string
	}
	allowed := func(role, name string) expectation { return expectation{status: http.StatusOK, role: role, name: name} }
	forbidden := expectation{status: http.StatusForbidden}
	notFound := expectation{status: http.StatusNotFound}

	tests := []struct {
		username string
		class    string
		id       string
		want     expectation
	}{
		{"alice", "A1", f.a1.String(), allowed("INSTRUCTOR", nameA1)},
		{"alice", "A2", f.a2.String(), allowed("STUDENT", nameA2)},
		{"alice", "A3 (같은 Organization, Membership 없음)", f.a3.String(), forbidden},
		{"alice", "B1 (다른 Organization)", f.b1.String(), forbidden},
		{"alice", "존재하지 않는 UUID", missing, notFound},
		{"alice", "UUID가 아닌 opaque ID", "not-a-uuid", notFound},
		{"alice", "대문자 UUID(canonical 아님)", strings.ToUpper(f.a1.String()), notFound},
		{"alice", "하이픈 없는 UUID", strings.ReplaceAll(f.a1.String(), "-", ""), notFound},
		{"alice", "nil UUID", uuid.Nil.String(), notFound},

		// ADMIN은 Membership bypass가 아니다.
		{"admin", "A1 (ADMIN, Membership 없음)", f.a1.String(), forbidden},
		{"admin", "A2 (ADMIN, STUDENT Membership)", f.a2.String(), allowed("STUDENT", nameA2)},
		{"admin", "A3 (ADMIN, Membership 없음)", f.a3.String(), forbidden},
		{"admin", "B1 (ADMIN, 다른 Organization)", f.b1.String(), forbidden},

		{"loner", "A1", f.a1.String(), forbidden},
		{"loner", "A2", f.a2.String(), forbidden},

		{"bob", "B1", f.b1.String(), allowed("INSTRUCTOR", nameB1)},
		{"bob", "A1 (다른 Organization)", f.a1.String(), forbidden},
		{"bob", "존재하지 않는 UUID", missing, notFound},
	}

	tokens := map[string]string{}
	for _, tt := range tests {
		if _, ok := tokens[tt.username]; !ok {
			tokens[tt.username] = f.loginAs(t, tt.username)
		}
	}

	for _, tt := range tests {
		t.Run(tt.username+"/"+tt.class, func(t *testing.T) {
			rec := f.class(tokens[tt.username], tt.id)

			if rec.Code != tt.want.status {
				t.Fatalf("GET /classes/%s = %d, want %d: %s", tt.id, rec.Code, tt.want.status, rec.Body.String())
			}
			switch tt.want.status {
			case http.StatusOK:
				var got classBody
				decodeStrict(t, rec, &got)
				if want := (classBody{ID: tt.id, Name: tt.want.name, MyRole: tt.want.role}); got != want {
					t.Fatalf("body = %+v, want %+v", got, want)
				}
			case http.StatusForbidden:
				if code := problemCode(t, rec); code != "forbidden" {
					t.Fatalf("problem.code = %q, want forbidden", code)
				}
			case http.StatusNotFound:
				if code := problemCode(t, rec); code != "not_found" {
					t.Fatalf("problem.code = %q, want not_found", code)
				}
			}
			if tt.want.status != http.StatusOK {
				// 접근할 수 없거나 없는 Class의 이름·역할은 응답에 없다.
				for _, leak := range []string{nameA1, nameA2, nameA3, nameB1, "myRole", "INSTRUCTOR", "STUDENT"} {
					if strings.Contains(rec.Body.String(), leak) {
						t.Fatalf("응답이 Class 데이터(%q)를 노출합니다: %s", leak, rec.Body.String())
					}
				}
			}
		})
	}
}

// Organization Role(users.organization_role)과 Class Role(class_memberships.role)은 서로 독립이다.
// 어느 쪽을 실제 DB에서 바꿔도 다른 쪽에서 결정된 접근 결과는 바뀌지 않는다.
func TestOrganizationRoleAndClassRoleAreIndependentAgainstPostgres(t *testing.T) {
	f := newClassFixture(t)
	aliceToken := f.loginAs(t, "alice")
	adminToken := f.loginAs(t, "admin")

	// MEMBER를 ADMIN으로 올려도 Membership이 없는 A3에 접근할 수 없고, 참여 Class와 역할은 그대로다.
	f.exec(t, `UPDATE users SET organization_role = 'ADMIN' WHERE id = $1`, f.alice)
	if rec := f.me(aliceToken); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"organizationRole":"ADMIN"`) {
		t.Fatalf("승격 뒤 /me = %d %s, want ADMIN", rec.Code, rec.Body.String())
	}
	if rec := f.class(aliceToken, f.a3.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("ADMIN으로 승격한 alice의 A3 = %d, want 403", rec.Code)
	}
	if got := decodeClassList(t, f.classes(aliceToken)); len(got) != 2 || got[0].MyRole != "INSTRUCTOR" || got[1].MyRole != "STUDENT" {
		t.Fatalf("승격 뒤 목록 = %+v, want A1 INSTRUCTOR, A2 STUDENT", got)
	}

	// ADMIN을 MEMBER로 내려도 Membership이 있는 A2 접근과 STUDENT 역할은 그대로다.
	f.exec(t, `UPDATE users SET organization_role = 'MEMBER' WHERE id = $1`, f.admin)
	rec := f.class(adminToken, f.a2.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("MEMBER로 내린 admin의 A2 = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got classBody
	decodeStrict(t, rec, &got)
	if got.MyRole != "STUDENT" {
		t.Fatalf("myRole = %q, want STUDENT", got.MyRole)
	}

	// Class 역할은 항상 현재 Membership 행에서 온다.
	f.exec(t, `UPDATE class_memberships SET role = 'STUDENT' WHERE class_id = $1 AND user_id = $2`, f.a1, f.alice)
	rec = f.class(aliceToken, f.a1.String())
	decodeStrict(t, rec, &got)
	if rec.Code != http.StatusOK || got.MyRole != "STUDENT" {
		t.Fatalf("Membership 변경 뒤 A1 = %d %+v, want 200 STUDENT", rec.Code, got)
	}

	// Membership을 삭제하면 같은 Organization의 Class여도 접근할 수 없다.
	f.exec(t, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, f.a1, f.alice)
	if rec := f.class(aliceToken, f.a1.String()); rec.Code != http.StatusForbidden {
		t.Fatalf("Membership 삭제 뒤 A1 = %d, want 403", rec.Code)
	}
}

// Auth 경로 위에서 Login → /me → Class 목록 → Class 상세 → Logout → 보호 API 401까지 이어진다.
// Frontend Browser acceptance가 아니라 Backend handler와 실제 PostgreSQL의 흐름이다.
func TestLoginClassesLogoutFlowAgainstPostgres(t *testing.T) {
	f := newClassFixture(t)

	token := f.loginAs(t, "alice")
	if rec := f.me(token); rec.Code != http.StatusOK {
		t.Fatalf("/me = %d, want 200", rec.Code)
	}
	list := decodeClassList(t, f.classes(token))
	if len(list) != 2 {
		t.Fatalf("목록 = %+v, want 2개", list)
	}
	rec := f.class(token, list[0].ID)
	var detail classBody
	decodeStrict(t, rec, &detail)
	if rec.Code != http.StatusOK || detail != list[0] {
		t.Fatalf("상세 = %d %+v, want 200 %+v", rec.Code, detail, list[0])
	}
	if rec := f.logout(token); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", rec.Code)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"/me":           f.me(token),
		"/classes":      f.classes(token),
		"/classes/{id}": f.class(token, list[0].ID),
		"Cookie 없는 목록":  f.send(http.MethodGet, "/api/v1/classes", ""),
	} {
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", name, rec.Code)
		}
		if code := problemCode(t, rec); code != "unauthenticated" {
			t.Fatalf("%s problem.code = %q, want unauthenticated", name, code)
		}
		if strings.Contains(rec.Body.String(), nameA1) {
			t.Fatalf("%s가 Class 데이터를 노출합니다", name)
		}
	}
}

// 만료·폐기된 Session은 Class 데이터에 접근할 수 없다.
func TestClassEndpointsRejectInvalidSessionsAgainstPostgres(t *testing.T) {
	f := newClassFixture(t)
	token := f.loginAs(t, "alice")
	if rec := f.classes(token); rec.Code != http.StatusOK {
		t.Fatalf("변경 전 /classes = %d, want 200", rec.Code)
	}

	f.exec(t, `UPDATE users SET disabled_at = $1 WHERE id = $2`, baseTime.Add(1), f.alice)

	if rec := f.classes(token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled 사용자의 /classes = %d, want 401", rec.Code)
	}
	if rec := f.class(token, f.a1.String()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled 사용자의 /classes/{id} = %d, want 401", rec.Code)
	}
}

// Class query가 실패하면 403/404가 아니라 500이어야 하고, 실제 driver/PostgreSQL 원문은 응답과 log에 나가지 않는다.
// 인증(Session 조회)은 정상이고 Class 관련 table만 실패하도록 실제 DB를 바꿔 실제 PostgreSQL 오류를 만든다.
func TestClassRepositoryFailureIsInternalAndDoesNotLeakDriverErrors(t *testing.T) {
	tests := []struct {
		name    string
		breakDB string
		// 실패하는 endpoint. 특히 Membership 조회 실패가 403으로 오인되면 안 된다.
		calls map[string]func(f *classFixture, token string) *httptest.ResponseRecorder
	}{
		{
			name:    "classes table unavailable",
			breakDB: `ALTER TABLE classes RENAME TO classes_unavailable`,
			calls: map[string]func(f *classFixture, token string) *httptest.ResponseRecorder{
				"list":   func(f *classFixture, token string) *httptest.ResponseRecorder { return f.classes(token) },
				"detail": func(f *classFixture, token string) *httptest.ResponseRecorder { return f.class(token, f.a1.String()) },
			},
		},
		{
			name:    "class_memberships table unavailable",
			breakDB: `ALTER TABLE class_memberships RENAME TO class_memberships_unavailable`,
			calls: map[string]func(f *classFixture, token string) *httptest.ResponseRecorder{
				"list":                     func(f *classFixture, token string) *httptest.ResponseRecorder { return f.classes(token) },
				"detail of joined class":   func(f *classFixture, token string) *httptest.ResponseRecorder { return f.class(token, f.a1.String()) },
				"detail of unjoined class": func(f *classFixture, token string) *httptest.ResponseRecorder { return f.class(token, f.a3.String()) },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newClassFixture(t)
			token := f.loginAs(t, "alice")
			f.exec(t, tt.breakDB)

			// 이 문자열이 응답과 log 어디에도 없음을 아래에서 확인하도록, 실제 driver가 만든 Cause 원문을 얻는다.
			store := postgres.NewStore(f.pool)
			var rawCauses []string
			for _, err := range []error{
				func() error { _, err := store.ClassByID(t.Context(), f.a1); return err }(),
				func() error { _, err := store.ClassMembership(t.Context(), f.a1, f.alice); return err }(),
				func() error { _, err := store.ClassesByUser(t.Context(), f.alice); return err }(),
			} {
				var repoErr *repository.Error
				if errors.As(err, &repoErr) && repoErr.Cause != nil {
					rawCauses = append(rawCauses, repoErr.Cause.Error())
				}
			}
			if len(rawCauses) == 0 {
				t.Fatal("실제 driver Cause를 얻지 못했습니다")
			}

			for name, call := range tt.calls {
				rec := call(f, token)

				if rec.Code != http.StatusInternalServerError {
					t.Fatalf("%s = %d, want 500 (403/404로 바뀌면 안 됩니다): %s", name, rec.Code, rec.Body.String())
				}
				if code := problemCode(t, rec); code != "internal_error" {
					t.Fatalf("%s problem.code = %q, want internal_error", name, code)
				}
				body := strings.ToLower(rec.Body.String())
				for _, leak := range []string{"classes", "class_memberships", "relation", "sqlstate", "42p01", "pgx", "does not exist", "postgres", nameA1} {
					if strings.Contains(body, strings.ToLower(leak)) {
						t.Fatalf("%s 응답이 내부 정보(%q)를 노출합니다: %s", name, leak, rec.Body.String())
					}
				}
				if values := rec.Header().Values("Set-Cookie"); len(values) != 0 {
					t.Fatalf("%s가 저장소 장애에서 Cookie를 변경했습니다: %v", name, values)
				}
			}

			logs := f.logs.String()
			if !strings.Contains(logs, "HTTP 요청 처리 실패") || !strings.Contains(logs, `"error_kind":"internal"`) || !strings.Contains(logs, `"sqlstate":"42P01"`) {
				t.Fatalf("저장소 장애의 분류 정보가 log에 남아야 합니다: %s", logs)
			}
			for _, secret := range append([]string{password, token}, rawCauses...) {
				if strings.Contains(logs, secret) {
					t.Fatalf("log가 민감한 값 또는 driver 원문을 포함합니다: %s", logs)
				}
			}
		})
	}
}
