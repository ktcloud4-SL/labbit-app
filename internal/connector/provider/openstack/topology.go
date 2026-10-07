package openstackprovider

import (
	"context"
	"fmt"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"strings"
)

// The Provider keeps one lifecycle; only topology creation and NIC observation
// depend on the cloud's actual network API.
type provisionTopology struct {
	networkID, subnetID, securityGroupID string
	tier                                 ktCloudTier
}
type provisionNICs struct {
	portIDs, networkIDs         []string
	labPortID, managementPortID string
	managementTier              ktCloudTier
}

func (a *Adapter) prepareProvisionNetwork(ctx context.Context, request coreprovider.ProvisionRequest, config ProvisionConfig) (provisionTopology, []coreprovider.ResourceResult, error) {
	if a.ktNetwork != nil {
		return a.prepareKTCloudNetwork(ctx, request, config)
	}
	baseName := provisionBaseName(request.LabInstanceID, request.Generation)
	description := fmt.Sprintf("Labbit %s generation %d operation %s", safeName(request.LabInstanceID, 36), request.Generation, safeName(request.OperationID, 36))
	resources := make([]coreprovider.ResourceResult, 0, 5+len(request.CreationSnapshot.VMs)*3)
	networkResource, err := a.EnsureNetwork(ctx, resourceIdentity(request.Generation, "lab-network"), NetworkSpec{
		Name:        baseName + "-network",
		Description: description,
	})
	if err != nil {
		return provisionTopology{}, resources, err
	}
	resources = upsertResource(resources, networkResource)

	noGateway := ""
	var gatewayIP *string
	if !request.CreationSnapshot.InternetOutbound {
		gatewayIP = &noGateway
	}
	subnetResource, err := a.EnsureSubnet(ctx, resourceIdentity(request.Generation, "lab-subnet"), SubnetSpec{
		Name:        baseName + "-subnet",
		Description: description,
		NetworkID:   networkResource.ProviderID,
		CIDR:        config.LabSubnetCIDR,
		GatewayIP:   gatewayIP,
		EnableDHCP:  true,
	})
	if err != nil {
		return provisionTopology{}, resources, err
	}
	resources = upsertResource(resources, subnetResource)

	if request.CreationSnapshot.InternetOutbound {
		routerResource, err := a.EnsureRouter(ctx, resourceIdentity(request.Generation, "lab-router"), RouterSpec{
			Name:              baseName + "-router",
			Description:       description,
			ExternalNetworkID: config.ExternalNetworkID,
			SubnetID:          subnetResource.ProviderID,
		})
		if routerResource.ProviderID != "" {
			resources = upsertResource(resources, routerResource)
		}
		if err != nil {
			return provisionTopology{}, resources, err
		}
	}

	labSecurityGroupResource, err := a.EnsureSecurityGroup(ctx, resourceIdentity(request.Generation, "lab-security-group"), SecurityGroupSpec{
		Name:        baseName + "-lab-sg",
		Description: description,
	})
	if err != nil {
		return provisionTopology{}, resources, err
	}
	resources = upsertResource(resources, labSecurityGroupResource)

	labRule, err := a.EnsureIngressRule(ctx, resourceIdentity(request.Generation, "lab-ingress"), SecurityRuleSpec{
		SecurityGroupID: labSecurityGroupResource.ProviderID,
		Description:     "Allow traffic inside this Lab network",
		RemoteCIDR:      config.LabSubnetCIDR,
	})
	if err != nil {
		return provisionTopology{}, resources, err
	}
	resources = upsertResource(resources, labRule)

	return provisionTopology{networkID: networkResource.ProviderID, subnetID: subnetResource.ProviderID, securityGroupID: labSecurityGroupResource.ProviderID}, resources, nil
}

