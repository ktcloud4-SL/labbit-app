package openstackprovider

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestKTCloudCIDRPreflightRejectionDoesNotHidePriorUncertainty(t *testing.T) {
	for _, kind := range []string{"invalid config", "missing directory", "exhausted", "foreign lock", "same instance lock", "prior intent invalid config", "prior intent foreign lock", "corrupt pool", "corrupt intent", "orphan intent", "corrupt lock"} {
		t.Run(kind, func(t *testing.T) {
			a := pooledKTAdapter(t, nil)
			dir := a.provision.KTCloudCIDRStateDir
			want, code := coreprovider.OutcomeFailed, errorInvalidProvision
			switch kind {
			case "invalid config":
				a.provision.KTCloudCIDRPairs = "invalid JSON"
			case "missing directory":
				a.provision.KTCloudCIDRStateDir = filepath.Join(dir, "not-created")
			case "exhausted":
				claimCIDR(t, a, "teacher", 1, false)
				claimCIDR(t, a, "other", 1, false)
				code = errorOpenStack
			case "foreign lock":
				if err := writeCIDRFile(dir, "LOCK", cidrCorrelation("teacher", 1)); err != nil {
					t.Fatal(err)
				}
			case "same instance lock":
				want, code = coreprovider.OutcomeUnknown, errorUnknown
				if err := writeCIDRFile(dir, "LOCK", cidrCorrelation("student", 1)); err != nil {
					t.Fatal(err)
				}
			default:
				want, code = coreprovider.OutcomeUnknown, errorUnknown
				claimCIDR(t, a, "student", 1, false)
				switch kind {
				case "prior intent invalid config":
					a.provision.KTCloudCIDRPairs = "invalid JSON"
				case "prior intent foreign lock":
					if err := writeCIDRFile(dir, "LOCK", cidrCorrelation("teacher", 1)); err != nil {
						t.Fatal(err)
					}
				case "corrupt pool":
					if err := os.WriteFile(filepath.Join(dir, "pool.json"), []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				case "corrupt intent":
					if err := os.WriteFile(filepath.Join(dir, intentName(0, 1)), []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				case "orphan intent":
					if err := os.Rename(filepath.Join(dir, slotName(0)), filepath.Join(dir, "orphan-reservation")); err != nil {
						t.Fatal(err)
					}
				case "corrupt lock":
					if err := os.WriteFile(filepath.Join(dir, "LOCK"), []byte("{"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := cidrFiles(t, dir)
			// Public Provider entry point: no fake callback can bypass its result path.
			result, err := a.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: cidrCorrelation("student", 1), CreationSnapshot: cidrRejectionSnapshot()})
			if err != nil || result.Outcome != want || result.Error == nil || result.Error.Code != code {
				t.Fatalf("result=%+v err=%v want=%s/%s", result, err, want, code)
			}
			// pooledKTAdapter rejects every Provider write. Local reservations, intents
			// and another process's LOCK must also survive the rejection byte-for-byte.
			if after := cidrFiles(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejection altered durable state: before=%v after=%v", before, after)
			}
		})
	}
}

func TestKTCloudCIDRResetScopeRejectionPreservesPreviousGeneration(t *testing.T) {
	for _, kind := range []string{"invalid config", "foreign lock"} {
		t.Run(kind, func(t *testing.T) {
			a := pooledKTAdapter(t, nil)
			claimCIDR(t, a, "student", 1, false)
			dir := a.provision.KTCloudCIDRStateDir
			if kind == "invalid config" {
				a.provision.KTCloudCIDRPairs = "invalid JSON"
			} else if err := writeCIDRFile(dir, "LOCK", cidrCorrelation("teacher", 1)); err != nil {
				t.Fatal(err)
			}
			refs := []coreprovider.ResourceRef{
				{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "previous-lab-tier", Generation: 1, LogicalName: "lab-tier"},
				{ResourceType: coreprovider.ResourceTypeTier, ProviderID: "previous-management-tier", Generation: 1, LogicalName: "workspace:management-tier"},
				{ResourceType: coreprovider.ResourceTypeFirewall, ProviderID: "previous-policy", Generation: 1, LogicalName: "workspace:management-ssh"},
				{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "previous-server", Generation: 1, LogicalName: "workspace"},
				{ResourceType: coreprovider.ResourceTypeVolume, ProviderID: "previous-root", Generation: 1, LogicalName: "workspace:root-volume"},
			}
			before := cidrFiles(t, dir)
			result, err := a.Reset(context.Background(), coreprovider.ResetRequest{Correlation: cidrCorrelation("student", 2), ProviderResources: refs, CreationSnapshot: cidrRejectionSnapshot()})
			if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorInvalidProvision {
				t.Fatalf("reset rejection=%+v err=%v", result, err)
			}
			if !reflect.DeepEqual(result.ProviderResources, resourceResults(refs, stateDeleteNotAttempted)) {
				t.Fatalf("reset rejection lost untouched old-generation references: %+v", result.ProviderResources)
			}
			if !reflect.DeepEqual(before, cidrFiles(t, dir)) {
				t.Fatal("reset rejection changed prior generation reservation/intent/LOCK")
			}
		})
	}
}

func cidrFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(data)
	}
	return files
}

func cidrRejectionSnapshot() coreprovider.CreationSnapshot {
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = "local-provider"
	snapshot.InternetOutbound = false
	snapshot.StartupScript = nil
	snapshot.VMs[0].FlavorSpec = coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 0}
	return snapshot
}
