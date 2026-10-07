package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/auth"
	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// fakeStore는 Targets와 Create의 target 판정만 검증하기 위한 in-memory Store다. 구현하지 않은 method는 호출되면 nil embed로 panic한다.
// 판정이 의존하는 repository 호출(어떤 것을 읽었는지)을 기록해 순서와 생략을 확인한다.
type fakeStore struct {
	repository.Repositories

	lab         repository.LabInstance
	labErr      error
	membership  repository.ClassMembership
	memberErr   error
	snapshot    repository.CreationSnapshotTargets
	snapshotErr error
	servers     map[string][]repository.ProviderServer

	transactions int
	calls        []string
}

func (f *fakeStore) WithinTransaction(ctx context.Context, fn func(context.Context, repository.Repositories) error) error {
	f.transactions++
	return fn(ctx, f)
}

func (f *fakeStore) LabInstanceForShare(context.Context, uuid.UUID) (repository.LabInstance, error) {
	f.calls = append(f.calls, "LabInstanceForShare")
	return f.lab, f.labErr
}

func (f *fakeStore) ClassMembership(context.Context, uuid.UUID, uuid.UUID) (repository.ClassMembership, error) {
	f.calls = append(f.calls, "ClassMembership")
	return f.membership, f.memberErr
}

func (f *fakeStore) CreationSnapshotTargets(context.Context, uuid.UUID) (repository.CreationSnapshotTargets, error) {
	f.calls = append(f.calls, "CreationSnapshotTargets")
	return f.snapshot, f.snapshotErr
}

func (f *fakeStore) ConnectorIDForLabInstance(context.Context, uuid.UUID) (uuid.UUID, error) {
	f.calls = append(f.calls, "ConnectorIDForLabInstance")
	return uuid.MustParse("00000000-0000-4000-8000-0000000000c1"), nil
}

func (f *fakeStore) ProviderServers(_ context.Context, _ uuid.UUID, _ int64, logicalName string) ([]repository.ProviderServer, error) {
	f.calls = append(f.calls, "ProviderServers:"+logicalName)
	return f.servers[logicalName], nil
}

func (f *fakeStore) CreateTerminalSession(context.Context, repository.NewTerminalSession) error {
	f.calls = append(f.calls, "CreateTerminalSession")
	return nil
}

func (f *fakeStore) EndTerminalSession(context.Context, uuid.UUID, time.Time, string) (bool, error) {
	f.calls = append(f.calls, "EndTerminalSession")
	return true, nil
}

func (f *fakeStore) EndActiveLiveSessionBySourceTerminal(context.Context, uuid.UUID, time.Time, string) (bool, error) {
	f.calls = append(f.calls, "EndActiveLiveSessionBySourceTerminal")
	return true, nil
}

func (f *fakeStore) called(name string) bool {
	for _, c := range f.calls {
		if c == name {
			return true
		}
	}
	return false
}

// Targets와 Create의 target 판정은 Auth, Connector, Relay를 쓰지 않는다. NewService가 요구하므로 자리만 채운다.
type (
	unusedAuth       struct{}
	unusedConnectors struct{ Connectors }
	unusedRelay      struct{ Relay }
)

func (unusedAuth) Authenticate(context.Context, auth.SessionToken) (auth.Principal, error) {
	return auth.Principal{}, errors.New("사용하지 않음")
}

var (
	orgID    = uuid.MustParse("00000000-0000-4000-8000-0000000000a1")
	classID  = uuid.MustParse("00000000-0000-4000-8000-0000000000b1")
	ownerID  = uuid.MustParse("00000000-0000-4000-8000-000000000001")
	otherID  = uuid.MustParse("00000000-0000-4000-8000-000000000002")
	labID    = uuid.MustParse("00000000-0000-4000-8000-0000000000d1")
	owner    = repository.User{ID: ownerID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}
	vmsThree = []repository.SnapshotVM{
		{VMKey: "vk-web", Role: "web", InstanceIndex: 0},
		{VMKey: "vk-worker-a", Role: "worker", InstanceIndex: 0},
		{VMKey: "vk-worker-b", Role: "worker", InstanceIndex: 1},
	}
)

