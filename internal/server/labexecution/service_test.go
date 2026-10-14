package labexecution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/operation"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

const (
	providerImageMarker  = "glance-image-marker-4c1d"
	providerFlavorMarker = "nova-flavor-marker-9e2a"
	startupScriptBody    = "#!/bin/sh\necho 'ready' > /tmp/<ready>&\n"
)

type world struct {
	store                          *fakeStore
	svc                            *Service
	org, connection                uuid.UUID
	class, otherClass              repository.Class
	instructor, studentA, studentB repository.User
	outsider, admin, coInstructor  repository.User
	spec                           repository.LabSpec
	image, flavor                  uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{store: newFakeStore(), org: uuid.New(), connection: uuid.New(), image: uuid.New(), flavor: uuid.New()}
	user := func(role repository.OrganizationRole) repository.User {
		return repository.User{ID: uuid.New(), OrganizationID: w.org, OrganizationRole: role}
	}
	w.instructor, w.studentA, w.studentB = user(repository.OrganizationRoleMember), user(repository.OrganizationRoleMember), user(repository.OrganizationRoleMember)
	w.outsider, w.admin, w.coInstructor = user(repository.OrganizationRoleMember), user(repository.OrganizationRoleAdmin), user(repository.OrganizationRoleMember)
	w.class = repository.Class{ID: uuid.New(), OrganizationID: w.org, Name: "Class"}
	w.otherClass = repository.Class{ID: uuid.New(), OrganizationID: w.org, Name: "Other"}
	s := w.store.committed
	s.classes[w.class.ID], s.classes[w.otherClass.ID] = w.class, w.otherClass
	member := func(c repository.Class, u repository.User, role repository.ClassRole) {
		s.memberships[[2]uuid.UUID{c.ID, u.ID}] = repository.ClassMembership{OrganizationID: w.org, ClassID: c.ID, UserID: u.ID, Role: role}
	}
	member(w.class, w.instructor, repository.ClassRoleInstructor)
	member(w.class, w.coInstructor, repository.ClassRoleInstructor)
	member(w.class, w.studentA, repository.ClassRoleStudent)
	member(w.class, w.studentB, repository.ClassRoleStudent)
	member(w.otherClass, w.outsider, repository.ClassRoleStudent)
	member(w.otherClass, w.instructor, repository.ClassRoleInstructor)

	s.images[w.image] = repository.ResolvedImageMapping{ID: w.image, OrganizationID: w.org, ProviderConnectionID: w.connection, Active: true, ProviderImageID: providerImageMarker}
	s.flavors[w.flavor] = repository.ResolvedFlavorMapping{ID: w.flavor, OrganizationID: w.org, ProviderConnectionID: w.connection, Active: true,
		ProviderFlavorID: providerFlavorMarker, VCPUs: 2, RAMMiB: 4096, DiskGiB: 40}
	script := startupScriptBody
	w.spec = repository.LabSpec{
		LabSpecSummary: repository.LabSpecSummary{ID: uuid.New(), OrganizationID: w.org, OwnerUserID: w.instructor.ID, Name: "Spec"},
		Revision:       3, InternetOutbound: true, StartupScript: &script, WorkspaceRole: "web", WorkspaceInstanceIndex: 1,
		VMRoles: []repository.LabSpecVMRole{
			{ID: uuid.New(), Role: "web", ImageMappingID: w.image, FlavorMappingID: w.flavor, VMCount: 2},
			{ID: uuid.New(), Role: "Web", ImageMappingID: w.image, FlavorMappingID: w.flavor, VMCount: 1},
		},
	}
	s.specs[w.spec.ID] = w.spec
	w.svc = NewService(w.store)
	fixed := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	w.svc.now = func() time.Time { return fixed }
	return w
}

func (w *world) input(key string, students ...repository.User) ProvisionInput {
	ids := make([]string, len(students))
	for i, s := range students {
		ids[i] = s.ID.String()
	}
	return ProvisionInput{ClassID: w.class.ID.String(), LabSpecID: w.spec.ID.String(), TargetStudentIDs: ids, IdempotencyKey: key, RequestID: "request-1"}
}

