// Package terminaltest는 TerminalSession 통합 test가 공유하는 지원 코드다. production 코드는 이 package를 import하지 않는다.
//
// Fixture는 실제 PostgreSQL schema의 제약(FK, unique, partial unique index)을 그대로 통과하도록 raw SQL로 데이터를 만든다.
// 새 Migration이나 test 전용 우회 경로를 쓰지 않는다.
package terminaltest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// Credential은 Fixture Connector의 Credential이다. 실제 Secret이 아니다.
const Credential = "terminaltest-connector-credential-7d41"

// Server는 Fixture의 SERVER ProviderResource다.
type Server struct {
	ResourceID uuid.UUID
	ProviderID string
}

// Fixture는 시딩한 데이터의 ID다.
//
//	Organization
//	  ├─ Class ─ INSTRUCTOR Instructor, STUDENT Owner, STUDENT Peer
//	  ├─ OtherClass ─ STUDENT Outsider (Class의 Membership 없음)
//	  └─ Connector ─ ProviderConnection ─ CreationSnapshot ─ LabExecution(Class)
//	       ├─ LabInstance(Owner, READY, generation 1): SERVER workspace/db(PRESENT), retired(DELETED), ghost(MISSING)
//	       └─ LabInstance(Peer, READY, generation 1): SERVER workspace(PRESENT)
//	OtherOrganization ─ Foreign
type Fixture struct {
	OrganizationID      uuid.UUID
	OtherOrganizationID uuid.UUID
	ClassID             uuid.UUID
	OtherClassID        uuid.UUID

	InstructorID uuid.UUID
	OwnerID      uuid.UUID
	PeerID       uuid.UUID
	OutsiderID   uuid.UUID
	ForeignID    uuid.UUID

	ConnectorID          uuid.UUID
	ProviderConnectionID uuid.UUID
	LabSpecID            uuid.UUID
	LabExecutionID       uuid.UUID
	LabInstanceID        uuid.UUID
	PeerLabInstanceID    uuid.UUID

	// Servers는 Owner LabInstance의 SERVER ProviderResource를 logical_name으로 찾는다.
	Servers map[string]Server
	// PeerServer는 Peer LabInstance의 workspace SERVER다.
	PeerServer Server
}

