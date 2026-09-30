package openstackprovider

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrInvalidResourceSpec    = errors.New("OpenStack resource specification is invalid")
	ErrNetworkAmbiguous       = errors.New("OpenStack network name is ambiguous")
	ErrNetworkCreate          = errors.New("OpenStack network creation failed")
	ErrSubnetAmbiguous        = errors.New("OpenStack subnet name is ambiguous")
	ErrSubnetConflict         = errors.New("OpenStack subnet does not match the requested specification")
	ErrSubnetCreate           = errors.New("OpenStack subnet creation failed")
	ErrSecurityGroupAmbiguous = errors.New("OpenStack security group name is ambiguous")
	ErrSecurityGroupCreate    = errors.New("OpenStack security group creation failed")
	ErrSecurityRuleList       = errors.New("OpenStack security group rule list failed")
	ErrSecurityRuleCreate     = errors.New("OpenStack security group rule creation failed")
	ErrResourceOwnership      = errors.New("OpenStack resource is not owned by this operation")
)

type ResourceIdentity struct {
	Generation  int64
	LogicalName string
}

type NetworkSpec struct {
	Name        string
	Description string
}

type SubnetSpec struct {
	Name        string
	Description string
	NetworkID   string
	CIDR        string
	GatewayIP   *string
	EnableDHCP  bool
}

type SecurityGroupSpec struct {
	Name        string
	Description string
}

type SecurityRuleSpec struct {
	SecurityGroupID string
	Description     string
	Protocol        rules.RuleProtocol
	PortMin         int
	PortMax         int
	RemoteCIDR      string
}

// EnsureNetwork creates a network only when the deterministic name is unused.
// A same-name resource is a reconciliation candidate, not ownership evidence.
func (a *Adapter) EnsureNetwork(
	ctx context.Context,
	identity ResourceIdentity,
	spec NetworkSpec,
) (coreprovider.ResourceResult, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	if !validIdentity(identity) || name == "" {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}

	pages, err := networks.List(a.network, networks.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, safeContextError(ctx, ErrNetworkList)
	}
	items, err := networks.ExtractNetworks(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ErrNetworkList
	}
	exact := make([]networks.Network, 0, len(items))
	for _, item := range items {
		if item.Name == name {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		return coreprovider.ResourceResult{}, ErrResourceOwnership
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, ErrNetworkAmbiguous
	}

	adminStateUp := true
	shared := false
	created, err := networks.Create(ctx, a.network, networks.CreateOpts{
		Name:         name,
		Description:  strings.TrimSpace(spec.Description),
		AdminStateUp: &adminStateUp,
		Shared:       &shared,
	}).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, safeMutationError(ctx, err, ErrNetworkCreate)
	}
	return networkResource(identity, *created), nil
}

// EnsureSubnet creates a subnet only when the deterministic name is unused.
// Existing resources must be reconciled against Provider IDs held by Control.
func (a *Adapter) EnsureSubnet(
	ctx context.Context,
	identity ResourceIdentity,
	spec SubnetSpec,
) (coreprovider.ResourceResult, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	networkID := strings.TrimSpace(spec.NetworkID)
	cidr := strings.TrimSpace(spec.CIDR)
	prefix, err := netip.ParsePrefix(cidr)
	if !validIdentity(identity) || name == "" || networkID == "" || err != nil {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}
	gatewayIP := spec.GatewayIP
	if spec.GatewayIP != nil {
		gateway := strings.TrimSpace(*spec.GatewayIP)
		if gateway != "" {
			address, parseErr := netip.ParseAddr(gateway)
			if parseErr != nil || !prefix.Contains(address) {
				return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
			}
		}
		gatewayIP = &gateway
	}

	pages, err := subnets.List(a.network, subnets.ListOpts{
		Name:      name,
		NetworkID: networkID,
	}).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, safeContextError(ctx, ErrSubnetList)
	}
	items, err := subnets.ExtractSubnets(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ErrSubnetList
	}
	exact := make([]subnets.Subnet, 0, len(items))
	for _, item := range items {
		if item.Name == name && item.NetworkID == networkID {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		if !subnetMatches(exact[0], spec, cidr) {
			return coreprovider.ResourceResult{}, ErrSubnetConflict
		}
		return coreprovider.ResourceResult{}, ErrResourceOwnership
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, ErrSubnetAmbiguous
	}

	ipVersion := gophercloud.IPv6
	if prefix.Addr().Is4() {
		ipVersion = gophercloud.IPv4
	}
	created, err := subnets.Create(ctx, a.network, subnets.CreateOpts{
		Name:        name,
		Description: strings.TrimSpace(spec.Description),
		NetworkID:   networkID,
		CIDR:        cidr,
		GatewayIP:   gatewayIP,
		IPVersion:   ipVersion,
		EnableDHCP:  &spec.EnableDHCP,
	}).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, safeMutationError(ctx, err, ErrSubnetCreate)
	}
	return subnetResource(identity, *created), nil
}

