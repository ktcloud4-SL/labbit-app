package openstackprovider

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrLifecycleRequest = errors.New("OpenStack lifecycle request is invalid")
	ErrResourceDelete   = errors.New("OpenStack resource deletion failed")
	ErrDeleteWait       = errors.New("OpenStack resource deletion could not be verified")
)

const (
	stateDeleted            = "DELETED"
	stateDeleteFailed       = "DELETE_FAILED"
	stateDeleteUnknown      = "UNKNOWN"
	stateDeleteNotAttempted = "DELETE_NOT_ATTEMPTED"
)

// Reset preserves the immutable CreationSnapshot contract. The complete next
// generation is preflighted before any current-generation resource is removed.
func (a *Adapter) Reset(ctx context.Context, request coreprovider.ResetRequest) (coreprovider.OperationResult, error) {
	if !adapterAvailable(a) {
		return failedResult(nil, errorInvalidProvision, "OpenStack Provider is not available"), nil
	}
	provisionRequest := coreprovider.ProvisionRequest{
		Correlation:      request.Correlation,
		CreationSnapshot: request.CreationSnapshot,
	}
	config := normalizedProvisionConfig(a.provision)
	if validateProvisionSettings(config, request.CreationSnapshot) != nil || validateProvisionRequest(provisionRequest) != nil {
		return failedResult(nil, errorInvalidProvision, "Reset request is invalid"), nil
	}
	oldResources, err := validateResetResourceSet(request.ProviderResources, request.CreationSnapshot, request.Generation)
	if err != nil {
		return failedResult(nil, errorInvalidProvision, "Reset resources are invalid"), nil
	}

	// This read-only check intentionally happens before cleanup. If the original
	// image, flavor, key pair, or network can no longer reproduce the snapshot,
	// the existing generation remains untouched.
	if err := a.preflightProvision(ctx, config, request.CreationSnapshot, quotaCreditForReset(oldResources, request.CreationSnapshot)); err != nil {
		return preflightFailure(resourceResults(oldResources, stateDeleteNotAttempted), err, "OpenStack Reset preflight failed"), nil
	}

	cleanup := a.cleanupResources(ctx, oldResources)
	if cleanup.Outcome != coreprovider.OutcomeSucceeded {
		return cleanup, nil
	}

	provision, err := a.Provision(ctx, provisionRequest)
	if err != nil {
		return coreprovider.OperationResult{
			Outcome:           coreprovider.OutcomeUnknown,
			ProviderResources: append(cloneResourceResults(cleanup.ProviderResources), provision.ProviderResources...),
			Error:             &coreprovider.SafeError{Code: errorUnknown, Message: "Reset replacement generation could not be verified"},
		}, nil
	}
	provision.ProviderResources = append(cloneResourceResults(cleanup.ProviderResources), provision.ProviderResources...)
	return provision, nil
}

// Cleanup deletes only the exact Provider IDs supplied by Control. It never
// treats discovered candidates as owned resources and never deletes by name.
func (a *Adapter) Cleanup(ctx context.Context, request coreprovider.CleanupRequest) (coreprovider.OperationResult, error) {
	if !adapterAvailable(a) {
		return failedResult(nil, errorInvalidProvision, "OpenStack Provider is not available"), nil
	}
	if !validCorrelation(request.Correlation) || request.ProviderResources == nil {
		return failedResult(nil, errorInvalidProvision, "Cleanup request is invalid"), nil
	}
	resources, err := validateResourceRefs(request.ProviderResources, request.Generation, false)
	if err != nil {
		return failedResult(nil, errorInvalidProvision, "Cleanup resources are invalid"), nil
	}
	return a.cleanupResources(ctx, resources), nil
}

func (a *Adapter) cleanupResources(ctx context.Context, resources []coreprovider.ResourceRef) coreprovider.OperationResult {
	ordered := append([]coreprovider.ResourceRef(nil), resources...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return deletePriority(ordered[left].ResourceType) < deletePriority(ordered[right].ResourceType)
	})
	results := resourceResults(ordered, stateDeleteNotAttempted)

	for index, resource := range ordered {
		err := a.deleteAndConfirm(ctx, resource)
		if err == nil {
			results[index].ObservedState = stateDeleted
			continue
		}
		if errors.Is(err, ErrMutationRejected) {
			results[index].ObservedState = stateDeleteFailed
			return failedResult(results, errorOpenStack, "OpenStack resource cleanup was rejected")
		}
		results[index].ObservedState = stateDeleteUnknown
		return unknownResult(results, "OpenStack resource cleanup could not be verified")
	}

	return coreprovider.OperationResult{
		Outcome:           coreprovider.OutcomeSucceeded,
		ProviderResources: results,
	}
}

