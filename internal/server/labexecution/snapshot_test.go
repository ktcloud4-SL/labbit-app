package labexecution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// 저장한 snapshot은 Connector가 OPERATION_COMMAND에서 받는 CreationSnapshot 모양 그대로다. Connector의 shared type으로 알 수 없는
// field 없이 decode되고, Connector가 Provider dispatch 전에 확인하는 불변조건(vmKey 고유, Workspace VM이 vms 안, Startup Script digest)을
// 만족한다. role이 대소문자만 다르거나 길어도 vmKey는 충돌하지 않는다.
func TestSnapshotDecodesAsConnectorCreationSnapshot(t *testing.T) {
	w := newWorld(t)
	long := strings.Repeat("worker-node-role-", 4)
	spec := w.spec
	spec.VMRoles = append(append([]repository.LabSpecVMRole(nil), spec.VMRoles...),
		repository.LabSpecVMRole{ID: uuid.New(), Role: long + "a", ImageMappingID: w.image, FlavorMappingID: w.flavor, VMCount: 1},
		repository.LabSpecVMRole{ID: uuid.New(), Role: long + "b", ImageMappingID: w.image, FlavorMappingID: w.flavor, VMCount: 1})

	raw, connection, err := resolveSnapshot(t.Context(), w.store.view(), w.org, spec)
	if err != nil || connection != w.connection {
		t.Fatalf("resolveSnapshot() = %v, %v", connection, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snap protocol.CreationSnapshot
	if err := decoder.Decode(&snap); err != nil {
		t.Fatalf("decode as Connector CreationSnapshot: %v\n%s", err, raw)
	}
	if snap.ProviderConnectionID != w.connection.String() || len(snap.VMs) != 5 || snap.StartupScript == nil {
		t.Fatalf("snapshot=%s", raw)
	}
	keys := map[string]bool{}
	workspace := false
	for _, vm := range snap.VMs {
		if vm.VMKey == "" || keys[strings.ToLower(vm.VMKey)] || vm.ImageID == "" || vm.FlavorID == "" || vm.FlavorSpec == nil || vm.InstanceIndex < 0 {
			t.Fatalf("vm=%+v", vm)
		}
		keys[strings.ToLower(vm.VMKey)] = true
		workspace = workspace || vm.VMKey == snap.WorkspaceVMKey
	}
	digest := sha256.Sum256([]byte(snap.StartupScript.Content))
	if !workspace || snap.StartupScript.SHA256 != hex.EncodeToString(digest[:]) || snap.StartupScript.Content != startupScriptBody {
		t.Fatalf("snapshot=%s", raw)
	}

	// Startup Script가 없으면 startupScript field를 생략한다(Connector는 있으면 digest를 검증한다).
	spec.StartupScript = nil
	raw, _, err = resolveSnapshot(t.Context(), w.store.view(), w.org, spec)
	if err != nil || bytes.Contains(raw, []byte("startupScript")) {
		t.Fatalf("no script snapshot=%s %v", raw, err)
	}
}

// mapping이 다른 Organization 행으로 돌아오는 것은 DB 제약상 불가능하므로 권한 오류가 아니라 모순이다.
func TestSnapshotFailsClosedOnForeignMappingRow(t *testing.T) {
	w := newWorld(t)
	foreign := w.store.committed.images[w.image]
	foreign.OrganizationID = uuid.New()
	tx := w.store.view()
	images := &foreignImages{fakeTx: tx, row: foreign}
	if _, _, err := resolveSnapshot(t.Context(), images, w.org, w.spec); !errors.Is(err, ErrInconsistentData) {
		t.Fatalf("err=%v", err)
	}
}

type foreignImages struct {
	*fakeTx
	row repository.ResolvedImageMapping
}

func (f *foreignImages) ResolvedImageMappingsForShare(_ context.Context, _ uuid.UUID, _ []uuid.UUID) ([]repository.ResolvedImageMapping, error) {
	return []repository.ResolvedImageMapping{f.row}, nil
}
