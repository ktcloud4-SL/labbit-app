package workspacefile

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

var (
	orgID       = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	classID     = uuid.MustParse("00000000-0000-4000-8000-0000000000b1")
	ownerID     = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	otherID     = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	otherOrgID  = uuid.MustParse("00000000-0000-4000-8000-0000000000a2")
	labID       = uuid.MustParse("00000000-0000-4000-8000-0000000000d1")
	connectorID = uuid.MustParse("00000000-0000-4000-8000-0000000000c1")
	owner       = repository.User{ID: ownerID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}
)

// 본문 marker다. 어떤 오류, log에도 나타나면 안 된다.
const sourceMarker = "SOURCE-MARKER-must-not-leak"

// fakeStore는 resolve의 판정만 검증하기 위한 in-memory Store다. 구현하지 않은 method는 호출되면 nil embed로 panic한다.
type fakeStore struct {
	repository.Repositories

	mu           sync.Mutex
	lab          repository.LabInstance
	labErr       error
	membership   repository.ClassMembership
	memberErr    error
	snapshot     repository.CreationSnapshotTargets
	snapshotErr  error
	connectorErr error
	servers      map[string][]repository.ProviderServer
	serversErr   error

	// current는 LabInstanceByID(외부 I/O 뒤 재확인)가 반환하는 LabInstance다. nil이면 lab이다.
	current    *repository.LabInstance
	currentErr error

	inTransaction bool
	transactions  int
	calls         []string
	serverQueries []serverQuery
}

type serverQuery struct {
	generation int64
	name       string
}

func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	f.mu.Lock()
	f.transactions++
	f.inTransaction = true
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inTransaction = false
		f.mu.Unlock()
	}()
	return fn(ctx, f)
}

func (f *fakeStore) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeStore) LabInstanceForShare(context.Context, uuid.UUID) (repository.LabInstance, error) {
	f.record("LabInstanceForShare")
	return f.lab, f.labErr
}

func (f *fakeStore) LabInstanceByID(context.Context, uuid.UUID) (repository.LabInstance, error) {
	f.record("LabInstanceByID")
	if f.currentErr != nil {
		return repository.LabInstance{}, f.currentErr
	}
	if f.current != nil {
		return *f.current, nil
	}
	return f.lab, nil
}

func (f *fakeStore) ClassMembership(context.Context, uuid.UUID, uuid.UUID) (repository.ClassMembership, error) {
	f.record("ClassMembership")
	return f.membership, f.memberErr
}

func (f *fakeStore) CreationSnapshotTargets(context.Context, uuid.UUID) (repository.CreationSnapshotTargets, error) {
	f.record("CreationSnapshotTargets")
	return f.snapshot, f.snapshotErr
}

func (f *fakeStore) ConnectorIDForLabInstance(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.record("ConnectorIDForLabInstance")
	if f.connectorErr != nil {
		return uuid.UUID{}, f.connectorErr
	}
	return connectorID, nil
}

func (f *fakeStore) ProviderServers(_ context.Context, _ uuid.UUID, generation int64, logicalName string) ([]repository.ProviderServer, error) {
	f.record("ProviderServers")
	f.mu.Lock()
	f.serverQueries = append(f.serverQueries, serverQuery{generation: generation, name: logicalName})
	f.mu.Unlock()
	return f.servers[logicalName], f.serversErr
}

func (f *fakeStore) insideTransaction() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inTransaction
}

func (f *fakeStore) calledNothing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls) == 0 && f.transactions == 0
}

// fakeTransport는 호출 수와 받은 값을 기록한다.
type fakeTransport struct {
	store *fakeStore

	mu    sync.Mutex
	calls []string
	// target, dir, file, max, expected, content는 마지막 호출이 받은 값이다.
	target   Target
	path     Path
	max      int64
	expected Revision
	content  []byte
	// wasInTransaction은 Transport가 호출된 순간 DB transaction이 열려 있었는지 기록한다.
	wasInTransaction bool

	entries  []Entry
	treeErr  error
	data     FileData
	readErr  error
	revision Revision
	saveErr  error

	// hook은 Transport가 응답하기 직전에 실행한다. Reset race를 흉내 내는 데 쓴다.
	hook func()
}

