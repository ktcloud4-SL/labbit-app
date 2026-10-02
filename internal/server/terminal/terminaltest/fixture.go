// Package terminaltest는 TerminalSession 통합 test가 공유하는 지원 코드다. production 코드는 이 package를 import하지 않는다.
//
// Fixture는 실제 PostgreSQL schema의 제약(FK, unique, partial unique index)을 그대로 통과하도록 raw SQL로 데이터를 만든다.
// 새 Migration이나 test 전용 우회 경로를 쓰지 않는다.
package terminaltest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// Credential은 Fixture Connector의 Credential이다. 실제 Secret이 아니다.
const Credential = "terminaltest-connector-credential-7d41"

// Browser에 나가면 안 되는 snapshot 값이다. Snapshot이 모든 VM에 넣어, 응답에 하나라도 나타나면 test가 실패한다.
// 실제 Provider 값이 아니다.
const (
	SnapshotImageIDMarker    = "image-marker-5e2a91c4"
	SnapshotFlavorIDMarker   = "flavor-marker-71c0d8be"
	SnapshotPrivateIPMarker  = "10.77.3.9"
	SnapshotStartupMarker    = "startup-script-marker-93bd04f7"
	SnapshotProviderConnMark = "snapshot-provider-connection-marker-c2e8"
)

// VM은 CreationSnapshot의 vms[] 한 원소(Terminal target에 관련된 값)다.
type VM struct {
	Key   string
	Role  string
	Index int
}

// DefaultVMs는 기본 Fixture CreationSnapshot의 VM이다. retired와 ghost는 snapshot에는 있지만 ProviderResource가 PRESENT가 아니다.
func DefaultVMs() []VM {
	return []VM{
		{Key: "workspace", Role: "workspace", Index: 0},
		{Key: "db", Role: "db", Index: 0},
		{Key: "retired", Role: "worker", Index: 0},
		{Key: "ghost", Role: "worker", Index: 1},
	}
}

// Snapshot은 실제 Connector 계약(connector.schema.json CreationSnapshot)의 모양을 가진 snapshot JSON이다.
// Browser에 나가면 안 되는 resolve된 Provider 정보(imageId, flavorId, flavorSpec, 주소, startupScript)를 vm마다 포함한다.
func Snapshot(workspaceVMKey string, vms []VM) string {
	type flavorSpec struct {
		VCPUs   int `json:"vcpus"`
		RAMMiB  int `json:"ramMiB"`
		DiskGiB int `json:"diskGiB"`
	}
	type resolvedVM struct {
		VMKey         string     `json:"vmKey"`
		Role          string     `json:"role"`
		InstanceIndex int        `json:"instanceIndex"`
		ImageID       string     `json:"imageId"`
		FlavorID      string     `json:"flavorId"`
		FlavorSpec    flavorSpec `json:"flavorSpec"`
		PrivateIP     string     `json:"privateIp"`
	}
	resolved := make([]resolvedVM, 0, len(vms))
	for _, vm := range vms {
		resolved = append(resolved, resolvedVM{
			VMKey: vm.Key, Role: vm.Role, InstanceIndex: vm.Index,
			ImageID: SnapshotImageIDMarker, FlavorID: SnapshotFlavorIDMarker, FlavorSpec: flavorSpec{VCPUs: 2, RAMMiB: 2048, DiskGiB: 20},
			PrivateIP: SnapshotPrivateIPMarker,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"providerConnectionId": SnapshotProviderConnMark,
		"vms":                  resolved,
		"workspaceVmKey":       workspaceVMKey,
		"internetOutbound":     false,
		"startupScript":        map[string]string{"content": SnapshotStartupMarker, "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

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
//	  └─ Connector ─ ProviderConnection ─ CreationSnapshot(vms: workspace, db, retired, ghost) ─ LabExecution(Class)
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

// New는 conn이 가리키는 (Migration이 적용된) database에 Fixture를 만든다. CreationSnapshot은 DefaultVMs이고 workspace가 Workspace VM이다.
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
	      VALUES ($1, $2, $3, $4, 1, $5, $6::jsonb)`, uuid.New(), f.OrganizationID, f.LabExecutionID, f.LabSpecID, f.ProviderConnectionID, Snapshot("workspace", DefaultVMs()))
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

// AddLabInstanceWithSnapshot은 Owner의 READY LabInstance(generation 1)를 별도의 종료된 LabExecution과 snapshot JSON 그대로의
// CreationSnapshot과 함께 추가한다. creation_snapshots는 생성 뒤 UPDATE할 수 없으므로 다른 VM 구성이나 손상된 snapshot은 이렇게 따로 만든다.
// ProviderResource는 만들지 않는다. 종료된 LabExecution이라 Class의 active LabExecution unique 제약과 겹치지 않는다.
func (f *Fixture) AddLabInstanceWithSnapshot(t *testing.T, conn *pgx.Conn, snapshot string) uuid.UUID {
	t.Helper()
	execution, instance := uuid.New(), uuid.New()
	Exec(t, conn, `INSERT INTO lab_executions (id, organization_id, class_id, lab_spec_id, instructor_user_id, status, finished_at)
	               VALUES ($1, $2, $3, $4, $5, 'FINISHED', now())`, execution, f.OrganizationID, f.ClassID, f.LabSpecID, f.InstructorID)
	Exec(t, conn, `INSERT INTO creation_snapshots (id, organization_id, lab_execution_id, source_lab_spec_id, source_lab_spec_revision, provider_connection_id, snapshot)
	               VALUES ($1, $2, $3, $4, 1, $5, $6::jsonb)`, uuid.New(), f.OrganizationID, execution, f.LabSpecID, f.ProviderConnectionID, snapshot)
	Exec(t, conn, `INSERT INTO lab_instances (id, organization_id, lab_execution_id, user_id, participant_role, status, generation)
	               VALUES ($1, $2, $3, $4, 'STUDENT', 'READY', 1)`, instance, f.OrganizationID, execution, f.OwnerID)
	return instance
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