func newStore() *fakeStore {
	return &fakeStore{
		lab: repository.LabInstance{
			ID: labID, OrganizationID: orgID, LabExecutionID: uuid.New(), ClassID: classID, UserID: ownerID, Status: LabInstanceStatusReady, Generation: 3,
		},
		membership: repository.ClassMembership{OrganizationID: orgID, ClassID: classID, UserID: ownerID, Role: repository.ClassRoleStudent},
		snapshot:   repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: vmsThree},
		servers:    map[string][]repository.ProviderServer{},
	}
}

func newService(t *testing.T, store *fakeStore) *Service {
	t.Helper()
	s, err := NewService(Options{Store: store, Auth: unusedAuth{}, Connectors: unusedConnectors{}, Relay: unusedRelay{}, Clock: realtime.SystemClock{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return s
}

func TestTargetsReturnsEveryVMFromTheSnapshot(t *testing.T) {
	store := newStore()
	s := newService(t, store)

	got, err := s.Targets(t.Context(), owner, labID.String())
	if err != nil {
		t.Fatalf("Targets() error = %v", err)
	}
	want := TargetCatalog{
		Generation: 3, WorkspaceVMKey: "vk-web",
		Items: []Target{
			{VMKey: "vk-web", Role: "web", InstanceIndex: 0},
			{VMKey: "vk-worker-a", Role: "worker", InstanceIndex: 0},
			{VMKey: "vk-worker-b", Role: "worker", InstanceIndex: 1},
		},
	}
	if got.Generation != want.Generation || got.WorkspaceVMKey != want.WorkspaceVMKey || len(got.Items) != len(want.Items) {
		t.Fatalf("Targets() = %+v, want %+v", got, want)
	}
	for i := range want.Items {
		if got.Items[i] != want.Items[i] {
			t.Fatalf("Items[%d] = %+v, want %+v", i, got.Items[i], want.Items[i])
		}
	}

	// Workspace VM만 반환하는 구현은 실패다. Terminal은 여러 VM 중에서 선택한다.
	if len(got.Items) < 2 {
		t.Fatalf("Items = %d, want 2 or more", len(got.Items))
	}
	// 판정은 하나의 transaction 안에서 LabInstance를 FOR SHARE로 읽고 그 안에서 snapshot을 읽는다.
	if store.transactions != 1 || len(store.calls) != 3 || store.calls[0] != "LabInstanceForShare" || store.calls[2] != "CreationSnapshotTargets" {
		t.Fatalf("transactions = %d, calls = %v", store.transactions, store.calls)
	}
	// Provider topology는 읽지 않는다.
	for _, c := range store.calls {
		if c == "ConnectorIDForLabInstance" || strings.HasPrefix(c, "ProviderServers") {
			t.Fatalf("target 조회가 Provider 정보를 읽음: %v", store.calls)
		}
	}
}

func TestTargetsWorkspaceHintMayBeAnyItem(t *testing.T) {
	// workspaceVmKey는 items의 어느 위치에 있어도 된다(첫 원소일 필요가 없다).
	store := newStore()
	store.snapshot.WorkspaceVMKey = "vk-worker-b"
	got, err := newService(t, store).Targets(t.Context(), owner, labID.String())
	if err != nil || got.WorkspaceVMKey != "vk-worker-b" || len(got.Items) != 3 {
		t.Fatalf("Targets() = %+v, %v", got, err)
	}
}

func TestTargetsAuthorization(t *testing.T) {
	tests := []struct {
		name  string
		user  repository.User
		setup func(*fakeStore)
		want  error
	}{
		{name: "another user's lab instance", user: repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember}, want: ErrForbidden},
		{
			// 강사는 학생의 LabInstance를 조회하지 못한다.
			name: "instructor of the class opens a student's lab instance",
			user: repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleMember},
			setup: func(s *fakeStore) {
				s.membership = repository.ClassMembership{OrganizationID: orgID, ClassID: classID, UserID: otherID, Role: repository.ClassRoleInstructor}
			},
			want: ErrForbidden,
		},
		{
			// Organization ADMIN도 LabInstance 소유를 대체하지 못한다.
			name: "organization admin opens another user's lab instance",
			user: repository.User{ID: otherID, OrganizationID: orgID, OrganizationRole: repository.OrganizationRoleAdmin},
			want: ErrForbidden,
		},
		{
			name: "user of another organization who owns nothing here",
			user: repository.User{ID: ownerID, OrganizationID: uuid.New(), OrganizationRole: repository.OrganizationRoleAdmin},
			want: ErrForbidden,
		},
		{
			name:  "owner whose class membership was removed",
			user:  owner,
			setup: func(s *fakeStore) { s.memberErr = repository.ErrNotFound },
			want:  ErrForbidden,
		},
		{
			name:  "membership belongs to another class",
			user:  owner,
			setup: func(s *fakeStore) { s.membership.ClassID = uuid.New() },
			want:  ErrInconsistentData,
		},
		{
			name:  "membership belongs to another user",
			user:  owner,
			setup: func(s *fakeStore) { s.membership.UserID = otherID },
			want:  ErrInconsistentData,
		},
		{
			name:  "membership belongs to another organization",
			user:  owner,
			setup: func(s *fakeStore) { s.membership.OrganizationID = uuid.New() },
			want:  ErrInconsistentData,
		},
		{
			name:  "membership has an unknown role",
			user:  owner,
			setup: func(s *fakeStore) { s.membership.Role = "TEACHING_ASSISTANT" },
			want:  ErrInconsistentData,
		},
		{name: "lab instance does not exist", user: owner, setup: func(s *fakeStore) { s.labErr = repository.ErrNotFound }, want: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore()
			if tt.setup != nil {
				tt.setup(store)
			}
			got, err := newService(t, store).Targets(t.Context(), tt.user, labID.String())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Targets() error = %v, want %v", err, tt.want)
			}
			if got.Items != nil || got.WorkspaceVMKey != "" {
				t.Fatalf("거절된 요청이 target을 반환함: %+v", got)
			}
			// 권한을 통과하지 못하면 snapshot을 읽지 않는다.
			if store.called("CreationSnapshotTargets") {
				t.Fatalf("권한 판정 전에 snapshot을 읽음: %v", store.calls)
			}
		})
	}
}