func (f *fakeTransport) enter(op string, target Target, path Path) {
	f.mu.Lock()
	f.calls = append(f.calls, op)
	f.target, f.path = target, path
	f.wasInTransaction = f.wasInTransaction || (f.store != nil && f.store.insideTransaction())
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
}

func (f *fakeTransport) Tree(_ context.Context, target Target, dir Path) ([]Entry, error) {
	f.enter("Tree", target, dir)
	return f.entries, f.treeErr
}

func (f *fakeTransport) Read(_ context.Context, target Target, file Path, maxBytes int64) (FileData, error) {
	f.enter("Read", target, file)
	f.mu.Lock()
	f.max = maxBytes
	f.mu.Unlock()
	return f.data, f.readErr
}

func (f *fakeTransport) Save(_ context.Context, target Target, file Path, expected Revision, content []byte) (Revision, error) {
	f.enter("Save", target, file)
	f.mu.Lock()
	f.expected, f.content = expected, append([]byte(nil), content...)
	f.mu.Unlock()
	return f.revision, f.saveErr
}

func (f *fakeTransport) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newStore() *fakeStore {
	return &fakeStore{
		lab: repository.LabInstance{
			ID: labID, OrganizationID: orgID, LabExecutionID: uuid.New(), ClassID: classID, UserID: ownerID,
			Status: LabInstanceStatusReady, Generation: 3,
		},
		membership: repository.ClassMembership{OrganizationID: orgID, ClassID: classID, UserID: ownerID, Role: repository.ClassRoleStudent},
		snapshot: repository.CreationSnapshotTargets{
			WorkspaceVMKey: "vk-web",
			VMs: []repository.SnapshotVM{
				{VMKey: "vk-db", Role: "db", InstanceIndex: 0},
				{VMKey: "vk-web", Role: "web", InstanceIndex: 0},
			},
		},
		servers: map[string][]repository.ProviderServer{
			"vk-web": {{ID: uuid.New(), ProviderID: "srv-web-g3", LifecycleStatus: ProviderResourcePresent}},
			"vk-db":  {{ID: uuid.New(), ProviderID: "srv-db-g3", LifecycleStatus: ProviderResourcePresent}},
		},
	}
}

func newService(t *testing.T, store *fakeStore, transport *fakeTransport, mutate ...func(*Options)) *Service {
	t.Helper()
	transport.store = store
	opts := Options{Store: store, Transport: transport}
	for _, m := range mutate {
		m(&opts)
	}
	s, err := NewService(opts)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return s
}

func TestNewServiceValidatesOptions(t *testing.T) {
	if _, err := NewService(Options{Transport: &fakeTransport{}}); err == nil {
		t.Error("NewService() without Store succeeded")
	}
	if _, err := NewService(Options{Store: newStore()}); err == nil {
		t.Error("NewService() without Transport succeeded")
	}
	if _, err := NewService(Options{Store: newStore(), Transport: &fakeTransport{}, MaxFileBytes: -1}); err == nil {
		t.Error("NewService() with negative MaxFileBytes succeeded")
	}
	s, err := NewService(Options{Store: newStore(), Transport: &fakeTransport{}})
	if err != nil || s.MaxFileBytes() != DefaultMaxFileBytes {
		t.Fatalf("default MaxFileBytes = %v, %v, want %d", s.MaxFileBytes(), err, DefaultMaxFileBytes)
	}
}

