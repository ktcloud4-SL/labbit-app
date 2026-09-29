package openstackprovider

import (
	"context"
	"errors"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrRouterAmbiguous = errors.New("OpenStack router name is ambiguous")
	ErrRouterConflict  = errors.New("OpenStack router does not match the requested specification")
	ErrRouterCreate    = errors.New("OpenStack router creation failed")
	ErrRouterInterface = errors.New("OpenStack router interface operation failed")
)

type RouterSpec struct {
	Name              string
	Description       string
	ExternalNetworkID string
	SubnetID          string
}

// EnsureRouter creates or verifies the generation-specific outbound router,
// then idempotently attaches the Lab subnet. The router result is preserved
// when interface attachment is uncertain so Control can reconcile or clean it.
func (a *Adapter) EnsureRouter(ctx context.Context, identity ResourceIdentity, spec RouterSpec) (coreprovider.ResourceResult, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	externalNetworkID := strings.TrimSpace(spec.ExternalNetworkID)
	subnetID := strings.TrimSpace(spec.SubnetID)
	if !validIdentity(identity) || name == "" || externalNetworkID == "" || subnetID == "" {
		return coreprovider.ResourceResult{}, ErrInvalidResourceSpec
	}

	pages, err := routers.List(a.network, routers.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, safeContextError(ctx, ErrRouterCreate)
	}
	items, err := routers.ExtractRouters(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ErrRouterCreate
	}
	exact := make([]routers.Router, 0, len(items))
	for _, item := range items {
		if item.Name == name {
			exact = append(exact, item)
		}
	}

	var item routers.Router
	switch len(exact) {
	case 1:
		item = exact[0]
		if item.GatewayInfo.NetworkID != externalNetworkID {
			return routerResource(identity, item), ErrRouterConflict
		}
	case 0:
		adminStateUp := true
		enableSNAT := true
		created, err := routers.Create(ctx, a.network, routers.CreateOpts{
			Name:         name,
			Description:  strings.TrimSpace(spec.Description),
			AdminStateUp: &adminStateUp,
			GatewayInfo: &routers.GatewayInfo{
				NetworkID:  externalNetworkID,
				EnableSNAT: &enableSNAT,
			},
		}).Extract()
		if err != nil {
			return coreprovider.ResourceResult{}, safeMutationError(ctx, err, ErrRouterCreate)
		}
		item = *created
	default:
		return coreprovider.ResourceResult{}, ErrRouterAmbiguous
	}

	resource := routerResource(identity, item)
	attached, err := a.routerHasSubnet(ctx, item.ID, subnetID)
	if err != nil {
		return resource, err
	}
	if attached {
		return resource, nil
	}
	if _, err := routers.AddInterface(ctx, a.network, item.ID, routers.AddInterfaceOpts{SubnetID: subnetID}).Extract(); err != nil {
		return resource, safeMutationError(ctx, err, ErrRouterInterface)
	}
	return resource, nil
}

func (a *Adapter) routerHasSubnet(ctx context.Context, routerID, subnetID string) (bool, error) {
	pages, err := ports.List(a.network, ports.ListOpts{DeviceID: routerID}).AllPages(ctx)
	if err != nil {
		return false, safeContextError(ctx, ErrRouterInterface)
	}
	items, err := ports.ExtractPorts(pages)
	if err != nil {
		return false, ErrRouterInterface
	}
	for _, item := range items {
		for _, fixedIP := range item.FixedIPs {
			if fixedIP.SubnetID == subnetID {
				return true, nil
			}
		}
	}
	return false, nil
}

func routerResource(identity ResourceIdentity, item routers.Router) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeRouter,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: normalizeStatus(item.Status),
	}
}
