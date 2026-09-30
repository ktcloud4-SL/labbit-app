package openstackprovider

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"golang.org/x/crypto/ssh"
)

func TestDispatchOperationProvisionCreatesDualNICServerAndTracksResources(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.sshProbe = func(_ context.Context, address string) error {
		if address != "172.16.8.200:22" {
			t.Fatalf("SSH probe address = %q", address)
		}
		fake.sshProbes++
		return nil
	}
	adapter.startupProbe = func(_ context.Context, address, hostIdentity string) error {
		if address != "172.16.8.200:22" {
			t.Fatalf("startup probe address = %q", address)
		}
		expectedIdentity, _ := sshHostKeyIdentity("provider-connection-1", "server-workspace")
		if hostIdentity != expectedIdentity {
			t.Fatalf("startup probe identity = %q, want %q", hostIdentity, expectedIdentity)
		}
		fake.startupProbes++
		return nil
	}

	snapshot := validSnapshot()
	result, err := coreprovider.DispatchOperation(context.Background(), adapter, coreprovider.OperationCommand{
		Correlation: coreprovider.Correlation{
			OperationID:   "operation-1",
			LabInstanceID: "lab-instance-1",
			Generation:    2,
		},
		MutationType:     coreprovider.MutationProvision,
		CreationSnapshot: &snapshot,
	})
	if err != nil {
		t.Fatalf("DispatchOperation() error = %v", err)
	}
	if result.Outcome != coreprovider.OutcomeSucceeded || result.Error != nil {
		t.Fatalf("unexpected result: %+v, safe error: %+v", result, result.Error)
	}
	if len(result.ProviderResources) != 8 {
		t.Fatalf("resource count = %d, resources = %+v", len(result.ProviderResources), result.ProviderResources)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeNetwork, 1)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeSubnet, 1)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeRouter, 1)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeSecurityGroup, 1)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeSecurityRule, 1)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypePort, 2)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 1)
	if fake.sshProbes != 1 || fake.startupProbes != 1 || fake.serverCreates != 1 {
		t.Fatalf("server creates = %d, SSH probes = %d, startup probes = %d", fake.serverCreates, fake.sshProbes, fake.startupProbes)
	}
	if len(fake.serverPortIDs) != 2 || fake.serverPortIDs[0] != "port-lab" || fake.serverPortIDs[1] != "port-management" {
		t.Fatalf("Nova port order = %#v", fake.serverPortIDs)
	}
	if fake.serverKeyPair != "openstack2" {
		t.Fatalf("Nova key pair = %q", fake.serverKeyPair)
	}
	if fake.userData != "#!/bin/sh\necho ready\n" {
		t.Fatalf("unexpected user data: %q", fake.userData)
	}
	if fake.subnetGatewaySet {
		t.Fatalf("Lab subnet gateway was explicitly set to %q, want Neutron default for outbound router", fake.subnetGateway)
	}
	if strings.Join(fake.labPortSecurityGroups, ",") != "security-group-lab" {
		t.Fatalf("Lab NIC security groups = %#v", fake.labPortSecurityGroups)
	}
	if strings.Join(fake.managementPortSecurityGroups, ",") != "security-group-management" {
		t.Fatalf("Management NIC security groups = %#v", fake.managementPortSecurityGroups)
	}
	if fake.labRuleSecurityGroup != "security-group-lab" {
		t.Fatalf("Lab ingress security group = %q", fake.labRuleSecurityGroup)
	}
}

func TestPreflightQuotaAllowsAvailableCapacity(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	if err := adapter.preflightQuota(context.Background(), testProvisionConfig(t), validSnapshot(), quotaUsage{}); err != nil {
		t.Fatalf("preflightQuota() error = %v", err)
	}
}

func TestProvisionRejectsNonExternalOutboundNetworkBeforeMutation(t *testing.T) {
	fake := &m2OpenStackFake{t: t, externalNetworkIsInternal: true}
	mutations := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
			mutations++
		}
		fake.ServeHTTP(response, request)
	}))
	adapter.provision = testProvisionConfig(t)

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "external-network", LabInstanceID: "external-network-lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorOpenStack || mutations != 0 {
		t.Fatalf("Provision() mutations=%d result=%+v safeError=%+v err=%v", mutations, result, result.Error, err)
	}
}

