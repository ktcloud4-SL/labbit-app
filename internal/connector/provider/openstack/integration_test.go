package openstackprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestOpenStackM1Integration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_INTEGRATION") != "1" {
		t.Skip("set LABBIT_OPENSTACK_INTEGRATION=1 to run against a local OpenStack cloud")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adapter, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("OpenStack adapter initialization failed: %v", err)
	}
	if err := adapter.ValidateConnection(ctx); err != nil {
		t.Fatalf("Keystone validation failed: %v", err)
	}

	images, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatalf("image discovery failed: %v", err)
	}
	flavors, err := adapter.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("flavor discovery failed: %v", err)
	}
	servers, err := adapter.ListServers(ctx)
	if err != nil {
		t.Fatalf("server discovery failed: %v", err)
	}
	networkItems, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("network discovery failed: %v", err)
	}
	t.Logf("Keystone and M1 discovery succeeded: images=%d flavors=%d servers=%d networks=%d", len(images), len(flavors), len(servers), len(networkItems))

	if os.Getenv("LABBIT_OPENSTACK_MUTATION_TEST") != "1" {
		return
	}
	testM1NetworkFoundation(t, adapter)
}

func TestOpenStackM2ProvisionIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_M2_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_M2_TEST=1 to create and clean one real Provision topology")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	adapter, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("OpenStack adapter initialization failed: %v", err)
	}
	images, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatalf("image discovery failed: %v", err)
	}
	flavors, err := adapter.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("flavor discovery failed: %v", err)
	}
	networkItems, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("network discovery failed: %v", err)
	}
	image := exactImage(t, images, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavors, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	managementNetwork := exactNetwork(t, networkItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	adapter.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration",
		ProjectID:            adapter.provision.ProjectID,
		ManagementNetworkID:  managementNetwork.ID,
		ExternalNetworkID:    adapter.provision.ExternalNetworkID,
		KeyPairName:          environmentOrDefault("LABBIT_OPENSTACK_TEST_KEYPAIR", "openstack2"),
		SSHAllowedCIDR:       environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32"),
		LabSubnetCIDR:        environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		ActiveTimeout:        5 * time.Minute,
		SSHReadyTimeout:      3 * time.Minute,
		PollInterval:         2 * time.Second,
	})

	runID := integrationRunID(t)
	snapshot := coreprovider.CreationSnapshot{
		ProviderConnectionID: "local-integration",
		VMs: []coreprovider.VMSpec{{
			VMKey:         "workspace",
			Role:          "WORKSPACE",
			InstanceIndex: 0,
			ImageID:       image.ID,
			FlavorID:      flavor.ID,
			FlavorSpec: coreprovider.FlavorSpec{
				VCPUs:   flavor.VCPUs,
				RAMMiB:  flavor.RAMMiB,
				DiskGiB: flavor.DiskGiB,
			},
		}},
		WorkspaceVMKey:   "workspace",
		InternetOutbound: true,
	}
	result, dispatchErr := coreprovider.DispatchOperation(ctx, adapter, coreprovider.OperationCommand{
		Correlation: coreprovider.Correlation{
			OperationID:   "m2-integration-" + runID,
			LabInstanceID: "m2-integration-" + runID,
			Generation:    1,
		},
		MutationType:     coreprovider.MutationProvision,
		CreationSnapshot: &snapshot,
	})
	t.Cleanup(func() { cleanupM2Resources(t, adapter, result.ProviderResources) })
	if dispatchErr != nil {
		t.Fatalf("Provision dispatch failed: %v", dispatchErr)
	}
	if result.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("Provision outcome = %s, safe error = %+v, tracked resources = %d", result.Outcome, result.Error, len(result.ProviderResources))
	}
	if len(result.ProviderResources) != 10 {
		t.Fatalf("tracked resource count = %d, want 10", len(result.ProviderResources))
	}
	t.Logf("M2 Provision reached VM ACTIVE and SSH ready; tracked resources=%d", len(result.ProviderResources))
}