// 소유한 READY LabInstance에서 CreationSnapshot의 workspaceVmKey로 대상 VM을 결정한다.
func TestResolvesTheWorkspaceVMFromTheImmutableCreationSnapshot(t *testing.T) {
	store := newStore()
	transport := &fakeTransport{entries: []Entry{{Name: "main.py", Kind: KindFile}}}
	s := newService(t, store, transport)

	if _, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String(), Path: "", RequestID: "req-1"}); err != nil {
		t.Fatalf("Tree() error = %v", err)
	}

	want := Target{
		LabInstanceID: labID, Generation: 3, ConnectorID: connectorID,
		WorkspaceVMKey: "vk-web", ProviderServerID: "srv-web-g3", RequestID: "req-1",
	}
	if transport.target != want {
		t.Fatalf("Transport target = %+v, want %+v", transport.target, want)
	}
	// 같은 LabInstance의 다른 VM(vk-db)이 아니라 workspaceVmKey의 현재 generation 서버를 조회한다.
	if len(store.serverQueries) != 1 || store.serverQueries[0] != (serverQuery{generation: 3, name: "vk-web"}) {
		t.Fatalf("ProviderServers queries = %+v", store.serverQueries)
	}
	// 소유·Membership·READY·snapshot·Connector·서버를 하나의 transaction에서 읽고 commit한 뒤 외부 I/O를 한다.
	if store.transactions != 1 {
		t.Fatalf("transactions = %d, want 1", store.transactions)
	}
	if transport.wasInTransaction {
		t.Fatal("Transport가 DB transaction 안에서 호출됨")
	}
	if got := store.calls[0]; got != "LabInstanceForShare" {
		t.Fatalf("first call = %q, want LabInstanceForShare", got)
	}
}

func TestRejectedPathNeverReachesTheStoreOrTheTransport(t *testing.T) {
	for _, raw := range []string{
		"/etc/passwd", "/foo", "../x", "a/../b", ".", "a/./b", `..\x`, `a\b`, "a\x00b", "a//b", "a/", "/a",
		"%2e%2e", "%2E%2E", "a%2fb", "a%5cb", "%252e%252e", "a%252fb", "a%255cb",
	} {
		for _, op := range []string{"tree", "read", "save"} {
			store := newStore()
			transport := &fakeTransport{}
			s := newService(t, store, transport)

			var err error
			switch op {
			case "tree":
				_, err = s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String(), Path: raw})
			case "read":
				_, err = s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: raw})
			default:
				_, err = s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: raw, IfMatchRevision: "rev1", Content: "x"})
			}
			if !errors.Is(err, ErrInvalidPath) {
				t.Errorf("%s(%q) error = %v, want ErrInvalidPath", op, raw, err)
			}
			if got := transport.callCount(); got != 0 {
				t.Errorf("%s(%q) called the transport %d times", op, raw, got)
			}
			if !store.calledNothing() {
				t.Errorf("%s(%q) touched the store: calls=%v transactions=%d", op, raw, store.calls, store.transactions)
			}
		}
	}
}

func TestReadAndSaveRejectTheEmptyPathButTreeAcceptsRoot(t *testing.T) {
	store := newStore()
	transport := &fakeTransport{}
	s := newService(t, store, transport)
	if _, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: ""}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Read(\"\") error = %v, want ErrInvalidPath", err)
	}
	if _, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "", IfMatchRevision: "r", Content: "x"}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("Save(\"\") error = %v, want ErrInvalidPath", err)
	}
	if transport.callCount() != 0 {
		t.Fatalf("transport called %d times", transport.callCount())
	}
	if _, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String(), Path: ""}); err != nil {
		t.Fatalf("Tree(root) error = %v", err)
	}
	if transport.target.LabInstanceID != labID || !transport.path.IsRoot() {
		t.Fatalf("Tree(root) reached the transport with %+v path=%q", transport.target, transport.path.String())
	}
}