func TestProvisionRejectsInsufficientQuotaBeforeMutation(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	mutations := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
			mutations++
		}
		if request.URL.Path == "/compute/v2/os-quota-sets/project-1/detail" {
			writeComputeQuota(t, response, 0)
			return
		}
		fake.ServeHTTP(response, request)
	}))
	adapter.provision = testProvisionConfig(t)

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "quota", LabInstanceID: "quota-lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorQuotaExceeded || mutations != 0 {
		t.Fatalf("Provision() mutations=%d result=%+v safeError=%+v err=%v", mutations, result, result.Error, err)
	}
}

func TestProvisionSupportsMultipleVMs(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.sshProbe = func(context.Context, string) error { return nil }
	adapter.startupProbe = func(context.Context, string, string) error { return nil }
	snapshot := validSnapshot()
	snapshot.VMs = append(snapshot.VMs, coreprovider.VMSpec{
		VMKey: "worker", Role: "WORKER", InstanceIndex: 1, ImageID: "image-ubuntu", FlavorID: "flavor-small",
		FlavorSpec: coreprovider.FlavorSpec{VCPUs: 1, RAMMiB: 2048, DiskGiB: 20},
	})

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "multi", LabInstanceID: "multi-lab", Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded || len(result.ProviderResources) != 11 {
		t.Fatalf("Provision() = %+v safeError=%+v err=%v", result, result.Error, err)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypePort, 4)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 2)
	if fake.serverCreates != 2 {
		t.Fatalf("server creates = %d, want 2", fake.serverCreates)
	}
}

func TestProvisionWithoutInternetDoesNotCreateRouter(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.sshProbe = func(context.Context, string) error { return nil }
	adapter.startupProbe = func(context.Context, string, string) error { return nil }
	snapshot := validSnapshot()
	snapshot.InternetOutbound = false

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "offline", LabInstanceID: "offline-lab", Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || result.Outcome != coreprovider.OutcomeSucceeded || len(result.ProviderResources) != 7 {
		t.Fatalf("Provision() = %+v safeError=%+v err=%v", result, result.Error, err)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeRouter, 0)
	if !fake.subnetGatewaySet || fake.subnetGateway != "" {
		t.Fatalf("offline subnet gateway set=%t value=%q, want explicitly disabled", fake.subnetGatewaySet, fake.subnetGateway)
	}
}

func TestProvisionWithoutInternetRejectsManagementSecurityGroupEgressBeforeMutation(t *testing.T) {
	fake := &m2OpenStackFake{t: t, managementSecurityGroupEgress: true}
	mutations := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
			mutations++
		}
		fake.ServeHTTP(response, request)
	}))
	adapter.provision = testProvisionConfig(t)
	snapshot := validSnapshot()
	snapshot.InternetOutbound = false

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "management-egress", LabInstanceID: "offline-lab", Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || mutations != 0 || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorOpenStack {
		t.Fatalf("Provision() mutations=%d result=%+v safeError=%+v err=%v", mutations, result, result.Error, err)
	}
}

func TestProvisionServerCreateFailureIsUnknownWithPartialResources(t *testing.T) {
	fake := &m2OpenStackFake{t: t, failServerStatus: http.StatusGatewayTimeout}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation: coreprovider.Correlation{
			OperationID:   "operation-2",
			LabInstanceID: "lab-instance-2",
			Generation:    1,
		},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Outcome != coreprovider.OutcomeUnknown || result.Error == nil || result.Error.Code != errorUnknown {
		t.Fatalf("unexpected result: %+v", result)
	}
	if strings.Contains(strings.ToLower(result.Error.Message), "raw-provider-secret") {
		t.Fatalf("Provider payload leaked through SafeError: %+v", result.Error)
	}
	if len(result.ProviderResources) != 7 {
		t.Fatalf("partial resource count = %d, resources = %+v", len(result.ProviderResources), result.ProviderResources)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypePort, 2)
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 0)
}