func TestTargetsRejectsMalformedLabInstanceID(t *testing.T) {
	for _, id := range []string{"", "not-a-uuid", "00000000-0000-4000-8000-0000000000D1" /* 대문자는 canonical이 아님 */} {
		store := newStore()
		if _, err := newService(t, store).Targets(t.Context(), owner, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Targets(%q) error = %v, want ErrNotFound", id, err)
		}
		if store.transactions != 0 {
			t.Fatalf("Targets(%q)가 DB를 읽음", id)
		}
	}
}

func TestTargetsRequiresReadyLabInstance(t *testing.T) {
	for _, status := range []string{"PENDING", "PROVISIONING", "ERROR", "DELETING", "SOMETHING_NEW", ""} {
		t.Run(status, func(t *testing.T) {
			store := newStore()
			store.lab.Status = status
			_, err := newService(t, store).Targets(t.Context(), owner, labID.String())
			if !errors.Is(err, ErrLabInstanceNotReady) {
				t.Fatalf("Targets() error = %v, want ErrLabInstanceNotReady", err)
			}
			if store.called("CreationSnapshotTargets") {
				t.Fatal("READY가 아닌데 snapshot을 읽음")
			}
		})
	}
}

// 저장된 snapshot은 신뢰하지 않는다. 일부만 반환하거나 값을 만들어 채우지 않고 fail closed한다.
func TestTargetsFailClosedOnInconsistentSnapshot(t *testing.T) {
	vm := func(key, role string, index int64) repository.SnapshotVM {
		return repository.SnapshotVM{VMKey: key, Role: role, InstanceIndex: index}
	}
	tests := []struct {
		name     string
		snapshot repository.CreationSnapshotTargets
	}{
		{name: "no vms", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web"}},
		{name: "empty vms", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: []repository.SnapshotVM{}}},
		{name: "no workspaceVmKey", snapshot: repository.CreationSnapshotTargets{VMs: vmsThree}},
		{name: "empty vmKey", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: []repository.SnapshotVM{vm("vk-web", "web", 0), vm("", "worker", 0)}}},
		{name: "empty role", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: []repository.SnapshotVM{vm("vk-web", "web", 0), vm("vk-x", "", 0)}}},
		{name: "negative instanceIndex", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: []repository.SnapshotVM{vm("vk-web", "web", 0), vm("vk-x", "worker", -1)}}},
		{name: "duplicate vmKey", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-web", VMs: []repository.SnapshotVM{vm("vk-web", "web", 0), vm("vk-web", "worker", 1)}}},
		{name: "workspaceVmKey is not one of the vms", snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "vk-missing", VMs: vmsThree}},
		{
			// key는 대소문자를 구분한다. 비슷한 key를 같은 VM으로 해석하지 않는다.
			name:     "workspaceVmKey differs only by case",
			snapshot: repository.CreationSnapshotTargets{WorkspaceVMKey: "VK-WEB", VMs: vmsThree},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore()
			store.snapshot = tt.snapshot
			got, err := newService(t, store).Targets(t.Context(), owner, labID.String())
			if !errors.Is(err, ErrInconsistentData) {
				t.Fatalf("Targets() error = %v, want ErrInconsistentData", err)
			}
			if got.Items != nil || got.WorkspaceVMKey != "" || got.Generation != 0 {
				t.Fatalf("손상된 snapshot에서 일부를 반환함: %+v", got)
			}
		})
	}
}