func TestAuthorizationRejectsBeforeTheTransport(t *testing.T) {
	otherMember := func(s *fakeStore) { s.lab.UserID = otherID }
	cases := []struct {
		name   string
		mutate func(*fakeStore)
		id     string
		want   error
	}{
		{"other user's LabInstance", otherMember, labID.String(), ErrForbidden},
		{"other organization's LabInstance", func(s *fakeStore) { s.lab.OrganizationID = otherOrgID }, labID.String(), ErrForbidden},
		{"instructor of another user's LabInstance", func(s *fakeStore) {
			s.lab.UserID = otherID
			s.membership.Role = repository.ClassRoleInstructor
		}, labID.String(), ErrForbidden},
		{"no current ClassMembership", func(s *fakeStore) { s.memberErr = repository.ErrNotFound }, labID.String(), ErrForbidden},
		{"membership of a different class", func(s *fakeStore) { s.membership.ClassID = uuid.New() }, labID.String(), ErrInconsistentData},
		{"membership of a different user", func(s *fakeStore) { s.membership.UserID = otherID }, labID.String(), ErrInconsistentData},
		{"membership of a different organization", func(s *fakeStore) { s.membership.OrganizationID = otherOrgID }, labID.String(), ErrInconsistentData},
		{"membership with an unknown role", func(s *fakeStore) { s.membership.Role = "OWNER" }, labID.String(), ErrInconsistentData},
		{"LabInstance not found", func(s *fakeStore) { s.labErr = repository.ErrNotFound }, labID.String(), ErrNotFound},
		{"LabInstance id is not canonical", func(*fakeStore) {}, strings.ToUpper(labID.String()), ErrNotFound},
		{"LabInstance id is not a UUID", func(*fakeStore) {}, "not-a-uuid", ErrNotFound},
		{"LabInstance id is empty", func(*fakeStore) {}, "", ErrNotFound},
	}
	for _, tc := range cases {
		for _, op := range []string{"tree", "read", "save"} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				store := newStore()
				tc.mutate(store)
				transport := &fakeTransport{}
				s := newService(t, store, transport)

				var err error
				switch op {
				case "tree":
					_, err = s.Tree(t.Context(), owner, TreeInput{LabInstanceID: tc.id})
				case "read":
					_, err = s.Read(t.Context(), owner, ReadInput{LabInstanceID: tc.id, Path: "a.txt"})
				default:
					_, err = s.Save(t.Context(), owner, SaveInput{LabInstanceID: tc.id, Path: "a.txt", IfMatchRevision: "rev1", Content: "x"})
				}
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
				if transport.callCount() != 0 {
					t.Fatalf("transport called %d times", transport.callCount())
				}
			})
		}
	}
}

// 강사와 Organization ADMIN도 다른 사용자의 Workspace를 열 수 없다. LabInstance.user_id가 현재 사용자여야 한다.
func TestOrganizationAdminCannotOpenAnotherUsersWorkspace(t *testing.T) {
	store := newStore()
	store.lab.UserID = otherID
	admin := repository.User{ID: ownerID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleAdmin}
	transport := &fakeTransport{}
	s := newService(t, store, transport)
	if _, err := s.Read(t.Context(), admin, ReadInput{LabInstanceID: labID.String(), Path: "a.txt"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Read() error = %v, want ErrForbidden", err)
	}
	if transport.callCount() != 0 {
		t.Fatal("transport called")
	}
}

func TestOnlyReadyLabInstancesAreUsable(t *testing.T) {
	for _, status := range []string{"PROVISIONING", "RESETTING", "FAILED", "CLEANING_UP", "DELETED", "ready", ""} {
		store := newStore()
		store.lab.Status = status
		transport := &fakeTransport{}
		s := newService(t, store, transport)
		_, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()})
		if !errors.Is(err, ErrLabInstanceNotReady) {
			t.Errorf("status %q: error = %v, want ErrLabInstanceNotReady", status, err)
		}
		if transport.callCount() != 0 {
			t.Errorf("status %q: transport called", status)
		}
	}
}