func TestProvisionRejectedServerCreateIsFailedWithPartialResources(t *testing.T) {
	fake := &m2OpenStackFake{t: t, failServerStatus: http.StatusBadRequest}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "operation", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorOpenStack || len(result.ProviderResources) != 7 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestProvisionRejectsInvalidSnapshotBeforeOpenStackCalls(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(response, "unexpected", http.StatusInternalServerError)
	}))
	adapter.provision = testProvisionConfig(t)
	snapshot := validSnapshot()
	snapshot.StartupScript.SHA256 = strings.Repeat("0", 64)

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "operation", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if calls != 0 || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorInvalidProvision {
		t.Fatalf("calls = %d, result = %+v", calls, result)
	}
}

func TestProvisionRejectsCollidingDerivedVMNamesBeforeOpenStackCalls(t *testing.T) {
	for _, vmKeys := range [][]string{
		{"web/1", "web-1"},
		{"worker-identifier-that-is-long-a", "worker-identifier-that-is-long-b"},
	} {
		t.Run(strings.Join(vmKeys, "_"), func(t *testing.T) {
			calls := 0
			adapter := newTestAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			adapter.provision = testProvisionConfig(t)
			snapshot := validSnapshot()
			snapshot.VMs = nil
			for index, vmKey := range vmKeys {
				snapshot.VMs = append(snapshot.VMs, coreprovider.VMSpec{
					VMKey: vmKey, Role: "WORKER", InstanceIndex: int64(index), ImageID: "image-ubuntu", FlavorID: "flavor-small",
					FlavorSpec: coreprovider.FlavorSpec{VCPUs: 1, RAMMiB: 2048, DiskGiB: 20},
				})
			}
			snapshot.WorkspaceVMKey = vmKeys[0]
			result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
				Correlation:      coreprovider.Correlation{OperationID: "name-collision", LabInstanceID: "lab", Generation: 1},
				CreationSnapshot: snapshot,
			})
			if err != nil || calls != 0 || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorInvalidProvision {
				t.Fatalf("Provision() calls=%d result=%+v err=%v", calls, result, err)
			}
		})
	}
}

func TestProvisionRejectsUnreadableSSHPrivateKeyBeforeMutation(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	mutations := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
			mutations++
		}
		fake.ServeHTTP(response, request)
	}))
	adapter.provision = testProvisionConfig(t)
	adapter.provision.SSHPrivateKeyFile = filepath.Join(t.TempDir(), "missing-private-key")

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "ssh-preflight", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || mutations != 0 || result.Outcome != coreprovider.OutcomeFailed {
		t.Fatalf("Provision() mutations=%d result=%+v err=%v", mutations, result, err)
	}
}

func TestProvisionRejectsMismatchedSSHPrivateKeyBeforeMutation(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	mutations := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost || request.Method == http.MethodPut || request.Method == http.MethodDelete {
			mutations++
		}
		fake.ServeHTTP(response, request)
	}))
	adapter.provision = testProvisionConfig(t)
	adapter.provision.SSHPrivateKeyFile = writeTestPrivateKey(t, "different-key")

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "ssh-key-mismatch", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || mutations != 0 || result.Outcome != coreprovider.OutcomeFailed {
		t.Fatalf("Provision() mutations=%d result=%+v err=%v", mutations, result, err)
	}
}

func TestProvisionRejectsProviderConnectionMismatchBeforeOpenStackCalls(t *testing.T) {
	calls := 0
	adapter := newTestAdapter(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	adapter.provision = testProvisionConfig(t)
	snapshot := validSnapshot()
	snapshot.ProviderConnectionID = "different-provider-connection"

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "affinity", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: snapshot,
	})
	if err != nil || calls != 0 || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorInvalidProvision {
		t.Fatalf("Provision() calls=%d result=%+v safeError=%+v err=%v", calls, result, result.Error, err)
	}
}

