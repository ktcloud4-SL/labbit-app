//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// mutationDB는 실제 Provision 접수로 만든 LabExecution(강사, StudentA, StudentB)을 가진다. LBT-18 Worker가 아직 없으므로
// "이전 Provision이 끝난 상태"는 이 격리된 test database에서만 SQL로 명시적으로 만든다(Production 코드에는 우회 경로가 없다).
type mutationDB struct {
	*provisionDB
	execution uuid.UUID
	// instance는 user ID → LabInstance ID다.
	instance map[uuid.UUID]uuid.UUID
}

func newMutationDB(t *testing.T, settle bool) *mutationDB {
	t.Helper()
	p := newProvisionDB(t)
	a, err := p.svc.Provision(t.Context(), p.user(t, p.f.InstructorID), p.input("seed-provision", p.f.StudentAID, p.f.StudentBID))
	if err != nil {
		t.Fatal(err)
	}
	m := &mutationDB{provisionDB: p, execution: a.LabExecutionID, instance: map[uuid.UUID]uuid.UUID{}}
	rows, err := p.pool.Query(t.Context(), `SELECT user_id, id FROM lab_instances WHERE lab_execution_id = $1`, a.LabExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var user, id uuid.UUID
		if err := rows.Scan(&user, &id); err != nil {
			t.Fatal(err)
		}
		m.instance[user] = id
	}
	if rows.Err() != nil || len(m.instance) != 3 {
		t.Fatalf("instances=%v err=%v", m.instance, rows.Err())
	}
	if settle {
		m.settle(t)
		// 현재 generation의 ProviderResource 이력이다. 접수는 이 row를 바꾸거나 지우지 않아야 한다.
		for user, id := range m.instance {
			labexecutiontest.Exec(t, p.conn, `INSERT INTO provider_resources (id, organization_id, lab_instance_id, provider_connection_id, generation,
				resource_type, logical_name, provider_id, lifecycle_status) VALUES ($1, $2, $3, $4, 1, 'SERVER', 'vm-1', $5, 'PRESENT')`,
				uuid.New(), p.f.OrganizationID, id, p.f.ProviderConnectionID, "server-"+user.String())
		}
	}
	return m
}

// settle은 지금까지의 모든 active OperationItem/Operation을 SUCCEEDED로, LabInstance/LabExecution을 READY로 만든다(Worker 대역).
func (m *mutationDB) settle(t *testing.T) {
	t.Helper()
	labexecutiontest.Exec(t, m.conn, `UPDATE operation_items SET status = 'SUCCEEDED', started_at = created_at, finished_at = created_at
		WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING')`)
	labexecutiontest.Exec(t, m.conn, `UPDATE operations SET status = 'SUCCEEDED', started_at = created_at, finished_at = created_at, updated_at = created_at
		WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING')`)
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_instances SET status = 'READY' WHERE lab_execution_id = $1`, m.execution)
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_executions SET status = 'READY' WHERE id = $1`, m.execution)
}

func (m *mutationDB) reset(user, owner uuid.UUID, key string) (labexecution.MutationAccepted, error) {
	return m.svc.Reset(context.Background(), m.userFor(user), labexecution.ResetInput{LabInstanceID: m.instance[owner].String(), IdempotencyKey: key, RequestID: "request-" + key})
}

func (m *mutationDB) cleanup(user uuid.UUID, key string) (labexecution.MutationAccepted, error) {
	return m.svc.Cleanup(context.Background(), m.userFor(user), labexecution.CleanupInput{LabExecutionID: m.execution.String(), IdempotencyKey: key, RequestID: "request-" + key})
}

func (m *mutationDB) userFor(id uuid.UUID) repository.User {
	u, err := m.store.UserByID(context.Background(), id)
	if err != nil {
		panic(err)
	}
	return u
}

