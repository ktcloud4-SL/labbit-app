package openstackprovider

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var ErrReconcileLookup = errors.New("OpenStack resource reconciliation failed")

const stateAbsent = "ABSENT"

// Reconcile distinguishes a confirmed 404 from a failed lookup. A lookup
// failure is never converted into false evidence that a resource is absent.
func (a *Adapter) Reconcile(ctx context.Context, request coreprovider.ReconcileRequest) (coreprovider.ReconcileResult, error) {
	if !adapterAvailable(a) {
		return reconcileFailure(errorInvalidProvision, "OpenStack Provider is not available"), nil
	}
	if !validCorrelation(request.Correlation) || request.KnownResources == nil {
		return reconcileFailure(errorInvalidProvision, "Reconcile request is invalid"), nil
	}
	knownResources, err := validateResourceRefs(request.KnownResources, request.Generation, false)
	if err != nil || !a.resourceProfileMatches(knownResources) {
		return reconcileFailure(errorInvalidProvision, "Reconcile resources are invalid"), nil
	}

	observations := make([]coreprovider.ResourceObservation, 0, len(knownResources))
	known := make(map[string]struct{}, len(knownResources))
	for _, resource := range knownResources {
		state, exists, err := a.observeResource(ctx, resource)
		if err != nil {
			return coreprovider.ReconcileResult{Observations: observations}, ErrReconcileLookup
		}
		observations = append(observations, coreprovider.ResourceObservation{
			ResourceType:  resource.ResourceType,
			ProviderID:    resource.ProviderID,
			Generation:    resource.Generation,
			Exists:        exists,
			ObservedState: state,
			Source:        coreprovider.SourceKnownResource,
			LogicalName:   resource.LogicalName,
		})
		known[resourceKey(resource.ResourceType, resource.ProviderID)] = struct{}{}
	}

	if request.DiscoverCandidates {
		candidates, err := a.discoverCandidates(ctx, request.Correlation, known)
		if err != nil {
			return coreprovider.ReconcileResult{Observations: observations}, ErrReconcileLookup
		}
		observations = append(observations, candidates...)
	}
	return coreprovider.ReconcileResult{Observations: observations}, nil
}