func TestProvisionSSHTimeoutIsFailedAndPreservesActiveServer(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.provision.SSHReadyTimeout = 5 * time.Millisecond
	adapter.sshProbe = func(context.Context, string) error { return errors.New("raw ssh failure") }

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "operation", LabInstanceID: "lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorBootTimeout {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, resource := range result.ProviderResources {
		if resource.ResourceType == coreprovider.ResourceTypeServer && resource.ObservedState != "ACTIVE" {
			t.Fatalf("server state = %q", resource.ObservedState)
		}
	}
}

func TestProvisionWaitsForStartupCompletionBeforeSuccess(t *testing.T) {
	fake := &m2OpenStackFake{t: t}
	adapter := newTestAdapter(t, fake)
	adapter.provision = testProvisionConfig(t)
	adapter.provision.StartupReadyTimeout = 5 * time.Millisecond
	adapter.sshProbe = func(context.Context, string) error { return nil }
	adapter.startupProbe = func(context.Context, string, string) error { return ErrStartupNotReady }

	result, err := adapter.Provision(context.Background(), coreprovider.ProvisionRequest{
		Correlation:      coreprovider.Correlation{OperationID: "startup", LabInstanceID: "startup-lab", Generation: 1},
		CreationSnapshot: validSnapshot(),
	})
	if err != nil || result.Outcome != coreprovider.OutcomeFailed || result.Error == nil || result.Error.Code != errorBootTimeout {
		t.Fatalf("Provision() = %+v safeError=%+v err=%v", result, result.Error, err)
	}
	assertResourceCount(t, result.ProviderResources, coreprovider.ResourceTypeServer, 1)
}

func TestProvisionNameIsStableAndBounded(t *testing.T) {
	first := provisionBaseName("Lab/Instance With A Very Long Identifier That Must Not Leak Into Resource Limits", 7)
	second := provisionBaseName("Lab/Instance With A Very Long Identifier That Must Not Leak Into Resource Limits", 7)
	otherGeneration := provisionBaseName("Lab/Instance With A Very Long Identifier That Must Not Leak Into Resource Limits", 8)
	if first != second || first == otherGeneration || len(first) > 63 {
		t.Fatalf("names: first=%q second=%q other=%q", first, second, otherGeneration)
	}
}

func validSnapshot() coreprovider.CreationSnapshot {
	content := "#!/bin/sh\necho ready\n"
	digest := sha256.Sum256([]byte(content))
	return coreprovider.CreationSnapshot{
		ProviderConnectionID: "provider-connection-1",
		VMs: []coreprovider.VMSpec{{
			VMKey:         "workspace",
			Role:          "WORKSPACE",
			InstanceIndex: 0,
			ImageID:       "image-ubuntu",
			FlavorID:      "flavor-small",
			FlavorSpec: coreprovider.FlavorSpec{
				VCPUs:   1,
				RAMMiB:  2048,
				DiskGiB: 20,
			},
		}},
		WorkspaceVMKey:   "workspace",
		InternetOutbound: true,
		StartupScript: &coreprovider.StartupScript{
			Content: content,
			SHA256:  hex.EncodeToString(digest[:]),
		},
	}
}

func testProvisionConfig(t *testing.T) ProvisionConfig {
	t.Helper()
	directory := t.TempDir()
	privateKeyFile := writeTestPrivateKey(t, "default-key")
	return normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID:      "provider-connection-1",
		ProjectID:                 "project-1",
		ManagementNetworkID:       "management-network",
		ManagementSecurityGroupID: "security-group-management",
		ExternalNetworkID:         "external-network",
		KeyPairName:               "openstack2",
		SSHAllowedCIDR:            "172.16.8.1/32",
		LabSubnetCIDR:             "198.19.0.0/24",
		SSHUsername:               "ubuntu",
		SSHPrivateKeyFile:         privateKeyFile,
		SSHKnownHostsFile:         filepath.Join(directory, "known_hosts"),
		ActiveTimeout:             time.Second,
		SSHReadyTimeout:           time.Second,
		StartupReadyTimeout:       time.Second,
		PollInterval:              time.Millisecond,
	})
}