func (a *Adapter) prepareProvisionNICs(ctx context.Context, request coreprovider.ProvisionRequest, vm coreprovider.VMSpec, config ProvisionConfig, topology provisionTopology, resources []coreprovider.ResourceResult) (provisionNICs, []coreprovider.ResourceResult, error) {
	if a.ktNetwork != nil {
		return a.prepareKTCloudNICs(ctx, request, vm, config, topology, resources)
	}
	vmName := provisionBaseName(request.LabInstanceID, request.Generation) + "-" + safeName(vm.VMKey, 28)
	description := fmt.Sprintf("Labbit %s generation %d operation %s", safeName(request.LabInstanceID, 36), request.Generation, safeName(request.OperationID, 36))
	labPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":lab")
	labPortResource, _, err := a.EnsurePort(ctx, labPortIdentity, PortSpec{
		Name:             vmName + "-lab",
		Description:      description,
		NetworkID:        topology.networkID,
		SubnetID:         topology.subnetID,
		SecurityGroupIDs: []string{topology.securityGroupID},
	})
	if err != nil {
		return provisionNICs{}, resources, err
	}
	resources = upsertResource(resources, labPortResource)

	managementPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":management")
	managementPortResource, _, err := a.EnsurePort(ctx, managementPortIdentity, PortSpec{
		Name:             vmName + "-management",
		Description:      description,
		NetworkID:        config.ManagementNetworkID,
		SecurityGroupIDs: []string{config.ManagementSecurityGroupID},
	})
	if err != nil {
		return provisionNICs{}, resources, err
	}
	resources = upsertResource(resources, managementPortResource)

	return provisionNICs{portIDs: []string{labPortResource.ProviderID, managementPortResource.ProviderID}, labPortID: labPortResource.ProviderID, managementPortID: managementPortResource.ProviderID}, resources, nil
}

func (a *Adapter) refreshProvisionNICs(ctx context.Context, request coreprovider.ProvisionRequest, vm coreprovider.VMSpec, nics provisionNICs, serverID string, resources []coreprovider.ResourceResult) (string, []coreprovider.ResourceResult, error) {
	if a.ktNetwork != nil {
		return a.refreshKTCloudNICs(ctx, request, vm, nics, serverID, resources)
	}
	labPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":lab")
	managementPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":management")
	refreshedLabResource, _, err := a.refreshPort(ctx, labPortIdentity, nics.labPortID)
	if err != nil {
		return "", resources, err
	}
	resources = upsertResource(resources, refreshedLabResource)
	refreshedManagementResource, refreshedManagementPort, err := a.refreshPort(ctx, managementPortIdentity, nics.managementPortID)
	if err != nil {
		return "", resources, err
	}
	resources = upsertResource(resources, refreshedManagementResource)

	address, err := managementAddress(refreshedManagementPort)
	return address, resources, err
}
func (a *Adapter) preflightImagesFlavorsKey(ctx context.Context, config ProvisionConfig, snapshot coreprovider.CreationSnapshot) error {
	keyPair, err := keypairs.Get(ctx, a.compute, config.KeyPairName, nil).Extract()
	if err != nil || keyPair == nil || keyPair.Name != config.KeyPairName {
		return ErrProvisionCheck
	}
	if err := validateSSHCredential(config, keyPair.PublicKey); err != nil {
		return err
	}
	seenImages := make(map[string]struct{}, len(snapshot.VMs))
	seenFlavors := make(map[string]coreprovider.FlavorSpec, len(snapshot.VMs))
	for _, vm := range snapshot.VMs {
		if _, ok := seenImages[vm.ImageID]; !ok {
			image, err := images.Get(ctx, a.image, vm.ImageID).Extract()
			if err != nil || image == nil || normalizeStatus(string(image.Status)) != "ACTIVE" {
				return ErrProvisionCheck
			}
			if a.ktNetwork != nil && (image.MinDiskGigabytes > 50 || image.MinRAMMegabytes > int(vm.FlavorSpec.RAMMiB) || strings.Contains(strings.ToLower(image.Name), "windows") || strings.EqualFold(fmt.Sprint(image.Properties["os_type"]), "windows")) {
				return ErrProvisionCheck
			}
			seenImages[vm.ImageID] = struct{}{}
		}
		if knownSpec, ok := seenFlavors[vm.FlavorID]; ok {
			if knownSpec != vm.FlavorSpec {
				return ErrProvisionCheck
			}
		} else {
			flavor, err := flavors.Get(ctx, a.compute, vm.FlavorID).Extract()
			if err != nil || flavor == nil || int64(flavor.VCPUs) != vm.FlavorSpec.VCPUs || int64(flavor.RAM) != vm.FlavorSpec.RAMMiB || int64(flavor.Disk) != vm.FlavorSpec.DiskGiB {
				return ErrProvisionCheck
			}
			seenFlavors[vm.FlavorID] = vm.FlavorSpec
		}
	}
	return nil
}
