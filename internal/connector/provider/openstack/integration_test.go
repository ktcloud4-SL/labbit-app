package openstackprovider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/external"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
	"golang.org/x/crypto/ssh"

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
	projectID := integrationProjectID(t, adapter)
	externalNetworkID := integrationExternalNetworkID(t, ctx, adapter)
	sshAllowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSecurityGroupID := integrationManagementSecurityGroup(t, ctx, adapter, sshAllowedCIDR)
	adapter.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID:      "local-integration",
		ProjectID:                 projectID,
		ManagementNetworkID:       managementNetwork.ID,
		ManagementSecurityGroupID: managementSecurityGroupID,
		ExternalNetworkID:         externalNetworkID,
		KeyPairName:               environmentOrDefault("LABBIT_OPENSTACK_TEST_KEYPAIR", "openstack2"),
		SSHAllowedCIDR:            sshAllowedCIDR,
		LabSubnetCIDR:             environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		ActiveTimeout:             5 * time.Minute,
		SSHReadyTimeout:           3 * time.Minute,
		PollInterval:              2 * time.Second,
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
	if len(result.ProviderResources) != 8 {
		t.Fatalf("tracked resource count = %d, want 8", len(result.ProviderResources))
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
	projectID := integrationProjectID(t, adapter)
	externalNetworkID := integrationExternalNetworkID(t, ctx, adapter)
	privateKeyFile := adapter.provision.SSHPrivateKeyFile
	keyPairName := integrationKeyPairName(t, ctx, adapter, privateKeyFile)
	sshAllowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSecurityGroupID := integrationManagementSecurityGroup(t, ctx, adapter, sshAllowedCIDR)
	adapter.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID:      "local-integration",
		ProjectID:                 projectID,
		ManagementNetworkID:       managementNetwork.ID,
		ManagementSecurityGroupID: managementSecurityGroupID,
		ExternalNetworkID:         externalNetworkID,
		KeyPairName:               keyPairName,
		SSHAllowedCIDR:            sshAllowedCIDR,
		LabSubnetCIDR:             environmentOrDefault("LABBIT_OPENSTACK_M3_LAB_CIDR", "198.21.0.0/24"),
		SSHUsername:               adapter.provision.SSHUsername,
		SSHPrivateKeyFile:         privateKeyFile,
		SSHKnownHostsFile:         t.TempDir() + "/known_hosts",
		ActiveTimeout:             5 * time.Minute,
		SSHReadyTimeout:           3 * time.Minute,
		StartupReadyTimeout:       5 * time.Minute,
		PollInterval:              2 * time.Second,
	})

	runID := integrationRunID(t)
	labInstanceID := "m3-integration-" + runID
	startupContent := "#!/bin/sh\nset -eu\necho labbit-startup-ready\n"
	startupDigest := sha256.Sum256([]byte(startupContent))
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
		StartupScript: &coreprovider.StartupScript{
			Content: startupContent,
			SHA256:  hex.EncodeToString(startupDigest[:]),
		},
	}

	tracked := []coreprovider.ResourceResult{}
	t.Cleanup(func() { cleanupM2Resources(t, adapter, tracked) })
	provision, err := adapter.Provision(ctx, coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "m3-provision-" + runID, LabInstanceID: labInstanceID, Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || provision.Outcome != coreprovider.OutcomeSucceeded {
		t.Fatalf("M3 initial Provision = %+v, %v", provision, err)
	}
	tracked = append(tracked, provision.ProviderResources...)
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
	if len(newResources) != 8 {
		t.Fatalf("M3 Reset generation 2 resources = %d, want 8", len(newResources))
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

func TestOpenStackInternetPolicyIntegration(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_INTERNET_POLICY_TEST") != "1" {
		t.Skip("set LABBIT_OPENSTACK_INTERNET_POLICY_TEST=1 to verify real Internet ON/OFF reachability")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	adapter, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("OpenStack adapter initialization failed: %v", err)
	}
	imageItems, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatalf("image discovery failed: %v", err)
	}
	flavorItems, err := adapter.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("flavor discovery failed: %v", err)
	}
	networkItems, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("network discovery failed: %v", err)
	}
	image := exactImage(t, imageItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavorItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	managementNetwork := exactNetwork(t, networkItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	projectID := integrationProjectID(t, adapter)
	externalNetworkID := integrationExternalNetworkID(t, ctx, adapter)
	privateKeyFile := adapter.provision.SSHPrivateKeyFile
	keyPairName := integrationKeyPairName(t, ctx, adapter, privateKeyFile)
	sshAllowedCIDR := environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32")
	managementSecurityGroupID := integrationManagementSecurityGroup(t, ctx, adapter, sshAllowedCIDR)
	targetHost, targetPortText, err := net.SplitHostPort(environmentOrDefault("LABBIT_OPENSTACK_TEST_INTERNET_TARGET", "1.1.1.1:443"))
	if err != nil || net.ParseIP(targetHost) == nil {
		t.Fatalf("LABBIT_OPENSTACK_TEST_INTERNET_TARGET must be an IP:port pair")
	}
	targetPort, err := strconv.Atoi(targetPortText)
	if err != nil || targetPort < 1 || targetPort > 65535 {
		t.Fatalf("LABBIT_OPENSTACK_TEST_INTERNET_TARGET port is invalid")
	}
	command := fmt.Sprintf("python3 -c \"import socket; socket.create_connection(('%s',%d),5).close()\"", targetHost, targetPort)
	startupContent := "#!/bin/sh\nset -eu\necho labbit-internet-policy-ready\n"
	startupDigest := sha256.Sum256([]byte(startupContent))

	for _, test := range []struct {
		name             string
		internetOutbound bool
		labCIDR          string
	}{
		{name: "internet-on", internetOutbound: true, labCIDR: environmentOrDefault("LABBIT_OPENSTACK_INTERNET_ON_LAB_CIDR", "198.22.0.0/24")},
		{name: "internet-off", internetOutbound: false, labCIDR: environmentOrDefault("LABBIT_OPENSTACK_INTERNET_OFF_LAB_CIDR", "198.23.0.0/24")},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter.provision = normalizedProvisionConfig(ProvisionConfig{
				ProviderConnectionID:      "local-integration",
				ProjectID:                 projectID,
				ManagementNetworkID:       managementNetwork.ID,
				ManagementSecurityGroupID: managementSecurityGroupID,
				ExternalNetworkID:         externalNetworkID,
				KeyPairName:               keyPairName,
				SSHAllowedCIDR:            sshAllowedCIDR,
				LabSubnetCIDR:             test.labCIDR,
				SSHUsername:               adapter.provision.SSHUsername,
				SSHPrivateKeyFile:         privateKeyFile,
				SSHKnownHostsFile:         t.TempDir() + "/known_hosts",
				ActiveTimeout:             5 * time.Minute,
				SSHReadyTimeout:           3 * time.Minute,
				StartupReadyTimeout:       5 * time.Minute,
				PollInterval:              2 * time.Second,
			})
			runID := integrationRunID(t)
			snapshot := coreprovider.CreationSnapshot{
				ProviderConnectionID: "local-integration",
				VMs: []coreprovider.VMSpec{{
					VMKey: "workspace", Role: "WORKSPACE", ImageID: image.ID, FlavorID: flavor.ID,
					FlavorSpec: coreprovider.FlavorSpec{VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB},
				}},
				WorkspaceVMKey:   "workspace",
				InternetOutbound: test.internetOutbound,
				StartupScript: &coreprovider.StartupScript{
					Content: startupContent,
					SHA256:  hex.EncodeToString(startupDigest[:]),
				},
			}
			result, err := adapter.Provision(ctx, coreprovider.ProvisionRequest{
				Correlation:      coreprovider.Correlation{OperationID: "internet-policy-" + runID, LabInstanceID: "internet-policy-" + runID, Generation: 1},
				CreationSnapshot: snapshot,
			})
			t.Cleanup(func() { cleanupM2Resources(t, adapter, result.ProviderResources) })
			if err != nil || result.Outcome != coreprovider.OutcomeSucceeded {
				t.Fatalf("Provision = %+v, %v", result, err)
			}
			serverID := providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypeServer, "workspace")
			managementPortID := providerResourceID(t, result.ProviderResources, coreprovider.ResourceTypePort, "workspace:management")
			managementPort, err := ports.Get(ctx, adapter.network, managementPortID).Extract()
			if err != nil {
				t.Fatalf("get Management port: %v", err)
			}
			managementIP, err := managementAddress(*managementPort)
			if err != nil {
				t.Fatal(err)
			}
			hostIdentity, err := sshHostKeyIdentity(adapter.provision.ProviderConnectionID, serverID)
			if err != nil {
				t.Fatal(err)
			}
			err = adapter.runSSHCommand(ctx, net.JoinHostPort(managementIP, "22"), hostIdentity, command)
			if test.internetOutbound && err != nil {
				t.Fatalf("Internet ON connection failed: %v", err)
			}
			if !test.internetOutbound && err == nil {
				t.Fatal("Internet OFF unexpectedly reached the external target")
			}
			t.Logf("Internet policy verified: internetOutbound=%t expectedReachable=%t", test.internetOutbound, test.internetOutbound)
		})
	}
}