func TestProvisionRegistersExecutionSnapshotInstancesAndOperationAtomically(t *testing.T) {
	w := newWorld(t)
	got, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-1", w.studentA))
	if err != nil || got.Replayed || got.OperationID == uuid.Nil || got.LabExecutionID == uuid.Nil {
		t.Fatalf("Provision() = %+v, %v", got, err)
	}
	s := w.store.committed
	if s.rowCounts() != [5]int{1, 1, 2, 1, 2} {
		t.Fatalf("rows=%v", s.rowCounts())
	}
	e := s.executions[0]
	if e.ID != got.LabExecutionID || e.ClassID != w.class.ID || e.LabSpecID != w.spec.ID || e.InstructorUserID != w.instructor.ID || e.Status != "PROVISIONING" || e.OrganizationID != w.org {
		t.Fatalf("execution=%+v", e)
	}
	// 선택하지 않은 studentB에게는 LabInstance가 없다. 모두 PENDING이며 READY로 선언하지 않는다.
	roles := map[uuid.UUID]repository.ClassRole{}
	for _, i := range s.instances {
		if i.LabExecutionID != e.ID || i.Status != "PENDING" {
			t.Fatalf("instance=%+v", i)
		}
		roles[i.UserID] = i.ParticipantRole
	}
	if len(roles) != 2 || roles[w.instructor.ID] != repository.ClassRoleInstructor || roles[w.studentA.ID] != repository.ClassRoleStudent {
		t.Fatalf("participants=%v", roles)
	}
	o := s.operations[0]
	if o.ID != got.OperationID || o.Type != "PROVISION" || o.Status != "PENDING" || *o.LabExecutionID != e.ID || o.RequestID != "request-1" {
		t.Fatalf("operation=%+v", o)
	}
	for _, item := range s.items {
		if item.OperationID != o.ID || item.Generation != 1 {
			t.Fatalf("item=%+v", item)
		}
	}

	snap := s.snapshots[0]
	if snap.LabExecutionID != e.ID || snap.SourceLabSpecID != w.spec.ID || snap.SourceLabSpecRevision != 3 || snap.ProviderConnectionID != w.connection {
		t.Fatalf("snapshot row=%+v", snap)
	}
	var body struct {
		ProviderConnectionID string `json:"providerConnectionId"`
		WorkspaceVMKey       string `json:"workspaceVmKey"`
		InternetOutbound     bool   `json:"internetOutbound"`
		VMs                  []struct {
			VMKey, Role, ImageID, FlavorID string
			InstanceIndex                  int64
			FlavorSpec                     struct{ VCPUs, RAMMiB, DiskGiB int64 }
		} `json:"vms"`
		StartupScript *struct{ Content, SHA256 string } `json:"startupScript"`
	}
	if err := json.Unmarshal(snap.Snapshot, &body); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(startupScriptBody))
	if body.ProviderConnectionID != w.connection.String() || !body.InternetOutbound || body.StartupScript == nil ||
		body.StartupScript.Content != startupScriptBody || body.StartupScript.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("snapshot=%s", snap.Snapshot)
	}
	// role은 byte 순서("Web" < "web")로, vmKey는 role 문자열과 무관한 고유 opaque key다.
	want := []struct {
		key, role string
		index     int64
	}{{"vm-1", "Web", 0}, {"vm-2", "web", 0}, {"vm-3", "web", 1}}
	if len(body.VMs) != len(want) || body.WorkspaceVMKey != "vm-3" {
		t.Fatalf("vms=%s", snap.Snapshot)
	}
	for i, vm := range body.VMs {
		if vm.VMKey != want[i].key || vm.Role != want[i].role || vm.InstanceIndex != want[i].index || vm.ImageID != providerImageMarker ||
			vm.FlavorID != providerFlavorMarker || vm.FlavorSpec.VCPUs != 2 || vm.FlavorSpec.RAMMiB != 4096 || vm.FlavorSpec.DiskGiB != 40 {
			t.Fatalf("vm[%d]=%+v", i, vm)
		}
	}
}

