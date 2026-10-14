package labexecution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// state는 fakeStore가 commit한 row다. transaction은 사본에서 작업하고 callback이 nil을 반환할 때만 교체한다(rollback 의미).
type state struct {
	classes     map[uuid.UUID]repository.Class
	memberships map[[2]uuid.UUID]repository.ClassMembership
	specs       map[uuid.UUID]repository.LabSpec
	images      map[uuid.UUID]repository.ResolvedImageMapping
	flavors     map[uuid.UUID]repository.ResolvedFlavorMapping

	executions []repository.NewLabExecution
	snapshots  []repository.NewCreationSnapshot
	instances  []repository.NewLabInstance
	operations []repository.Operation
	items      []repository.NewOperationItem
}

func (s *state) clone() *state {
	c := *s
	c.executions = append([]repository.NewLabExecution(nil), s.executions...)
	c.snapshots = append([]repository.NewCreationSnapshot(nil), s.snapshots...)
	c.instances = append([]repository.NewLabInstance(nil), s.instances...)
	c.operations = append([]repository.Operation(nil), s.operations...)
	c.items = append([]repository.NewOperationItem(nil), s.items...)
	return &c
}

// fakeStore는 Service가 쓰는 query만 구현한다. 실제 Repository처럼 권한을 판단하지 않고 저장된 값을 그대로 반환한다.
type fakeStore struct {
	repository.Repositories
	committed *state
	// beforeTransaction이 있으면 transaction을 시작하기 직전(transaction 밖의 권한 확인 뒤)에 commit된 상태로 호출된다.
	beforeTransaction func(committed *state)
	// beforeExecutionInsert가 있으면 CreateLabExecution 직전에 호출된다. 경쟁 요청의 commit을 재현한다.
	beforeExecutionInsert func(committed *state)
	// failInstance가 있으면 CreateLabInstance가 이 오류를 반환한다.
	failInstance error
	// readInstance는 LabInstanceByID의 결과다.
	readInstances map[uuid.UUID]repository.LabInstance
	// readExecutions는 LabExecutionByID의 결과다.
	readExecutions map[uuid.UUID]repository.LabExecution
}

func newFakeStore() *fakeStore {
	return &fakeStore{committed: &state{
		classes: map[uuid.UUID]repository.Class{}, memberships: map[[2]uuid.UUID]repository.ClassMembership{},
		specs: map[uuid.UUID]repository.LabSpec{}, images: map[uuid.UUID]repository.ResolvedImageMapping{},
		flavors: map[uuid.UUID]repository.ResolvedFlavorMapping{},
	}, readInstances: map[uuid.UUID]repository.LabInstance{}, readExecutions: map[uuid.UUID]repository.LabExecution{}}
}

func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	if f.beforeTransaction != nil {
		f.beforeTransaction(f.committed)
	}
	tx := &fakeTx{store: f, s: f.committed.clone()}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	f.committed = tx.s
	return nil
}

// store 밖(transaction 없이)의 query는 commit된 상태를 읽는다.
func (f *fakeStore) view() *fakeTx { return &fakeTx{store: f, s: f.committed} }

func (f *fakeStore) ClassByID(ctx context.Context, id uuid.UUID) (repository.Class, error) {
	return f.view().ClassByID(ctx, id)
}
func (f *fakeStore) ClassMembership(ctx context.Context, classID, userID uuid.UUID) (repository.ClassMembership, error) {
	return f.view().ClassMembership(ctx, classID, userID)
}
func (f *fakeStore) OperationByIdempotency(ctx context.Context, user uuid.UUID, kind string, hash []byte) (repository.Operation, error) {
	return f.view().OperationByIdempotency(ctx, user, kind, hash)
}
func (f *fakeStore) LabExecutionByID(_ context.Context, id uuid.UUID) (repository.LabExecution, error) {
	e, ok := f.readExecutions[id]
	if !ok {
		return repository.LabExecution{}, repository.ErrNotFound
	}
	return e, nil
}
func (f *fakeStore) LabInstanceByID(_ context.Context, id uuid.UUID) (repository.LabInstance, error) {
	li, ok := f.readInstances[id]
	if !ok {
		return repository.LabInstance{}, repository.ErrNotFound
	}
	return li, nil
}

type fakeTx struct {
	repository.Repositories
	store *fakeStore
	s     *state
}

func (t *fakeTx) ClassByID(_ context.Context, id uuid.UUID) (repository.Class, error) {
	c, ok := t.s.classes[id]
	if !ok {
		return repository.Class{}, repository.ErrNotFound
	}
	return c, nil
}

func (t *fakeTx) ClassMembership(_ context.Context, classID, userID uuid.UUID) (repository.ClassMembership, error) {
	m, ok := t.s.memberships[[2]uuid.UUID{classID, userID}]
	if !ok {
		return repository.ClassMembership{}, repository.ErrNotFound
	}
	return m, nil
}

func (t *fakeTx) LabSpecForShare(_ context.Context, id uuid.UUID) (repository.LabSpec, error) {
	s, ok := t.s.specs[id]
	if !ok {
		return repository.LabSpec{}, repository.ErrNotFound
	}
	return s, nil
}

