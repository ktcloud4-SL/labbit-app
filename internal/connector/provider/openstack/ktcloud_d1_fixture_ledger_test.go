package openstackprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Fixture-only ledger, held open through cleanup. Existing paths (including
// symlinks) are never adopted, even when an old run was marked deleted.
type ktFixtureLedger struct {
	*os.File
}

func TestKTFixtureCleanupDoesNotAdoptMatchingCandidates(t *testing.T) {
	for _, kind := range []string{"unknown Tier create", "unknown policy create", "FAILED policy job", "successful job without confirmed mapping"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				// An unrelated matching name/comment must never be turned into
				// a cleanup reference when the create result is unconfirmed.
				fmt.Fprint(w, `{"httpStatus":200,"pagination":{"total":1,"offset":0},"data":[{"networkId":"foreign-tier","policyId":"foreign-policy","networkName":"same-name","comment":"same-comment"}]}`)
			})
			tierID, attempted := "confirmed-tier", true
			if kind == "unknown Tier create" {
				tierID, attempted = "", false
			}
			if cleanupKTFixtureKnownResources(context.Background(), n, tierID, "", attempted) == nil {
				t.Fatal("unconfirmed creation passed automatic cleanup")
			}
			if requests.Load() != 0 {
				t.Fatal("unconfirmed cleanup searched or deleted a candidate")
			}
		})
	}
}

func TestKTFixtureCleanupUsesConfirmedIDsAndChecksAbsence(t *testing.T) {
	var mutations []string
	n := ktCloudNetworkPeer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			mutations = append(mutations, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fmt.Fprint(w, `{"httpStatus":200,"pagination":{"total":0,"offset":0},"data":[]}`)
	})
	if err := cleanupKTFixtureKnownResources(context.Background(), n, "confirmed-tier", "confirmed-policy", true); err != nil {
		t.Fatal(err)
	}
	if len(mutations) != 2 || mutations[0] != "/nsm/v1/firewall/policy/confirmed-policy" || mutations[1] != "/nsm/v1/network/confirmed-tier" {
		t.Fatal("confirmed cleanup scope or order changed")
	}
}

func TestKTFixturePolicyIDIsRecordedOnlyAfterCompleteMapping(t *testing.T) {
	config := ProvisionConfig{KTCloudConnectorTierID: "connector", SSHAllowedCIDR: "172.25.0.10/32"}
	for _, scenario := range []string{"valid", "wrong target", "wrong source IP", "deny", "NAT enabled", "status disabled", "status empty", "status UNKNOWN", "status ERROR", "duplicate", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			p := ktCloudFirewallPolicy{ID: "candidate", Comment: "fixture", Action: "accept", Status: "enable", NAT: "disable", Source: []ktCloudInterface{{ID: "connector"}}, Destination: []ktCloudInterface{{ID: "owned-tier"}}, SourceIPs: []ktCloudAddress{{Name: config.SSHAllowedCIDR}}, DestinationIPs: []ktCloudAddress{{Name: "all"}}, Services: []ktCloudService{{Protocol: "TCP", Start: "22", End: "22"}}}
			switch scenario {
			case "wrong target":
				p.Destination[0].ID = "foreign-tier"
			case "wrong source IP":
				p.SourceIPs[0].Name = "0.0.0.0/0"
			case "deny":
				p.Action = "deny"
			case "status disabled":
				p.Status = "disable"
			case "status empty":
				p.Status = ""
			case "status UNKNOWN":
				p.Status = "UNKNOWN"
			case "status ERROR":
				p.Status = "ERROR"
			case "NAT enabled":
				p.NAT = "enable"
			}
			items := []ktCloudFirewallPolicy{p}
			if scenario == "duplicate" {
				items = append(items, p)
			}
			if scenario == "missing" {
				items = nil
			}
			id, err := confirmedKTFixturePolicy(items, "fixture", config, "owned-tier")
			if scenario == "valid" {
				if err != nil || id != "candidate" {
					t.Fatal("verified policy ID lost")
				}
			} else if id != "" || (scenario != "missing" && err == nil) {
				t.Fatal("unverified candidate became a cleanup ID")
			}
		})
	}
}

