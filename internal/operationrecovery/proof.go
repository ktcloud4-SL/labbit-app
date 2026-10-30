// Package operationrecovery defines the trusted-operator recovery boundary.
// It is deliberately separate from automatic Reconcile and public HTTP/WSS.
package operationrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
)

var ErrUnsafe = errors.New("failed Provision recovery evidence is incomplete or does not match")
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9@:/._-]{1,200}$`)

type Target struct {
	ItemID, OrganizationID, OperationID, InstanceID, ExecutionID uuid.UUID
	ConnectionID, ConnectorID                                    uuid.UUID
	Generation                                                   int64
	SnapshotSHA256, VMKey                                        string
}

type Request struct {
	OperationID, InstanceID, ConnectionID uuid.UUID
	Generation                            int64
	ProjectID, VMKey, Nonce               string
}

type Resource struct {
	Type, ProviderID, LogicalName, ObservedState string
	Generation                                   int64
}

type Evidence struct {
	Request                              Request
	Outcome, LedgerSHA256, ReceiptSHA256 string
	ObservedAt                           time.Time
	Resources                            []Resource
}

type Plan struct {
	Target                                             Target
	ProjectID, LedgerSHA256, ReceiptSHA256, SSHHostKey string
	Operator, Reason                                   string
	Resources                                          []Resource
}

func SHA256(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Validate never treats a hash as authentication. The caller must acquire e
// directly through its explicitly pinned operator SSH channel, with a new nonce.
func Validate(t Target, req Request, e Evidence, hostKey, operator, reason string, now time.Time) (Plan, error) {
	if t.ItemID == uuid.Nil || t.OrganizationID == uuid.Nil || t.ExecutionID == uuid.Nil || t.ConnectorID == uuid.Nil ||
		t.OperationID == uuid.Nil || t.InstanceID == uuid.Nil || t.ConnectionID == uuid.Nil || t.Generation != 1 ||
		!digestPattern.MatchString(t.SnapshotSHA256) || t.VMKey == "" ||
		req.OperationID != t.OperationID || req.InstanceID != t.InstanceID || req.ConnectionID != t.ConnectionID ||
		req.Generation != t.Generation || req.VMKey != t.VMKey || !labelPattern.MatchString(req.ProjectID) ||
		!digestPattern.MatchString(req.Nonce) || e.Request != req || e.Outcome != "FAILED" ||
		!digestPattern.MatchString(e.ReceiptSHA256) || !digestPattern.MatchString(e.LedgerSHA256) ||
		e.ObservedAt.After(now.Add(5*time.Second)) || e.ObservedAt.Before(now.Add(-2*time.Minute)) ||
		!labelPattern.MatchString(operator) || !labelPattern.MatchString(reason) || len(hostKey) < 20 || len(hostKey) > 100 {
		return Plan{}, ErrUnsafe
	}
	if err := ValidateResources(t.VMKey, t.Generation, e.Resources); err != nil {
		return Plan{}, err
	}
	refs := append([]Resource(nil), e.Resources...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].LogicalName < refs[j].LogicalName })
	return Plan{Target: t, ProjectID: req.ProjectID, LedgerSHA256: e.LedgerSHA256, ReceiptSHA256: e.ReceiptSHA256,
		SSHHostKey: hostKey, Operator: operator, Reason: reason, Resources: refs}, nil
}

// Version one is intentionally restricted to the complete KT D1 single-VM,
// generation-one topology. Partial/absent resources and Reset are unsupported.
func ValidateResources(vm string, generation int64, resources []Resource) error {
	expected := map[string]string{"lab-tier": "KT_TIER", vm + ":management-tier": "KT_TIER", vm + ":management-ssh": "KT_FIREWALL_POLICY", vm: "SERVER", vm + ":root-volume": "VOLUME"}
	if generation != 1 || vm == "" || len(resources) != len(expected) {
		return ErrUnsafe
	}
	seenID, seenName := map[string]bool{}, map[string]bool{}
	for _, r := range resources {
		if r.Generation != generation || expected[r.LogicalName] != r.Type || r.Type == "" || seenName[r.LogicalName] ||
			seenID[r.ProviderID] || uuid.Validate(r.ProviderID) != nil ||
			(r.ObservedState != "ACTIVE" && r.ObservedState != "PRESENT" && r.ObservedState != "IN-USE" && r.ObservedState != "AVAILABLE") {
			return ErrUnsafe
		}
		seenID[r.ProviderID], seenName[r.LogicalName] = true, true
	}
	return nil
}

func (p Plan) Digest() string { data, _ := json.Marshal(p); return SHA256(data) }
