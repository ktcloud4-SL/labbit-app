package openstackprovider

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Fixture-only ledger, held open through cleanup. Existing paths (including
// symlinks) are never adopted, even when an old run was marked deleted.
type ktFixtureLedger struct {
	*os.File
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