func TestTargetsFailClosedWhenSnapshotCannotBeRead(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		// LabInstance는 DB 제약상 LabExecution의 CreationSnapshot을 가진다. 없으면 NotFound(404)가 아니라 저장된 관계의 모순이다.
		{name: "snapshot does not exist", err: repository.ErrNotFound},
		{name: "snapshot has a shape that cannot be read", err: &repository.Error{Kind: repository.KindInternal, Op: "CreationSnapshotTargets", Cause: errors.New("json 타입 불일치")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore()
			store.snapshotErr = tt.err
			got, err := newService(t, store).Targets(t.Context(), owner, labID.String())
			if err == nil || got.Items != nil {
				t.Fatalf("Targets() = %+v, %v, want an error", got, err)
			}
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrLabInstanceNotReady) {
				t.Fatalf("snapshot 문제가 권한/상태 오류로 보임: %v", err)
			}
		})
	}
}

func TestInconsistentSnapshotErrorsDoNotCarrySnapshotValues(t *testing.T) {
	store := newStore()
	store.snapshot = repository.CreationSnapshotTargets{
		WorkspaceVMKey: "secret-looking-key-91f2",
		VMs:            []repository.SnapshotVM{{VMKey: "secret-looking-vm-77ad", Role: "r", InstanceIndex: 0}, {VMKey: "secret-looking-vm-77ad", Role: "r", InstanceIndex: 1}},
	}
	_, err := newService(t, store).Targets(t.Context(), owner, labID.String())
	if !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("error = %v", err)
	}
	for _, leaked := range []string{"secret-looking-key-91f2", "secret-looking-vm-77ad"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("오류 문구가 snapshot 값 %q를 포함함: %v", leaked, err)
		}
	}
}

func createInput(target string) CreateInput {
	return CreateInput{LabInstanceID: labID.String(), TargetVMKey: target, Cols: []byte("80"), Rows: []byte("24")}
}

