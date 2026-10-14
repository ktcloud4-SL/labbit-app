//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

type provisionDB struct {
	*testDB
	conn *pgx.Conn
	// holder는 경쟁 test가 LabSpec row를 잡아 두는 전용 연결이다. Store pool과 conn(대기 수 확인)과 분리해
	// 경쟁 요청이 pool을 모두 써도 잠금 해제와 대기 확인이 막히지 않는다.
	holder *pgx.Conn
	f      *labexecutiontest.Fixture
	svc    *labexecution.Service
}

// provisionPoolConns는 경쟁 test의 동시 요청 수보다 넉넉한 pool 크기다. pgxpool 기본값(max(4, CPU 수))은 runner마다 달라
// 동시 요청 6개가 모두 lock 대기에 도달하기 전에 pool이 바닥날 수 있다.
const provisionPoolConns = 16

func newProvisionDB(t *testing.T) *provisionDB {
	t.Helper()
	db := newTestDB(t)
	config, err := pgxpool.ParseConfig(db.dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = provisionPoolConns
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db = &testDB{dsn: db.dsn, pool: pool, store: postgres.NewStore(pool)}
	conn := postgrestest.Connect(t, db.dsn)
	return &provisionDB{testDB: db, conn: conn, holder: postgrestest.Connect(t, db.dsn), f: labexecutiontest.New(t, conn), svc: labexecution.NewService(db.store)}
}

func (p *provisionDB) user(t *testing.T, id uuid.UUID) repository.User {
	t.Helper()
	u, err := p.store.UserByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (p *provisionDB) input(key string, students ...uuid.UUID) labexecution.ProvisionInput {
	ids := make([]string, len(students))
	for i, s := range students {
		ids[i] = s.String()
	}
	return labexecution.ProvisionInput{ClassID: p.f.ClassID.String(), LabSpecID: p.f.LabSpecID.String(), TargetStudentIDs: ids, IdempotencyKey: key, RequestID: "request-" + key}
}

// counts는 Provision이 만드는 모든 table의 row 수다(executions, snapshots, instances, operations, items).
func (p *provisionDB) counts(t *testing.T) [5]int {
	t.Helper()
	var c [5]int
	if err := p.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM lab_executions), (SELECT count(*) FROM creation_snapshots),
		(SELECT count(*) FROM lab_instances), (SELECT count(*) FROM operations), (SELECT count(*) FROM operation_items)`).Scan(&c[0], &c[1], &c[2], &c[3], &c[4]); err != nil {
		t.Fatal(err)
	}
	return c
}

// waitForLockWaiters는 다른 session n개가 lock을 기다릴 때까지 기다린다. 경쟁 요청이 모두 같은 지점에 도달했음을 보장한다.
func (p *provisionDB) waitForLockWaiters(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := p.conn.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock waiters=%d want %d", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdLabSpec은 별도 transaction에서 LabSpec row를 FOR UPDATE로 잠근다. release를 호출하면 rollback한다.
// 이미 replay 사전 확인을 통과한 Provision 요청을 모두 같은 지점(LabSpecForShare)에 세운 뒤 한꺼번에 경쟁시키는 데 쓴다.
func (p *provisionDB) holdLabSpec(t *testing.T) (release func()) {
	t.Helper()
	tx, err := p.holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM lab_specs WHERE id = $1 FOR UPDATE`, p.f.LabSpecID); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = tx.Rollback(context.Background()) }) }
	t.Cleanup(release)
	return release
}

type provisionResult struct {
	accepted labexecution.Accepted
	err      error
}

func (p *provisionDB) race(t *testing.T, user repository.User, inputs []labexecution.ProvisionInput) []provisionResult {
	t.Helper()
	release := p.holdLabSpec(t)
	results := make([]provisionResult, len(inputs))
	var wg sync.WaitGroup
	for i, in := range inputs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := p.svc.Provision(context.Background(), user, in)
			results[i] = provisionResult{a, err}
		}()
	}
	p.waitForLockWaiters(t, len(inputs))
	release()
	wg.Wait()
	return results
}

