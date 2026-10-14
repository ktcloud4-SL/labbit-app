//go:build backend_router_harness

package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

var errHarnessGate = errors.New("C1: operator gate/preflight invalid; no mutation")
var errHarnessLedger = errors.New("C1: exclusive persistent ledger unavailable; no further commands")

var c1BaselineKinds = []string{"SERVER", "VOLUME", "KT_TIER", "KT_FIREWALL_POLICY", "PUBLIC_IP", "PORT_FORWARD", "KEYPAIR"}

// NSM inventory metadata, keyed explicitly by networkId rather than by position.
// refId is the physical attachment ID, not the baseline resource identity.
type c1ExistingTier struct {
	NetworkID string `json:"networkId"`
	RefID     string `json:"refId"`
	Name      string `json:"networkName"`
	CIDR      string `json:"cidr"`
}

// Operator attestation, not a SaaS Runtime/wire contract. Provider secrets stay
// on the Connector. The Backend cannot inspect its local Provider configuration.
type c1Plan struct {
	Approval             string                    `json:"approval"`
	ExpiresAt            time.Time                 `json:"expiresAt"`
	PodName              string                    `json:"podName"`
	PodUID               uuid.UUID                 `json:"podUid"`
	ConnectorID          uuid.UUID                 `json:"connectorId"`
	SessionID            uuid.UUID                 `json:"sessionId"`
	ProjectID            uuid.UUID                 `json:"projectId"`
	EvidenceSHA256       string                    `json:"evidenceSha256"`
	ProviderRuntimeReady bool                      `json:"providerRuntimeReady"`
	LinuxImageVerified   bool                      `json:"linuxImageVerified"`
	RootGiB              int                       `json:"rootGiB"`
	CostAndQuotaApproved bool                      `json:"costAndQuotaApproved"`
	CleanupApproved      bool                      `json:"cleanupApproved"`
	BaselineComplete     bool                      `json:"baselineComplete"`
	Baseline             map[string][]string       `json:"baseline"`
	ExistingTiers        []c1ExistingTier          `json:"existingTiers"`
	LabCIDR              string                    `json:"labCidr"`
	ManagementCIDR       string                    `json:"managementCidr"`
	Snapshot             protocol.CreationSnapshot `json:"creationSnapshot"`
}

func (p c1Plan) validate(now time.Time, podName string, podUID uuid.UUID) error {
	if p.Approval != "PROVISION_AND_EXACT_ID_CLEANUP" || !p.ExpiresAt.After(now) || p.ExpiresAt.After(now.Add(15*time.Minute)) ||
		p.PodName == "" || p.PodName != podName || p.PodUID == uuid.Nil || p.PodUID != podUID ||
		p.ConnectorID == uuid.Nil || p.SessionID == uuid.Nil || p.ProjectID == uuid.Nil ||
		!p.ProviderRuntimeReady || !p.LinuxImageVerified || p.RootGiB != 50 || !p.CostAndQuotaApproved || !p.CleanupApproved || !p.BaselineComplete {
		return errHarnessGate
	}
	hash, err := hex.DecodeString(p.EvidenceSHA256)
	if err != nil || len(hash) != sha256.Size {
		return errHarnessGate
	}
	if len(p.Baseline) != len(c1BaselineKinds) {
		return errHarnessGate
	}
	for _, kind := range c1BaselineKinds {
		ids, exists := p.Baseline[kind]
		if !exists || ids == nil {
			return errHarnessGate
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if !c1SafeID(id) || seen[id] {
				return errHarnessGate
			}
			seen[id] = true
		}
	}
	if len(p.Baseline["KT_TIER"]) == 0 || len(p.ExistingTiers) != len(p.Baseline["KT_TIER"]) {
		return errHarnessGate
	}
	lab, err := netip.ParsePrefix(p.LabCIDR)
	management, managementErr := netip.ParsePrefix(p.ManagementCIDR)
	if err != nil || managementErr != nil || !lab.Addr().Is4() || !management.Addr().Is4() ||
		lab.Bits() != 24 || management.Bits() != 24 || lab != lab.Masked() || management != management.Masked() || lab.Overlaps(management) {
		return errHarnessGate
	}
	pendingTiers := make(map[string]bool, len(p.ExistingTiers))
	for _, id := range p.Baseline["KT_TIER"] {
		pendingTiers[id] = true
	}
	for _, tier := range p.ExistingTiers {
		if !pendingTiers[tier.NetworkID] {
			return errHarnessGate
		}
		delete(pendingTiers, tier.NetworkID)
		// The existing Provider fixture and LBT-145 inventory identify the
		// external entry by name, empty refId and absent CIDR. A name alone
		// cannot excuse a regular attached Tier's missing CIDR.
		if tier.Name == "external" && tier.RefID == "" {
			if tier.CIDR != "" {
				return errHarnessGate
			}
			continue
		}
		prefix, err := netip.ParsePrefix(tier.CIDR)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || lab.Overlaps(prefix) || management.Overlaps(prefix) {
			return errHarnessGate
		}
	}
	s := p.Snapshot
	if id, err := uuid.Parse(s.ProviderConnectionID); err != nil || id == uuid.Nil {
		return errHarnessGate
	}
	if len(s.VMs) != 1 || s.InternetOutbound || s.StartupScript != nil {
		return errHarnessGate
	}
	vm := s.VMs[0]
	if vm.VMKey != "workspace" || s.WorkspaceVMKey != vm.VMKey || vm.Role != "workspace" || vm.InstanceIndex != 0 ||
		vm.ImageRef != "" || vm.FlavorRef != "" || vm.FlavorSpec == nil || vm.FlavorSpec.VCPUs <= 0 || vm.FlavorSpec.RAMMiB <= 0 || vm.FlavorSpec.DiskGiB < 0 || vm.FlavorSpec.DiskGiB > 50 {
		return errHarnessGate
	}
	for _, id := range []string{vm.ImageID, vm.FlavorID} {
		if value, err := uuid.Parse(id); err != nil || value == uuid.Nil {
			return errHarnessGate
		}
	}
	return nil
}