func TestWorkspaceTargetResolutionFailures(t *testing.T) {
	present := repository.ProviderServer{ID: uuid.New(), ProviderID: "srv-1", LifecycleStatus: ProviderResourcePresent}
	cases := []struct {
		name   string
		mutate func(*fakeStore)
		want   error
	}{
		{"no SERVER ProviderResource in the current generation", func(s *fakeStore) { s.servers["vk-web"] = nil }, ErrTargetUnavailable},
		{"only a resource of the other VM exists", func(s *fakeStore) { delete(s.servers, "vk-web") }, ErrTargetUnavailable},
		{"resource is not PRESENT", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "srv-1", LifecycleStatus: "DELETED"}}
		}, ErrTargetUnavailable},
		{"only non-PRESENT resources", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{
				{ID: uuid.New(), ProviderID: "a", LifecycleStatus: "MISSING"},
				{ID: uuid.New(), ProviderID: "b", LifecycleStatus: "DELETED"},
			}
		}, ErrTargetUnavailable},
		{"one PRESENT among stale rows is the target", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "old", LifecycleStatus: "DELETED"}, present}
		}, nil},
		{"duplicate PRESENT resources", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{present, {ID: uuid.New(), ProviderID: "srv-2", LifecycleStatus: ProviderResourcePresent}}
		}, ErrInconsistentData},
		{"PRESENT resource without a provider id", func(s *fakeStore) {
			s.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "", LifecycleStatus: ProviderResourcePresent}}
		}, ErrInconsistentData},
		{"snapshot has no workspaceVmKey", func(s *fakeStore) { s.snapshot.WorkspaceVMKey = "" }, ErrInconsistentData},
		{"workspaceVmKey is not one of the vms", func(s *fakeStore) { s.snapshot.WorkspaceVMKey = "vk-ghost" }, ErrInconsistentData},
		{"workspaceVmKey is duplicated in vms", func(s *fakeStore) {
			s.snapshot.VMs = append(s.snapshot.VMs, repository.SnapshotVM{VMKey: "vk-web", Role: "web", InstanceIndex: 1})
		}, ErrInconsistentData},
		{"snapshot has no vms", func(s *fakeStore) { s.snapshot.VMs = nil }, ErrInconsistentData},
		{"snapshot not found", func(s *fakeStore) { s.snapshotErr = repository.ErrNotFound }, ErrInconsistentData},
		{"connector relation missing", func(s *fakeStore) { s.connectorErr = repository.ErrNotFound }, ErrInconsistentData},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			tc.mutate(store)
			transport := &fakeTransport{}
			s := newService(t, store, transport)

			_, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()})
			if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want == nil {
				if transport.target.ProviderServerID != "srv-1" {
					t.Fatalf("Transport target = %+v, want the PRESENT resource srv-1", transport.target)
				}
				return
			}
			if transport.callCount() != 0 {
				t.Fatalf("transport called %d times on %v", transport.callCount(), err)
			}
		})
	}
}

// 이 target은 Browser가 아니라 CreationSnapshot과 현재 generation의 ProviderResource에서만 온다.
func TestTargetNeverComesFromTheCaller(t *testing.T) {
	store := newStore()
	store.lab.Generation = 7
	store.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "srv-web-g7", LifecycleStatus: ProviderResourcePresent}}
	transport := &fakeTransport{}
	s := newService(t, store, transport)

	if _, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String(), Path: "vk-db"}); err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	if transport.target.Generation != 7 || transport.target.WorkspaceVMKey != "vk-web" || transport.target.ProviderServerID != "srv-web-g7" {
		t.Fatalf("Transport target = %+v", transport.target)
	}
	// 경로가 VM key처럼 보여도 target 선택에 영향을 주지 않는다.
	if transport.path.String() != "vk-db" {
		t.Fatalf("path = %q", transport.path.String())
	}
}

func TestTreeComposesSortsAndFiltersItems(t *testing.T) {
	store := newStore()
	transport := &fakeTransport{entries: []Entry{
		{Name: "zeta.txt", Kind: KindFile},
		{Name: "src", Kind: KindDirectory},
		{Name: "한글.txt", Kind: KindFile},
		{Name: "Alpha", Kind: KindDirectory},
		// 아래 항목은 경로 규칙으로 표현할 수 없거나 종류를 알 수 없어 목록에 넣지 않는다. 요청 전체를 실패시키지도 않는다.
		{Name: `back\slash`, Kind: KindFile},
		{Name: "new\nline", Kind: KindFile},
		{Name: "100%2fa", Kind: KindFile},
		{Name: "a/b", Kind: KindFile},
		{Name: "..", Kind: KindDirectory},
		{Name: ".", Kind: KindDirectory},
		{Name: "", Kind: KindFile},
		{Name: "link", Kind: "symlink"},
		{Name: "sock", Kind: ""},
	}}
	s := newService(t, store, transport)

	got, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String(), Path: "proj"})
	if err != nil {
		t.Fatalf("Tree() error = %v", err)
	}
	want := Listing{Path: "proj", Items: []Item{
		{Name: "Alpha", Path: "proj/Alpha", Kind: KindDirectory},
		{Name: "src", Path: "proj/src", Kind: KindDirectory},
		{Name: "zeta.txt", Path: "proj/zeta.txt", Kind: KindFile},
		{Name: "한글.txt", Path: "proj/한글.txt", Kind: KindFile},
	}}
	if got.Path != want.Path || len(got.Items) != len(want.Items) {
		t.Fatalf("Tree() = %+v, want %+v", got, want)
	}
	for i := range want.Items {
		if got.Items[i] != want.Items[i] {
			t.Fatalf("Items[%d] = %+v, want %+v", i, got.Items[i], want.Items[i])
		}
	}
}