func TestOpenStackM3LifecycleIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_M3_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_M3_TEST=1 to run Provision, Reset, Reconcile, and Cleanup against a local OpenStack cloud")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	adapter, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("OpenStack adapter initialization failed: %v", err)
	}
	images, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatalf("image discovery failed: %v", err)
	}
	flavors, err := adapter.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("flavor discovery failed: %v", err)
	}
	networkItems, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("network discovery failed: %v", err)
	}
	image := exactImage(t, images, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavors, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	managementNetwork := exactNetwork(t, networkItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	adapter.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration",
		ProjectID:            adapter.provision.ProjectID,
		ManagementNetworkID:  managementNetwork.ID,
		ExternalNetworkID:    adapter.provision.ExternalNetworkID,
		KeyPairName:          environmentOrDefault("LABBIT_OPENSTACK_TEST_KEYPAIR", "openstack2"),
		SSHAllowedCIDR:       environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32"),
		LabSubnetCIDR:        environmentOrDefault("LABBIT_OPENSTACK_M3_LAB_CIDR", "198.21.0.0/24"),
		ActiveTimeout:        5 * time.Minute,
		SSHReadyTimeout:      3 * time.Minute,
		PollInterval:         2 * time.Second,
	})

	runID := integrationRunID(t)
	labInstanceID := "m3-integration-" + runID
	snapshot := coreprovider.CreationSnapshot{
		ProviderConnectionID: "local-integration",
		VMs: []coreprovider.VMSpec{{
			VMKey:         "workspace",
			Role:          "WORKSPACE",
			InstanceIndex: 0,
			ImageID:       image.ID,
			FlavorID:      flavor.ID,
			FlavorSpec: coreprovider.FlavorSpec{
				VCPUs:   flavor.VCPUs,
				RAMMiB:  flavor.RAMMiB,
				DiskGiB: flavor.DiskGiB,
			},
		}},
		WorkspaceVMKey:   "workspace",
		InternetOutbound: true,
	}

	provision, err := adapter.Provision(ctx, coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "m3-provision-" + runID, LabInstanceID: labInstanceID, Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || provision.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("M3 initial Provision = %+v, %v", provision, err)
	}
	tracked := append([]coreprovider.ResourceResult(nil), provision.ProviderResources...)
	t.Cleanup(func() { cleanupM2Resources(t, adapter, tracked) })
	oldResources := resourceRefs(provision.ProviderResources, 1)

	reset, err := adapter.Reset(ctx, coreprovider.ResetRequest{
		Correlation:       coreprovider.Correlation{OperationID: "m3-reset-" + runID, LabInstanceID: labInstanceID, Generation: 2},
		CreationSnapshot:  snapshot,
		ProviderResources: oldResources,
	})
	newResources := resourceResultsForGeneration(reset.ProviderResources, 2)
	tracked = append(tracked, newResources...)
	if err != nil || reset.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("M3 Reset = %+v, %v", reset, err)
	}
	if len(newResources) != 10 {
		t.Fatalf("M3 Reset generation 2 resources = %d, want 10", len(newResources))
	}

	reconciled, err := adapter.Reconcile(ctx, coreprovider.ReconcileRequest{
		Correlation:        coreprovider.Correlation{OperationID: "m3-reconcile-live-" + runID, LabInstanceID: labInstanceID, Generation: 2},
		KnownResources:     resourceRefs(newResources, 2),
		DiscoverCandidates: true,
	})
	if err != nil || reconciled.Error != nil || len(reconciled.Observations) != len(newResources) {
		t.Fatalf("M3 live Reconcile = %+v, %v", reconciled, err)
	}
	for _, observation := range reconciled.Observations {
		if !observation.Exists || observation.Source != coreprovider.SourceKnownResource {
			t.Fatalf("unexpected live observation: %+v", observation)
		}
	}

	cleanup, err := adapter.Cleanup(ctx, coreprovider.CleanupRequest{
		Correlation:       coreprovider.Correlation{OperationID: "m3-cleanup-" + runID, LabInstanceID: labInstanceID, Generation: 2},
		ProviderResources: resourceRefs(newResources, 2),
	})
	if err != nil || cleanup.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("M3 Cleanup = %+v, %v", cleanup, err)
	}

	final, err := adapter.Reconcile(ctx, coreprovider.ReconcileRequest{
		Correlation:        coreprovider.Correlation{OperationID: "m3-reconcile-final-" + runID, LabInstanceID: labInstanceID, Generation: 2},
		KnownResources:     resourceRefs(newResources, 2),
		DiscoverCandidates: true,
	})
	if err != nil || final.Error != nil || len(final.Observations) != len(newResources) {
		t.Fatalf("M3 final Reconcile = %+v, %v", final, err)
	}
	for _, observation := range final.Observations {
		if observation.Exists || observation.ObservedState != stateAbsent || observation.Source != coreprovider.SourceKnownResource {
			t.Fatalf("residual resource observation: %+v", observation)
		}
	}
	t.Logf("M3 lifecycle succeeded: Provision -> Reset -> Reconcile -> Cleanup; residual resources=0")
}

