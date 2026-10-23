package openstackprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func testWorkspaceArtifact(t *testing.T) (ProvisionConfig, []byte) {
	t.Helper()
	// Minimal ELF header: artifact validation only, not a runnable guest helper.
	data := make([]byte, 64)
	copy(data, []byte{'\x7f', 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(data[16:], 2)
	binary.LittleEndian.PutUint16(data[18:], 62)
	binary.LittleEndian.PutUint32(data[20:], 1)
	binary.LittleEndian.PutUint16(data[52:], 64)
	file := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(file, data, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
	digest := sha256.Sum256(data)
	return ProvisionConfig{WorkspaceRoot: "/var/lib/labbit-workspaces/workspace", WorkspaceHelper: "/usr/local/libexec/labbit-workspace-sftp", WorkspaceHelperSource: file, WorkspaceHelperSHA256: hex.EncodeToString(digest[:])}, data
}

func TestWorkspaceArtifactValidation(t *testing.T) {
	config, expected := testWorkspaceArtifact(t)
	data, err := workspaceBootstrapArtifact(config)
	if err != nil || !bytes.Equal(data, expected) {
		t.Fatalf("artifact read: %v", err)
	}
	upper := config
	upper.WorkspaceHelperSHA256 = strings.ToUpper(config.WorkspaceHelperSHA256)
	if _, err := workspaceBootstrapArtifact(upper); err != nil {
		t.Fatal("uppercase digest rejected")
	}
	for _, mutate := range []func(*ProvisionConfig){
		func(c *ProvisionConfig) { c.WorkspaceHelperSource += "-missing" },
		func(c *ProvisionConfig) { c.WorkspaceHelperSHA256 = strings.Repeat("0", 64) },
		func(c *ProvisionConfig) { c.WorkspaceHelperSHA256 = "" },
		func(c *ProvisionConfig) { c.WorkspaceRoot = "/" },
		func(c *ProvisionConfig) { c.WorkspaceRoot = "/var/lib/../workspace" },
		func(c *ProvisionConfig) { c.WorkspaceHelper = c.WorkspaceRoot + "/helper" },
		func(c *ProvisionConfig) { c.WorkspaceRoot = c.WorkspaceHelper + "/workspace" },
	} {
		invalid := config
		mutate(&invalid)
		if _, err := workspaceBootstrapArtifact(invalid); err == nil {
			t.Fatal("invalid artifact accepted")
		}
	}
	invalid := config
	bad := []byte("not a Linux helper")
	if err := os.Chmod(config.WorkspaceHelperSource, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.WorkspaceHelperSource, bad, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config.WorkspaceHelperSource, 0o444); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bad)
	invalid.WorkspaceHelperSHA256 = hex.EncodeToString(digest[:])
	if _, err := workspaceBootstrapArtifact(invalid); err == nil {
		t.Fatal("non-ELF helper accepted")
	}
}

func TestWorkspaceArtifactFailurePrecedesProvisionMutationAndResetDelete(t *testing.T) {
	for _, scenario := range []struct{ reset, overlap bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		reset := scenario.reset
		fake := &m2OpenStackFake{t: t}
		mutations := 0
		adapter := newTestAdapter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				mutations++
			}
			fake.ServeHTTP(w, r)
		}))
		adapter.provision = testProvisionConfig(t)
		adapter.provision.WorkspaceHelperSource = filepath.Join(t.TempDir(), "missing")
		adapter.provision.WorkspaceHelperSHA256 = strings.Repeat("0", 64)
		if scenario.overlap {
			artifact, _ := testWorkspaceArtifact(t)
			adapter.provision.WorkspaceRoot = artifact.WorkspaceHelper + "/workspace"
			adapter.provision.WorkspaceHelper = artifact.WorkspaceHelper
			adapter.provision.WorkspaceHelperSource = artifact.WorkspaceHelperSource
			adapter.provision.WorkspaceHelperSHA256 = artifact.WorkspaceHelperSHA256
		}
		correlation := coreprovider.Correlation{OperationID: "bootstrap-preflight", LabInstanceID: "lab-instance-1", Generation: 2}
		var result coreprovider.OperationResult
		var err error
		if reset {
			result, err = adapter.Reset(context.Background(), coreprovider.ResetRequest{Correlation: correlation, CreationSnapshot: validSnapshot(), ProviderResources: validResetResourceRefs(validSnapshot(), 1)})
		} else {
			result, err = adapter.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: correlation, CreationSnapshot: validSnapshot()})
		}
		if err != nil || result.Outcome != coreprovider.OutcomeFailed || mutations != 0 {
			t.Fatalf("reset=%v mutations=%d outcome=%s error=%v", reset, mutations, result.Outcome, err)
		}
	}
}

func TestWorkspaceBootstrapDoesNotTouchOtherVMsOrDisabledDeployments(t *testing.T) {
	adapter := &Adapter{}
	request := coreprovider.ProvisionRequest{CreationSnapshot: validSnapshot()}
	if err := adapter.prepareWorkspace(context.Background(), request, "other-vm", "server", []byte("artifact")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.prepareWorkspace(context.Background(), request, request.CreationSnapshot.WorkspaceVMKey, "server", nil); err != nil {
		t.Fatal(err)
	}
	if data, err := workspaceBootstrapArtifact(ProvisionConfig{}); err != nil || data != nil {
		t.Fatal("manual preparation mode changed")
	}
}

func TestWorkspaceBootstrapFailureRetainsCreatedResourcesAndStartupBytes(t *testing.T) {
	artifact, _ := testWorkspaceArtifact(t)
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.provision.WorkspaceRoot = artifact.WorkspaceRoot
	adapter.provision.WorkspaceHelper = artifact.WorkspaceHelper
	adapter.provision.WorkspaceHelperSource = artifact.WorkspaceHelperSource
	adapter.provision.WorkspaceHelperSHA256 = artifact.WorkspaceHelperSHA256
	adapter.sshProbe = func(context.Context, string) error { return nil }
	adapter.startupProbe = func(context.Context, string, string) error { fake.startupProbes++; return nil }
	snapshot := validSnapshot()
	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{Correlation: coreprovider.Correlation{OperationID: "bootstrap-partial", LabInstanceID: "lab-instance-1", Generation: 2}, CreationSnapshot: snapshot})
	// The fake's detail metadata does not satisfy the final VMAccess ownership
	// check. Fail closed with every confirmed resource; never claim success.
	if err != nil || result.Outcome != coreprovider.OutcomeUnknown || len(result.ProviderResources) != 8 {
		t.Fatalf("outcome=%s resources=%d error=%v", result.Outcome, len(result.ProviderResources), err)
	}
	if fake.userData != snapshot.StartupScript.Content || fake.startupProbes != 1 {
		t.Fatal("startup was changed or not awaited before bootstrap")
	}
}