func TestProvisionReplayIsOrderInsensitiveAndCreatesNothing(t *testing.T) {
	w := newWorld(t)
	first, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-1", w.studentA, w.studentB))
	if err != nil {
		t.Fatal(err)
	}
	before := w.store.committed.rowCounts()
	// 대상 순서가 달라도 같은 집합이다. 그 사이 LabSpec·Mapping·Membership이 바뀌어도 replay는 기존 Operation이다.
	w.store.committed.images[w.image] = repository.ResolvedImageMapping{ID: w.image, OrganizationID: w.org, ProviderConnectionID: w.connection}
	delete(w.store.committed.memberships, [2]uuid.UUID{w.class.ID, w.studentB.ID})
	in := w.input("key-1", w.studentB, w.studentA)
	in.RequestID = "request-2"
	replay, err := w.svc.Provision(t.Context(), w.instructor, in)
	if err != nil || !replay.Replayed || replay.OperationID != first.OperationID || replay.LabExecutionID != first.LabExecutionID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	if w.store.committed.rowCounts() != before || w.store.committed.operations[0].RequestID != "request-1" {
		t.Fatalf("replay changed rows=%v", w.store.committed.rowCounts())
	}

	// 같은 key를 다른 학생 집합·다른 LabSpec·다른 Class에 쓰면 409이고 새 row가 없다.
	otherSpec := w.spec
	otherSpec.ID = uuid.New()
	w.store.committed.specs[otherSpec.ID] = otherSpec
	for name, mutate := range map[string]func(*ProvisionInput){
		"students": func(in *ProvisionInput) { in.TargetStudentIDs = []string{w.studentA.ID.String()} },
		"labSpec":  func(in *ProvisionInput) { in.LabSpecID = otherSpec.ID.String() },
		"class": func(in *ProvisionInput) {
			in.ClassID = w.otherClass.ID.String()
			in.TargetStudentIDs = []string{w.outsider.ID.String()}
		},
	} {
		in := w.input("key-1", w.studentA, w.studentB)
		mutate(&in)
		if _, err := w.svc.Provision(t.Context(), w.instructor, in); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
	if w.store.committed.rowCounts() != before {
		t.Fatalf("conflict changed rows=%v", w.store.committed.rowCounts())
	}
	// 다른 key는 active LabExecution 때문에 409다. LabSpec mapping 상태보다 먼저 판정한다.
	if _, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-2", w.studentA)); !errors.Is(err, ErrActiveExecution) {
		t.Fatalf("active=%v", err)
	}
	// key는 사용자별 공간이다. 공동 강사가 같은 key를 써도 replay가 아니며 active LabExecution이 먼저 판정된다
	// (LabSpec owner 판정은 active LabExecution이 없을 때 TestProvisionAuthorizationAndValidation이 확인한다).
	if _, err := w.svc.Provision(t.Context(), w.coInstructor, w.input("key-1", w.studentA, w.studentB)); !errors.Is(err, ErrActiveExecution) {
		t.Fatalf("co-instructor=%v", err)
	}
}

