package openstackprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/quotasets"

	networkquotas "github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/quotas"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrQuotaLookup   = errors.New("OpenStack quota lookup failed")
	ErrQuotaExceeded = errors.New("OpenStack resource quota is insufficient")
)

type quotaUsage struct {
	ktTiers            map[string]struct{}
	instances          int
	cores              int
	ramMiB             int
	networks           int
	subnets            int
	ports              int
	routers            int
	securityGroups     int
	securityGroupRules int
}

func (a *Adapter) preflightQuota(ctx context.Context, config ProvisionConfig, snapshot coreprovider.CreationSnapshot, credit quotaUsage) error {
	computeQuota, err := quotasets.GetDetail(ctx, a.compute, config.ProjectID).Extract()
	if err != nil {
		return safeContextError(ctx, ErrQuotaLookup)
	}
	networkQuota, err := networkquotas.GetDetail(ctx, a.network, config.ProjectID).Extract()
	if err != nil {
		return safeContextError(ctx, ErrQuotaLookup)
	}

	required := quotaRequired(snapshot)
	checks := []struct {
		name       string
		sufficient bool
	}{
		{name: "instances", sufficient: computeQuotaAvailable(computeQuota.Instances, required.instances, credit.instances)},
		{name: "cores", sufficient: computeQuotaAvailable(computeQuota.Cores, required.cores, credit.cores)},
		{name: "ram", sufficient: computeQuotaAvailable(computeQuota.RAM, required.ramMiB, credit.ramMiB)},
		{name: "network", sufficient: networkQuotaAvailable(networkQuota.Network, required.networks, credit.networks)},
		{name: "subnet", sufficient: networkQuotaAvailable(networkQuota.Subnet, required.subnets, credit.subnets)},
		{name: "port", sufficient: networkQuotaAvailable(networkQuota.Port, required.ports, credit.ports)},
		{name: "router", sufficient: networkQuotaAvailable(networkQuota.Router, required.routers, credit.routers)},
		{name: "security_group", sufficient: networkQuotaAvailable(networkQuota.SecurityGroup, required.securityGroups, credit.securityGroups)},
		{name: "security_group_rule", sufficient: networkQuotaAvailable(networkQuota.SecurityGroupRule, required.securityGroupRules, credit.securityGroupRules)},
	}
	for _, check := range checks {
		if !check.sufficient {
			return fmt.Errorf("%w: %s", ErrQuotaExceeded, check.name)
		}
	}
	return nil
}

func quotaRequired(snapshot coreprovider.CreationSnapshot) quotaUsage {
	required := quotaUsage{
		instances:          len(snapshot.VMs),
		networks:           1,
		subnets:            1,
		ports:              len(snapshot.VMs) * 2,
		securityGroups:     1,
		securityGroupRules: 3, // one Lab ingress plus Neutron's two default egress rules
	}
	for _, vm := range snapshot.VMs {
		required.cores += int(vm.FlavorSpec.VCPUs)
		required.ramMiB += int(vm.FlavorSpec.RAMMiB)
	}
	if snapshot.InternetOutbound {
		required.routers = 1
		// Neutron creates one internal interface port and one external gateway port.
		required.ports += 2
	}
	return required
}

// quotaCreditForExistingReset credits only resources that still exist by the
// exact Provider IDs persisted by Control. A stale ID must not make destructive
// Reset preflight appear to have more capacity than cleanup can actually free.
func (a *Adapter) quotaCreditForExistingReset(ctx context.Context, resources []coreprovider.ResourceRef) (quotaUsage, error) {
	credit := quotaUsage{}
	creditedRules := make(map[string]struct{})
	for _, resource := range resources {
		if resource.ResourceType == coreprovider.ResourceTypeServer {
			server, err := a.lookupServer(ctx, resource.ProviderID)
			if err != nil {
				if gophercloud.ResponseCodeIs(err, 404) {
					continue
				}
				return quotaUsage{}, safeContextError(ctx, ErrQuotaLookup)
			}
			if server == nil || server.ID != resource.ProviderID {
				return quotaUsage{}, ErrQuotaLookup
			}
			if a.ktNetwork != nil {
				cores, ram, err := ktCloudServerQuotaCredit(*server)
				if err != nil {
					return quotaUsage{}, err
				}
				credit.instances++
				credit.cores += cores
				credit.ramMiB += ram
				continue
			}
			flavorID, ok := server.Flavor["id"].(string)
			flavorID = strings.TrimSpace(flavorID)
			if !ok || flavorID == "" {
				return quotaUsage{}, ErrQuotaLookup
			}
			flavor, err := flavors.Get(ctx, a.compute, flavorID).Extract()
			if err != nil || flavor == nil {
				return quotaUsage{}, safeContextError(ctx, ErrQuotaLookup)
			}
			credit.instances++
			credit.cores += flavor.VCPUs
			credit.ramMiB += flavor.RAM
			continue
		}
		_, exists, err := a.observeResource(ctx, resource)
		if err != nil {
			return quotaUsage{}, errors.Join(ErrQuotaLookup, err)
		}
		if !exists {
			continue
		}
		if resource.ResourceType == coreprovider.ResourceTypeTier {
			if credit.ktTiers == nil {
				credit.ktTiers = make(map[string]struct{})
			}
			credit.ktTiers[resource.ProviderID] = struct{}{}
		}
		switch resource.ResourceType {
		case coreprovider.ResourceTypeNetwork:
			credit.networks++
		case coreprovider.ResourceTypeSubnet:
			credit.subnets++
		case coreprovider.ResourceTypePort:
			credit.ports++
		case coreprovider.ResourceTypeRouter:
			credit.routers++
			pages, err := ports.List(a.network, ports.ListOpts{DeviceID: resource.ProviderID}).AllPages(ctx)
			if err != nil {
				return quotaUsage{}, safeContextError(ctx, ErrQuotaLookup)
			}
			items, err := ports.ExtractPorts(pages)
			if err != nil {
				return quotaUsage{}, ErrQuotaLookup
			}
			// Provision accounts for one internal interface and one external
			// gateway port. Never credit more than that requirement.
			credit.ports += min(len(items), 2)
		case coreprovider.ResourceTypeSecurityGroup:
			credit.securityGroups++
			group, err := groups.Get(ctx, a.network, resource.ProviderID).Extract()
			if err != nil {
				return quotaUsage{}, safeContextError(ctx, ErrQuotaLookup)
			}
			for _, rule := range group.Rules {
				if _, alreadyCredited := creditedRules[rule.ID]; alreadyCredited {
					continue
				}
				creditedRules[rule.ID] = struct{}{}
				credit.securityGroupRules++
			}
		case coreprovider.ResourceTypeSecurityRule:
			if _, alreadyCredited := creditedRules[resource.ProviderID]; !alreadyCredited {
				creditedRules[resource.ProviderID] = struct{}{}
				credit.securityGroupRules++
			}
		}
	}
	return credit, nil
}

func computeQuotaAvailable(detail quotasets.QuotaDetail, required, credit int) bool {
	return quotaAvailable(detail.Limit, detail.InUse, detail.Reserved, required, credit)
}

func networkQuotaAvailable(detail networkquotas.QuotaDetail, required, credit int) bool {
	return quotaAvailable(detail.Limit, detail.Used, detail.Reserved, required, credit)
}

func quotaAvailable(limit, used, reserved, required, credit int) bool {
	if required == 0 || limit < 0 {
		return true
	}
	effectiveUsed := used + reserved - credit
	if effectiveUsed < 0 {
		effectiveUsed = 0
	}
	return effectiveUsed+required <= limit
}
