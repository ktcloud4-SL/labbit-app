package openstackprovider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
)

// Read-only inventory after exact-ID teardown. No resource is deleted here.
func TestLBT147ReadOnlyResiduals(t *testing.T) {
	if os.Getenv("LABBIT_OPENSTACK_RESIDUAL_AUDIT") != "1" {
		t.Skip("opt-in read-only local cloud audit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatal("read-only authentication failed")
	}
	counts := map[string]int{}
	match := func(name string) bool {
		return strings.HasPrefix(name, "labbit-lbt147") || strings.HasPrefix(name, "labbit-m2-") || strings.HasPrefix(name, "labbit-m3-") || strings.HasPrefix(name, "labbit-internet-policy-") || strings.HasPrefix(name, "labbit-app-audit-") || strings.HasPrefix(name, "labbit-integration-management-") || strings.HasPrefix(name, "labbit-integration-key-")
	}
	images, err := a.ListImages(ctx)
	if err != nil {
		t.Fatal("image inventory failed")
	}
	counts["imagesTotal"] = len(images)
	flavors, err := a.ListFlavors(ctx)
	if err != nil {
		t.Fatal("flavor inventory failed")
	}
	counts["flavorsTotal"] = len(flavors)
	servers, err := a.ListServers(ctx)
	if err != nil {
		t.Fatal("server inventory failed")
	}
	counts["serversTotal"] = len(servers)
	for _, item := range servers {
		if match(item.Name) {
			counts["matchingServers"]++
		}
	}
	networks, err := a.ListNetworks(ctx)
	if err != nil {
		t.Fatal("network inventory failed")
	}
	counts["networksTotal"] = len(networks)
	for _, item := range networks {
		if match(item.Name) {
			counts["matchingNetworks"]++
		}
	}
	subnets, err := a.ListSubnets(ctx)
	if err != nil {
		t.Fatal("subnet inventory failed")
	}
	counts["subnetsTotal"] = len(subnets)
	for _, item := range subnets {
		if match(item.Name) {
			counts["matchingSubnets"]++
		}
	}
	groups, err := a.ListSecurityGroups(ctx)
	if err != nil {
		t.Fatal("security group inventory failed")
	}
	counts["securityGroupsTotal"] = len(groups)
	for _, item := range groups {
		if match(item.Name) {
			counts["matchingSecurityGroups"]++
		}
	}
	portPages, err := ports.List(a.network, ports.ListOpts{}).AllPages(ctx)
	if err != nil {
		t.Fatal("port inventory failed")
	}
	portItems, err := ports.ExtractPorts(portPages)
	if err != nil {
		t.Fatal("port inventory decode failed")
	}
	counts["portsTotal"] = len(portItems)
	for _, item := range portItems {
		if match(item.Name) {
			counts["matchingPorts"]++
		}
	}
	keyPages, err := keypairs.List(a.compute, nil).AllPages(ctx)
	if err != nil {
		t.Fatal("keypair inventory failed")
	}
	keyItems, err := keypairs.ExtractKeyPairs(keyPages)
	if err != nil {
		t.Fatal("keypair inventory decode failed")
	}
	counts["keypairsTotal"] = len(keyItems)
	for _, item := range keyItems {
		if match(item.Name) {
			counts["matchingKeypairs"]++
		}
	}
	routerPages, err := routers.List(a.network, routers.ListOpts{}).AllPages(ctx)
	if err != nil {
		t.Fatal("router inventory failed")
	}
	routerItems, err := routers.ExtractRouters(routerPages)
	if err != nil {
		t.Fatal("router inventory decode failed")
	}
	counts["routersTotal"] = len(routerItems)
	for _, item := range routerItems {
		if match(item.Name) {
			counts["matchingRouters"]++
		}
	}
	residual := 0
	for _, key := range []string{"matchingServers", "matchingNetworks", "matchingSubnets", "matchingSecurityGroups", "matchingPorts", "matchingKeypairs", "matchingRouters"} {
		residual += counts[key]
		if _, exists := counts[key]; !exists {
			counts[key] = 0
		}
	}
	counts["matchingTestResidualTotal"] = residual
	summary, _ := json.Marshal(counts)
	t.Logf("Final read-only inventory metadata: %s", summary)
	if residual != 0 || counts["serversTotal"] != 0 || counts["networksTotal"] != 4 || counts["imagesTotal"] != 3 || counts["flavorsTotal"] != 4 {
		t.Fatal("final inventory does not match pre-discovery baseline or retains matching test resources")
	}
}