// New는 conn이 가리키는 (Migration이 적용된) database에 Fixture를 만든다.
func New(t *testing.T, conn *pgx.Conn) *Fixture {
	t.Helper()
	f := &Fixture{
		OrganizationID: uuid.New(), OtherOrganizationID: uuid.New(), ClassID: uuid.New(), OtherClassID: uuid.New(),
		InstructorID: uuid.New(), OwnerID: uuid.New(), PeerID: uuid.New(), OutsiderID: uuid.New(), ForeignID: uuid.New(),
		ConnectorID: uuid.New(), ProviderConnectionID: uuid.New(), LabSpecID: uuid.New(), LabExecutionID: uuid.New(),
		LabInstanceID: uuid.New(), PeerLabInstanceID: uuid.New(),
		Servers: map[string]Server{},
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(t.Context(), sql, args...); err != nil {
			t.Fatalf("fixture SQL 실패: %v\n%s", err, sql)
		}
	}

	exec(`INSERT INTO organizations (id, name) VALUES ($1, 'Fixture Academy'), ($2, 'Other Academy')`, f.OrganizationID, f.OtherOrganizationID)
	for _, id := range []uuid.UUID{f.InstructorID, f.OwnerID, f.PeerID, f.OutsiderID} {
		exec(`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, id, f.OrganizationID)
	}
	exec(`INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, f.ForeignID, f.OtherOrganizationID)
	exec(`INSERT INTO classes (id, organization_id, name) VALUES ($1, $3, 'Class'), ($2, $3, 'Other Class')`, f.ClassID, f.OtherClassID, f.OrganizationID)
	for _, m := range []struct {
		class, user uuid.UUID
		role        string
	}{
		{f.ClassID, f.InstructorID, "INSTRUCTOR"}, {f.ClassID, f.OwnerID, "STUDENT"}, {f.ClassID, f.PeerID, "STUDENT"},
		{f.OtherClassID, f.OutsiderID, "STUDENT"},
	} {
		exec(`INSERT INTO class_memberships (organization_id, class_id, user_id, role) VALUES ($1, $2, $3, $4)`, f.OrganizationID, m.class, m.user, m.role)
	}

	digest := connector.CredentialDigest(Credential)
	exec(`INSERT INTO connectors (id, organization_id, name) VALUES ($1, $2, 'Fixture Connector')`, f.ConnectorID, f.OrganizationID)
	exec(`INSERT INTO connector_credentials (id, connector_id, credential_hash) VALUES ($1, $2, $3)`, uuid.New(), f.ConnectorID, digest[:])
	exec(`INSERT INTO provider_connections (id, organization_id, connector_id, name) VALUES ($1, $2, $3, 'Fixture Provider')`, f.ProviderConnectionID, f.OrganizationID, f.ConnectorID)

	exec(`INSERT INTO lab_specs (id, organization_id, owner_user_id, name, internet_outbound, workspace_role, workspace_instance_index)
	      VALUES ($1, $2, $3, 'Spec', false, 'workspace', 0)`, f.LabSpecID, f.OrganizationID, f.InstructorID)
	exec(`INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status)
	      VALUES ($1, $2, $3, $4, $5, 'ACTIVE')`, f.LabExecutionID, f.OrganizationID, f.ClassID, f.LabSpecID, f.InstructorID)
	exec(`INSERT INTO creation_snapshots (id, organization_id, lab_execution_id, source_lab_spec_id, source_lab_spec_revision, provider_connection_id, snapshot)
	      VALUES ($1, $2, $3, $4, 1, $5, '{}'::jsonb)`, uuid.New(), f.OrganizationID, f.LabExecutionID, f.LabSpecID, f.ProviderConnectionID)
	for _, li := range []struct{ id, user uuid.UUID }{{f.LabInstanceID, f.OwnerID}, {f.PeerLabInstanceID, f.PeerID}} {
		exec(`INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
		      VALUES ($1, $2, $3, $4, 'STUDENT', 'READY', 1)`, li.id, f.OrganizationID, f.LabExecutionID, li.user)
	}

	for _, r := range []struct{ name, lifecycle string }{{"workspace", "PRESENT"}, {"db", "PRESENT"}, {"retired", "DELETED"}, {"ghost", "MISSING"}} {
		f.Servers[r.name] = f.AddResource(t, conn, f.LabInstanceID, 1, "SERVER", r.name, r.lifecycle)
	}
	// SERVER가 아닌 리소스는 대상이 될 수 없다. 같은 logical_name을 써도 구분해야 한다.
	f.AddResource(t, conn, f.LabInstanceID, 1, "NETWORK", "net", "PRESENT")
	f.AddResource(t, conn, f.LabInstanceID, 1, "NETWORK", "workspace", "PRESENT")
	f.PeerServer = f.AddResource(t, conn, f.PeerLabInstanceID, 1, "SERVER", "workspace", "PRESENT")
	return f
}

// AddResource는 LabInstance의 generation에 ProviderResource를 추가한다.
func (f *Fixture) AddResource(t *testing.T, conn *pgx.Conn, labInstanceID uuid.UUID, generation int64, resourceType, logicalName, lifecycle string) Server {
	t.Helper()
	server := Server{ResourceID: uuid.New(), ProviderID: "provider-" + uuid.NewString()}
	if _, err := conn.Exec(t.Context(),
		`INSERT INTO provider_resources (id, organization_id, lab_instance_id, provider_connection_id, generation, resource_type, logical_name, provider_id, lifecycle_status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		server.ResourceID, f.OrganizationID, labInstanceID, f.ProviderConnectionID, generation, resourceType, logicalName, server.ProviderID, lifecycle); err != nil {
		t.Fatalf("ProviderResource 추가 실패: %v", err)
	}
	return server
}

// LoginSession은 user의 유효한 Browser 로그인 Session을 저장하고 Cookie 값(raw token)을 반환한다.
// 운영 경로와 같은 형식(CSPRNG 32 bytes Base64URL, DB에는 SHA-256 digest)을 쓴다.
func LoginSession(t *testing.T, conn *pgx.Conn, userID uuid.UUID) string {
	t.Helper()
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	digest := sha256.Sum256(raw[:])
	now := time.Now()
	if _, err := conn.Exec(t.Context(),
		`INSERT INTO auth_sessions (id, user_id, token_hash, created_at, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), userID, digest[:], now, now.Add(8*time.Hour)); err != nil {
		t.Fatalf("로그인 Session 저장 실패: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

// Exec는 test가 상태를 직접 바꿀 때 쓴다(Reset, 상태 전이 같은 다른 작업의 결과를 흉내 낸다).
func Exec(t *testing.T, conn *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("SQL 실패: %v\n%s", err, sql)
	}
}

// BumpGeneration은 Reset이 끝난 것처럼 LabInstance의 generation을 올리고 새 generation에 workspace SERVER를 만든다.
func (f *Fixture) BumpGeneration(t *testing.T, conn *pgx.Conn, labInstanceID uuid.UUID) (generation int64, workspace Server) {
	t.Helper()
	if err := conn.QueryRow(t.Context(), `UPDATE lab_instances SET generation = generation + 1 WHERE id = $1 RETURNING generation`, labInstanceID).Scan(&generation); err != nil {
		t.Fatalf("generation 증가 실패: %v", err)
	}
	return generation, f.AddResource(t, conn, labInstanceID, generation, "SERVER", "workspace", "PRESENT")
}

// Count는 table의 row 수다.
func Count(t *testing.T, conn *pgx.Conn, table string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&n); err != nil {
		t.Fatalf("%s row 수 조회 실패: %v", table, err)
	}
	return n
}