func TestProvisionRegistersAtomicallyWithImmutableSnapshotAndFixedTargets(t *testing.T) {
	p := newProvisionDB(t)
	f := p.f
	instructor := p.user(t, f.InstructorID)
	got, err := p.svc.Provision(t.Context(), instructor, p.input("provision-key", f.StudentBID, f.StudentAID))
	if err != nil || got.Replayed {
		t.Fatalf("Provision() = %+v, %v", got, err)
	}
	if c := p.counts(t); c != [5]int{1, 1, 3, 1, 3} {
		t.Fatalf("rows=%v", c)
	}

	var (
		classID, specID, instructorID uuid.UUID
		status                        string
		finished                      *time.Time
	)
	if err := p.pool.QueryRow(t.Context(), `SELECT class_id, lab_spec_id, instructor_user_id, status, finished_at FROM lab_executions WHERE id = $1`, got.LabExecutionID).
		Scan(&classID, &specID, &instructorID, &status, &finished); err != nil {
		t.Fatal(err)
	}
	if classID != f.ClassID || specID != f.LabSpecID || instructorID != f.InstructorID || status != "PROVISIONING" || finished != nil {
		t.Fatalf("execution=%v %v %v %s %v", classID, specID, instructorID, status, finished)
	}

	// 강사 1명 + 선택 학생 2명만 LabInstance를 가진다(StudentC는 선택하지 않음). 모두 generation 1, PENDING이다.
	rows, err := p.pool.Query(t.Context(), `SELECT i.user_id, i.participant_role, i.status, i.generation, oi.generation, oi.status, o.id
		FROM lab_instances i JOIN operation_items oi ON oi.lab_instance_id = i.id JOIN operations o ON o.id = oi.operation_id
		WHERE i.lab_execution_id = $1`, got.LabExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	participants := map[uuid.UUID]string{}
	for rows.Next() {
		var user, operationID uuid.UUID
		var role, status, itemStatus string
		var generation, itemGeneration int
		if err := rows.Scan(&user, &role, &status, &generation, &itemGeneration, &itemStatus, &operationID); err != nil {
			t.Fatal(err)
		}
		if status != "PENDING" || generation != 1 || itemGeneration != 1 || itemStatus != "PENDING" || operationID != got.OperationID {
			t.Fatalf("instance user=%s %s %d item=%d %s op=%s", user, status, generation, itemGeneration, itemStatus, operationID)
		}
		participants[user] = role
	}
	if rows.Err() != nil || len(participants) != 3 || participants[f.InstructorID] != "INSTRUCTOR" || participants[f.StudentAID] != "STUDENT" || participants[f.StudentBID] != "STUDENT" {
		t.Fatalf("participants=%v %v", participants, rows.Err())
	}
	op, err := p.store.OperationByID(t.Context(), got.OperationID)
	if err != nil || op.Type != "PROVISION" || op.Status != "PENDING" || *op.LabExecutionID != got.LabExecutionID || op.LabInstanceID != nil || op.RequestID != "request-provision-key" {
		t.Fatalf("operation=%+v %v", op, err)
	}

	// Snapshot은 원본 revision·ProviderConnection을 column에, 실제 Provider 값을 Connector CreationSnapshot 모양으로 고정한다.
	var revision int64
	var connection uuid.UUID
	var raw []byte
	if err := p.pool.QueryRow(t.Context(), `SELECT source_lab_spec_revision, provider_connection_id, snapshot FROM creation_snapshots WHERE lab_execution_id = $1`, got.LabExecutionID).
		Scan(&revision, &connection, &raw); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		ProviderConnectionID string
		WorkspaceVMKey       string
		InternetOutbound     bool
		VMs                  []struct {
			VMKey, Role, ImageID, FlavorID string
			InstanceIndex                  int
			FlavorSpec                     struct{ VCPUs, RAMMiB, DiskGiB int }
		}
		StartupScript *struct{ Content, SHA256 string }
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || connection != f.ProviderConnectionID || snap.ProviderConnectionID != f.ProviderConnectionID.String() || !snap.InternetOutbound ||
		len(snap.VMs) != 3 || snap.StartupScript == nil || snap.StartupScript.Content != labexecutiontest.StartupScript || len(snap.StartupScript.SHA256) != 64 {
		t.Fatalf("snapshot=%s revision=%d", raw, revision)
	}
	for _, vm := range snap.VMs {
		if vm.ImageID != labexecutiontest.ImageMarker || vm.FlavorID != labexecutiontest.FlavorMarker || vm.FlavorSpec.VCPUs != 2 || vm.FlavorSpec.RAMMiB != 4096 || vm.FlavorSpec.DiskGiB != 40 {
			t.Fatalf("vm=%+v", vm)
		}
	}

	// 기존 Terminal consumer(CreationSnapshotTargets)가 같은 snapshot을 읽고 Workspace VM이 vms 안에 있다.
	var instructorInstance uuid.UUID
	if err := p.pool.QueryRow(t.Context(), `SELECT id FROM lab_instances WHERE lab_execution_id = $1 AND participant_role = 'INSTRUCTOR'`, got.LabExecutionID).Scan(&instructorInstance); err != nil {
		t.Fatal(err)
	}
	targets, err := p.store.CreationSnapshotTargets(t.Context(), instructorInstance)
	if err != nil || len(targets.VMs) != 3 || targets.WorkspaceVMKey != snap.WorkspaceVMKey {
		t.Fatalf("targets=%+v %v", targets, err)
	}
	workspaceFound := false
	for _, vm := range targets.VMs {
		workspaceFound = workspaceFound || (vm.VMKey == targets.WorkspaceVMKey && vm.Role == labexecutiontest.WorkspaceRole && vm.InstanceIndex == labexecutiontest.WorkspaceIndex)
	}
	if !workspaceFound {
		t.Fatalf("workspace VM not resolved: %+v", targets)
	}

	// Snapshot은 UPDATE할 수 없다. LabSpec·mapping·Membership이 바뀌어도 snapshot과 대상 집합은 그대로다.
	if _, err := p.pool.Exec(t.Context(), `UPDATE creation_snapshots SET snapshot = '{}'::jsonb WHERE lab_execution_id = $1`, got.LabExecutionID); err == nil {
		t.Fatal("creation_snapshots UPDATE succeeded")
	}
	labexecutiontest.Exec(t, p.conn, `UPDATE lab_specs SET revision = revision + 1, internet_outbound = false, startup_script = NULL WHERE id = $1`, f.LabSpecID)
	labexecutiontest.Exec(t, p.conn, `UPDATE provider_image_mappings SET provider_image_id = 'replaced-image', is_active = false WHERE id = $1`, f.ImageMappingID)
	labexecutiontest.Exec(t, p.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, f.ClassID, f.StudentBID)
	labexecutiontest.Exec(t, p.conn, `INSERT INTO users (id, organization_id, organization_role) VALUES ($1, $2, 'MEMBER')`, uuid.New(), f.OrganizationID)
	var after []byte
	if err := p.pool.QueryRow(t.Context(), `SELECT snapshot FROM creation_snapshots WHERE lab_execution_id = $1`, got.LabExecutionID).Scan(&after); err != nil || string(after) != string(raw) {
		t.Fatalf("snapshot changed: %s %v", after, err)
	}
	view, err := p.svc.Execution(t.Context(), instructor, got.LabExecutionID.String())
	want := []uuid.UUID{f.StudentAID, f.StudentBID}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if err != nil || len(view.TargetUserIDs) != 2 || view.TargetUserIDs[0] != want[0] || view.TargetUserIDs[1] != want[1] || len(view.LabInstances) != 3 {
		t.Fatalf("view=%+v %v", view, err)
	}

	// 같은 key의 같은 의미(순서만 다름) 재요청은 기존 Operation이며 새 row가 없다. 이후 바뀐 mapping·Membership과 무관하다.
	replay, err := p.svc.Provision(t.Context(), instructor, p.input("provision-key", f.StudentAID, f.StudentBID))
	if err != nil || !replay.Replayed || replay.OperationID != got.OperationID || replay.LabExecutionID != got.LabExecutionID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	if _, err := p.svc.Provision(t.Context(), instructor, p.input("provision-key", f.StudentAID)); !errors.Is(err, labexecution.ErrIdempotencyConflict) {
		t.Fatalf("same key other meaning=%v", err)
	}
	if _, err := p.svc.Provision(t.Context(), instructor, p.input("other-key", f.StudentAID)); !errors.Is(err, labexecution.ErrActiveExecution) {
		t.Fatalf("other key=%v", err)
	}
	if c := p.counts(t); c != [5]int{1, 1, 3, 1, 3} {
		t.Fatalf("rows after replay/conflict=%v", c)
	}
}

// 실패 주입: 마지막 단계(operation_items INSERT)에서 DB가 오류를 내면 앞서 INSERT한 LabExecution·Snapshot·LabInstance·Operation이
// 모두 rollback된다. trigger는 이 test database에만 만든다.
func TestProvisionRollsBackEveryRowOnLateFailure(t *testing.T) {
	p := newProvisionDB(t)
	labexecutiontest.Exec(t, p.conn, `CREATE FUNCTION labbit_test_fail_item() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected item failure'; END; $$`)
	labexecutiontest.Exec(t, p.conn, `CREATE TRIGGER labbit_test_fail_item BEFORE INSERT ON operation_items FOR EACH ROW EXECUTE FUNCTION labbit_test_fail_item()`)
	instructor := p.user(t, p.f.InstructorID)

	_, err := p.svc.Provision(t.Context(), instructor, p.input("rollback-key", p.f.StudentAID))
	if err == nil || errors.Is(err, labexecution.ErrActiveExecution) || !errors.Is(err, repository.ErrInternal) {
		t.Fatalf("err=%v", err)
	}
	if c := p.counts(t); c != [5]int{} {
		t.Fatalf("rows after failure=%v", c)
	}
	// 실패한 시도는 key를 소모하지 않고 Class를 잠그지 않는다.
	labexecutiontest.Exec(t, p.conn, `DROP TRIGGER labbit_test_fail_item ON operation_items`)
	if got, err := p.svc.Provision(t.Context(), instructor, p.input("rollback-key", p.f.StudentAID)); err != nil || got.Replayed {
		t.Fatalf("retry=%+v %v", got, err)
	}
	if c := p.counts(t); c != [5]int{1, 1, 2, 1, 2} {
		t.Fatalf("rows after retry=%v", c)
	}
}

// 서로 다른 key의 동시 Provision: 모두 replay 사전 확인을 통과한 뒤 경쟁하며 Class당 하나만 성공한다.
func TestConcurrentProvisionWithDifferentKeysAllowsOneActiveExecution(t *testing.T) {
	p := newProvisionDB(t)
	instructor := p.user(t, p.f.InstructorID)
	const n = 6
	inputs := make([]labexecution.ProvisionInput, n)
	for i := range inputs {
		inputs[i] = p.input(uuid.NewString(), p.f.StudentAID)
	}
	results := p.race(t, instructor, inputs)
	succeeded := 0
	for _, r := range results {
		switch {
		case r.err == nil && !r.accepted.Replayed:
			succeeded++
		case errors.Is(r.err, labexecution.ErrActiveExecution):
		default:
			t.Fatalf("unexpected result=%+v %v", r.accepted, r.err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded=%d", succeeded)
	}
	if c := p.counts(t); c != [5]int{1, 1, 2, 1, 2} {
		t.Fatalf("rows=%v", c)
	}
}

// 같은 key·같은 의미의 동시 Provision: 진 요청은 active LabExecution unique 충돌을 만나지만 409가 아니라 commit된 Operation의 replay다.
// 진 요청이 만든 row는 남지 않는다.
func TestConcurrentProvisionWithSameKeyReplaysInsteadOfConflict(t *testing.T) {
	p := newProvisionDB(t)
	instructor := p.user(t, p.f.InstructorID)
	const n = 6
	inputs := make([]labexecution.ProvisionInput, n)
	for i := range inputs {
		// 학생 순서를 바꿔도 같은 의미다.
		if i%2 == 0 {
			inputs[i] = p.input("same-key", p.f.StudentAID, p.f.StudentBID)
		} else {
			inputs[i] = p.input("same-key", p.f.StudentBID, p.f.StudentAID)
		}
	}
	results := p.race(t, instructor, inputs)
	var created labexecution.Accepted
	replays := 0
	for _, r := range results {
		if r.err != nil {
			t.Fatalf("same key result error=%v", r.err)
		}
		if r.accepted.Replayed {
			replays++
			continue
		}
		if created.OperationID != uuid.Nil {
			t.Fatal("two requests created an Operation")
		}
		created = r.accepted
	}
	if created.OperationID == uuid.Nil || replays != n-1 {
		t.Fatalf("created=%+v replays=%d", created, replays)
	}
	for _, r := range results {
		if r.accepted.OperationID != created.OperationID || r.accepted.LabExecutionID != created.LabExecutionID {
			t.Fatalf("result=%+v created=%+v", r.accepted, created)
		}
	}
	if c := p.counts(t); c != [5]int{1, 1, 3, 1, 3} {
		t.Fatalf("rows=%v", c)
	}
}

// 같은 key를 다른 의미에 동시에 쓰면 같은 Class(대상 학생만 다름)든 다른 Class든 하나만 접수되고 나머지는 Idempotency 충돌이다.
// 다른 Class의 경우 두 LabExecution INSERT는 모두 성공하지만 진 요청의 Operation INSERT가 기다린 뒤 rollback되어 그 Class에는 row가 남지 않는다.
func TestConcurrentProvisionWithSameKeyDifferentMeaningConflicts(t *testing.T) {
	for name, other := range map[string]func(p *provisionDB) labexecution.ProvisionInput{
		"same class other students": func(p *provisionDB) labexecution.ProvisionInput { return p.input("shared-key", p.f.StudentCID) },
		"other class": func(p *provisionDB) labexecution.ProvisionInput {
			in := p.input("shared-key", p.f.OutsiderID)
			in.ClassID = p.f.OtherClassID.String()
			return in
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newProvisionDB(t)
			instructor := p.user(t, p.f.InstructorID)
			results := p.race(t, instructor, []labexecution.ProvisionInput{p.input("shared-key", p.f.StudentAID), other(p)})
			succeeded, conflicts := 0, 0
			for _, r := range results {
				switch {
				case r.err == nil && !r.accepted.Replayed:
					succeeded++
				case errors.Is(r.err, labexecution.ErrIdempotencyConflict):
					conflicts++
				default:
					t.Fatalf("unexpected result=%+v %v", r.accepted, r.err)
				}
			}
			if succeeded != 1 || conflicts != 1 {
				t.Fatalf("succeeded=%d conflicts=%d", succeeded, conflicts)
			}
			if c := p.counts(t); c[0] != 1 || c[1] != 1 || c[3] != 1 || c[2] != c[4] {
				t.Fatalf("rows=%v", c)
			}
		})
	}
}

// LabSpec 수정과 경쟁: 진행 중인 수정(FOR UPDATE)이 commit될 때까지 Provision이 기다린 뒤 새 revision의 본문·VM Role·mapping만으로
// snapshot을 만든다. 이전 revision과 새 VM Role이 섞이지 않는다.
func TestProvisionWaitsForConcurrentLabSpecUpdateAndUsesOneRevision(t *testing.T) {
	p := newProvisionDB(t)
	f := p.f
	instructor := p.user(t, f.InstructorID)
	commit := make(chan struct{})
	updated := make(chan error, 1)
	go func() {
		updated <- p.store.WithinTransaction(context.Background(), func(ctx context.Context, r repository.Repositories) error {
			current, err := r.LabSpecForUpdate(ctx, f.LabSpecID)
			if err != nil {
				return err
			}
			_, ok, err := r.UpdateLabSpec(ctx, repository.LabSpecUpdate{ID: f.LabSpecID, OrganizationID: f.OrganizationID, ExpectedRevision: current.Revision,
				LabSpecDefinition: repository.LabSpecDefinition{Name: "Spec v2", InternetOutbound: false, WorkspaceRole: "solo", WorkspaceInstanceIndex: 0,
					VMRoles: []repository.NewLabSpecVMRole{{ID: uuid.New(), Role: "solo", ImageMappingID: f.ImageMapping2ID, FlavorMappingID: f.FlavorMappingID, VMCount: 1}}}})
			if err != nil || !ok {
				return errors.Join(err, errors.New("update not applied"))
			}
			<-commit
			return nil
		})
	}()
	// 수정 transaction이 LabSpec row를 잠근 뒤에 Provision을 시작한다.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var locked bool
		if err := p.conn.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_locks l JOIN pg_class c ON c.oid = l.relation WHERE c.relname = 'lab_specs' AND l.mode = 'RowShareLock' AND l.granted)`).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		if locked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("update did not lock LabSpec")
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan provisionResult, 1)
	go func() {
		a, err := p.svc.Provision(context.Background(), instructor, p.input("race-update", f.StudentAID))
		done <- provisionResult{a, err}
	}()
	p.waitForLockWaiters(t, 1)
	close(commit)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	var revision int64
	var raw []byte
	if err := p.pool.QueryRow(t.Context(), `SELECT source_lab_spec_revision, snapshot FROM creation_snapshots WHERE lab_execution_id = $1`, r.accepted.LabExecutionID).Scan(&revision, &raw); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		InternetOutbound bool
		WorkspaceVMKey   string
		StartupScript    *json.RawMessage
		VMs              []struct{ VMKey, Role, ImageID string }
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if revision != 2 || snap.InternetOutbound || snap.StartupScript != nil || len(snap.VMs) != 1 || snap.VMs[0].Role != "solo" ||
		snap.VMs[0].ImageID != labexecutiontest.ImageMarker2 || snap.WorkspaceVMKey != snap.VMs[0].VMKey {
		t.Fatalf("revision=%d snapshot=%s", revision, raw)
	}
}

// Mapping 변경과 경쟁: 진행 중인 mapping 비활성화가 commit될 때까지 Provision이 mapping FOR SHARE에서 기다린 뒤 비활성 값을 보고
// 거절한다. commit 전의 활성 값으로 snapshot을 만들지 않는다.
func TestProvisionWaitsForConcurrentMappingChange(t *testing.T) {
	p := newProvisionDB(t)
	instructor := p.user(t, p.f.InstructorID)
	tx, err := p.holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `UPDATE provider_flavor_mappings SET is_active = false WHERE id = $1`, p.f.FlavorMappingID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.svc.Provision(context.Background(), instructor, p.input("race-mapping", p.f.StudentAID))
		done <- err
	}()
	p.waitForLockWaiters(t, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, labexecution.ErrLabSpecNotProvisionable) {
		t.Fatalf("err=%v", err)
	}
	if c := p.counts(t); c != [5]int{} {
		t.Fatalf("rows=%v", c)
	}
}

func TestProvisionAuthorizationAgainstPostgres(t *testing.T) {
	p := newProvisionDB(t)
	f := p.f
	for name, tt := range map[string]struct {
		user uuid.UUID
		in   labexecution.ProvisionInput
		want error
	}{
		"student":                 {f.StudentAID, p.input("k", f.StudentBID), labexecution.ErrForbidden},
		"ADMIN without member":    {f.AdminID, p.input("k", f.StudentBID), labexecution.ErrForbidden},
		"other organization":      {f.ForeignID, p.input("k", f.StudentBID), labexecution.ErrForbidden},
		"co-instructor not owner": {f.CoInstructorID, p.input("k", f.StudentBID), labexecution.ErrForbidden},
		"other class student":     {f.InstructorID, p.input("k", f.OutsiderID), labexecution.ErrInvalidTargets},
		"instructor as student":   {f.InstructorID, p.input("k", f.CoInstructorID), labexecution.ErrInvalidTargets},
		"foreign user":            {f.InstructorID, p.input("k", f.ForeignID), labexecution.ErrInvalidTargets},
		"admin as student":        {f.InstructorID, p.input("k", f.AdminID), labexecution.ErrInvalidTargets},
	} {
		if _, err := p.svc.Provision(t.Context(), p.user(t, tt.user), tt.in); !errors.Is(err, tt.want) {
			t.Fatalf("%s: err=%v want %v", name, err, tt.want)
		}
	}
	if c := p.counts(t); c != [5]int{} {
		t.Fatalf("rows=%v", c)
	}
}