// Names and comments can locate candidates, but cannot replace a recorded
// create receipt or the successful policy-job/mapping confirmation.
func requireKTFixtureCleanupOwnership(tierID, policyID string, policyAttempted bool) error {
	if tierID == "" || (policyAttempted && policyID == "") {
		return errors.New("fixture ownership unconfirmed; preserve ledger for manual review")
	}
	return nil
}

func openKTFixtureLedger(path string) (*ktFixtureLedger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return &ktFixtureLedger{f}, nil
}

func (ledger *ktFixtureLedger) Save(data []byte) error {
	owned, err := ledger.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(ledger.Name())
	if err != nil || !os.SameFile(owned, current) || !current.Mode().IsRegular() {
		return errors.New("fixture ledger ownership changed")
	}
	if _, err := ledger.WriteAt(data, 0); err != nil {
		return err
	}
	if err := ledger.Truncate(int64(len(data))); err != nil {
		return err
	}
	return ledger.Sync()
}

func TestKTFixtureLedgerPreservesPreviousRunBeforeCloudAccess(t *testing.T) {
	for _, state := range []string{"TIER_CREATE_RESPONSE_RECORDED", "CLEANUP_ABSENCE_CONFIRMED"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ownership.json")
			original := []byte(`{"tierId":"previous-tier","state":"` + state + `"}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			// Exercise the actual gated entry point. Missing provider config would
			// fail later; the ledger guard must win before any authentication.
			command := exec.Command(os.Args[0], "-test.run=^TestKTCloudD1DisposableNetworkIntegration$", "-test.v")
			command.Env = append(os.Environ(), "LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST=1", "LABBIT_KTCLOUD_TEST_LEDGER="+path, "LABBIT_KTCLOUD_TEST_TIER_CIDR=172.25.240.0/24", "LABBIT_KTCLOUD_TEST_SOURCE_CIDR=172.25.1.10/32", "LABBIT_PROVIDER_CONFIG_FILE="+filepath.Join(t.TempDir(), "missing.yaml"))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "fresh exclusive ownership ledger is required before cloud access") {
				t.Fatalf("existing ledger did not stop gated fixture before cloud access: %s", output)
			}
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, original) {
				t.Fatal("previous run ownership was overwritten")
			}
		})
	}
}

func TestKTFixtureLedgerConcurrentClaimAndOwnedUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership.json")
	var group sync.WaitGroup
	winners := make(chan *ktFixtureLedger, 12)
	start := make(chan struct{})
	for i := 0; i < cap(winners); i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if ledger, err := openKTFixtureLedger(path); err == nil {
				winners <- ledger
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatalf("exclusive claims=%d", len(winners))
	}
	ledger := <-winners
	defer ledger.Close()
	for _, value := range []string{`{"tierId":"owned-tier","state":"CREATED"}`, `{"state":"ABSENT"}`} {
		if err := ledger.Save([]byte(value)); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != value {
			t.Fatal("owned receipt was not persisted exactly")
		}
	}
}

func TestKTFixtureLedgerRejectsSymlinkAndReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "original.json")
	if err := os.WriteFile(target, []byte("previous-owned-ids"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(dir, "link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlink creation unavailable on this host")
		}
		if ledger, err := openKTFixtureLedger(link); err == nil {
			ledger.Close()
			t.Fatal("existing symlink was adopted")
		}
		actual, _ := os.ReadFile(target)
		if string(actual) != "previous-owned-ids" {
			t.Fatal("symlink target changed")
		}
	})
	t.Run("replacement", func(t *testing.T) {
		path := filepath.Join(dir, "new.json")
		ledger, err := openKTFixtureLedger(path)
		if err != nil {
			t.Fatal(err)
		}
		defer ledger.Close()
		if err := ledger.Save([]byte("this-run")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, path+".old"); err != nil {
			t.Skip("open-file rename unavailable on this host")
		}
		if err := os.WriteFile(path, []byte("other-run"), 0600); err != nil {
			t.Fatal(err)
		}
		if ledger.Save([]byte("overwrite")) == nil {
			t.Fatal("replacement ledger was overwritten")
		}
		actual, _ := os.ReadFile(path)
		if string(actual) != "other-run" {
			t.Fatal("replacement content changed")
		}
	})
}