func (a *Adapter) observeResource(ctx context.Context, resource coreprovider.ResourceRef) (string, bool, error) {
	switch resource.ResourceType {
	case coreprovider.ResourceTypeTier, coreprovider.ResourceTypeFirewall:
		if a.ktNetwork == nil {
			return "", false, ErrReconcileLookup
		}
		return a.observeKTCloudResource(ctx, resource)
	case coreprovider.ResourceTypeVolume:
		if a.volume == nil {
			return "", false, ErrReconcileLookup
		}
		item, err := volumes.Get(ctx, a.volume, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		if item == nil || item.ID != resource.ProviderID || item.Status == "" {
			return "", false, ErrReconcileLookup
		}
		return normalizeStatus(item.Status), true, nil
	case coreprovider.ResourceTypeServer:
		item, err := a.lookupServer(ctx, resource.ProviderID)
		if err != nil {
			return observationError(ctx, err)
		}
		if item == nil || item.ID != resource.ProviderID || item.Status == "" {
			return "", false, ErrReconcileLookup
		}
		return normalizeStatus(item.Status), true, nil
	case coreprovider.ResourceTypePort:
		item, err := ports.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return normalizeStatus(item.Status), true, nil
	case coreprovider.ResourceTypeRouter:
		item, err := routers.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return normalizeStatus(item.Status), true, nil
	case coreprovider.ResourceTypeSecurityRule:
		_, err := rules.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return "PRESENT", true, nil
	case coreprovider.ResourceTypeSubnet:
		_, err := subnets.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return "PRESENT", true, nil
	case coreprovider.ResourceTypeSecurityGroup:
		_, err := groups.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return "PRESENT", true, nil
	case coreprovider.ResourceTypeNetwork:
		item, err := networks.Get(ctx, a.network, resource.ProviderID).Extract()
		if err != nil {
			return observationError(ctx, err)
		}
		return normalizeStatus(item.Status), true, nil
	default:
		return "", false, ErrReconcileLookup
	}
}

func observationError(ctx context.Context, err error) (string, bool, error) {
	if gophercloud.ResponseCodeIs(err, 404) {
		return stateAbsent, false, nil
	}
	return "", false, safeContextError(ctx, ErrReconcileLookup)
}

func (a *Adapter) discoverCandidates(ctx context.Context, correlation coreprovider.Correlation, known map[string]struct{}) ([]coreprovider.ResourceObservation, error) {
	baseName := provisionBaseName(correlation.LabInstanceID, correlation.Generation)
	candidates := make([]coreprovider.ResourceObservation, 0)
	seen := make(map[string]struct{})
	appendCandidate := func(observation coreprovider.ResourceObservation) {
		key := resourceKey(observation.ResourceType, observation.ProviderID)
		if _, ok := known[key]; ok {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, observation)
	}

	serverItems, err := a.listServerItems(ctx, servers.ListOpts{})
	if err != nil {
		return nil, safeContextError(ctx, ErrReconcileLookup)
	}
	expectedGeneration := strconv.FormatInt(correlation.Generation, 10)
	for _, item := range serverItems {
		if item.Metadata["labbit_lab_instance_id"] != correlation.LabInstanceID || item.Metadata["labbit_generation"] != expectedGeneration {
			continue
		}
		appendCandidate(candidateObservation(coreprovider.ResourceTypeServer, item.ID, correlation.Generation, normalizeStatus(item.Status), item.Metadata["labbit_vm_key"]))
		if a.ktNetwork != nil {
			for _, attachment := range item.AttachedVolumes {
				if attachment.ID != "" {
					appendCandidate(candidateObservation(coreprovider.ResourceTypeVolume, attachment.ID, correlation.Generation, "PRESENT", item.Metadata["labbit_vm_key"]+":root-volume"))
				}
			}
		}
	}
	if a.ktNetwork != nil {
		items, err := a.discoverKTCloudNetworkCandidates(ctx, correlation)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			appendCandidate(item)
		}
		sort.Slice(candidates, func(i, j int) bool {
			return resourceKey(candidates[i].ResourceType, candidates[i].ProviderID) < resourceKey(candidates[j].ResourceType, candidates[j].ProviderID)
		})
		return candidates, nil
	}

	networkPages, err := networks.List(a.network, networks.ListOpts{Name: baseName + "-network"}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrReconcileLookup)
	}
	networkItems, err := networks.ExtractNetworks(networkPages)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	for _, item := range networkItems {
		if item.Name == baseName+"-network" {
			appendCandidate(candidateObservation(coreprovider.ResourceTypeNetwork, item.ID, correlation.Generation, normalizeStatus(item.Status), "lab-network"))
		}
	}

	subnetPages, err := subnets.List(a.network, subnets.ListOpts{Name: baseName + "-subnet"}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrReconcileLookup)
	}
	subnetItems, err := subnets.ExtractSubnets(subnetPages)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	for _, item := range subnetItems {
		if item.Name == baseName+"-subnet" {
			appendCandidate(candidateObservation(coreprovider.ResourceTypeSubnet, item.ID, correlation.Generation, "PRESENT", "lab-subnet"))
		}
	}

	routerPages, err := routers.List(a.network, routers.ListOpts{Name: baseName + "-router"}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrReconcileLookup)
	}
	routerItems, err := routers.ExtractRouters(routerPages)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	for _, item := range routerItems {
		if item.Name == baseName+"-router" {
			appendCandidate(candidateObservation(coreprovider.ResourceTypeRouter, item.ID, correlation.Generation, normalizeStatus(item.Status), "lab-router"))
		}
	}

	securityGroups := []struct {
		name        string
		logicalName string
	}{
		{name: baseName + "-lab-sg", logicalName: "lab-security-group"},
	}
	for _, expected := range securityGroups {
		groupPages, err := groups.List(a.network, groups.ListOpts{Name: expected.name}).AllPages(ctx)
		if err != nil {
			return nil, safeContextError(ctx, ErrReconcileLookup)
		}
		groupItems, err := groups.ExtractGroups(groupPages)
		if err != nil {
			return nil, ErrReconcileLookup
		}
		for _, item := range groupItems {
			if item.Name != expected.name {
				continue
			}
			appendCandidate(candidateObservation(coreprovider.ResourceTypeSecurityGroup, item.ID, correlation.Generation, "PRESENT", expected.logicalName))
			for _, rule := range item.Rules {
				if !isLabbitSecurityRule(rule) {
					continue
				}
				appendCandidate(candidateObservation(coreprovider.ResourceTypeSecurityRule, rule.ID, correlation.Generation, "PRESENT", ""))
			}
		}
	}

	// Neutron has no prefix query for port names. Listing then applying the
	// deterministic, generation-specific prefix lets Reconcile surface orphaned
	// pre-Nova ports without claiming ownership or deleting them.
	portPages, err := ports.List(a.network, ports.ListOpts{}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrReconcileLookup)
	}
	portItems, err := ports.ExtractPorts(portPages)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	for _, item := range portItems {
		if !strings.HasPrefix(item.Name, baseName+"-") {
			continue
		}
		appendCandidate(candidateObservation(coreprovider.ResourceTypePort, item.ID, correlation.Generation, normalizeStatus(item.Status), candidatePortLogicalName(item.Name, baseName)))
	}

	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].ResourceType == candidates[right].ResourceType {
			return candidates[left].ProviderID < candidates[right].ProviderID
		}
		return candidates[left].ResourceType < candidates[right].ResourceType
	})
	return candidates, nil
}

func isLabbitSecurityRule(rule rules.SecGroupRule) bool {
	return rule.Description == "Allow traffic inside this Lab network" || rule.Description == "Allow Connector Management SSH"
}

func candidateObservation(resourceType, providerID string, generation int64, state, logicalName string) coreprovider.ResourceObservation {
	return coreprovider.ResourceObservation{
		ResourceType:  resourceType,
		ProviderID:    providerID,
		Generation:    generation,
		Exists:        true,
		ObservedState: state,
		Source:        coreprovider.SourceDiscoveredCandidate,
		LogicalName:   logicalName,
	}
}

func candidatePortLogicalName(name, baseName string) string {
	suffix := strings.TrimPrefix(name, baseName+"-")
	switch {
	case strings.HasSuffix(suffix, "-management"):
		return strings.TrimSuffix(suffix, "-management") + ":management"
	case strings.HasSuffix(suffix, "-lab"):
		return strings.TrimSuffix(suffix, "-lab") + ":lab"
	default:
		return ""
	}
}

func resourceKey(resourceType, providerID string) string {
	return resourceType + "\x00" + providerID
}

func reconcileFailure(code, message string) coreprovider.ReconcileResult {
	return coreprovider.ReconcileResult{
		Observations: []coreprovider.ResourceObservation{},
		Error:        &coreprovider.SafeError{Code: code, Message: message},
	}
}