// mutationCounts는 Reset/Cleanup 접수 뒤의 (RESET Operation, CLEANUP Operation, 그 item, active item) 수다.
func (m *mutationDB) mutationCounts(t *testing.T) [4]int {
	t.Helper()
	var c [4]int
	if err := m.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM operations WHERE operation_type = 'RESET'),
		(SELECT count(*) FROM operations WHERE operation_type = 'CLEANUP'),
		(SELECT count(*) FROM operation_items i JOIN operations o ON o.id = i.operation_id WHERE o.operation_type IN ('RESET', 'CLEANUP')),
		(SELECT count(*) FROM operation_items WHERE status IN ('PENDING', 'RUNNING', 'RECONCILING'))`).Scan(&c[0], &c[1], &c[2], &c[3]); err != nil {
		t.Fatal(err)
	}
	return c
}

// history는 접수가 바꾸면 안 되는 이력 전체의 digest다: LabExecution, CreationSnapshot(본문 포함), LabInstance generation/status,
// ProviderResource, Class/LabSpec.
func (m *mutationDB) history(t *testing.T) string {
	t.Helper()
	var digest string
	if err := m.pool.QueryRow(t.Context(), `SELECT md5(concat_ws('|',
		(SELECT string_agg(concat_ws(',', id, status, finished_at, updated_at), ';' ORDER BY id) FROM lab_executions),
		(SELECT string_agg(concat_ws(',', id, lab_execution_id, source_lab_spec_revision, snapshot::text, created_at), ';' ORDER BY id) FROM creation_snapshots),
		(SELECT string_agg(concat_ws(',', id, status, generation, updated_at), ';' ORDER BY id) FROM lab_instances),
		(SELECT string_agg(concat_ws(',', id, generation, provider_id, lifecycle_status, deleted_at, updated_at), ';' ORDER BY id) FROM provider_resources),
		(SELECT string_agg(concat_ws(',', id, name), ';' ORDER BY id) FROM classes),
		(SELECT string_agg(concat_ws(',', id, revision), ';' ORDER BY id) FROM lab_specs)))`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

// insertActiveItem은 다른 key·다른 사용자의 진행 중인 변경 작업을 SQL로 직접 넣는다(Worker가 RUNNING/RECONCILING으로 둔 상태).
func (m *mutationDB) insertActiveItem(t *testing.T, instance uuid.UUID, status string) {
	t.Helper()
	op := uuid.New()
	labexecutiontest.Exec(t, m.conn, `INSERT INTO operations (id, organization_id, operation_type, requested_by_user_id, lab_execution_id,
		idempotency_key_hash, request_fingerprint, status) VALUES ($1, $2, 'CLEANUP', $3, $4, $5, $5, $6)`,
		op, m.f.OrganizationID, m.f.CoInstructorID, m.execution, []byte(op.String()), status)
	labexecutiontest.Exec(t, m.conn, `INSERT INTO operation_items (id, organization_id, operation_id, lab_instance_id, generation, status)
		SELECT $1, organization_id, $2, id, generation, $3 FROM lab_instances WHERE id = $4`, uuid.New(), op, status, instance)
}

func TestResetRegistersNextGenerationAgainstImmutableSnapshot(t *testing.T) {
	m := newMutationDB(t, true)
	// StudentB는 이미 Reset을 두 번 거쳐 generation 3인 상태다.
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_instances SET generation = 3 WHERE id = $1`, m.instance[m.f.StudentBID])
	// Provision 뒤 LabSpec과 mapping이 바뀌었다. Reset은 이를 다시 resolve하지 않는다(D-19).
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_spec_vm_roles SET image_mapping_id = $1 WHERE lab_spec_id = $2`, m.f.ImageMapping2ID, m.f.LabSpecID)
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_specs SET revision = revision + 1 WHERE id = $1`, m.f.LabSpecID)
	labexecutiontest.Exec(t, m.conn, `UPDATE provider_image_mappings SET is_active = false WHERE id = $1`, m.f.ImageMappingID)
	before := m.history(t)

	for _, tt := range []struct {
		owner      uuid.UUID
		generation int64
	}{{m.f.StudentAID, 1}, {m.f.StudentBID, 3}} {
		got, err := m.reset(m.f.InstructorID, tt.owner, "reset-"+tt.owner.String())
		if err != nil || got.Replayed || got.TargetID != m.instance[tt.owner] {
			t.Fatalf("Reset() = %+v, %v", got, err)
		}
		var (
			kind, status, itemStatus string
			target                   uuid.UUID
			execution                *uuid.UUID
			itemGeneration           int64
			items                    int
		)
		if err := m.pool.QueryRow(t.Context(), `SELECT o.operation_type, o.status, o.lab_instance_id, o.lab_execution_id,
			(SELECT count(*) FROM operation_items WHERE operation_id = o.id), i.generation, i.status
			FROM operations o JOIN operation_items i ON i.operation_id = o.id WHERE o.id = $1`, got.OperationID).
			Scan(&kind, &status, &target, &execution, &items, &itemGeneration, &itemStatus); err != nil {
			t.Fatal(err)
		}
		if kind != "RESET" || status != "PENDING" || target != m.instance[tt.owner] || execution != nil || items != 1 ||
			itemGeneration != tt.generation+1 || itemStatus != "PENDING" {
			t.Fatalf("operation=%s/%s target=%s execution=%v items=%d item=%d/%s", kind, status, target, execution, items, itemGeneration, itemStatus)
		}
	}
	// LabInstance의 현재 generation, CreationSnapshot(원래 Image marker 포함), ProviderResource, LabExecution은 그대로다.
	if after := m.history(t); after != before {
		t.Fatal("Reset acceptance changed LabExecution/Snapshot/LabInstance/ProviderResource history")
	}
	var snapshots, withOriginalImage int
	if err := m.pool.QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (WHERE snapshot::text LIKE '%' || $1 || '%') FROM creation_snapshots`,
		labexecutiontest.ImageMarker).Scan(&snapshots, &withOriginalImage); err != nil || snapshots != 1 || withOriginalImage != 1 {
		t.Fatalf("snapshots=%d original=%d err=%v", snapshots, withOriginalImage, err)
	}
	if c := m.mutationCounts(t); c != [4]int{2, 0, 2, 2} {
		t.Fatalf("counts=%v", c)
	}
}

func TestCleanupTargetsStoredInstancesAndKeepsExecutionActive(t *testing.T) {
	m := newMutationDB(t, true)
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_instances SET generation = 2 WHERE id = $1`, m.instance[m.f.StudentAID])
	// 접수 뒤 Class Membership이 바뀌었다: StudentB는 Class를 떠났고 StudentC는 선택되지 않았던 학생이다.
	labexecutiontest.Exec(t, m.conn, `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`, m.f.ClassID, m.f.StudentBID)
	before := m.history(t)

	got, err := m.cleanup(m.f.CoInstructorID, "cleanup-1")
	if err != nil || got.Replayed || got.TargetID != m.execution {
		t.Fatalf("Cleanup() = %+v, %v", got, err)
	}
	rows, err := m.pool.Query(t.Context(), `SELECT i.lab_instance_id, i.generation, i.status FROM operation_items i
		JOIN operations o ON o.id = i.operation_id WHERE o.id = $1 AND o.operation_type = 'CLEANUP' AND o.lab_execution_id = $2 AND o.status = 'PENDING'`,
		got.OperationID, m.execution)
	if err != nil {
		t.Fatal(err)
	}
	items := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var generation int64
		var status string
		if err := rows.Scan(&id, &generation, &status); err != nil || status != "PENDING" {
			t.Fatalf("item status=%s err=%v", status, err)
		}
		items[id] = generation
	}
	want := map[uuid.UUID]int64{m.instance[m.f.InstructorID]: 1, m.instance[m.f.StudentAID]: 2, m.instance[m.f.StudentBID]: 1}
	if fmt.Sprint(items) != fmt.Sprint(want) {
		t.Fatalf("items=%v want %v", items, want)
	}
	if after := m.history(t); after != before {
		t.Fatal("Cleanup acceptance changed history (finished_at, status, generation, snapshot or ProviderResource)")
	}
	// finished_at을 기록하지 않았으므로 Class당 active LabExecution 제약이 그대로 다음 Provision을 막는다.
	if _, err := m.svc.Provision(t.Context(), m.user(t, m.f.InstructorID), m.input("next-provision", m.f.StudentCID)); !errors.Is(err, labexecution.ErrActiveExecution) {
		t.Fatalf("next Provision err=%v", err)
	}
	replay, err := m.cleanup(m.f.CoInstructorID, "cleanup-1")
	if err != nil || !replay.Replayed || replay.OperationID != got.OperationID {
		t.Fatalf("replay=%+v, %v", replay, err)
	}
	if c := m.mutationCounts(t); c != [4]int{0, 1, 3, 3} {
		t.Fatalf("counts=%v", c)
	}
}

