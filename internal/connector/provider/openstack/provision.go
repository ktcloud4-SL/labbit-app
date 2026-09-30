package openstackprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/external"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/groups"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/security/rules"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"golang.org/x/crypto/ssh"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrProvisionConfig  = errors.New("OpenStack Provision config is invalid")
	ErrProvisionRequest = errors.New("OpenStack Provision request is invalid")
	ErrProvisionCheck   = errors.New("OpenStack Provision preflight failed")
)

const (
	errorInvalidProvision = "ERR_CONNECTOR_INTERNAL"
	errorOpenStack        = "ERR_INFRA_OPENSTACK"
	errorQuotaExceeded    = "ERR_RESOURCE_QUOTA_EXCEEDED"
	errorBootTimeout      = "ERR_VM_BOOT_TIMEOUT"
	errorUnknown          = "ERR_UNKNOWN_RECONCILING"
)

var _ coreprovider.Provider = (*Adapter)(nil)

// Provision creates one isolated Lab network and attaches every VM to both the
// Lab network and the configured Management network. Each confirmed Provider
// resource is returned immediately in the final result even when a later step
// fails, so Control can persist partial progress for reconciliation/cleanup.
func (a *Adapter) Provision(ctx context.Context, request coreprovider.ProvisionRequest) (coreprovider.OperationResult, error) {
	if a == nil || a.image == nil || a.compute == nil || a.network == nil {
		return failedResult(nil, errorInvalidProvision, "OpenStack Provider is not available"), nil
	}
	config := normalizedProvisionConfig(a.provision)
	if err := validateProvisionSettings(config, request.CreationSnapshot); err != nil {
		return failedResult(nil, errorInvalidProvision, "OpenStack Provision settings are incomplete"), nil
	}
	if err := validateProvisionRequest(request); err != nil {
		return failedResult(nil, errorInvalidProvision, "Provision request is invalid"), nil
	}
	if err := a.preflightProvision(ctx, config, request.CreationSnapshot, quotaUsage{}); err != nil {
		return preflightFailure(nil, err, "OpenStack Provision preflight failed"), nil
	}

	baseName := provisionBaseName(request.LabInstanceID, request.Generation)
	description := fmt.Sprintf("Labbit %s generation %d operation %s", safeName(request.LabInstanceID, 36), request.Generation, safeName(request.OperationID, 36))
	resources := make([]coreprovider.ResourceResult, 0, 5+len(request.CreationSnapshot.VMs)*3)

	networkResource, err := a.EnsureNetwork(ctx, resourceIdentity(request.Generation, "lab-network"), NetworkSpec{
		Name:        baseName + "-network",
		Description: description,
	})
	if err != nil {
		return mutationFailure(resources, err, "Lab network could not be created"), nil
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
		return mutationFailure(resources, err, "Lab subnet could not be created"), nil
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
			return mutationFailure(resources, err, "Lab outbound router could not be created"), nil
		}
	}

	labSecurityGroupResource, err := a.EnsureSecurityGroup(ctx, resourceIdentity(request.Generation, "lab-security-group"), SecurityGroupSpec{
		Name:        baseName + "-lab-sg",
		Description: description,
	})
	if err != nil {
		return mutationFailure(resources, err, "Lab security group could not be created"), nil
	}
	resources = upsertResource(resources, labSecurityGroupResource)

	labRule, err := a.EnsureIngressRule(ctx, resourceIdentity(request.Generation, "lab-ingress"), SecurityRuleSpec{
		SecurityGroupID: labSecurityGroupResource.ProviderID,
		Description:     "Allow traffic inside this Lab network",
		RemoteCIDR:      config.LabSubnetCIDR,
	})
	if err != nil {
		return mutationFailure(resources, err, "Lab traffic rule could not be created"), nil
	}
	resources = upsertResource(resources, labRule)

	for _, vm := range request.CreationSnapshot.VMs {
		vmName := baseName + "-" + safeName(vm.VMKey, 28)
		labPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":lab")
		labPortResource, _, err := a.EnsurePort(ctx, labPortIdentity, PortSpec{
			Name:             vmName + "-lab",
			Description:      description,
			NetworkID:        networkResource.ProviderID,
			SubnetID:         subnetResource.ProviderID,
			SecurityGroupIDs: []string{labSecurityGroupResource.ProviderID},
		})
		if err != nil {
			return mutationFailure(resources, err, "Lab NIC could not be created"), nil
		}
		resources = upsertResource(resources, labPortResource)

		managementPortIdentity := resourceIdentity(request.Generation, vm.VMKey+":management")
		managementPortResource, managementPort, err := a.EnsurePort(ctx, managementPortIdentity, PortSpec{
			Name:             vmName + "-management",
			Description:      description,
			NetworkID:        config.ManagementNetworkID,
			SecurityGroupIDs: []string{config.ManagementSecurityGroupID},
		})
		if err != nil {
			return mutationFailure(resources, err, "Management NIC could not be created"), nil
		}
		resources = upsertResource(resources, managementPortResource)

		metadata := map[string]string{
			"labbit_operation_id":    request.OperationID,
			"labbit_lab_instance_id": request.LabInstanceID,
			"labbit_generation":      strconv.FormatInt(request.Generation, 10),
			"labbit_vm_key":          vm.VMKey,
		}
		userData := []byte(nil)
		if request.CreationSnapshot.StartupScript != nil {
			userData = []byte(request.CreationSnapshot.StartupScript.Content)
		}
		serverResource, server, err := a.EnsureServer(ctx, resourceIdentity(request.Generation, vm.VMKey), ServerSpec{
			Name:     vmName,
			ImageID:  vm.ImageID,
			FlavorID: vm.FlavorID,
			KeyPair:  config.KeyPairName,
			PortIDs:  []string{labPortResource.ProviderID, managementPortResource.ProviderID},
			UserData: userData,
			Metadata: metadata,
		})
		if err != nil {
			return mutationFailure(resources, err, "Workspace VM create result is unknown"), nil
		}
		resources = upsertResource(resources, serverResource)

		activeContext, cancelActive := context.WithTimeout(ctx, config.ActiveTimeout)
		activeResource, err := a.waitServerActive(activeContext, resourceIdentity(request.Generation, vm.VMKey), server.ID)
		cancelActive()
		if activeResource.ProviderID != "" {
			resources = upsertResource(resources, activeResource)
		}
		if err != nil {
			if errors.Is(err, ErrServerGet) || ((errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil) {
				return unknownResult(resources, "Workspace VM state could not be verified"), nil
			}
			return failedResult(resources, errorBootTimeout, "Workspace VM did not become ACTIVE"), nil
		}
		refreshedLabResource, _, err := a.refreshPort(ctx, labPortIdentity, labPortResource.ProviderID)
		if err != nil {
			return unknownResult(resources, "Lab NIC state could not be verified"), nil
		}
		resources = upsertResource(resources, refreshedLabResource)
		refreshedManagementResource, refreshedManagementPort, err := a.refreshPort(ctx, managementPortIdentity, managementPortResource.ProviderID)
		if err != nil {
			return unknownResult(resources, "Management NIC state could not be verified"), nil
		}
		resources = upsertResource(resources, refreshedManagementResource)
		managementPort = refreshedManagementPort

		if vm.VMKey == request.CreationSnapshot.WorkspaceVMKey || request.CreationSnapshot.StartupScript != nil {
			sshContext, cancelSSH := context.WithTimeout(ctx, config.SSHReadyTimeout)
			err = a.waitSSHReady(sshContext, managementPort, server.ID)
			cancelSSH()
			if err != nil {
				if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
					return unknownResult(resources, "Workspace VM SSH state could not be verified"), nil
				}
				return failedResult(resources, errorBootTimeout, "Workspace VM SSH did not become ready"), nil
			}
		}
		if request.CreationSnapshot.StartupScript != nil {
			startupContext, cancelStartup := context.WithTimeout(ctx, config.StartupReadyTimeout)
			err = a.waitStartupReady(startupContext, managementPort, server.ID)
			cancelStartup()
			if err != nil {
				if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
					return unknownResult(resources, "VM initialization state could not be verified"), nil
				}
				return failedResult(resources, errorBootTimeout, "VM initialization did not complete"), nil
			}
		}
	}

	return coreprovider.OperationResult{
		Outcome:           coreprovider.OutcomeSucceeded,
		ProviderResources: resources,
	}, nil
}