// 같은 key의 동시 요청: 경쟁 요청이 active LabExecution unique 충돌 직전에 commit했다. 잘못된 409가 아니라 replay여야 하고
// 진 쪽이 만든 row는 하나도 남지 않는다. 다른 key나 다른 의미면 각각 409다.
func TestProvisionActiveConflictIsResolvedFromCommittedState(t *testing.T) {
	for name, tt := range map[string]struct {
		winnerKey   string
		winnerFP    func(w *world) operation.Fingerprint
		wantErr     error
		wantReplay  bool
		wantRowsLen int
	}{
		"same key same meaning": {"key-1", func(w *world) operation.Fingerprint {
			return provisionFingerprint(w.class.ID, w.spec.ID, []uuid.UUID{w.studentA.ID})
		}, nil, true, 1},
		"same key other meaning": {"key-1", func(w *world) operation.Fingerprint {
			return provisionFingerprint(w.class.ID, w.spec.ID, []uuid.UUID{w.studentB.ID})
		}, ErrIdempotencyConflict, false, 1},
		"other key": {"key-2", func(w *world) operation.Fingerprint {
			return provisionFingerprint(w.class.ID, w.spec.ID, []uuid.UUID{w.studentA.ID})
		}, ErrActiveExecution, false, 1},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			winnerExecution := uuid.New()
			winnerOperation := uuid.New()
			w.store.beforeExecutionInsert = func(c *state) {
				w.store.beforeExecutionInsert = nil
				hash, _ := operation.HashKey(tt.winnerKey)
				fp := tt.winnerFP(w)
				c.executions = append(c.executions, repository.NewLabExecution{ID: winnerExecution, ClassID: w.class.ID})
				c.operations = append(c.operations, repository.Operation{ID: winnerOperation, OrganizationID: w.org, RequestedByUserID: w.instructor.ID,
					Type: "PROVISION", LabExecutionID: &winnerExecution, IdempotencyKeyHash: hash, RequestFingerprint: fp[:]})
			}
			got, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-1", w.studentA))
			if !errors.Is(err, tt.wantErr) || got.Replayed != tt.wantReplay {
				t.Fatalf("Provision() = %+v, %v want %v", got, err, tt.wantErr)
			}
			if tt.wantReplay && (got.OperationID != winnerOperation || got.LabExecutionID != winnerExecution) {
				t.Fatalf("replay target=%+v", got)
			}
			if c := w.store.committed; len(c.executions) != 1 || len(c.operations) != 1 || len(c.snapshots)+len(c.instances)+len(c.items) != 0 {
				t.Fatalf("loser rows survived=%v", c.rowCounts())
			}
		})
	}
}

func TestProvisionAuthorizationAndValidation(t *testing.T) {
	w := newWorld(t)
	foreignOrg := repository.User{ID: uuid.New(), OrganizationID: uuid.New()}
	inactive, mixed := uuid.New(), uuid.New()
	w.store.committed.images[inactive] = repository.ResolvedImageMapping{ID: inactive, OrganizationID: w.org, ProviderConnectionID: w.connection, ProviderImageID: "x"}
	w.store.committed.flavors[mixed] = repository.ResolvedFlavorMapping{ID: mixed, OrganizationID: w.org, ProviderConnectionID: uuid.New(), Active: true, ProviderFlavorID: "y", VCPUs: 1, RAMMiB: 1}
	specWith := func(mutate func(*repository.LabSpec)) string {
		s := w.spec
		s.ID = uuid.New()
		s.VMRoles = append([]repository.LabSpecVMRole(nil), w.spec.VMRoles...)
		mutate(&s)
		w.store.committed.specs[s.ID] = s
		return s.ID.String()
	}
	otherOrgSpec := specWith(func(s *repository.LabSpec) { s.OrganizationID = uuid.New() })
	notOwned := specWith(func(s *repository.LabSpec) { s.OwnerUserID = w.coInstructor.ID })
	inactiveImage := specWith(func(s *repository.LabSpec) { s.VMRoles[0].ImageMappingID = inactive })
	missingFlavor := specWith(func(s *repository.LabSpec) { s.VMRoles[1].FlavorMappingID = uuid.New() })
	mixedConnection := specWith(func(s *repository.LabSpec) { s.VMRoles[1].FlavorMappingID = mixed })
	tooMany := specWith(func(s *repository.LabSpec) { s.VMRoles[0].VMCount = maxSnapshotVMs })
	brokenWorkspace := specWith(func(s *repository.LabSpec) { s.WorkspaceInstanceIndex = 9 })

	for name, tt := range map[string]struct {
		user   repository.User
		mutate func(*ProvisionInput)
		want   error
	}{
		"empty key":            {w.instructor, func(in *ProvisionInput) { in.IdempotencyKey = "" }, ErrInvalidRequest},
		"long key":             {w.instructor, func(in *ProvisionInput) { in.IdempotencyKey = strings.Repeat("k", 256) }, ErrInvalidRequest},
		"missing class":        {w.instructor, func(in *ProvisionInput) { in.ClassID = uuid.NewString() }, ErrNotFound},
		"non-canonical class":  {w.instructor, func(in *ProvisionInput) { in.ClassID = strings.ToUpper(w.class.ID.String()) }, ErrNotFound},
		"student":              {w.studentA, nil, ErrForbidden},
		"ADMIN without member": {w.admin, nil, ErrForbidden},
		"other organization":   {foreignOrg, nil, ErrForbidden},
		"labSpec missing":      {w.instructor, func(in *ProvisionInput) { in.LabSpecID = uuid.NewString() }, ErrNotFound},
		"labSpec malformed":    {w.instructor, func(in *ProvisionInput) { in.LabSpecID = "spec-1" }, ErrNotFound},
		"labSpec other org":    {w.instructor, func(in *ProvisionInput) { in.LabSpecID = otherOrgSpec }, ErrNotFound},
		"labSpec not owner":    {w.instructor, func(in *ProvisionInput) { in.LabSpecID = notOwned }, ErrForbidden},
		"no students":          {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = nil }, ErrInvalidRequest},
		"duplicate student": {w.instructor, func(in *ProvisionInput) {
			in.TargetStudentIDs = []string{w.studentA.ID.String(), w.studentA.ID.String()}
		}, ErrInvalidRequest},
		"malformed student":    {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = []string{"student-a"} }, ErrInvalidTargets},
		"unknown user":         {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = []string{uuid.NewString()} }, ErrInvalidTargets},
		"other class student":  {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = []string{w.outsider.ID.String()} }, ErrInvalidTargets},
		"instructor as target": {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = []string{w.coInstructor.ID.String()} }, ErrInvalidTargets},
		"other org user":       {w.instructor, func(in *ProvisionInput) { in.TargetStudentIDs = []string{foreignOrg.ID.String()} }, ErrInvalidTargets},
		"inactive image":       {w.instructor, func(in *ProvisionInput) { in.LabSpecID = inactiveImage }, ErrLabSpecNotProvisionable},
		"missing flavor":       {w.instructor, func(in *ProvisionInput) { in.LabSpecID = missingFlavor }, ErrLabSpecNotProvisionable},
		"mixed connections":    {w.instructor, func(in *ProvisionInput) { in.LabSpecID = mixedConnection }, ErrLabSpecNotProvisionable},
		"too many VMs":         {w.instructor, func(in *ProvisionInput) { in.LabSpecID = tooMany }, ErrLabSpecNotProvisionable},
		"broken stored spec":   {w.instructor, func(in *ProvisionInput) { in.LabSpecID = brokenWorkspace }, ErrInconsistentData},
	} {
		t.Run(name, func(t *testing.T) {
			in := w.input("key-"+name, w.studentA)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			if _, err := w.svc.Provision(t.Context(), tt.user, in); !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
			if w.store.committed.rowCounts() != [5]int{} {
				t.Fatalf("rejected request left rows=%v", w.store.committed.rowCounts())
			}
		})
	}
}