// 이미 진행 중인 변경 작업(Worker가 아직 끝내지 않은 PROVISION, RUNNING/RECONCILING Cleanup)이 있으면 Reset/Cleanup 모두 거절하고
// 아무 row도 남기지 않는다. RECONCILING은 결과 불명이므로 완료로 간주하지 않는다.
func TestMutationRejectsActiveItemsIncludingReconciling(t *testing.T) {
	t.Run("pending provision", func(t *testing.T) {
		m := newMutationDB(t, false)
		if _, err := m.reset(m.f.InstructorID, m.f.StudentAID, "reset"); !errors.Is(err, labexecution.ErrMutationInProgress) {
			t.Fatalf("reset err=%v", err)
		}
		if _, err := m.cleanup(m.f.InstructorID, "cleanup"); !errors.Is(err, labexecution.ErrMutationInProgress) {
			t.Fatalf("cleanup err=%v", err)
		}
		if c := m.mutationCounts(t); c != [4]int{0, 0, 0, 3} {
			t.Fatalf("counts=%v", c)
		}
	})
	for _, status := range []string{"RUNNING", "RECONCILING"} {
		t.Run(status, func(t *testing.T) {
			m := newMutationDB(t, true)
			m.insertActiveItem(t, m.instance[m.f.StudentAID], status)
			if _, err := m.reset(m.f.InstructorID, m.f.StudentAID, "reset"); !errors.Is(err, labexecution.ErrMutationInProgress) {
				t.Fatalf("reset err=%v", err)
			}
			if _, err := m.cleanup(m.f.InstructorID, "cleanup"); !errors.Is(err, labexecution.ErrMutationInProgress) {
				t.Fatalf("cleanup err=%v", err)
			}
			// 다른 학생의 Reset은 영향을 받지 않는다.
			if _, err := m.reset(m.f.InstructorID, m.f.StudentBID, "reset-b"); err != nil {
				t.Fatalf("reset other student err=%v", err)
			}
			if c := m.mutationCounts(t); c != [4]int{1, 1, 2, 2} {
				t.Fatalf("counts=%v", c)
			}
		})
	}
}