func TestTreeAtRootUsesBareNames(t *testing.T) {
	transport := &fakeTransport{entries: []Entry{{Name: "main.py", Kind: KindFile}, {Name: "docs", Kind: KindDirectory}}}
	s := newService(t, newStore(), transport)
	got, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()})
	if err != nil || got.Path != "" || len(got.Items) != 2 || got.Items[0].Path != "docs" || got.Items[1].Path != "main.py" {
		t.Fatalf("Tree(root) = %+v, %v", got, err)
	}
}

func TestEmptyDirectoryIsAnEmptyListNotNil(t *testing.T) {
	s := newService(t, newStore(), &fakeTransport{})
	got, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()})
	if err != nil || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("Tree() = %+v, %v, want an empty non-nil list", got, err)
	}
}

func TestTransportErrorsPassThroughUnchanged(t *testing.T) {
	for _, want := range []error{
		ErrPathNotFound, ErrNotAFile, ErrNotADirectory, ErrFilePermissionDenied, ErrStaleRevision,
		ErrConnectorUnavailable, ErrTransportUnavailable, ErrSaveOutcomeUnknown, context.Canceled, context.DeadlineExceeded,
	} {
		store := newStore()
		transport := &fakeTransport{treeErr: want, readErr: want, saveErr: want}
		s := newService(t, store, transport)

		if _, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()}); !errors.Is(err, want) {
			t.Errorf("Tree() error = %v, want %v", err, want)
		}
		if _, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"}); !errors.Is(err, want) {
			t.Errorf("Read() error = %v, want %v", err, want)
		}
		if _, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: "r1", Content: "x"}); !errors.Is(err, want) {
			t.Errorf("Save() error = %v, want %v", err, want)
		}
		// 실패한 외부 I/O 뒤에는 성공 판단을 위한 재확인을 하지 않는다.
		for _, call := range store.calls {
			if call == "LabInstanceByID" {
				t.Errorf("%v: 실패 뒤에도 대상을 재확인함", want)
			}
		}
	}
}

func TestTreeMapsTooLargeToTheDirectoryError(t *testing.T) {
	s := newService(t, newStore(), &fakeTransport{treeErr: ErrTooLarge})
	if _, err := s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()}); !errors.Is(err, ErrDirectoryTooLarge) {
		t.Fatalf("Tree() error = %v, want ErrDirectoryTooLarge", err)
	}
}

func TestReadReturnsTextAndRevision(t *testing.T) {
	transport := &fakeTransport{data: FileData{Content: []byte("print('안녕')\n"), Revision: "rev-1"}}
	s := newService(t, newStore(), transport, func(o *Options) { o.MaxFileBytes = 64 })

	got, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "src/a.py"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.Path != "src/a.py" || got.Content != "print('안녕')\n" || got.Revision != "rev-1" {
		t.Fatalf("Read() = %+v", got)
	}
	// Connector에는 설정된 크기 한도를 알려 큰 파일의 본문을 보내지 않게 한다.
	if transport.max != 64 {
		t.Fatalf("Transport maxBytes = %d, want 64", transport.max)
	}
}

func TestReadAcceptsAnEmptyFileAndAFileExactlyAtTheLimit(t *testing.T) {
	for _, content := range []string{"", strings.Repeat("a", 16)} {
		transport := &fakeTransport{data: FileData{Content: []byte(content), Revision: "r"}}
		s := newService(t, newStore(), transport, func(o *Options) { o.MaxFileBytes = 16 })
		got, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"})
		if err != nil || got.Content != content {
			t.Errorf("Read(%d bytes) = %+v, %v", len(content), got, err)
		}
	}
}

