package openstackprovider

import (
	"context"
	"strings"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func (a *Adapter) resourceProfileMatches(resources []coreprovider.ResourceRef) bool {
	for _, resource := range resources {
		if a.ktNetwork == nil {
			if resource.ResourceType == coreprovider.ResourceTypeTier || resource.ResourceType == coreprovider.ResourceTypeFirewall || resource.ResourceType == coreprovider.ResourceTypeVolume {
				return false
			}
			continue
		}
		if strings.ContainsAny(resource.ProviderID, "/?#") {
			return false
		}
		switch resource.ResourceType {
		case coreprovider.ResourceTypeServer, coreprovider.ResourceTypeFirewall:
		case coreprovider.ResourceTypeVolume:
			if a.volume == nil {
				return false
			}
		case coreprovider.ResourceTypeTier:
			if resource.ProviderID == a.provision.KTCloudConnectorTierID {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (a *Adapter) validateResetResourceSet(resources []coreprovider.ResourceRef, snapshot coreprovider.CreationSnapshot, nextGeneration int64) ([]coreprovider.ResourceRef, error) {
	if a.ktNetwork == nil {
		return validateResetResourceSet(resources, snapshot, nextGeneration)
	}
	if nextGeneration < 2 || len(resources) == 0 || len(snapshot.VMs) != 1 || snapshot.InternetOutbound {
		return nil, ErrLifecycleRequest
	}
	validated, err := validateResourceRefs(resources, nextGeneration, true)
	if err != nil {
		return nil, err
	}
	vm := snapshot.VMs[0]
	expected := map[string]struct{}{
		resourceSetKey(coreprovider.ResourceTypeTier, "lab-tier"):                     {},
		resourceSetKey(coreprovider.ResourceTypeTier, vm.VMKey+":management-tier"):    {},
		resourceSetKey(coreprovider.ResourceTypeFirewall, vm.VMKey+":management-ssh"): {},
		resourceSetKey(coreprovider.ResourceTypeServer, vm.VMKey):                     {},
		resourceSetKey(coreprovider.ResourceTypeVolume, vm.VMKey+":root-volume"):      {},
	}
	return validateExpectedResetSet(validated, nextGeneration, expected)
}

func (a *Adapter) observeKTCloudResource(ctx context.Context, resource coreprovider.ResourceRef) (string, bool, error) {
	switch resource.ResourceType {
	case coreprovider.ResourceTypeTier:
		item, exists, err := a.ktNetwork.tier(ctx, resource.ProviderID)
		if err != nil {
			return "", false, ErrReconcileLookup
		}
		if !exists {
			return stateAbsent, false, nil
		}
		return normalizeStatus(item.Status), true, nil
	case coreprovider.ResourceTypeFirewall:
		items, err := a.ktNetwork.policies(ctx)
		if err != nil {
			return "", false, ErrReconcileLookup
		}
		matches := 0
		for _, item := range items {
			if item.ID == resource.ProviderID {
				matches++
			}
		}
		if matches > 1 {
			return "", false, ErrReconcileLookup
		}
		if matches == 0 {
			return stateAbsent, false, nil
		}
		return "PRESENT", true, nil
	}
	return "", false, ErrReconcileLookup
}

func (a *Adapter) discoverKTCloudNetworkCandidates(ctx context.Context, correlation coreprovider.Correlation) ([]coreprovider.ResourceObservation, error) {
	baseName := provisionBaseName(correlation.LabInstanceID, correlation.Generation)
	tiers, err := a.ktNetwork.tiers(ctx)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	policies, err := a.ktNetwork.policies(ctx)
	if err != nil {
		return nil, ErrReconcileLookup
	}
	candidates := make([]coreprovider.ResourceObservation, 0)
	for _, tier := range tiers {
		if tier.Shared || tier.ID == a.provision.KTCloudConnectorTierID {
			continue
		}
		logical := ""
		if tier.Name == baseName+"-lab-tier" {
			logical = "lab-tier"
		} else if strings.HasPrefix(tier.Name, baseName+"-") && strings.HasSuffix(tier.Name, "-management-tier") {
			logical = strings.TrimSuffix(strings.TrimPrefix(tier.Name, baseName+"-"), "-management-tier") + ":management-tier"
		}
		if logical != "" {
			candidates = append(candidates, candidateObservation(coreprovider.ResourceTypeTier, tier.ID, correlation.Generation, normalizeStatus(tier.Status), logical))
		}
	}
	for _, policy := range policies {
		if strings.HasPrefix(policy.Comment, baseName+":") && strings.HasSuffix(policy.Comment, ":management-ssh") {
			logical := strings.TrimPrefix(policy.Comment, baseName+":")
			candidates = append(candidates, candidateObservation(coreprovider.ResourceTypeFirewall, policy.ID, correlation.Generation, "PRESENT", logical))
		}
	}
	return candidates, nil
}