func TestProvisionFailureRollsBackEveryRow(t *testing.T) {
	w := newWorld(t)
	w.store.failInstance = &repository.Error{Kind: repository.KindInternal, Op: "CreateLabInstance", Cause: errInjected}
	_, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-1", w.studentA))
	if err == nil || errors.Is(err, ErrActiveExecution) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err=%v", err)
	}
	if w.store.committed.rowCounts() != [5]int{} {
		t.Fatalf("rows=%v", w.store.committed.rowCounts())
	}
	// 같은 key로 다시 시도하면 처음 요청처럼 접수된다(실패한 시도가 key를 소모하지 않음).
	w.store.failInstance = nil
	if got, err := w.svc.Provision(t.Context(), w.instructor, w.input("key-1", w.studentA)); err != nil || got.Replayed {
		t.Fatalf("retry=%+v %v", got, err)
	}
}

func TestFingerprintIsCanonicalSemanticOnly(t *testing.T) {
	class, spec, a, b := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if provisionFingerprint(class, spec, []uuid.UUID{a, b}) != provisionFingerprint(class, spec, []uuid.UUID{b, a}) {
		t.Fatal("student order changed fingerprint")
	}
	for _, other := range []operation.Fingerprint{
		provisionFingerprint(uuid.New(), spec, []uuid.UUID{a, b}),
		provisionFingerprint(class, uuid.New(), []uuid.UUID{a, b}),
		provisionFingerprint(class, spec, []uuid.UUID{a}),
	} {
		if other == provisionFingerprint(class, spec, []uuid.UUID{a, b}) {
			t.Fatal("different meaning shares fingerprint")
		}
	}
}