func validateProvisionConfig(config ProvisionConfig) error {
	labPrefix, labErr := netip.ParsePrefix(config.LabSubnetCIDR)
	sshPrefix, sshErr := netip.ParsePrefix(config.SSHAllowedCIDR)
	if config.ProjectID == "" || config.ManagementNetworkID == "" || config.ManagementSecurityGroupID == "" || config.KeyPairName == "" || labErr != nil || sshErr != nil || !labPrefix.Addr().Is4() || !sshPrefix.Addr().Is4() || labPrefix != labPrefix.Masked() {
		return ErrProvisionConfig
	}
	return nil
}

func validateProvisionSettings(config ProvisionConfig, snapshot coreprovider.CreationSnapshot) error {
	if err := validateProvisionConfig(config); err != nil {
		return err
	}
	if snapshot.InternetOutbound && config.ExternalNetworkID == "" {
		return ErrProvisionConfig
	}
	if config.ProviderConnectionID == "" || snapshot.ProviderConnectionID != config.ProviderConnectionID {
		return ErrProvisionConfig
	}
	if config.SSHUsername == "" || config.SSHPrivateKeyFile == "" || config.SSHKnownHostsFile == "" {
		return ErrProvisionConfig
	}
	return nil
}

func validateProvisionRequest(request coreprovider.ProvisionRequest) error {
	if strings.TrimSpace(request.OperationID) == "" || len(request.OperationID) > 255 || strings.TrimSpace(request.LabInstanceID) == "" || len(request.LabInstanceID) > 255 || request.Generation < 1 {
		return ErrProvisionRequest
	}
	snapshot := request.CreationSnapshot
	if strings.TrimSpace(snapshot.ProviderConnectionID) == "" || len(snapshot.VMs) == 0 || strings.TrimSpace(snapshot.WorkspaceVMKey) == "" {
		return ErrProvisionRequest
	}
	seen := make(map[string]struct{}, len(snapshot.VMs))
	derivedNames := make(map[string]struct{}, len(snapshot.VMs))
	workspaceFound := false
	for _, vm := range snapshot.VMs {
		vmKey := strings.TrimSpace(vm.VMKey)
		if vmKey == "" || len(vmKey) > 255 || strings.TrimSpace(vm.Role) == "" || vm.InstanceIndex < 0 || strings.TrimSpace(vm.ImageID) == "" || strings.TrimSpace(vm.FlavorID) == "" || vm.FlavorSpec.VCPUs < 1 || vm.FlavorSpec.RAMMiB < 1 || vm.FlavorSpec.DiskGiB < 0 {
			return ErrProvisionRequest
		}
		if _, ok := seen[vmKey]; ok {
			return ErrProvisionRequest
		}
		seen[vmKey] = struct{}{}
		derivedName := safeName(vmKey, 28)
		if _, ok := derivedNames[derivedName]; ok {
			return ErrProvisionRequest
		}
		derivedNames[derivedName] = struct{}{}
		workspaceFound = workspaceFound || vmKey == snapshot.WorkspaceVMKey
	}
	if !workspaceFound {
		return ErrProvisionRequest
	}
	if snapshot.StartupScript != nil {
		if len(snapshot.StartupScript.Content) > 64*1024 {
			return ErrProvisionRequest
		}
		digest := sha256.Sum256([]byte(snapshot.StartupScript.Content))
		expected, err := hex.DecodeString(snapshot.StartupScript.SHA256)
		if err != nil || len(expected) != sha256.Size || !equalBytes(digest[:], expected) {
			return ErrProvisionRequest
		}
	}
	return nil
}