// Cleanup은 LabInstance id 순서로 item을 등록한다. 마지막 LabInstance에만 진행 중인 작업이 있으면 앞서 INSERT한 item과 Operation까지
// 모두 rollback되어 일부 LabInstance만 등록된 Cleanup이 남지 않는다.
func TestCleanupRollsBackEveryItemWhenLastInsertConflicts(t *testing.T) {
	m := newMutationDB(t, true)
	var last uuid.UUID
	if err := m.pool.QueryRow(t.Context(), `SELECT id FROM lab_instances WHERE lab_execution_id = $1 ORDER BY id DESC LIMIT 1`, m.execution).Scan(&last); err != nil {
		t.Fatal(err)
	}
	m.insertActiveItem(t, last, "RECONCILING")
	before := m.history(t)
	if _, err := m.cleanup(m.f.InstructorID, "cleanup"); !errors.Is(err, labexecution.ErrMutationInProgress) {
		t.Fatalf("err=%v", err)
	}
	// insertActiveItem이 만든 CLEANUP Operation 1개와 item 1개만 남는다.
	if c := m.mutationCounts(t); c != [4]int{0, 1, 1, 1} {
		t.Fatalf("counts=%v", c)
	}
	if m.history(t) != before {
		t.Fatal("history changed")
	}
}