func providerResourceID(t *testing.T, resources []coreprovider.ResourceResult, resourceType, logicalName string) string {
	t.Helper()
	for _, resource := range resources {
		if resource.ResourceType == resourceType && resource.LogicalName == logicalName {
			return resource.ProviderID
		}
	}
	t.Fatalf("resource not found: type=%s logicalName=%s", resourceType, logicalName)
	return ""
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

func integrationManagementSecurityGroup(t *testing.T, ctx context.Context, adapter *Adapter, sshAllowedCIDR string) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("LABBIT_OPENSTACK_TEST_MANAGEMENT_SECURITY_GROUP_ID")); configured != "" {
		return configured
	}
	stateful := true
	group, err := groups.Create(ctx, adapter.network, groups.CreateOpts{
		Name:        "labbit-integration-management-" + integrationRunID(t),
		Description: "Temporary shared Management SG for Labbit integration tests",
		Stateful:    &stateful,
	}).Extract()
	if err != nil {
		t.Fatalf("create integration Management security group: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := groups.Delete(cleanupCtx, adapter.network, group.ID).ExtractErr(); err != nil && !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("temporary Management security group cleanup failed")
		}
	})
	for _, rule := range group.Rules {
		if err := rules.Delete(ctx, adapter.network, rule.ID).ExtractErr(); err != nil {
			t.Fatalf("remove default Management security group rule: %v", err)
		}
	}
	if _, err := rules.Create(ctx, adapter.network, rules.CreateOpts{
		Direction:      rules.DirIngress,
		EtherType:      rules.EtherType4,
		SecGroupID:     group.ID,
		Protocol:       rules.ProtocolTCP,
		PortRangeMin:   22,
		PortRangeMax:   22,
		RemoteIPPrefix: sshAllowedCIDR,
		Description:    "Allow Connector SSH only",
	}).Extract(); err != nil {
		t.Fatalf("create integration Management SSH rule: %v", err)
	}
	return group.ID
}