func (a *Adapter) preflightProvision(ctx context.Context, config ProvisionConfig, snapshot coreprovider.CreationSnapshot, credit quotaUsage) error {
	managementNetwork, err := networks.Get(ctx, a.network, config.ManagementNetworkID).Extract()
	if err != nil || normalizeStatus(managementNetwork.Status) != "ACTIVE" || len(managementNetwork.Subnets) == 0 {
		return ErrProvisionCheck
	}
	if err := a.validateManagementSecurityGroup(ctx, config); err != nil {
		return err
	}
	if snapshot.InternetOutbound {
		var externalNetwork struct {
			networks.Network
			external.NetworkExternalExt
		}
		err := networks.Get(ctx, a.network, config.ExternalNetworkID).ExtractInto(&externalNetwork)
		if err != nil || normalizeStatus(externalNetwork.Status) != "ACTIVE" || !externalNetwork.External {
			return ErrProvisionCheck
		}
	}
	keyPair, err := keypairs.Get(ctx, a.compute, config.KeyPairName, nil).Extract()
	if err != nil || keyPair.Name != config.KeyPairName {
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
			if err != nil || normalizeStatus(string(image.Status)) != "ACTIVE" {
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
			if err != nil || int64(flavor.VCPUs) != vm.FlavorSpec.VCPUs || int64(flavor.RAM) != vm.FlavorSpec.RAMMiB || int64(flavor.Disk) != vm.FlavorSpec.DiskGiB {
				return ErrProvisionCheck
			}
			seenFlavors[vm.FlavorID] = vm.FlavorSpec
		}
	}
	return a.preflightQuota(ctx, config, snapshot, credit)
}

func validateSSHCredential(config ProvisionConfig, providerPublicKey string) error {
	privateKey, err := os.ReadFile(config.SSHPrivateKeyFile)
	if err != nil {
		return ErrProvisionCheck
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return ErrProvisionCheck
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(providerPublicKey)))
	if err != nil || ssh.FingerprintSHA256(publicKey) != ssh.FingerprintSHA256(signer.PublicKey()) {
		return ErrProvisionCheck
	}
	knownHosts, err := os.OpenFile(config.SSHKnownHostsFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ErrProvisionCheck
	}
	if err := knownHosts.Close(); err != nil {
		return ErrProvisionCheck
	}
	return nil
}