func resourceRefs(resources []coreprovider.ResourceResult, generation int64) []coreprovider.ResourceRef {
	refs := make([]coreprovider.ResourceRef, 0, len(resources))
	for _, resource := range resources {
		if resource.Generation == generation {
			refs = append(refs, resource.ResourceRef)
		}
	}
	return refs
}

func resourceResultsForGeneration(resources []coreprovider.ResourceResult, generation int64) []coreprovider.ResourceResult {
	filtered := make([]coreprovider.ResourceResult, 0, len(resources))
	for _, resource := range resources {
		if resource.Generation == generation {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func testM1NetworkFoundation(t *testing.T, adapter *Adapter) {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	runID := hex.EncodeToString(random)
	name := "labbit-m1-integration-" + runID
	identity := ResourceIdentity{Generation: 1, LogicalName: name}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	networkResult, err := adapter.EnsureNetwork(ctx, identity, NetworkSpec{
		Name:        name,
		Description: "Labbit M1 integration test " + runID,
	})
	if err != nil {
		t.Fatalf("network creation failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := networks.Delete(cleanupContext, adapter.network, networkResult.ProviderID).ExtractErr(); err != nil {
			t.Errorf("temporary network cleanup failed for id %s", networkResult.ProviderID)
			return
		}
		if _, err := networks.Get(cleanupContext, adapter.network, networkResult.ProviderID).Extract(); !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("temporary network still exists after cleanup for id %s", networkResult.ProviderID)
		}
	})

	subnetResult, err := adapter.EnsureSubnet(ctx, identity, SubnetSpec{
		Name:        name + "-subnet",
		Description: "Labbit M1 integration test " + runID,
		NetworkID:   networkResult.ProviderID,
		CIDR:        "198.18.0.0/24",
		EnableDHCP:  true,
	})
	if err != nil {
		t.Fatalf("subnet creation failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := subnets.Delete(cleanupContext, adapter.network, subnetResult.ProviderID).ExtractErr(); err != nil {
			t.Errorf("temporary subnet cleanup failed for id %s", subnetResult.ProviderID)
			return
		}
		if _, err := subnets.Get(cleanupContext, adapter.network, subnetResult.ProviderID).Extract(); !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("temporary subnet still exists after cleanup for id %s", subnetResult.ProviderID)
		}
	})

	securityGroupResult, err := adapter.EnsureSecurityGroup(ctx, identity, SecurityGroupSpec{
		Name:        name + "-sg",
		Description: "Labbit M1 integration test " + runID,
	})
	if err != nil {
		t.Fatalf("security group creation failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := groups.Delete(cleanupContext, adapter.network, securityGroupResult.ProviderID).ExtractErr(); err != nil {
			t.Errorf("temporary security group cleanup failed for id %s", securityGroupResult.ProviderID)
			return
		}
		if _, err := groups.Get(cleanupContext, adapter.network, securityGroupResult.ProviderID).Extract(); !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("temporary security group still exists after cleanup for id %s", securityGroupResult.ProviderID)
		}
	})

	if second, err := adapter.EnsureNetwork(ctx, identity, NetworkSpec{Name: name}); err != nil || second.ProviderID != networkResult.ProviderID {
		t.Fatalf("network lookup after creation failed: result=%+v error=%v", second, err)
	}
	if second, err := adapter.EnsureSubnet(ctx, identity, SubnetSpec{Name: name + "-subnet", NetworkID: networkResult.ProviderID, CIDR: "198.18.0.0/24", EnableDHCP: true}); err != nil || second.ProviderID != subnetResult.ProviderID {
		t.Fatalf("subnet lookup after creation failed: result=%+v error=%v", second, err)
	}
	if second, err := adapter.EnsureSecurityGroup(ctx, identity, SecurityGroupSpec{Name: name + "-sg"}); err != nil || second.ProviderID != securityGroupResult.ProviderID {
		t.Fatalf("security group lookup after creation failed: result=%+v error=%v", second, err)
	}

	t.Logf("M1 network foundation succeeded and cleanup is scheduled: network=%s subnet=%s securityGroup=%s", networkResult.ProviderID, subnetResult.ProviderID, securityGroupResult.ProviderID)
}

