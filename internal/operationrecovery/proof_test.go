package operationrecovery

import (
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func proofFixture() (Target, Request, Evidence) {
	t := Target{ItemID: uuid.New(), OrganizationID: uuid.New(), OperationID: uuid.New(), InstanceID: uuid.New(), ExecutionID: uuid.New(), ConnectionID: uuid.New(), ConnectorID: uuid.New(), Generation: 1, SnapshotSHA256: SHA256([]byte("snapshot")), VMKey: "vm-1"}
	r := Request{OperationID: t.OperationID, InstanceID: t.InstanceID, ConnectionID: t.ConnectionID, Generation: 1, ProjectID: "project", VMKey: t.VMKey, Nonce: SHA256([]byte("nonce"))}
	e := Evidence{Request: r, Outcome: "FAILED", LedgerSHA256: SHA256([]byte("ledger")), ReceiptSHA256: SHA256([]byte("receipt")), ObservedAt: time.Now().UTC()}
	for name, kind := range map[string]string{"lab-tier": "KT_TIER", "vm-1:management-tier": "KT_TIER", "vm-1:management-ssh": "KT_FIREWALL_POLICY", "vm-1": "SERVER", "vm-1:root-volume": "VOLUME"} {
		e.Resources = append(e.Resources, Resource{Type: kind, ProviderID: uuid.NewString(), LogicalName: name, ObservedState: "PRESENT", Generation: 1})
	}
	return t, r, e
}

func TestFailureProofRejectsUntrustedOrIncompleteResult(t *testing.T) {
	for _, scenario := range []string{"valid", "success", "unknown", "stale", "future", "wrong nonce", "wrong connection", "wrong instance", "wrong operation", "wrong generation", "missing root", "duplicate ID", "absent", "unexpected logical", "invalid UUID"} {
		t.Run(scenario, func(t *testing.T) {
			target, req, e := proofFixture()
			switch scenario {
			case "success":
				e.Outcome = "SUCCEEDED"
			case "unknown":
				e.Outcome = "UNKNOWN"
			case "stale":
				e.ObservedAt = e.ObservedAt.Add(-3 * time.Minute)
			case "future":
				e.ObservedAt = e.ObservedAt.Add(time.Minute)
			case "wrong nonce":
				e.Request.Nonce = SHA256([]byte("replay"))
			case "wrong connection":
				e.Request.ConnectionID = uuid.New()
			case "wrong instance":
				e.Request.InstanceID = uuid.New()
			case "wrong operation":
				e.Request.OperationID = uuid.New()
			case "wrong generation":
				e.Request.Generation = 2
			case "missing root":
				e.Resources = e.Resources[:4]
			case "duplicate ID":
				e.Resources[0].ProviderID = e.Resources[1].ProviderID
			case "absent":
				e.Resources[0].ObservedState = "ABSENT"
			case "unexpected logical":
				e.Resources[0].LogicalName = "foreign"
			case "invalid UUID":
				e.Resources[0].ProviderID = "../foreign"
			}
			_, err := Validate(target, req, e, "SHA256:"+strings.Repeat("a", 43), "operator", "LBT-197", time.Now())
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("scenario=%s err=%v", scenario, err)
			}
		})
	}
}

func TestRecoveryPlanChangesWithAuthorityButNotResourceOrdering(t *testing.T) {
	target, req, e := proofFixture()
	key := "SHA256:" + strings.Repeat("a", 43)
	a, err := Validate(target, req, e, key, "operator", "LBT-197", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(e.Resources)-1; i < j; i, j = i+1, j-1 {
		e.Resources[i], e.Resources[j] = e.Resources[j], e.Resources[i]
	}
	b, err := Validate(target, req, e, key, "operator", "LBT-197", time.Now())
	if err != nil || a.Digest() != b.Digest() {
		t.Fatal("resource order changed canonical plan")
	}
	for _, scenario := range []string{"snapshot", "connector", "receipt", "ledger", "project", "host", "operator"} {
		c := b
		switch scenario {
		case "snapshot":
			c.Target.SnapshotSHA256 = SHA256([]byte("changed"))
		case "connector":
			c.Target.ConnectorID = uuid.New()
		case "receipt":
			c.ReceiptSHA256 = SHA256([]byte("changed"))
		case "ledger":
			c.LedgerSHA256 = SHA256([]byte("changed"))
		case "project":
			c.ProjectID = "different"
		case "host":
			c.SSHHostKey += "different"
		case "operator":
			c.Operator = "different"
		}
		if c.Digest() == a.Digest() {
			t.Fatalf("authority change %s did not invalidate apply", scenario)
		}
	}
}
