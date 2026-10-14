package labexecution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

// creationSnapshot은 contracts/connector/connector.schema.json의 CreationSnapshot 모양 그대로 저장한다. Worker가 Connector에
// 다시 해석하지 않고 전달할 수 있고, Terminal/Workspace target 조회(CreationSnapshotTargets)가 vms[]와 workspaceVmKey를 읽는다.
// 출처(LabSpec ID/revision)와 ProviderConnection은 creation_snapshots column에도 함께 고정한다.
type creationSnapshot struct {
	ProviderConnectionID string                 `json:"providerConnectionId"`
	VMs                  []resolvedVM           `json:"vms"`
	WorkspaceVMKey       string                 `json:"workspaceVmKey"`
	InternetOutbound     bool                   `json:"internetOutbound"`
	StartupScript        *startupScriptSnapshot `json:"startupScript,omitempty"`
}

type resolvedVM struct {
	VMKey         string             `json:"vmKey"`
	Role          string             `json:"role"`
	InstanceIndex int64              `json:"instanceIndex"`
	ImageID       string             `json:"imageId"`
	FlavorID      string             `json:"flavorId"`
	FlavorSpec    resolvedFlavorSpec `json:"flavorSpec"`
}

type resolvedFlavorSpec struct {
	VCPUs   int64 `json:"vcpus"`
	RAMMiB  int64 `json:"ramMiB"`
	DiskGiB int64 `json:"diskGiB"`
}

type startupScriptSnapshot struct {
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// resolveSnapshot은 FOR SHARE로 잠근 LabSpec revision과 그 VM Role이 참조하는 mapping을 생성 시점의 실제 Provider 값으로
// resolve한다. mapping도 FOR SHARE로 잠가 commit 전까지 비활성화·변경과 경쟁하지 않는다.
//
// 모든 mapping은 같은 Organization의 활성 mapping이고 하나의 ProviderConnection에 속해야 한다. 하나라도 아니면 다른 mapping을 고르지
// 않고 ErrLabSpecNotProvisionable이다. 저장된 LabSpec 자체의 모순(VM Role 없음, Workspace VM이 VM Role에 없음)은 ErrInconsistentData다.
//
// vmKey는 VM의 opaque 논리 키 vm-1, vm-2, ...이며 role(byte 순서)과 instanceIndex 순서로 붙인다. role 문자열은 대소문자나 길이만
// 다를 수 있어 그대로 key에 쓰면 Provider 이름 파생 시 충돌할 수 있으므로 key에 넣지 않는다.
func resolveSnapshot(ctx context.Context, repos repository.Repositories, organizationID uuid.UUID, spec repository.LabSpec) (json.RawMessage, uuid.UUID, error) {
	if spec.Revision < 1 || len(spec.VMRoles) == 0 {
		return nil, uuid.Nil, ErrInconsistentData
	}
	roles := append([]repository.LabSpecVMRole(nil), spec.VMRoles...)
	sort.Slice(roles, func(i, j int) bool { return roles[i].Role < roles[j].Role })

	imageIDs := make([]uuid.UUID, 0, len(roles))
	flavorIDs := make([]uuid.UUID, 0, len(roles))
	var total int64
	for _, r := range roles {
		if r.VMCount < 1 {
			return nil, uuid.Nil, ErrInconsistentData
		}
		total += r.VMCount
		if total > maxSnapshotVMs {
			return nil, uuid.Nil, ErrLabSpecNotProvisionable
		}
		imageIDs = append(imageIDs, r.ImageMappingID)
		flavorIDs = append(flavorIDs, r.FlavorMappingID)
	}

	images, err := repos.ResolvedImageMappingsForShare(ctx, organizationID, distinct(imageIDs))
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("labexecution: Image mapping 조회: %w", err)
	}
	flavors, err := repos.ResolvedFlavorMappingsForShare(ctx, organizationID, distinct(flavorIDs))
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("labexecution: Flavor mapping 조회: %w", err)
	}
	imageByID := make(map[uuid.UUID]repository.ResolvedImageMapping, len(images))
	for _, m := range images {
		if m.OrganizationID != organizationID || imageByID[m.ID].ID != uuid.Nil {
			return nil, uuid.Nil, ErrInconsistentData
		}
		imageByID[m.ID] = m
	}
	flavorByID := make(map[uuid.UUID]repository.ResolvedFlavorMapping, len(flavors))
	for _, m := range flavors {
		if m.OrganizationID != organizationID || flavorByID[m.ID].ID != uuid.Nil {
			return nil, uuid.Nil, ErrInconsistentData
		}
		flavorByID[m.ID] = m
	}

	connection := uuid.Nil
	sameConnection := func(id uuid.UUID) bool {
		if connection == uuid.Nil {
			connection = id
		}
		return id != uuid.Nil && id == connection
	}
	snapshot := creationSnapshot{InternetOutbound: spec.InternetOutbound, VMs: make([]resolvedVM, 0, total)}
	for _, r := range roles {
		image, ok := imageByID[r.ImageMappingID]
		if !ok || !image.Active || image.ProviderImageID == "" || !sameConnection(image.ProviderConnectionID) {
			return nil, uuid.Nil, ErrLabSpecNotProvisionable
		}
		flavor, ok := flavorByID[r.FlavorMappingID]
		if !ok || !flavor.Active || flavor.ProviderFlavorID == "" || !sameConnection(flavor.ProviderConnectionID) {
			return nil, uuid.Nil, ErrLabSpecNotProvisionable
		}
		if flavor.VCPUs < 1 || flavor.RAMMiB < 1 || flavor.DiskGiB < 0 {
			return nil, uuid.Nil, ErrInconsistentData
		}
		for index := int64(0); index < r.VMCount; index++ {
			key := fmt.Sprintf("vm-%d", len(snapshot.VMs)+1)
			snapshot.VMs = append(snapshot.VMs, resolvedVM{
				VMKey: key, Role: r.Role, InstanceIndex: index, ImageID: image.ProviderImageID, FlavorID: flavor.ProviderFlavorID,
				FlavorSpec: resolvedFlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB},
			})
			if r.Role == spec.WorkspaceRole && index == spec.WorkspaceInstanceIndex {
				snapshot.WorkspaceVMKey = key
			}
		}
	}
	if snapshot.WorkspaceVMKey == "" {
		return nil, uuid.Nil, ErrInconsistentData
	}
	snapshot.ProviderConnectionID = connection.String()
	if spec.StartupScript != nil {
		digest := sha256.Sum256([]byte(*spec.StartupScript))
		snapshot.StartupScript = &startupScriptSnapshot{Content: *spec.StartupScript, SHA256: hex.EncodeToString(digest[:])}
	}

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	// Startup Script 본문을 HTML escape 없이 그대로 보존한다. digest는 원문 기준이다.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(snapshot); err != nil {
		return nil, uuid.Nil, fmt.Errorf("labexecution: CreationSnapshot 직렬화: %w", err)
	}
	return json.RawMessage(bytes.TrimSpace(encoded.Bytes())), connection, nil
}

func distinct(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