func (t *fakeTx) ResolvedImageMappingsForShare(_ context.Context, org uuid.UUID, ids []uuid.UUID) ([]repository.ResolvedImageMapping, error) {
	var out []repository.ResolvedImageMapping
	for _, id := range ids {
		if m, ok := t.s.images[id]; ok && m.OrganizationID == org {
			out = append(out, m)
		}
	}
	return out, nil
}

func (t *fakeTx) ResolvedFlavorMappingsForShare(_ context.Context, org uuid.UUID, ids []uuid.UUID) ([]repository.ResolvedFlavorMapping, error) {
	var out []repository.ResolvedFlavorMapping
	for _, id := range ids {
		if m, ok := t.s.flavors[id]; ok && m.OrganizationID == org {
			out = append(out, m)
		}
	}
	return out, nil
}

func (t *fakeTx) ClassMembershipsForShare(_ context.Context, classID uuid.UUID, users []uuid.UUID) ([]repository.ClassMembership, error) {
	var out []repository.ClassMembership
	for _, u := range users {
		if m, ok := t.s.memberships[[2]uuid.UUID{classID, u}]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (t *fakeTx) ActiveLabExecutionByClass(_ context.Context, classID uuid.UUID) (repository.LabExecutionSummary, error) {
	for _, e := range t.s.executions {
		if e.ClassID == classID {
			return repository.LabExecutionSummary{ID: e.ID, Status: e.Status}, nil
		}
	}
	return repository.LabExecutionSummary{}, repository.ErrNotFound
}

func (t *fakeTx) CreateLabExecution(_ context.Context, e repository.NewLabExecution) error {
	if t.store.beforeExecutionInsert != nil {
		t.store.beforeExecutionInsert(t.store.committed)
		t.s.operations = append([]repository.Operation(nil), t.store.committed.operations...)
		t.s.executions = append([]repository.NewLabExecution(nil), t.store.committed.executions...)
	}
	for _, existing := range t.s.executions {
		if existing.ClassID == e.ClassID {
			return &repository.Error{Kind: repository.KindConflict, Op: "CreateLabExecution"}
		}
	}
	t.s.executions = append(t.s.executions, e)
	return nil
}

func (t *fakeTx) CreateCreationSnapshot(_ context.Context, s repository.NewCreationSnapshot) error {
	if !json.Valid(s.Snapshot) {
		return &repository.Error{Kind: repository.KindConstraintViolation, Op: "CreateCreationSnapshot"}
	}
	t.s.snapshots = append(t.s.snapshots, s)
	return nil
}

func (t *fakeTx) CreateLabInstance(_ context.Context, i repository.NewLabInstance) error {
	if t.store.failInstance != nil {
		return t.store.failInstance
	}
	t.s.instances = append(t.s.instances, i)
	return nil
}

func (t *fakeTx) InsertOperation(_ context.Context, o repository.NewOperation) (bool, error) {
	for _, existing := range t.s.operations {
		if existing.RequestedByUserID == o.RequestedByUserID && existing.Type == o.Type && bytes.Equal(existing.IdempotencyKeyHash, o.IdempotencyKeyHash) {
			return false, nil
		}
	}
	t.s.operations = append(t.s.operations, repository.Operation{
		ID: o.ID, OrganizationID: o.OrganizationID, RequestedByUserID: o.RequestedByUserID, Type: o.Type, Status: "PENDING",
		LabExecutionID: o.LabExecutionID, LabInstanceID: o.LabInstanceID, RequestID: o.RequestID,
		IdempotencyKeyHash: o.IdempotencyKeyHash, RequestFingerprint: o.RequestFingerprint, CreatedAt: o.CreatedAt, UpdatedAt: o.CreatedAt,
		Traceparent: o.Traceparent, Tracestate: o.Tracestate,
	})
	return true, nil
}

func (t *fakeTx) OperationByIdempotency(_ context.Context, user uuid.UUID, kind string, hash []byte) (repository.Operation, error) {
	for _, o := range t.s.operations {
		if o.RequestedByUserID == user && o.Type == kind && bytes.Equal(o.IdempotencyKeyHash, hash) {
			return o, nil
		}
	}
	return repository.Operation{}, &repository.Error{Kind: repository.KindNotFound, Op: "OperationByIdempotency"}
}

func (t *fakeTx) OperationByID(_ context.Context, id uuid.UUID) (repository.Operation, error) {
	for _, o := range t.s.operations {
		if o.ID == id {
			return o, nil
		}
	}
	return repository.Operation{}, repository.ErrNotFound
}

func (t *fakeTx) CreateOperationItem(_ context.Context, i repository.NewOperationItem) error {
	t.s.items = append(t.s.items, i)
	return nil
}

func (s *state) rowCounts() [5]int {
	return [5]int{len(s.executions), len(s.snapshots), len(s.instances), len(s.operations), len(s.items)}
}

func sortedUUIDs(ids []uuid.UUID) []uuid.UUID {
	out := append([]uuid.UUID(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

var errInjected = errors.New("injected failure dial tcp 10.0.0.7:5432 password=hunter2")