func (a *Adapter) deleteAndConfirm(ctx context.Context, resource coreprovider.ResourceRef) error {
	err := a.deleteResource(ctx, resource)
	if err != nil {
		if gophercloud.ResponseCodeIs(err, 404) {
			return nil
		}
		for status := 400; status < 500; status++ {
			if gophercloud.ResponseCodeIs(err, status) {
				return errors.Join(ErrResourceDelete, ErrMutationRejected)
			}
		}
		return safeContextError(ctx, ErrResourceDelete)
	}

	timeout := normalizedProvisionConfig(a.provision).ActiveTimeout
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(normalizedProvisionConfig(a.provision).PollInterval)
	defer ticker.Stop()
	for {
		_, exists, err := a.observeResource(waitContext, resource)
		if err != nil {
			return errors.Join(ErrDeleteWait, err)
		}
		if !exists {
			return nil
		}
		select {
		case <-waitContext.Done():
			return errors.Join(ErrDeleteWait, waitContext.Err())
		case <-ticker.C:
		}
	}
}

func (a *Adapter) deleteResource(ctx context.Context, resource coreprovider.ResourceRef) error {
	switch resource.ResourceType {
	case coreprovider.ResourceTypeServer:
		return servers.Delete(ctx, a.compute, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypePort:
		return ports.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypeRouter:
		if err := a.removeRouterInterfaces(ctx, resource.ProviderID); err != nil {
			return err
		}
		return routers.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypeSecurityRule:
		return rules.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypeSubnet:
		return subnets.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypeSecurityGroup:
		return groups.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	case coreprovider.ResourceTypeNetwork:
		return networks.Delete(ctx, a.network, resource.ProviderID).ExtractErr()
	default:
		return errors.Join(ErrLifecycleRequest, ErrMutationRejected)
	}
}

func adapterAvailable(adapter *Adapter) bool {
	return adapter != nil && adapter.image != nil && adapter.compute != nil && adapter.network != nil
}

func validCorrelation(correlation coreprovider.Correlation) bool {
	return strings.TrimSpace(correlation.OperationID) != "" && len(correlation.OperationID) <= 255 &&
		strings.TrimSpace(correlation.LabInstanceID) != "" && len(correlation.LabInstanceID) <= 255 &&
		correlation.Generation >= 1
}

func validateResourceRefs(resources []coreprovider.ResourceRef, maxGeneration int64, requireOlder bool) ([]coreprovider.ResourceRef, error) {
	validated := make([]coreprovider.ResourceRef, 0, len(resources))
	seen := make(map[string]coreprovider.ResourceRef, len(resources))
	for _, resource := range resources {
		resource.ResourceType = strings.TrimSpace(resource.ResourceType)
		resource.ProviderID = strings.TrimSpace(resource.ProviderID)
		resource.LogicalName = strings.TrimSpace(resource.LogicalName)
		if !supportedResourceType(resource.ResourceType) || resource.ProviderID == "" || len(resource.ProviderID) > 255 || resource.Generation < 1 || resource.Generation > maxGeneration || len(resource.LogicalName) > 255 {
			return nil, ErrLifecycleRequest
		}
		if requireOlder && resource.Generation >= maxGeneration {
			return nil, ErrLifecycleRequest
		}
		key := resource.ResourceType + "\x00" + resource.ProviderID
		if previous, ok := seen[key]; ok {
			if previous != resource {
				return nil, ErrLifecycleRequest
			}
			continue
		}
		seen[key] = resource
		validated = append(validated, resource)
	}
	return validated, nil
}

func validateResetResourceSet(resources []coreprovider.ResourceRef, snapshot coreprovider.CreationSnapshot, nextGeneration int64) ([]coreprovider.ResourceRef, error) {
	if nextGeneration < 2 || len(resources) == 0 {
		return nil, ErrLifecycleRequest
	}
	validated, err := validateResourceRefs(resources, nextGeneration, true)
	if err != nil {
		return nil, err
	}

	expected := map[string]struct{}{
		resourceSetKey(coreprovider.ResourceTypeNetwork, "lab-network"):              {},
		resourceSetKey(coreprovider.ResourceTypeSubnet, "lab-subnet"):                {},
		resourceSetKey(coreprovider.ResourceTypeSecurityGroup, "lab-security-group"): {},
		resourceSetKey(coreprovider.ResourceTypeSecurityRule, "lab-ingress"):         {},
	}
	if snapshot.InternetOutbound {
		expected[resourceSetKey(coreprovider.ResourceTypeRouter, "lab-router")] = struct{}{}
	}
	for _, vm := range snapshot.VMs {
		expected[resourceSetKey(coreprovider.ResourceTypePort, vm.VMKey+":lab")] = struct{}{}
		expected[resourceSetKey(coreprovider.ResourceTypePort, vm.VMKey+":management")] = struct{}{}
		expected[resourceSetKey(coreprovider.ResourceTypeServer, vm.VMKey)] = struct{}{}
	}
	if len(validated) != len(expected) {
		return nil, ErrLifecycleRequest
	}
	seen := make(map[string]struct{}, len(validated))
	for _, resource := range validated {
		if resource.Generation != nextGeneration-1 || resource.LogicalName == "" {
			return nil, ErrLifecycleRequest
		}
		key := resourceSetKey(resource.ResourceType, resource.LogicalName)
		if _, ok := expected[key]; !ok {
			return nil, ErrLifecycleRequest
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrLifecycleRequest
		}
		seen[key] = struct{}{}
	}
	return validated, nil
}

func resourceSetKey(resourceType, logicalName string) string {
	return strings.TrimSpace(resourceType) + "\x00" + strings.TrimSpace(logicalName)
}

func supportedResourceType(resourceType string) bool {
	switch resourceType {
	case coreprovider.ResourceTypeNetwork,
		coreprovider.ResourceTypeSubnet,
		coreprovider.ResourceTypeRouter,
		coreprovider.ResourceTypeSecurityGroup,
		coreprovider.ResourceTypeSecurityRule,
		coreprovider.ResourceTypePort,
		coreprovider.ResourceTypeServer:
		return true
	default:
		return false
	}
}

func deletePriority(resourceType string) int {
	switch resourceType {
	case coreprovider.ResourceTypeServer:
		return 0
	case coreprovider.ResourceTypePort:
		return 1
	case coreprovider.ResourceTypeRouter:
		return 2
	case coreprovider.ResourceTypeSecurityRule:
		return 3
	case coreprovider.ResourceTypeSubnet:
		return 4
	case coreprovider.ResourceTypeSecurityGroup:
		return 5
	case coreprovider.ResourceTypeNetwork:
		return 6
	default:
		return 7
	}
}

func (a *Adapter) removeRouterInterfaces(ctx context.Context, routerID string) error {
	pages, err := ports.List(a.network, ports.ListOpts{DeviceID: routerID}).AllPages(ctx)
	if err != nil {
		return safeContextError(ctx, ErrResourceDelete)
	}
	items, err := ports.ExtractPorts(pages)
	if err != nil {
		return ErrResourceDelete
	}
	for _, item := range items {
		if !strings.HasPrefix(item.DeviceOwner, "network:router_interface") {
			continue
		}
		_, err := routers.RemoveInterface(ctx, a.network, routerID, routers.RemoveInterfaceOpts{PortID: item.ID}).Extract()
		if err != nil && !gophercloud.ResponseCodeIs(err, 404) {
			return safeMutationError(ctx, err, ErrResourceDelete)
		}
	}
	return nil
}

func resourceResults(resources []coreprovider.ResourceRef, state string) []coreprovider.ResourceResult {
	results := make([]coreprovider.ResourceResult, 0, len(resources))
	for _, resource := range resources {
		results = append(results, coreprovider.ResourceResult{ResourceRef: resource, ObservedState: state})
	}
	return results
}
