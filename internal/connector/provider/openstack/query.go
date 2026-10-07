package openstackprovider

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrImageList         = errors.New("OpenStack image list failed")
	ErrFlavorList        = errors.New("OpenStack flavor list failed")
	ErrServerList        = errors.New("OpenStack server list failed")
	ErrNetworkList       = errors.New("OpenStack network list failed")
	ErrSubnetList        = errors.New("OpenStack subnet list failed")
	ErrSecurityGroupList = errors.New("OpenStack security group list failed")
)

var _ coreprovider.QueryProvider = (*Adapter)(nil)

func (a *Adapter) ListImages(ctx context.Context) ([]coreprovider.Image, error) {
	if a == nil || a.image == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := images.List(a.image, images.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrImageList)
	}
	items, err := images.ExtractImages(pages)
	if err != nil {
		return nil, ErrImageList
	}

	result := make([]coreprovider.Image, 0, len(items))
	for _, item := range items {
		result = append(result, coreprovider.Image{
			ID:     item.ID,
			Name:   item.Name,
			Status: normalizeStatus(string(item.Status)),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func (a *Adapter) ListFlavors(ctx context.Context) ([]coreprovider.Flavor, error) {
	if a == nil || a.compute == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := flavors.ListDetail(a.compute, flavors.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrFlavorList)
	}
	items, err := flavors.ExtractFlavors(pages)
	if err != nil {
		return nil, ErrFlavorList
	}

	result := make([]coreprovider.Flavor, 0, len(items))
	for _, item := range items {
		result = append(result, coreprovider.Flavor{
			ID:      item.ID,
			Name:    item.Name,
			VCPUs:   int64(item.VCPUs),
			RAMMiB:  int64(item.RAM),
			DiskGiB: int64(item.Disk),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func (a *Adapter) ListServers(ctx context.Context) ([]coreprovider.Server, error) {
	if a == nil || a.compute == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := servers.List(a.compute, servers.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrServerList)
	}
	items, err := servers.ExtractServers(pages)
	if err != nil {
		return nil, ErrServerList
	}

	result := make([]coreprovider.Server, 0, len(items))
	for _, item := range items {
		result = append(result, coreprovider.Server{
			ID:     item.ID,
			Name:   item.Name,
			Status: normalizeStatus(item.Status),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func (a *Adapter) ListNetworks(ctx context.Context) ([]coreprovider.Network, error) {
	if a == nil || a.network == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := networks.List(a.network, networks.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrNetworkList)
	}
	items, err := networks.ExtractNetworks(pages)
	if err != nil {
		return nil, ErrNetworkList
	}

	result := make([]coreprovider.Network, 0, len(items))
	for _, item := range items {
		result = append(result, mapNetwork(item))
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func (a *Adapter) ListSubnets(ctx context.Context) ([]coreprovider.Subnet, error) {
	if a == nil || a.network == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := subnets.List(a.network, subnets.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrSubnetList)
	}
	items, err := subnets.ExtractSubnets(pages)
	if err != nil {
		return nil, ErrSubnetList
	}

	result := make([]coreprovider.Subnet, 0, len(items))
	for _, item := range items {
		result = append(result, mapSubnet(item))
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func (a *Adapter) ListSecurityGroups(ctx context.Context) ([]coreprovider.SecurityGroup, error) {
	if a == nil || a.network == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := groups.List(a.network, groups.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrSecurityGroupList)
	}
	items, err := groups.ExtractGroups(pages)
	if err != nil {
		return nil, ErrSecurityGroupList
	}

	result := make([]coreprovider.SecurityGroup, 0, len(items))
	for _, item := range items {
		result = append(result, mapSecurityGroup(item))
	}
	sort.Slice(result, func(i, j int) bool {
		return lessNameID(result[i].Name, result[i].ID, result[j].Name, result[j].ID)
	})
	return result, nil
}

func mapNetwork(item networks.Network) coreprovider.Network {
	subnetIDs := append([]string(nil), item.Subnets...)
	sort.Strings(subnetIDs)
	return coreprovider.Network{
		ID:        item.ID,
		Name:      item.Name,
		Status:    normalizeStatus(item.Status),
		Shared:    item.Shared,
		SubnetIDs: subnetIDs,
	}
}

func mapSubnet(item subnets.Subnet) coreprovider.Subnet {
	return coreprovider.Subnet{
		ID:         item.ID,
		Name:       item.Name,
		NetworkID:  item.NetworkID,
		CIDR:       item.CIDR,
		GatewayIP:  item.GatewayIP,
		EnableDHCP: item.EnableDHCP,
	}
}

func mapSecurityGroup(item groups.SecGroup) coreprovider.SecurityGroup {
	return coreprovider.SecurityGroup{
		ID:          item.ID,
		Name:        item.Name,
		Description: item.Description,
	}
}

func lessNameID(leftName, leftID, rightName, rightID string) bool {
	left := strings.ToLower(leftName)
	right := strings.ToLower(rightName)
	if left == right {
		return leftID < rightID
	}
	return left < right
}

func normalizeStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "UNKNOWN"
	}
	return strings.ToUpper(status)
}