func c1SafeID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func c1Private(info os.FileInfo, permission os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == permission
}

// The protected mount must survive Pod/process replacement. O_EXCL claims one
// attempt across processes; even a completed, empty, or interrupted ledger blocks.
// os.Root anchors all file operations to that directory (no path traversal).
func c1OpenRoot(dir string) (*os.Root, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errHarnessGate
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil || canonical != dir {
		return nil, errHarnessGate
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errHarnessGate
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || !c1Private(info, 0700) {
		root.Close()
		return nil, errHarnessGate
	}
	return root, nil
}

func c1ReadPlan(root *os.Root) (c1Plan, string, error) {
	var p c1Plan
	info, err := root.Lstat("approval.json")
	if err != nil || !info.Mode().IsRegular() || !c1Private(info, 0600) || info.Size() > 65536 {
		return p, "", errHarnessGate
	}
	f, err := root.Open("approval.json")
	if err != nil {
		return p, "", errHarnessGate
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !c1Private(opened, 0600) {
		return p, "", errHarnessGate
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return p, "", errHarnessGate
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF {
		return p, "", errHarnessGate
	}
	hash := sha256.Sum256(data)
	return p, hex.EncodeToString(hash[:]), nil
}

type c1Ledger struct {
	root *os.Root
	file *os.File
}

func c1Claim(root *os.Root) (*c1Ledger, error) {
	f, err := root.OpenFile("attempt.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL|os.O_APPEND, 0600)
	if err != nil {
		return nil, errHarnessLedger
	}
	dir, err := root.Open(".")
	if err != nil {
		f.Close()
		return nil, errHarnessLedger
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		f.Close()
		return nil, errHarnessLedger
	}
	return &c1Ledger{root: root, file: f}, nil
}

func (l *c1Ledger) record(value any) error {
	info, err := l.root.Lstat("attempt.jsonl")
	opened, statErr := l.file.Stat()
	if err != nil || statErr != nil || !info.Mode().IsRegular() || !c1Private(info, 0600) || !os.SameFile(info, opened) {
		return errHarnessLedger
	}
	data, err := json.Marshal(value)
	if err != nil {
		return errHarnessLedger
	}
	data = append(data, '\n')
	if n, err := l.file.Write(data); err != nil || n != len(data) {
		return errHarnessLedger
	}
	if l.file.Sync() != nil {
		return errHarnessLedger
	}
	return nil
}