func TestReadRefusesContentThatIsNotUTF8TextOrTooLarge(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    error
	}{
		{"invalid UTF-8", []byte("ok\xff\xfe"), ErrUnsupportedEncoding},
		{"truncated multibyte sequence", []byte("\xed\x95"), ErrUnsupportedEncoding},
		{"surrogate encoded as UTF-8", []byte("\xed\xa0\x80"), ErrUnsupportedEncoding},
		{"NUL byte", []byte("a\x00b"), ErrBinaryContent},
		{"binary header", []byte("\x7fELF\x02\x01\x00"), ErrBinaryContent},
		{"larger than the limit even if Connector said otherwise", []byte(strings.Repeat("a", 17)), ErrTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &fakeTransport{data: FileData{Content: tc.content, Revision: "r"}}
			s := newService(t, newStore(), transport, func(o *Options) { o.MaxFileBytes = 16 })
			got, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Read() error = %v, want %v", err, tc.want)
			}
			if got.Content != "" {
				t.Fatalf("Read() returned content alongside an error: %+v", got)
			}
		})
	}
}

func TestReadRefusesARevisionThatBreaksTheConnectorContract(t *testing.T) {
	for _, revision := range []Revision{"", `a"b`, Revision(strings.Repeat("a", 129))} {
		transport := &fakeTransport{data: FileData{Content: []byte("x"), Revision: revision}}
		s := newService(t, newStore(), transport)
		if _, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"}); !errors.Is(err, ErrTransportUnavailable) {
			t.Errorf("revision %q: error = %v, want ErrTransportUnavailable", revision, err)
		}
	}
}

func TestSaveSendsTheContentAndTheExpectedRevision(t *testing.T) {
	transport := &fakeTransport{revision: "rev-2"}
	s := newService(t, newStore(), transport)

	got, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "src/a.py", IfMatchRevision: "rev-1", Content: "print('hi')\n"})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if got.Path != "src/a.py" || got.Revision != "rev-2" {
		t.Fatalf("Save() = %+v", got)
	}
	if transport.expected != "rev-1" || string(transport.content) != "print('hi')\n" || transport.path.String() != "src/a.py" {
		t.Fatalf("Transport got expected=%q content=%q path=%q", transport.expected, transport.content, transport.path.String())
	}
	if transport.wasInTransaction {
		t.Fatal("Transport가 DB transaction 안에서 호출됨")
	}
}

func TestSaveValidatesTheContentBeforeAnyIO(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    error
	}{
		{"invalid UTF-8", "ok\xff", ErrUnsupportedEncoding},
		{"NUL", "a\x00b", ErrBinaryContent},
		{"over the limit", strings.Repeat("a", 17), ErrTooLarge},
		// 크기는 UTF-8 byte 수다. 문자 수가 아니다.
		{"over the limit in bytes only", strings.Repeat("한", 6), ErrTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			transport := &fakeTransport{revision: "r2"}
			s := newService(t, store, transport, func(o *Options) { o.MaxFileBytes = 16 })
			_, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: "r1", Content: tc.content})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Save() error = %v, want %v", err, tc.want)
			}
			if transport.callCount() != 0 || !store.calledNothing() {
				t.Fatalf("rejected content reached the store or the transport (store calls %v, transport %d)", store.calls, transport.callCount())
			}
		})
	}
}

func TestSaveAcceptsAnEmptyFileAndContentExactlyAtTheLimit(t *testing.T) {
	for _, content := range []string{"", strings.Repeat("a", 16), strings.Repeat("한", 5) + "a"} {
		transport := &fakeTransport{revision: "r2"}
		s := newService(t, newStore(), transport, func(o *Options) { o.MaxFileBytes = 16 })
		if _, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: "r1", Content: content}); err != nil {
			t.Errorf("Save(%d bytes) error = %v", len(content), err)
		}
	}
}

func TestSaveWithAnUnusableIfMatchIsStaleAfterAuthorizationAndNeverCallsTheTransport(t *testing.T) {
	for _, revision := range []string{"", `a"b`, "a b", strings.Repeat("a", 129), "*", "W/rev"} {
		store := newStore()
		transport := &fakeTransport{revision: "r2"}
		s := newService(t, store, transport)
		_, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: revision, Content: "x"})
		if !errors.Is(err, ErrStaleRevision) {
			t.Errorf("If-Match %q: error = %v, want ErrStaleRevision", revision, err)
		}
		if transport.callCount() != 0 {
			t.Errorf("If-Match %q: transport called", revision)
		}
		// 권한을 먼저 판정한다. 다른 사용자는 revision이 틀려도 403이다.
		store.lab.UserID = otherID
		_, err = s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: revision, Content: "x"})
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("If-Match %q for another user: error = %v, want ErrForbidden", revision, err)
		}
	}
}

