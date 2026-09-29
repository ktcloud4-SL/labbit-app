package openstackprovider

import (
	"testing"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func TestQuotaRequiredForTwoVMOutboundTopology(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.VMs = append(snapshot.VMs, coreprovider.VMSpec{
		VMKey: "worker", Role: "WORKER", InstanceIndex: 1, ImageID: "image-ubuntu", FlavorID: "flavor-small",
		FlavorSpec: coreprovider.FlavorSpec{VCPUs: 2, RAMMiB: 4096, DiskGiB: 20},
	})
	required := quotaRequired(snapshot)
	if required.instances != 2 || required.cores != 3 || required.ramMiB != 6144 || required.networks != 1 || required.subnets != 1 || required.ports != 6 || required.routers != 1 || required.securityGroups != 2 || required.securityGroupRules != 6 {
		t.Fatalf("unexpected quota requirement: %+v", required)
	}
}

func TestQuotaCreditForResetUsesTrackedGeneration(t *testing.T) {
	resources := []coreprovider.ResourceRef{
		{ResourceType: coreprovider.ResourceTypeServer, ProviderID: "server", LogicalName: "workspace", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "network", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSubnet, ProviderID: "subnet", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeRouter, ProviderID: "router", Generation: 1},
		{ResourceType: coreprovider.ResourceTypePort, ProviderID: "management-port", Generation: 1},
		{ResourceType: coreprovider.ResourceTypePort, ProviderID: "lab-port", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityGroup, ProviderID: "lab-sg", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityGroup, ProviderID: "management-sg", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityRule, ProviderID: "lab-rule", Generation: 1},
		{ResourceType: coreprovider.ResourceTypeSecurityRule, ProviderID: "ssh-rule", Generation: 1},
	}
	credit := quotaCreditForReset(resources, validSnapshot())
	if credit.instances != 1 || credit.cores != 1 || credit.ramMiB != 2048 || credit.networks != 1 || credit.subnets != 1 || credit.ports != 4 || credit.routers != 1 || credit.securityGroups != 2 || credit.securityGroupRules != 6 {
		t.Fatalf("unexpected quota credit: %+v", credit)
	}
}

func TestQuotaAvailableAccountsForReservedAndResetCredit(t *testing.T) {
	if quotaAvailable(10, 8, 1, 2, 0) {
		t.Fatal("quota with only one free slot accepted a requirement of two")
	}
	if !quotaAvailable(10, 8, 1, 2, 1) {
		t.Fatal("reset credit was not applied")
	}
	if !quotaAvailable(-1, 100, 100, 100, 0) {
		t.Fatal("unlimited quota was rejected")
	}
}