func writeTestPrivateKey(t *testing.T, label string) string {
	t.Helper()
	privateKeyFile := filepath.Join(t.TempDir(), "id_ed25519")
	privateKey := testPrivateKey(label)
	block, err := ssh.MarshalPrivateKey(privateKey, "")
	if err != nil {
		t.Fatalf("marshal test SSH private key: %v", err)
	}
	if err := os.WriteFile(privateKeyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write test SSH private key: %v", err)
	}
	return privateKeyFile
}

func testAuthorizedPublicKey(t *testing.T) string {
	t.Helper()
	privateKey := testPrivateKey("default-key")
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		t.Fatalf("build test SSH public key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
}

func testPrivateKey(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("labbit-openstack-provider-test-key:" + label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func assertResourceCount(t *testing.T, resources []coreprovider.ResourceResult, resourceType string, expected int) {
	t.Helper()
	count := 0
	for _, resource := range resources {
		if resource.ResourceType == resourceType {
			count++
			if resource.Generation < 1 || resource.ProviderID == "" || resource.LogicalName == "" {
				t.Fatalf("invalid tracked resource: %+v", resource)
			}
		}
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", resourceType, count, expected)
	}
}

type m2OpenStackFake struct {
	t                             *testing.T
	failServerStatus              int
	serverCreates                 int
	sshProbes                     int
	startupProbes                 int
	serverPortIDs                 []string
	serverKeyPair                 string
	userData                      string
	subnetGateway                 string
	subnetGatewaySet              bool
	labPortSecurityGroups         []string
	managementPortSecurityGroups  []string
	labRuleSecurityGroup          string
	externalNetworkIsInternal     bool
	managementSecurityGroupEgress bool
}

func (f *m2OpenStackFake) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	f.t.Helper()
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/networks/management-network":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"network": map[string]any{"id": "management-network", "name": "management", "status": "ACTIVE", "subnets": []string{"management-subnet"}}})
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/networks/external-network":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"network": map[string]any{"id": "external-network", "name": "public", "status": "ACTIVE", "subnets": []string{"external-subnet"}, "router:external": !f.externalNetworkIsInternal}})
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/security-groups/security-group-management":
		securityGroupRules := []any{map[string]any{
			"id": "management-ssh", "direction": "ingress", "ethertype": "IPv4", "protocol": "tcp",
			"port_range_min": 22, "port_range_max": 22, "remote_ip_prefix": "172.16.8.1/32",
			"security_group_id": "security-group-management",
		}}
		if f.managementSecurityGroupEgress {
			securityGroupRules = append(securityGroupRules, map[string]any{
				"id": "management-egress", "direction": "egress", "ethertype": "IPv4", "protocol": "",
				"security_group_id": "security-group-management",
			})
		}
		writeJSON(f.t, response, http.StatusOK, map[string]any{"security_group": map[string]any{
			"id": "security-group-management", "name": "labbit-management", "stateful": true,
			"security_group_rules": securityGroupRules,
		}})
	case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/os-quota-sets/project-1/detail":
		writeComputeQuota(f.t, response, 100000)
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/quotas/project-1/details.json":
		writeNetworkQuota(f.t, response, 100000)
	case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/os-keypairs/openstack2":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"keypair": map[string]any{"name": "openstack2", "public_key": testAuthorizedPublicKey(f.t), "fingerprint": "test"}})
	case request.Method == http.MethodGet && request.URL.Path == "/image/v2/images/image-ubuntu":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"id": "image-ubuntu", "name": "ubuntu", "status": "active"})
	case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/flavors/flavor-small":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"flavor": map[string]any{"id": "flavor-small", "name": "m1.small", "vcpus": 1, "ram": 2048, "disk": 20}})
	case request.URL.Path == "/network/v2.0/networks":
		f.handleNetwork(response, request)
	case request.URL.Path == "/network/v2.0/subnets":
		f.handleSubnet(response, request)
	case request.URL.Path == "/network/v2.0/security-groups":
		f.handleSecurityGroup(response, request)
	case request.URL.Path == "/network/v2.0/security-group-rules":
		f.handleSecurityRule(response, request)
	case request.URL.Path == "/network/v2.0/routers":
		f.handleRouter(response, request)
	case request.Method == http.MethodPut && request.URL.Path == "/network/v2.0/routers/router-lab/add_router_interface":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"id": "router-lab", "port_id": "router-interface-port", "subnet_id": "subnet-lab"})
	case request.URL.Path == "/network/v2.0/ports":
		f.handlePort(response, request)
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports/port-lab":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"port": map[string]any{
			"id": "port-lab", "name": "lab", "network_id": "network-lab", "status": "ACTIVE",
			"fixed_ips": []map[string]string{{"subnet_id": "subnet-lab", "ip_address": "198.19.0.10"}}, "security_groups": []string{"security-group-lab"},
		}})
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports/port-management":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"port": map[string]any{
			"id": "port-management", "name": "management", "network_id": "management-network", "status": "ACTIVE",
			"fixed_ips": []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.8.200"}}, "security_groups": []string{"security-group-management"},
		}})
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports/port-worker-lab":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"port": map[string]any{
			"id": "port-worker-lab", "name": "worker-lab", "network_id": "network-lab", "status": "ACTIVE",
			"fixed_ips": []map[string]string{{"subnet_id": "subnet-lab", "ip_address": "198.19.0.11"}}, "security_groups": []string{"security-group-lab"},
		}})
	case request.Method == http.MethodGet && request.URL.Path == "/network/v2.0/ports/port-worker-management":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"port": map[string]any{
			"id": "port-worker-management", "name": "worker-management", "network_id": "management-network", "status": "ACTIVE",
			"fixed_ips": []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.8.201"}}, "security_groups": []string{"security-group-management"},
		}})
	case request.URL.Path == "/compute/v2/servers/detail":
		if request.Method != http.MethodGet {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(f.t, response, http.StatusOK, map[string]any{"servers": []any{}, "servers_links": []any{}})
	case request.URL.Path == "/compute/v2/servers":
		f.handleServerCollection(response, request)
	case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/servers/server-workspace":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"server": map[string]any{
			"id": "server-workspace", "name": "workspace", "status": "ACTIVE", "key_name": "openstack2",
			"metadata": map[string]string{"labbit_lab_instance_id": "lab-instance-1", "labbit_generation": "2", "labbit_vm_key": "workspace"},
		}})
	case request.Method == http.MethodGet && request.URL.Path == "/compute/v2/servers/server-worker":
		writeJSON(f.t, response, http.StatusOK, map[string]any{"server": map[string]any{
			"id": "server-worker", "name": "worker", "status": "ACTIVE", "key_name": "openstack2",
			"metadata": map[string]string{"labbit_vm_key": "worker"},
		}})
	default:
		f.t.Fatalf("unexpected OpenStack request: %s %s", request.Method, request.URL.String())
	}
}

