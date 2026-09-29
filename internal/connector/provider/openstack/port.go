package openstackprovider

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrPortList      = errors.New("OpenStack port list failed")
	ErrPortAmbiguous = errors.New("OpenStack port name is ambiguous")
	ErrPortConflict  = errors.New("OpenStack port does not match the requested specification")
	ErrPortCreate    = errors.New("OpenStack port creation failed")
)

type PortSpec struct {
	Name             string
	Description      string
	NetworkID        string
	SubnetID         string
	SecurityGroupIDs []string
}

// EnsurePort returns the exact network/name/specification port or creates it.
// Ports are created before Nova so every NIC has a durable ProviderResource ID
// even when VM creation or readiness later fails.
func (a *Adapter) EnsurePort(
	ctx context.Context,
	identity ResourceIdentity,
	spec PortSpec,
) (coreprovider.ResourceResult, ports.Port, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ports.Port{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	networkID := strings.TrimSpace(spec.NetworkID)
	subnetID := strings.TrimSpace(spec.SubnetID)
	securityGroupIDs := normalizedIDs(spec.SecurityGroupIDs)
	if !validIdentity(identity) || name == "" || networkID == "" || len(securityGroupIDs) == 0 {
		return coreprovider.ResourceResult{}, ports.Port{}, ErrInvalidResourceSpec
	}

	pages, err := ports.List(a.network, ports.ListOpts{Name: name, NetworkID: networkID}).AllPages(ctx)
	if err != nil {
		return coreprovider.ResourceResult{}, ports.Port{}, safeContextError(ctx, ErrPortList)
	}
	items, err := ports.ExtractPorts(pages)
	if err != nil {
		return coreprovider.ResourceResult{}, ports.Port{}, ErrPortList
	}
	exact := make([]ports.Port, 0, len(items))
	for _, item := range items {
		if item.Name == name && item.NetworkID == networkID {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		if !portMatches(exact[0], subnetID, securityGroupIDs) {
			return coreprovider.ResourceResult{}, ports.Port{}, ErrPortConflict
		}
		return portResource(identity, exact[0]), exact[0], nil
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, ports.Port{}, ErrPortAmbiguous
	}

	adminStateUp := true
	createOpts := ports.CreateOpts{
		NetworkID:      networkID,
		Name:           name,
		Description:    strings.TrimSpace(spec.Description),
		AdminStateUp:   &adminStateUp,
		SecurityGroups: &securityGroupIDs,
	}
	if subnetID != "" {
		createOpts.FixedIPs = []ports.IP{{SubnetID: subnetID}}
	}
	created, err := ports.Create(ctx, a.network, createOpts).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, ports.Port{}, safeMutationError(ctx, err, ErrPortCreate)
	}
	return portResource(identity, *created), *created, nil
}

func (a *Adapter) refreshPort(ctx context.Context, identity ResourceIdentity, portID string) (coreprovider.ResourceResult, ports.Port, error) {
	if a == nil || a.network == nil {
		return coreprovider.ResourceResult{}, ports.Port{}, ErrClientUnavailable
	}
	item, err := ports.Get(ctx, a.network, portID).Extract()
	if err != nil {
		return coreprovider.ResourceResult{}, ports.Port{}, safeContextError(ctx, ErrPortList)
	}
	return portResource(identity, *item), *item, nil
}

func normalizedIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func portMatches(item ports.Port, subnetID string, securityGroupIDs []string) bool {
	actualGroups := normalizedIDs(item.SecurityGroups)
	if !slices.Equal(actualGroups, securityGroupIDs) {
		return false
	}
	if subnetID == "" {
		return true
	}
	for _, fixedIP := range item.FixedIPs {
		if fixedIP.SubnetID == subnetID {
			return true
		}
	}
	return false
}

func portResource(identity ResourceIdentity, item ports.Port) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypePort,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: normalizeStatus(item.Status),
	}
}