func (a *Adapter) validateManagementSecurityGroup(ctx context.Context, config ProvisionConfig) error {
	group, err := groups.Get(ctx, a.network, config.ManagementSecurityGroupID).Extract()
	if err != nil || group.ID != config.ManagementSecurityGroupID || !group.Stateful {
		return ErrProvisionCheck
	}
	sshIngressRules := 0
	for _, rule := range group.Rules {
		direction := strings.ToLower(strings.TrimSpace(rule.Direction))
		if direction == string(rules.DirEgress) {
			return ErrProvisionCheck
		}
		if direction != string(rules.DirIngress) || strings.ToLower(strings.TrimSpace(rule.EtherType)) != "ipv4" ||
			strings.ToLower(strings.TrimSpace(rule.Protocol)) != string(rules.ProtocolTCP) || rule.PortRangeMin != 22 || rule.PortRangeMax != 22 ||
			strings.TrimSpace(rule.RemoteIPPrefix) != config.SSHAllowedCIDR || rule.RemoteGroupID != "" || rule.RemoteAddressGroupID != "" {
			return ErrProvisionCheck
		}
		sshIngressRules++
	}
	if sshIngressRules != 1 {
		return ErrProvisionCheck
	}
	return nil
}

func resourceIdentity(generation int64, logicalName string) ResourceIdentity {
	return ResourceIdentity{Generation: generation, LogicalName: logicalName}
}

func provisionBaseName(labInstanceID string, generation int64) string {
	input := strings.TrimSpace(labInstanceID) + ":" + strconv.FormatInt(generation, 10)
	digest := sha256.Sum256([]byte(input))
	prefix := safeName(labInstanceID, 24)
	return fmt.Sprintf("labbit-%s-g%d-%s", prefix, generation, hex.EncodeToString(digest[:5]))
}

func safeName(value string, maxLength int) string {
	var builder strings.Builder
	lastDash := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if valid {
			builder.WriteRune(character)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
		if builder.Len() >= maxLength {
			break
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "resource"
	}
	return result
}

func mutationFailure(resources []coreprovider.ResourceResult, err error, message string) coreprovider.OperationResult {
	if errors.Is(err, ErrMutationRejected) {
		return failedResult(resources, errorOpenStack, message)
	}
	if errors.Is(err, ErrNetworkCreate) || errors.Is(err, ErrSubnetCreate) || errors.Is(err, ErrRouterCreate) || errors.Is(err, ErrRouterInterface) || errors.Is(err, ErrSecurityGroupCreate) || errors.Is(err, ErrSecurityRuleCreate) || errors.Is(err, ErrPortCreate) || errors.Is(err, ErrServerCreate) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return unknownResult(resources, message)
	}
	return failedResult(resources, errorOpenStack, message)
}

func preflightFailure(resources []coreprovider.ResourceResult, err error, message string) coreprovider.OperationResult {
	if errors.Is(err, ErrQuotaExceeded) {
		return failedResult(resources, errorQuotaExceeded, "OpenStack resource quota is insufficient")
	}
	return failedResult(resources, errorOpenStack, message)
}

func failedResult(resources []coreprovider.ResourceResult, code, message string) coreprovider.OperationResult {
	return coreprovider.OperationResult{
		Outcome:           coreprovider.OutcomeFailed,
		ProviderResources: cloneResourceResults(resources),
		Error:             &coreprovider.SafeError{Code: code, Message: message},
	}
}

func unknownResult(resources []coreprovider.ResourceResult, message string) coreprovider.OperationResult {
	return coreprovider.OperationResult{
		Outcome:           coreprovider.OutcomeUnknown,
		ProviderResources: cloneResourceResults(resources),
		Error:             &coreprovider.SafeError{Code: errorUnknown, Message: message},
	}
}

func upsertResource(resources []coreprovider.ResourceResult, resource coreprovider.ResourceResult) []coreprovider.ResourceResult {
	for index := range resources {
		if resources[index].ResourceType == resource.ResourceType && resources[index].ProviderID == resource.ProviderID {
			resources[index] = resource
			return resources
		}
	}
	return append(resources, resource)
}

func cloneResourceResults(resources []coreprovider.ResourceResult) []coreprovider.ResourceResult {
	if resources == nil {
		return []coreprovider.ResourceResult{}
	}
	result := make([]coreprovider.ResourceResult, len(resources))
	copy(result, resources)
	return result
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
