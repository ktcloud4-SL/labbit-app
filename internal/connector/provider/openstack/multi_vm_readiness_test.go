package openstackprovider

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestProvisionWithoutStartupScriptChecksEveryVMSSH(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	var addresses []string
	adapter.sshProbe = func(_ context.Context, address string) error {
		addresses = append(addresses, address)
		return nil
	}
	adapter.startupProbe = func(context.Context, string, string) error {
		t.Error("startup probe ran without a Startup Script")
		return nil
	}
	result, err := adapter.Provision(context.Background(), multiVMNoScriptRequest())
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Provision() outcome=%s error=%v", result.Outcome, err)
	}
	if want := []string{"172.16.8.200:22", "172.16.8.201:22"}; !reflect.DeepEqual(addresses, want) {
		t.Fatalf("SSH probes=%v, want %v", addresses, want)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 2)
}

func TestProvisionWithoutStartupScriptRejectsWorkerSSHFailureAndKeepsResources(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.provision.SSHReadyTimeout = 10 * time.Millisecond
	var workspaceChecks, workerChecks int
	adapter.sshProbe = func(_ context.Context, address string) error {
		if address == "172.16.8.200:22" {
			workspaceChecks++
			return nil
		}
		if address == "172.16.8.201:22" {
			workerChecks++
			return errors.New("test worker SSH unavailable")
		}
		t.Errorf("unexpected SSH target %q", address)
		return ErrSSHNotReady
	}
	result, err := adapter.Provision(context.Background(), multiVMNoScriptRequest())
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorBootTimeout {
		t.Fatalf("Provision() outcome=%s safeError=%+v error=%v", result.Outcome, result.Error, err)
	}
	if workspaceChecks != 1 || workerChecks == 0 {
		t.Fatalf("workspace checks=%d worker checks=%d", workspaceChecks, workerChecks)
	}
	if len(result.ProviderResources) != 11 {
		t.Fatalf("partial resources=%d, want all 11 created resource IDs retained", len(result.ProviderResources))
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 2)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypePort, 4)
}

func TestProvisionWithoutStartupScriptKeepsResourcesOnWorkerCancellation(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter.sshProbe = func(_ context.Context, address string) error {
		if address == "172.16.8.200:22" {
			return nil
		}
		if address != "172.16.8.201:22" {
			t.Fatalf("unexpected SSH target %q", address)
		}
		cancel()
		return context.Canceled
	}
	result, err := adapter.Provision(ctx, multiVMNoScriptRequest())
	if err != nil || result.Outcome != coreprovider.OutcomeUnknown || result.Error == nil || result.Error.Code != errorUnknown {
		t.Fatalf("Provision() outcome=%s safeError=%+v error=%v", result.Outcome, result.Error, err)
	}
	if len(result.ProviderResources) != 11 {
		t.Fatalf("canceled Provision lost created resources: got %d, want 11", len(result.ProviderResources))
	}
}

func multiVMNoScriptRequest() coreprovider.ProvisionRequest {
	snapshot := validSnapshot()
	snapshot.StartupScript = nil
	snapshot.VMs = append(snapshot.VMs, coreprovider.VMSpec{
		VMKey: "worker", Role: "WORKER", InstanceIndex: 1, ImageID: "image-ubuntu", FlavorID: "flavor-small",
		FlavorSpec: coreprovider.FlavorSpec{VCPUs: 1, RAMMiB: 2048, DiskGiB: 20},
	})
	return coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "multi-no-script", LabInstanceID: "multi-no-script-lab", Generation: 1},
		CreationSnapshot: snapshot,
	}
}