func (f *m2OpenStackFake) handleNetwork(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"networks": []any{}, "networks_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		created := body["network"]
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"network": map[string]any{"id": "network-lab", "name": created["name"], "status": "ACTIVE", "shared": false, "subnets": []any{}}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *m2OpenStackFake) handleSubnet(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"subnets": []any{}, "subnets_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		created := body["subnet"]
		gateway, exists := created["gateway_ip"]
		f.subnetGatewaySet = exists
		f.subnetGateway, _ = gateway.(string)
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"subnet": map[string]any{
			"id": "subnet-lab", "name": created["name"], "network_id": "network-lab", "cidr": "198.19.0.0/24", "gateway_ip": f.subnetGateway, "enable_dhcp": true,
		}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *m2OpenStackFake) handleRouter(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"routers": []any{}, "routers_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		created := body["router"]
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"router": map[string]any{
			"id": "router-lab", "name": created["name"], "status": "ACTIVE", "admin_state_up": true,
			"external_gateway_info": created["external_gateway_info"],
		}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeComputeQuota(t *testing.T, response http.ResponseWriter, limit int) {
	detail := func() map[string]int { return map[string]int{"in_use": 0, "reserved": 0, "limit": limit} }
	writeJSON(t, response, http.StatusOK, map[string]any{"quota_set": map[string]any{
		"id": "project-1", "instances": detail(), "cores": detail(), "ram": detail(),
	}})
}

func writeNetworkQuota(t *testing.T, response http.ResponseWriter, limit int) {
	detail := func() map[string]int { return map[string]int{"used": 0, "reserved": 0, "limit": limit} }
	writeJSON(t, response, http.StatusOK, map[string]any{"quota": map[string]any{
		"network": detail(), "subnet": detail(), "port": detail(), "router": detail(),
		"security_group": detail(), "security_group_rule": detail(),
	}})
}

func (f *m2OpenStackFake) handleSecurityGroup(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"security_groups": []any{}, "security_groups_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		name, _ := body["security_group"]["name"].(string)
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"security_group": map[string]any{
			"id": "security-group-lab", "name": name, "stateful": true, "security_group_rules": []any{},
		}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *m2OpenStackFake) handleSecurityRule(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"security_group_rules": []any{}, "security_group_rules_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		created := body["security_group_rule"]
		f.labRuleSecurityGroup, _ = created["security_group_id"].(string)
		created["id"] = "rule-lab"
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"security_group_rule": created})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *m2OpenStackFake) handlePort(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"ports": []any{}, "ports_links": []any{}})
	case http.MethodPost:
		var body map[string]map[string]any
		decodeJSON(f.t, request, &body)
		created := body["port"]
		name, _ := created["name"].(string)
		management := strings.HasSuffix(name, "-management")
		worker := strings.Contains(name, "-worker-")
		securityGroupIDs := stringSlice(created["security_groups"])
		id := "port-lab"
		networkID := "network-lab"
		fixedIPs := []map[string]string{{"subnet_id": "subnet-lab", "ip_address": "198.19.0.10"}}
		if management {
			id = "port-management"
			networkID = "management-network"
			fixedIPs = []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.8.200"}}
			f.managementPortSecurityGroups = append([]string(nil), securityGroupIDs...)
		} else {
			f.labPortSecurityGroups = append([]string(nil), securityGroupIDs...)
		}
		if worker {
			if management {
				id = "port-worker-management"
				fixedIPs = []map[string]string{{"subnet_id": "management-subnet", "ip_address": "172.16.8.201"}}
			} else {
				id = "port-worker-lab"
				fixedIPs = []map[string]string{{"subnet_id": "subnet-lab", "ip_address": "198.19.0.11"}}
			}
		}
		writeJSON(f.t, response, http.StatusCreated, map[string]any{"port": map[string]any{
			"id": id, "name": name, "network_id": networkID, "status": "DOWN", "fixed_ips": fixedIPs, "security_groups": securityGroupIDs,
		}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func (f *m2OpenStackFake) handleServerCollection(response http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeJSON(f.t, response, http.StatusOK, map[string]any{"servers": []any{}, "servers_links": []any{}})
	case http.MethodPost:
		f.serverCreates++
		if f.failServerStatus != 0 {
			http.Error(response, "raw-provider-secret", f.failServerStatus)
			return
		}
		var body struct {
			Server struct {
				Name     string `json:"name"`
				KeyName  string `json:"key_name"`
				UserData string `json:"user_data"`
				Networks []struct {
					Port string `json:"port"`
				} `json:"networks"`
			} `json:"server"`
		}
		decodeJSON(f.t, request, &body)
		f.serverKeyPair = body.Server.KeyName
		decoded, err := base64.StdEncoding.DecodeString(body.Server.UserData)
		if err != nil {
			f.t.Fatalf("decode user data: %v", err)
		}
		f.userData = string(decoded)
		for _, network := range body.Server.Networks {
			f.serverPortIDs = append(f.serverPortIDs, network.Port)
		}
		serverID := "server-workspace"
		if strings.HasSuffix(body.Server.Name, "-worker") {
			serverID = "server-worker"
		}
		writeJSON(f.t, response, http.StatusAccepted, map[string]any{"server": map[string]any{"id": serverID, "name": body.Server.Name, "status": "BUILD"}})
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func decodeJSON(t *testing.T, request *http.Request, value any) {
	t.Helper()
	if err := json.NewDecoder(request.Body).Decode(value); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}