func TestSaveRefusesANewRevisionThatBreaksTheConnectorContract(t *testing.T) {
	transport := &fakeTransport{revision: `bad"rev`}
	s := newService(t, newStore(), transport)
	_, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: "r1", Content: "x"})
	// 쓰기는 끝났을 수 있으므로 성공도, 실패 확정도 아니다.
	if !errors.Is(err, ErrSaveOutcomeUnknown) {
		t.Fatalf("Save() error = %v, want ErrSaveOutcomeUnknown", err)
	}
}

// Reset이 그 사이에 generation을 바꿨거나 LabInstance가 READY를 벗어났다면 성공으로 처리하지 않는다.
func TestResultIsNotSuccessIfTheWorkspaceChangedDuringTheRequest(t *testing.T) {
	changes := map[string]func(*fakeStore){
		"generation advanced by Reset": func(s *fakeStore) {
			lab := s.lab
			lab.Generation = s.lab.Generation + 1
			s.current = &lab
		},
		"LabInstance left READY": func(s *fakeStore) {
			lab := s.lab
			lab.Status = "RESETTING"
			s.current = &lab
		},
		"LabInstance disappeared": func(s *fakeStore) { s.currentErr = repository.ErrNotFound },
	}
	for name, change := range changes {
		for _, op := range []string{"tree", "read", "save"} {
			t.Run(op+"/"+name, func(t *testing.T) {
				store := newStore()
				transport := &fakeTransport{
					entries:  []Entry{{Name: "a", Kind: KindFile}},
					data:     FileData{Content: []byte(sourceMarker), Revision: "r1"},
					revision: "r2",
				}
				transport.hook = func() { change(store) }
				s := newService(t, store, transport)

				var (
					err error
					out any
				)
				switch op {
				case "tree":
					out, err = s.Tree(t.Context(), owner, TreeInput{LabInstanceID: labID.String()})
				case "read":
					out, err = s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"})
				default:
					out, err = s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: "a", IfMatchRevision: "r1", Content: "x"})
				}
				if !errors.Is(err, ErrTargetChanged) {
					t.Fatalf("error = %v, want ErrTargetChanged", err)
				}
				// 오래된 VM의 내용을 응답으로 돌려주지 않는다.
				if file, ok := out.(File); ok && file.Content != "" {
					t.Fatalf("Read() returned stale content: %+v", file)
				}
			})
		}
	}
}

func TestStillCurrentReadFailureIsNotSuccess(t *testing.T) {
	store := newStore()
	store.currentErr = errors.New("db down")
	s := newService(t, store, &fakeTransport{data: FileData{Content: []byte("x"), Revision: "r"}})
	if _, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "a"}); err == nil {
		t.Fatal("Read() succeeded although the workspace could not be re-checked")
	}
}

// 오류 문구에 경로, 본문 marker가 들어가지 않는다.
func TestErrorsNeverCarryThePathOrTheContent(t *testing.T) {
	secretPath := "secret-project/" + sourceMarker + ".txt"
	store := newStore()
	transport := &fakeTransport{
		data:    FileData{Content: []byte("a\x00" + sourceMarker), Revision: "r"},
		saveErr: ErrStaleRevision,
	}
	s := newService(t, store, transport)

	for _, call := range []func() error{
		func() error {
			_, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: secretPath})
			return err
		},
		func() error {
			_, err := s.Save(t.Context(), owner, SaveInput{LabInstanceID: labID.String(), Path: secretPath, IfMatchRevision: "r", Content: sourceMarker})
			return err
		},
		func() error {
			_, err := s.Read(t.Context(), owner, ReadInput{LabInstanceID: labID.String(), Path: "../" + sourceMarker})
			return err
		},
	} {
		err := call()
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), sourceMarker) {
			t.Fatalf("error text leaks the marker: %q", err)
		}
	}
}