func exactImage(t *testing.T, items []coreprovider.Image, name string) coreprovider.Image {
	t.Helper()
	var matches []coreprovider.Image
	for _, item := range items {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("image name %q matched %d resources", name, len(matches))
	}
	return matches[0]
}

func exactFlavor(t *testing.T, items []coreprovider.Flavor, name string) coreprovider.Flavor {
	t.Helper()
	var matches []coreprovider.Flavor
	for _, item := range items {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("flavor name %q matched %d resources", name, len(matches))
	}
	return matches[0]
}

func exactNetwork(t *testing.T, items []coreprovider.Network, name string) coreprovider.Network {
	t.Helper()
	var matches []coreprovider.Network
	for _, item := range items {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("network name %q matched %d resources", name, len(matches))
	}
	return matches[0]
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func integrationRunID(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(random)
}

func cleanupM2Resources(t *testing.T, adapter *Adapter, resources []coreprovider.ResourceResult) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for index := len(resources) - 1; index >= 0; index-- {
		resource := resources[index]
		var err error
		switch resource.ResourceType {
		case coreprovider.ResourceTypeServer:
			err = servers.Delete(ctx, adapter.compute, resource.ProviderID).ExtractErr()
			if err == nil || gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
				err = waitServerDeleted(ctx, adapter, resource.ProviderID)
			}
		case coreprovider.ResourceTypePort:
			err = ports.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
		case coreprovider.ResourceTypeRouter:
			err = adapter.removeRouterInterfaces(ctx, resource.ProviderID)
			if err == nil {
				err = routers.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
			}
		case coreprovider.ResourceTypeSecurityRule:
			err = rules.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
		case coreprovider.ResourceTypeSecurityGroup:
			err = groups.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
		case coreprovider.ResourceTypeSubnet:
			err = subnets.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
		case coreprovider.ResourceTypeNetwork:
			err = networks.Delete(ctx, adapter.network, resource.ProviderID).ExtractErr()
		}
		if err != nil && !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("cleanup failed for %s id %s", resource.ResourceType, resource.ProviderID)
		}
	}
	for _, resource := range resources {
		if err := verifyM2ResourceDeleted(ctx, adapter, resource); err != nil {
			t.Errorf("resource remains after cleanup: %s id %s", resource.ResourceType, resource.ProviderID)
		}
	}
}

func waitServerDeleted(ctx context.Context, adapter *Adapter, serverID string) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_, err := servers.Get(ctx, adapter.compute, serverID).Extract()
		if gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func verifyM2ResourceDeleted(ctx context.Context, adapter *Adapter, resource coreprovider.ResourceResult) error {
	var err error
	switch resource.ResourceType {
	case coreprovider.ResourceTypeServer:
		_, err = servers.Get(ctx, adapter.compute, resource.ProviderID).Extract()
	case coreprovider.ResourceTypePort:
		_, err = ports.Get(ctx, adapter.network, resource.ProviderID).Extract()
	case coreprovider.ResourceTypeRouter:
		_, err = routers.Get(ctx, adapter.network, resource.ProviderID).Extract()
	case coreprovider.ResourceTypeSecurityRule:
		_, err = rules.Get(ctx, adapter.network, resource.ProviderID).Extract()
	case coreprovider.ResourceTypeSecurityGroup:
		_, err = groups.Get(ctx, adapter.network, resource.ProviderID).Extract()
	case coreprovider.ResourceTypeSubnet:
		_, err = subnets.Get(ctx, adapter.network, resource.ProviderID).Extract()
	case coreprovider.ResourceTypeNetwork:
		_, err = networks.Get(ctx, adapter.network, resource.ProviderID).Extract()
	default:
		return nil
	}
	if gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
		return nil
	}
	if err == nil {
		return errors.New("resource still exists")
	}
	return err
}