// race는 같은 LabExecution의 요청들을 replay 사전 확인 뒤 LabExecution 잠금 지점에 모두 세운 다음 한꺼번에 경쟁시킨다.
func (m *mutationDB) race(t *testing.T, calls []func() (labexecution.MutationAccepted, error)) []mutationResult {
	t.Helper()
	tx, err := m.holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM lab_executions WHERE id = $1 FOR UPDATE`, m.execution); err != nil {
		t.Fatal(err)
	}
	results := make([]mutationResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := call()
			results[i] = mutationResult{a, err}
		}()
	}
	m.waitForLockWaiters(t, len(calls))
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	return results
}

type mutationResult struct {
	accepted labexecution.MutationAccepted
	err      error
}

func TestConcurrentMutationsArbitrateWithActiveItemIndex(t *testing.T) {
	t.Run("same key reset replays", func(t *testing.T) {
		m := newMutationDB(t, true)
		calls := make([]func() (labexecution.MutationAccepted, error), 5)
		for i := range calls {
			calls[i] = func() (labexecution.MutationAccepted, error) {
				return m.reset(m.f.InstructorID, m.f.StudentAID, "same-key")
			}
		}
		results := m.race(t, calls)
		created, replayed := 0, 0
		var operationID uuid.UUID
		for _, r := range results {
			if r.err != nil {
				t.Fatalf("err=%v", r.err)
			}
			if operationID != uuid.Nil && r.accepted.OperationID != operationID {
				t.Fatalf("different operations %s %s", operationID, r.accepted.OperationID)
			}
			operationID = r.accepted.OperationID
			if r.accepted.Replayed {
				replayed++
			} else {
				created++
			}
		}
		if created != 1 || replayed != 4 {
			t.Fatalf("created=%d replayed=%d", created, replayed)
		}
		if c := m.mutationCounts(t); c != [4]int{1, 0, 1, 1} {
			t.Fatalf("counts=%v", c)
		}
	})
	t.Run("same key cleanup replays", func(t *testing.T) {
		m := newMutationDB(t, true)
		calls := make([]func() (labexecution.MutationAccepted, error), 4)
		for i := range calls {
			calls[i] = func() (labexecution.MutationAccepted, error) { return m.cleanup(m.f.InstructorID, "same-cleanup") }
		}
		created := 0
		for _, r := range m.race(t, calls) {
			if r.err != nil {
				t.Fatalf("err=%v", r.err)
			}
			if !r.accepted.Replayed {
				created++
			}
		}
		if created != 1 {
			t.Fatalf("created=%d", created)
		}
		if c := m.mutationCounts(t); c != [4]int{0, 1, 3, 3} {
			t.Fatalf("counts=%v", c)
		}
	})
	t.Run("different keys and kinds conflict", func(t *testing.T) {
		m := newMutationDB(t, true)
		results := m.race(t, []func() (labexecution.MutationAccepted, error){
			func() (labexecution.MutationAccepted, error) {
				return m.reset(m.f.InstructorID, m.f.StudentAID, "reset-1")
			},
			func() (labexecution.MutationAccepted, error) {
				return m.reset(m.f.CoInstructorID, m.f.StudentAID, "reset-2")
			},
			func() (labexecution.MutationAccepted, error) { return m.cleanup(m.f.InstructorID, "cleanup-1") },
			func() (labexecution.MutationAccepted, error) { return m.cleanup(m.f.CoInstructorID, "cleanup-2") },
		})
		var winners []mutationResult
		for _, r := range results {
			switch {
			case r.err == nil:
				winners = append(winners, r)
			case !errors.Is(r.err, labexecution.ErrMutationInProgress):
				t.Fatalf("err=%v", r.err)
			}
		}
		// StudentA를 공유하므로 넷 중 정확히 하나만 등록된다. 등록된 쪽의 row만 남는다.
		if len(winners) != 1 {
			t.Fatalf("winners=%d results=%+v", len(winners), results)
		}
		c := m.mutationCounts(t)
		if c != [4]int{1, 0, 1, 1} && c != [4]int{0, 1, 3, 3} {
			t.Fatalf("counts=%v", c)
		}
	})
}

// 강사 Membership 경쟁: 등록 transaction은 강사 Membership을 FOR SHARE로 다시 읽어 진행 중인 삭제·강등의 commit/rollback을 기다린다.
func TestMutationRechecksInstructorMembershipUnderConcurrentChange(t *testing.T) {
	for _, kind := range []string{"reset", "cleanup"} {
		for _, change := range []struct{ name, sql string }{
			{"delete", `DELETE FROM class_memberships WHERE class_id = $1 AND user_id = $2`},
			{"demote", `UPDATE class_memberships SET role = 'STUDENT' WHERE class_id = $1 AND user_id = $2`},
		} {
			for _, commit := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s %s commit=%v", kind, change.name, commit), func(t *testing.T) {
					m := newMutationDB(t, true)
					tx, err := m.holder.Begin(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback(context.Background()) }()
					if _, err := tx.Exec(t.Context(), change.sql, m.f.ClassID, m.f.InstructorID); err != nil {
						t.Fatal(err)
					}
					done := make(chan mutationResult, 1)
					go func() {
						var a labexecution.MutationAccepted
						var err error
						if kind == "reset" {
							a, err = m.reset(m.f.InstructorID, m.f.StudentAID, "membership-race")
						} else {
							a, err = m.cleanup(m.f.InstructorID, "membership-race")
						}
						done <- mutationResult{a, err}
					}()
					m.waitForLockWaiters(t, 1)
					if commit {
						err = tx.Commit(t.Context())
					} else {
						err = tx.Rollback(t.Context())
					}
					if err != nil {
						t.Fatal(err)
					}
					r := <-done
					if commit {
						if !errors.Is(r.err, labexecution.ErrForbidden) {
							t.Fatalf("err=%v", r.err)
						}
						if c := m.mutationCounts(t); c != [4]int{} {
							t.Fatalf("counts=%v", c)
						}
						return
					}
					if r.err != nil || r.accepted.Replayed {
						t.Fatalf("result=%+v %v", r.accepted, r.err)
					}
				})
			}
		}
	}
}

// generation 경쟁: Reset 접수는 LabInstance를 FOR SHARE로 잠근 뒤 generation을 읽는다. 진행 중인 generation 변경(Worker의 전환)이
// 있으면 그 commit을 기다린 뒤의 값에 +1한 item을 등록하고, rollback이면 원래 값에 +1이다. LabInstance generation 자체는 바꾸지 않는다.
func TestResetUsesGenerationAfterConcurrentWriter(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(fmt.Sprintf("commit=%v", commit), func(t *testing.T) {
			m := newMutationDB(t, true)
			id := m.instance[m.f.StudentAID]
			tx, err := m.holder.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(t.Context(), `UPDATE lab_instances SET generation = 2 WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			done := make(chan mutationResult, 1)
			go func() {
				a, err := m.reset(m.f.InstructorID, m.f.StudentAID, "generation-race")
				done <- mutationResult{a, err}
			}()
			m.waitForLockWaiters(t, 1)
			if commit {
				err = tx.Commit(t.Context())
			} else {
				err = tx.Rollback(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			r := <-done
			if r.err != nil {
				t.Fatal(r.err)
			}
			var current, item int64
			if err := m.pool.QueryRow(t.Context(), `SELECT li.generation, i.generation FROM lab_instances li JOIN operation_items i ON i.lab_instance_id = li.id
				WHERE i.operation_id = $1`, r.accepted.OperationID).Scan(&current, &item); err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if commit {
				want = 2
			}
			if current != want || item != want+1 {
				t.Fatalf("current=%d item=%d want %d/%d", current, item, want, want+1)
			}
		})
	}
}