// Create는 Targets의 결과를 신뢰하지 않고 targetVmKey가 immutable CreationSnapshot의 VM인지 다시 확인한다.
// snapshot에 없는 key는 현재 generation에 같은 이름의 SERVER ProviderResource가 PRESENT로 있어도 target이 아니다.
func TestCreateRejectsTargetThatIsNotInTheSnapshot(t *testing.T) {
	store := newStore()
	store.servers["hidden-server"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "provider-hidden", LifecycleStatus: ProviderResourcePresent}}

	_, err := newService(t, store).Create(t.Context(), owner, createInput("hidden-server"))
	if !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("Create() error = %v, want ErrTargetNotFound", err)
	}
	// ProviderResource는 조회조차 하지 않았고 어떤 side effect도 없다.
	for _, c := range store.calls {
		if c == "CreateTerminalSession" || c == "ProviderServers:hidden-server" || c == "ConnectorIDForLabInstance" {
			t.Fatalf("snapshot에 없는 key가 %s까지 진행됨: %v", c, store.calls)
		}
	}
}

func TestCreateFailsClosedOnInconsistentSnapshot(t *testing.T) {
	store := newStore()
	store.snapshot.VMs = append(append([]repository.SnapshotVM(nil), vmsThree...), repository.SnapshotVM{VMKey: "vk-web", Role: "dup", InstanceIndex: 9})
	store.servers["vk-web"] = []repository.ProviderServer{{ID: uuid.New(), ProviderID: "provider-web", LifecycleStatus: ProviderResourcePresent}}

	_, err := newService(t, store).Create(t.Context(), owner, createInput("vk-web"))
	if !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("Create() error = %v, want ErrInconsistentData", err)
	}
	if store.called("CreateTerminalSession") || store.called("ProviderServers:vk-web") {
		t.Fatalf("손상된 snapshot에서 TerminalSession 생성이 진행됨: %v", store.calls)
	}
}

// snapshot의 VM이지만 현재 generation에 SERVER ProviderResource가 없거나 PRESENT가 아니면 Browser의 잘못된 입력이 아니라
// 지금 사용할 수 없는 target이다(Reset 진행·Provider drift).
func TestCreateReportsSnapshotVMWithoutPresentResourceAsUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		servers []repository.ProviderServer
	}{
		{name: "no server resource in the current generation"},
		{name: "server resource is deleted", servers: []repository.ProviderServer{{ID: uuid.New(), ProviderID: "p", LifecycleStatus: "DELETED"}}},
		{name: "server resource is missing", servers: []repository.ProviderServer{{ID: uuid.New(), ProviderID: "p", LifecycleStatus: "MISSING"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore()
			store.servers["vk-worker-a"] = tt.servers
			_, err := newService(t, store).Create(t.Context(), owner, createInput("vk-worker-a"))
			if !errors.Is(err, ErrTargetUnavailable) {
				t.Fatalf("Create() error = %v, want ErrTargetUnavailable", err)
			}
			if store.called("CreateTerminalSession") {
				t.Fatal("거절된 요청이 TerminalSession을 저장함")
			}
		})
	}
}

// Create는 Targets와 같은 권한 경계를 쓴다. 한쪽만 느슨해지는 drift를 막는다.
func TestCreateUsesTheSameAuthorizationAsTargets(t *testing.T) {
	setups := map[string]func(*fakeStore){
		"membership removed": func(s *fakeStore) { s.memberErr = repository.ErrNotFound },
		"not ready":          func(s *fakeStore) { s.lab.Status = "PROVISIONING" },
		"other user's lab":   func(s *fakeStore) { s.lab.UserID = otherID },
		"other organization": func(s *fakeStore) { s.lab.OrganizationID = uuid.New() },
		"inconsistent":       func(s *fakeStore) { s.membership.ClassID = uuid.New() },
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			targetsStore, createStore := newStore(), newStore()
			setup(targetsStore)
			setup(createStore)
			_, targetsErr := newService(t, targetsStore).Targets(t.Context(), owner, labID.String())
			_, createErr := newService(t, createStore).Create(t.Context(), owner, createInput("vk-web"))
			if targetsErr == nil || !errors.Is(createErr, targetsErr) && !errors.Is(targetsErr, createErr) {
				t.Fatalf("Targets error = %v, Create error = %v, want the same", targetsErr, createErr)
			}
		})
	}
}
