package openstackprovider

import (
	"context"
	"errors"
	"fmt"

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
func (a *Adapter) quotaCreditForExistingReset(ctx context.Context, resources []coreprovider.ResourceRef, snapshot coreprovider.CreationSnapshot) (quotaUsage, error) {
	credit := quotaUsage{}
	vmByKey := make(map[string]coreprovider.VMSpec, len(snapshot.VMs))
	for _, vm := range snapshot.VMs {
		vmByKey[vm.VMKey] = vm
	}
	creditedRules := make(map[string]struct{})
	for _, resource := range resources {
		_, exists, err := a.observeResource(ctx, resource)
		if err != nil {
			return quotaUsage{}, errors.Join(ErrQuotaLookup, err)
		}
		if !exists {
			continue
		}
		switch resource.ResourceType {
		case coreprovider.ResourceTypeServer:
			credit.instances++
			if vm, ok := vmByKey[resource.LogicalName]; ok {
				credit.cores += int(vm.FlavorSpec.VCPUs)
				credit.ramMiB += int(vm.FlavorSpec.RAMMiB)
			}
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