func integrationProjectID(t *testing.T, adapter *Adapter) string {
	t.Helper()
	if configured := strings.TrimSpace(adapter.provision.ProjectID); configured != "" {
		return configured
	}
	result, ok := adapter.provider.GetAuthResult().(tokens.CreateResult)
	if !ok {
		t.Fatal("authenticated OpenStack project ID is unavailable; set OS_PROJECT_ID")
	}
	project, err := result.ExtractProject()
	if err != nil || project == nil || strings.TrimSpace(project.ID) == "" {
		t.Fatal("authenticated OpenStack project ID is unavailable; set OS_PROJECT_ID")
	}
	return project.ID
}

func integrationExternalNetworkID(t *testing.T, ctx context.Context, adapter *Adapter) string {
	t.Helper()
	if configured := strings.TrimSpace(adapter.provision.ExternalNetworkID); configured != "" {
		return configured
	}
	pages, err := networks.List(adapter.network, networks.ListOpts{}).AllPages(ctx)
	if err != nil {
		t.Fatalf("external network discovery failed: %v", err)
	}
	var items []struct {
		networks.Network
		external.NetworkExternalExt
	}
	if err := networks.ExtractNetworksInto(pages, &items); err != nil {
		t.Fatalf("external network discovery failed: %v", err)
	}
	var matches []string
	for _, item := range items {
		if item.External && normalizeStatus(item.Status) == "ACTIVE" {
			matches = append(matches, item.ID)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("active external network count = %d; set LABBIT_OPENSTACK_EXTERNAL_NETWORK_ID", len(matches))
	}
	return matches[0]
}

func integrationKeyPairName(t *testing.T, ctx context.Context, adapter *Adapter, privateKeyFile string) string {
	t.Helper()
	if configured := strings.TrimSpace(os.Getenv("LABBIT_OPENSTACK_TEST_KEYPAIR")); configured != "" {
		return configured
	}
	privateKey, err := os.ReadFile(strings.TrimSpace(privateKeyFile))
	if err != nil {
		t.Fatal("integration SSH private key is unavailable; set LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE")
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		t.Fatal("integration SSH private key is invalid")
	}
	name := "labbit-integration-key-" + integrationRunID(t)
	_, err = keypairs.Create(ctx, adapter.compute, keypairs.CreateOpts{
		Name:      name,
		PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
	}).Extract()
	if err != nil {
		t.Fatalf("create integration keypair: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := keypairs.Delete(cleanupCtx, adapter.compute, name, nil).ExtractErr(); err != nil && !gophercloud.ResponseCodeIs(err, http.StatusNotFound) {
			t.Errorf("temporary integration keypair cleanup failed")
		}
	})
	return name
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