// EnsureSecurityGroup creates a stateful group only when the deterministic
// name is unused. Ingress rules are managed separately.
func (a *Adapter) EnsureSecurityGroup(
	ctx context.Context,
	identity ResourceIdentity,
	spec SecurityGroupSpec,
) (coreprovider.ResourceResult, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	if !validIdentity(identity) || name == "" || name == "default" {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}

	pages, err := groups.List(a.network, groups.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, safeContextError(ctx, ErrSecurityGroupList)
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ErrSecurityGroupList
	}
	exact := make([]groups.SecGroup, 0, len(items))
	for _, item := range items {
		if item.Name == name {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		return coreprovider.ResourceResult{}, ErrResourceOwnership
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, ErrSecurityGroupAmbiguous
	}

	stateful := true
	created, err := groups.Create(ctx, a.network, groups.CreateOpts{
		Name:        name,
		Description: strings.TrimSpace(spec.Description),
		Stateful:    &stateful,
	}).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, safeMutationError(ctx, err, ErrSecurityGroupCreate)
	}
	return securityGroupResource(identity, *created), nil
}

// EnsureIngressRule returns the exact IPv4 ingress rule or creates it. A rule
// is independently tracked even though deleting its parent group also removes
// it, which preserves evidence when Provision stops part-way through.
func (a *Adapter) EnsureIngressRule(
	ctx context.Context,
	identity ResourceIdentity,
	spec SecurityRuleSpec,
) (coreprovider.ResourceResult, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	groupID := strings.TrimSpace(spec.SecurityGroupID)
	remoteCIDR := strings.TrimSpace(spec.RemoteCIDR)
	prefix, err := netip.ParsePrefix(remoteCIDR)
	if !validIdentity(identity) || groupID == "" || err != nil || !prefix.Addr().Is4() || spec.PortMin < 0 || spec.PortMax < spec.PortMin {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}
	if spec.Protocol == rules.ProtocolTCP && (spec.PortMin < 1 || spec.PortMax > 65535) {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}

	listOpts := rules.ListOpts{
		Direction:      string(rules.DirIngress),
		EtherType:      string(rules.EtherType4),
		SecGroupID:     groupID,
		Protocol:       string(spec.Protocol),
		PortRangeMin:   spec.PortMin,
		PortRangeMax:   spec.PortMax,
		RemoteIPPrefix: prefix.Masked().String(),
	}
	pages, err := rules.List(a.network, listOpts).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, safeContextError(ctx, ErrSecurityRuleList)
	}
	items, err := rules.ExtractRules(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ErrSecurityRuleList
	}
	exact := make([]rules.SecGroupRule, 0, len(items))
	for _, item := range items {
		if securityRuleMatches(item, listOpts) {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		return coreprovider.ResourceResult{}, ErrResourceOwnership
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, ErrSecurityRuleList
	}

	created, err := rules.Create(ctx, a.network, rules.CreateOpts{
		Direction:      rules.DirIngress,
		Description:    strings.TrimSpace(spec.Description),
		EtherType:      rules.EtherType4,
		SecGroupID:     groupID,
		PortRangeMin:   spec.PortMin,
		PortRangeMax:   spec.PortMax,
		Protocol:       spec.Protocol,
		RemoteIPPrefix: prefix.Masked().String(),
	}).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, safeMutationError(ctx, err, ErrSecurityRuleCreate)
	}
	return securityRuleResource(identity, *created), nil
}

func subnetMatches(item subnets.Subnet, spec SubnetSpec, cidr string) bool {
	if item.CIDR != cidr || item.EnableDHCP != spec.EnableDHCP {
		return false
	}
	if spec.GatewayIP == nil {
		return true
	}
	return item.GatewayIP == strings.TrimSpace(*spec.GatewayIP)
}

func validIdentity(identity ResourceIdentity) bool {
	return identity.Generation > 0 && strings.TrimSpace(identity.LogicalName) != ""
}

func networkResource(identity ResourceIdentity, item networks.Network) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeNetwork,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: normalizeStatus(item.Status),
	}
}

func subnetResource(identity ResourceIdentity, item subnets.Subnet) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeSubnet,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: "PRESENT",
	}
}

func securityGroupResource(identity ResourceIdentity, item groups.SecGroup) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeSecurityGroup,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: "PRESENT",
	}
}

func securityRuleMatches(item rules.SecGroupRule, options rules.ListOpts) bool {
	return item.Direction == options.Direction &&
		item.EtherType == options.EtherType &&
		item.SecGroupID == options.SecGroupID &&
		item.Protocol == options.Protocol &&
		item.PortRangeMin == options.PortRangeMin &&
		item.PortRangeMax == options.PortRangeMax &&
		item.RemoteIPPrefix == options.RemoteIPPrefix
}

func securityRuleResource(identity ResourceIdentity, item rules.SecGroupRule) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeSecurityRule,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: "PRESENT",
	}
}
