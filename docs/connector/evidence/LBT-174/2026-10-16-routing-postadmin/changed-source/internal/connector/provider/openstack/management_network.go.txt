package openstackprovider

import (
	"context"
	"encoding/json"
	"net/netip"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
)

// Management is an IPv4, directly connected SSH path. Suppress default routing on
// the owned port, never on the shared network or in immutable user-data. The Lab
// NIC supplies metadata routes and (only for Internet ON) the default route.
type managementSubnetInfo struct {
	ID   string
	CIDR string
}

func (a *Adapter) managementSubnet(ctx context.Context, config ProvisionConfig) (managementSubnetInfo, error) {
	var extension struct {
		Extension struct {
			Alias string `json:"alias"`
		} `json:"extension"`
	}
	if _, err := a.network.Get(ctx, a.network.ServiceURL("extensions", "extra_dhcp_opt"), &extension, nil); err != nil || extension.Extension.Alias != "extra_dhcp_opt" {
		return managementSubnetInfo{}, ErrProvisionCheck
	}
	network, err := networks.Get(ctx, a.network, config.ManagementNetworkID).Extract()
	if err != nil || network == nil || network.ID != config.ManagementNetworkID || normalizeStatus(network.Status) != "ACTIVE" || len(network.Subnets) != 1 {
		return managementSubnetInfo{}, ErrProvisionCheck
	}
	subnet, err := subnets.Get(ctx, a.network, network.Subnets[0]).Extract()
	if err != nil || subnet == nil || subnet.ID != network.Subnets[0] || subnet.NetworkID != network.ID || subnet.IPVersion != 4 || !subnet.EnableDHCP || len(subnet.HostRoutes) != 0 {
		return managementSubnetInfo{}, ErrProvisionCheck
	}
	prefix, pe := netip.ParsePrefix(subnet.CIDR)
	ssh, se := netip.ParsePrefix(config.SSHAllowedCIDR)
	lab, le := netip.ParsePrefix(config.LabSubnetCIDR)
	if pe != nil || se != nil || le != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || !ssh.Addr().Is4() || ssh != ssh.Masked() || ssh.Bits() < prefix.Bits() || !prefix.Contains(ssh.Addr()) || prefix.Overlaps(lab) {
		return managementSubnetInfo{}, ErrProvisionCheck
	}
	return managementSubnetInfo{ID: subnet.ID, CIDR: prefix.String()}, nil
}

type managementDHCPOption struct {
	Name    string  `json:"opt_name"`
	Value   *string `json:"opt_value"`
	Version int     `json:"ip_version"`
}

type managementPort struct {
	ports.Port
	DHCPOptions []managementDHCPOption `json:"extra_dhcp_opts"`
}

func (p *managementPort) UnmarshalJSON(data []byte) error {
	// Port has its own UnmarshalJSON; embedding alone would silently discard
	// extension fields. Keep its timestamp handling and decode the options too.
	if err := json.Unmarshal(data, &p.Port); err != nil {
		return err
	}
	var extension struct {
		Options []managementDHCPOption `json:"extra_dhcp_opts"`
	}
	if err := json.Unmarshal(data, &extension); err != nil {
		return err
	}
	p.DHCPOptions = extension.Options
	return nil
}

// The SDK's extra DHCP create builder rejects an empty required string. Neutron
// distinguishes an explicit empty router override from null (remove override).
// Preserve it on the wire; the nonempty classless route controls client routing.
type noManagementRoutes struct {
	ports.CreateOpts
	cidr string
}

func (o noManagementRoutes) ToPortCreateMap() (map[string]any, error) {
	route, valid := managementOnLinkRoute(o.cidr)
	if !valid {
		return nil, ErrInvalidResourceSpec
	}
	body, err := o.CreateOpts.ToPortCreateMap()
	if err != nil {
		return nil, err
	}
	empty := ""
	body["port"].(map[string]any)["extra_dhcp_opts"] = []managementDHCPOption{
		{Name: "router", Value: &empty, Version: 4},
		{Name: "classless-static-route", Value: &route, Version: 4},
	}
	return body, nil
}

// A valid, nonempty RFC 3442 option makes conforming clients ignore option 3.
// Empty 121 did not suppress the default route in the validated dnsmasq deployment.
func managementOnLinkRoute(cidr string) (string, bool) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.Bits() == 0 {
		return "", false
	}
	return prefix.String() + ",0.0.0.0", true
}

func managementRoutesSuppressed(options []managementDHCPOption, cidr string) bool {
	route, valid := managementOnLinkRoute(cidr)
	if !valid {
		return false
	}
	if len(options) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, option := range options {
		name := option.Name
		if (name != "router" && name != "classless-static-route") || seen[name] || option.Value == nil || option.Version != 4 {
			return false
		}
		if (name == "router" && *option.Value != "") || (name == "classless-static-route" && *option.Value != route) {
			return false
		}
		seen[name] = true
	}
	return true
}

func (a *Adapter) verifyManagementPort(ctx context.Context, id string, subnet managementSubnetInfo, config ProvisionConfig) (ports.Port, error) {
	var observed managementPort
	if err := ports.Get(ctx, a.network, id).ExtractInto(&observed); err != nil {
		return ports.Port{}, safeContextError(ctx, ErrPortList)
	}
	p := observed
	if p.ID != id || p.NetworkID != config.ManagementNetworkID || len(p.FixedIPs) != 1 || p.FixedIPs[0].SubnetID != subnet.ID || !portMatches(p.Port, subnet.ID, []string{config.ManagementSecurityGroupID}) || !managementRoutesSuppressed(p.DHCPOptions, subnet.CIDR) {
		return ports.Port{}, ErrPortConflict
	}
	prefix, err := netip.ParsePrefix(subnet.CIDR)
	address, ae := netip.ParseAddr(p.FixedIPs[0].IPAddress)
	if err != nil || ae != nil || !address.Is4() || !prefix.Contains(address) {
		return ports.Port{}, ErrPortConflict
	}
	return p.Port, nil
}