// 등록 transaction이 LabExecution과 LabInstance를 잠그고 있는 동안 Worker의 generation 전환과 finished_at 기록은 lock을 얻지 못한다.
// commit 뒤에는 같은 UPDATE가 성공한다.
func TestLabExecutionForShareHoldsGenerationAndFinishLocksUntilCommit(t *testing.T) {
	m := newMutationDB(t, true)
	writer := postgrestest.Connect(t, m.dsn)
	labexecutiontest.Exec(t, writer, `SET lock_timeout = '200ms'`)
	err := m.store.WithinTransaction(t.Context(), func(ctx context.Context, repos repository.Repositories) error {
		locked, err := repos.LabExecutionForShare(ctx, m.execution)
		if err != nil {
			return err
		}
		if len(locked.LabInstances) != 3 || !locked.HasCreationSnapshot || locked.FinishedAt != nil {
			return fmt.Errorf("locked=%+v", locked)
		}
		for i := 1; i < len(locked.LabInstances); i++ {
			if locked.LabInstances[i-1].ID.String() >= locked.LabInstances[i].ID.String() {
				return errors.New("LabInstances are not in id order")
			}
		}
		for _, sql := range []string{
			`UPDATE lab_instances SET generation = generation + 1 WHERE id = '` + m.instance[m.f.StudentAID].String() + `'`,
			`UPDATE lab_executions SET finished_at = now() WHERE id = '` + m.execution.String() + `'`,
		} {
			_, err := writer.Exec(t.Context(), sql)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
				return fmt.Errorf("%s during lock err=%v, want lock_not_available", sql, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	labexecutiontest.Exec(t, writer, `UPDATE lab_instances SET generation = generation + 1 WHERE id = $1`, m.instance[m.f.StudentAID])
	labexecutiontest.Exec(t, writer, `UPDATE lab_executions SET finished_at = now() WHERE id = $1`, m.execution)
}

// Worker가 Cleanup을 끝내 finished_at을 기록한 뒤에는 새 Reset/Cleanup을 거절하지만, 그 전에 접수된 요청의 replay는 그대로 200이다.
// 그리고 같은 Class에서 다음 Provision을 시작할 수 있다(D-10).
func TestFinishedExecutionRejectsNewMutationsButReplaysAndAllowsNextProvision(t *testing.T) {
	m := newMutationDB(t, true)
	first, err := m.cleanup(m.f.InstructorID, "cleanup-1")
	if err != nil {
		t.Fatal(err)
	}
	m.settle(t)
	labexecutiontest.Exec(t, m.conn, `UPDATE lab_executions SET finished_at = now(), status = 'CLEANED' WHERE id = $1`, m.execution)

	if _, err := m.cleanup(m.f.InstructorID, "cleanup-2"); !errors.Is(err, labexecution.ErrExecutionFinished) {
		t.Fatalf("cleanup err=%v", err)
	}
	if _, err := m.reset(m.f.InstructorID, m.f.StudentAID, "reset-1"); !errors.Is(err, labexecution.ErrExecutionFinished) {
		t.Fatalf("reset err=%v", err)
	}
	if replay, err := m.cleanup(m.f.InstructorID, "cleanup-1"); err != nil || !replay.Replayed || replay.OperationID != first.OperationID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	if a, err := m.svc.Provision(t.Context(), m.user(t, m.f.InstructorID), m.input("next-provision", m.f.StudentCID)); err != nil || a.Replayed {
		t.Fatalf("next Provision=%+v %v", a, err)
	}
}
