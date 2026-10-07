package openstackprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/quotasets"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func (a *Adapter) validateProvisionSettings(config ProvisionConfig, snapshot coreprovider.CreationSnapshot) error {
	if a.ktNetwork == nil {
		if config.KTCloudConnectorTierID != "" || config.KTCloudManagementCIDR != "" {
			return ErrProvisionConfig
		}
		return validateProvisionSettings(config, snapshot)
	}
	lab, le := netip.ParsePrefix(config.LabSubnetCIDR)
	management, me := netip.ParsePrefix(config.KTCloudManagementCIDR)
	source, se := netip.ParsePrefix(config.SSHAllowedCIDR)
	if len(snapshot.VMs) != 1 || snapshot.InternetOutbound || config.ProjectID == "" || config.KTCloudConnectorTierID == "" || a.volume == nil || config.ManagementNetworkID != "" || config.ManagementSecurityGroupID != "" || config.ExternalNetworkID != "" || le != nil || me != nil || se != nil || !lab.Addr().Is4() || !management.Addr().Is4() || !source.Addr().Is4() || lab.Bits() != 24 || management.Bits() != 24 || source.Bits() != 32 || lab != lab.Masked() || management != management.Masked() || lab.Overlaps(management) || config.ProviderConnectionID == "" || snapshot.ProviderConnectionID != config.ProviderConnectionID || config.KeyPairName == "" || config.SSHUsername == "" || config.SSHPrivateKeyFile == "" || config.SSHKnownHostsFile == "" {
		return ErrProvisionConfig
	}
	if snapshot.StartupScript != nil && base64.StdEncoding.EncodedLen(len(snapshot.StartupScript.Content)) > 2048 {
		return ErrProvisionConfig
	}
	return nil
}

func (a *Adapter) preflightKTCloudProvision(ctx context.Context, config ProvisionConfig, snapshot coreprovider.CreationSnapshot, credit quotaUsage) error {
	projectResult, ok := a.provider.GetAuthResult().(interface {
		ExtractProject() (*tokens.Project, error)
	})
	if !ok {
		return ErrProvisionCheck
	}
	project, err := projectResult.ExtractProject()
	if err != nil || project == nil || project.ID != config.ProjectID {
		return ErrProvisionCheck
	}
	tiers, err := a.ktNetwork.tiers(ctx)
	if err != nil {
		return ErrProvisionCheck
	}
	source, _ := netip.ParsePrefix(config.SSHAllowedCIDR)
	lab, _ := netip.ParsePrefix(config.LabSubnetCIDR)
	management, _ := netip.ParsePrefix(config.KTCloudManagementCIDR)
	connectorFound := false
	for _, tier := range tiers {
		prefix, e := netip.ParsePrefix(tier.CIDR)
		if tier.ID == config.KTCloudConnectorTierID {
			if e != nil || normalizeStatus(tier.Status) != "ACTIVE" || tier.RefID == "" || !prefix.Contains(source.Addr()) {
				return ErrProvisionCheck
			}
			connectorFound = true
		}
		if e != nil {
			if tier.RefID != "" {
				return ErrProvisionCheck
			}
			continue
		}
		if _, owned := credit.ktTiers[tier.ID]; owned {
			continue
		}
		if prefix.Overlaps(lab) || prefix.Overlaps(management) {
			return ErrProvisionCheck
		}
	}
	if !connectorFound {
		return ErrProvisionCheck
	}
	policies, err := a.ktNetwork.policies(ctx)
	if err != nil {
		return ErrProvisionCheck
	}
	for _, policy := range policies {
		if ktPolicyEnabled(policy) && strings.EqualFold(policy.Action, "accept") && ktWildcardInterfaces(policy.Destination) {
			return ErrProvisionCheck
		}
	}
	if err = a.preflightImagesFlavorsKey(ctx, config, snapshot); err != nil {
		return err
	}
	quota, err := quotasets.GetDetail(ctx, a.compute, config.ProjectID).Extract()
	if err != nil {
		return ErrQuotaLookup
	}
	required := quotaRequired(snapshot)
	if !computeQuotaAvailable(quota.Instances, required.instances, credit.instances) || !computeQuotaAvailable(quota.Cores, required.cores, credit.cores) || !computeQuotaAvailable(quota.RAM, required.ramMiB, credit.ramMiB) {
		return ErrQuotaExceeded
	}
	// NSM has no documented quota endpoint and KT's Cinder quota route returns
	// 500. Neither is treated as an unlimited quota or a successful quota check.
	return nil
}

func ktPolicyEnabled(policy ktCloudFirewallPolicy) bool {
	return !strings.EqualFold(policy.Status, "disable")
}
func ktWildcardInterfaces(items []ktCloudInterface) bool {
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		if item.ID == "" || strings.EqualFold(item.Name, "any") || strings.EqualFold(item.Name, "all") {
			return true
		}
	}
	return false
}

func ktResource(identity ResourceIdentity, kind, id, state string) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{ResourceRef: coreprovider.ResourceRef{ResourceType: kind, ProviderID: id, Generation: identity.Generation, LogicalName: identity.LogicalName}, ObservedState: normalizeStatus(state)}
}

func (a *Adapter) createKTCloudTier(ctx context.Context, identity ResourceIdentity, name, cidr string, resources []coreprovider.ResourceResult) (ktCloudTier, []coreprovider.ResourceResult, error) {
	inventory, err := a.ktNetwork.tiers(ctx)
	if err != nil {
		return ktCloudTier{}, resources, err
	}
	for _, item := range inventory {
		if item.Name == name || item.CIDR == cidr {
			return ktCloudTier{}, resources, ErrResourceOwnership
		}
	}
	receipt, err := a.ktNetwork.createTier(ctx, name, cidr)
	if receipt.Data.ID != "" {
		resources = upsertResource(resources, ktResource(identity, coreprovider.ResourceTypeTier, receipt.Data.ID, "BUILD"))
	}
	if err != nil {
		return ktCloudTier{}, resources, err
	}
	wait, cancel := context.WithTimeout(ctx, a.provision.ActiveTimeout)
	defer cancel()
	for {
		item, exists, e := a.ktNetwork.tier(wait, receipt.Data.ID)
		if e != nil {
			return ktCloudTier{}, resources, ErrNetworkCreate
		}
		if exists {
			resources = upsertResource(resources, ktResource(identity, coreprovider.ResourceTypeTier, item.ID, item.Status))
			if item.Name != name || item.CIDR != cidr || item.Shared {
				return ktCloudTier{}, resources, ErrNetworkCreate
			}
			if normalizeStatus(item.Status) == "ACTIVE" && item.RefID != "" {
				return item, resources, nil
			}
			if normalizeStatus(item.Status) == "ERROR" {
				return ktCloudTier{}, resources, ErrNetworkCreate
			}
		}
		select {
		case <-wait.Done():
			return ktCloudTier{}, resources, errors.Join(ErrNetworkCreate, wait.Err())
		case <-time.After(a.provision.PollInterval):
		}
	}
}

func (a *Adapter) prepareKTCloudNetwork(ctx context.Context, request coreprovider.ProvisionRequest, config ProvisionConfig) (provisionTopology, []coreprovider.ResourceResult, error) {
	tier, resources, err := a.createKTCloudTier(ctx, resourceIdentity(request.Generation, "lab-tier"), provisionBaseName(request.LabInstanceID, request.Generation)+"-lab-tier", config.LabSubnetCIDR, nil)
	return provisionTopology{tier: tier}, resources, err
}

func (a *Adapter) prepareKTCloudNICs(ctx context.Context, request coreprovider.ProvisionRequest, vm coreprovider.VMSpec, config ProvisionConfig, topology provisionTopology, resources []coreprovider.ResourceResult) (provisionNICs, []coreprovider.ResourceResult, error) {
	baseName := provisionBaseName(request.LabInstanceID, request.Generation)
	tierName := baseName + "-" + safeName(vm.VMKey, 28) + "-management-tier"
	tier, resources, err := a.createKTCloudTier(ctx, resourceIdentity(request.Generation, vm.VMKey+":management-tier"), tierName, config.KTCloudManagementCIDR, resources)
	if err != nil {
		return provisionNICs{}, resources, err
	}
	comment := baseName + ":" + vm.VMKey + ":management-ssh"
	policies, err := a.ktNetwork.policies(ctx)
	if err != nil {
		return provisionNICs{}, resources, err
	}
	for _, policy := range policies {
		if policy.Comment == comment {
			return provisionNICs{}, resources, ErrResourceOwnership
		}
	}
	spec := ktCloudFirewallSpec{Accept: true, Protocol: "TCP", Start: "22", End: "22", Source: []string{config.KTCloudConnectorTierID}, Destination: []string{tier.ID}, SourceIPs: []string{config.SSHAllowedCIDR}, Comment: comment}
	receipt, err := a.ktNetwork.createPolicy(ctx, spec)
	if err != nil {
		return provisionNICs{}, resources, err
	}
	wait, cancel := context.WithTimeout(ctx, a.provision.ActiveTimeout)
	defer cancel()
	for {
		state, e := a.ktNetwork.job(wait, receipt.JobID)
		if e != nil {
			return provisionNICs{}, resources, ErrSecurityRuleCreate
		}
		if state == "FAILED" || state == "FAILURE" {
			return provisionNICs{}, resources, ErrSecurityRuleCreate
		}
		if state == "SUCCESS" {
			break
		}
		select {
		case <-wait.Done():
			return provisionNICs{}, resources, errors.Join(ErrSecurityRuleCreate, wait.Err())
		case <-time.After(a.provision.PollInterval):
		}
	}
	policies, err = a.ktNetwork.policies(ctx)
	if err != nil {
		return provisionNICs{}, resources, ErrSecurityRuleCreate
	}
	matches := []ktCloudFirewallPolicy{}
	for _, policy := range policies {
		if policy.Comment == comment {
			matches = append(matches, policy)
		}
	}
	if len(matches) != 1 || matches[0].ID == "" {
		return provisionNICs{}, resources, ErrSecurityRuleCreate
	}
	resources = upsertResource(resources, ktResource(resourceIdentity(request.Generation, vm.VMKey+":management-ssh"), coreprovider.ResourceTypeFirewall, matches[0].ID, "PRESENT"))
	if !ktSSHPolicyMatches(matches[0], config, tier.ID) {
		return provisionNICs{}, resources, ErrSecurityRuleCreate
	}
	return provisionNICs{networkIDs: []string{tier.RefID, topology.tier.RefID}, managementTier: tier}, resources, nil
}

func ktSSHPolicyMatches(policy ktCloudFirewallPolicy, config ProvisionConfig, tierID string) bool {
	return strings.EqualFold(policy.Action, "accept") && ktPolicyEnabled(policy) && len(policy.Source) == 1 && policy.Source[0].ID == config.KTCloudConnectorTierID && len(policy.Destination) == 1 && policy.Destination[0].ID == tierID && len(policy.SourceIPs) == 1 && policy.SourceIPs[0].Name == config.SSHAllowedCIDR && len(policy.Services) == 1 && strings.EqualFold(policy.Services[0].Protocol, "TCP") && policy.Services[0].Start == "22" && policy.Services[0].End == "22" && strings.EqualFold(policy.NAT, "disable") && len(policy.DestinationIPs) == 1 && strings.EqualFold(policy.DestinationIPs[0].Name, "all")
}

func (a *Adapter) createKTCloudServer(ctx context.Context, opts servers.CreateOptsBuilder) (*servers.Server, error) {
	body, err := opts.ToServerCreateMap()
	if err != nil {
		return nil, ErrInvalidResourceSpec
	}
	serverMap, ok := body["server"].(map[string]any)
	if !ok {
		return nil, ErrInvalidResourceSpec
	}
	delete(serverMap, "imageRef")
	devices, ok := serverMap["block_device_mapping_v2"].([]any)
	if !ok || len(devices) != 1 {
		return nil, ErrInvalidResourceSpec
	}
	device, ok := devices[0].(map[string]any)
	if !ok {
		return nil, ErrInvalidResourceSpec
	}
	device["boot_index"] = "0"
	var result servers.CreateResult
	response, err := a.compute.Post(ctx, a.compute.ServiceURL("servers"), body, &result.Body, &gophercloud.RequestOpts{OkCodes: []int{200, 202}})
	_, result.Header, result.Err = gophercloud.ParseResponse(response, err)
	if err != nil {
		return nil, err
	}
	envelope, ok := result.Body.(map[string]any)
	if !ok {
		return nil, ErrServerCreate
	}
	for _, key := range []string{"computeFault", "badRequest", "forbidden", "overLimit"} {
		if fault, exists := envelope[key]; exists {
			fields, ok := fault.(map[string]any)
			if !ok {
				return nil, ErrServerCreate
			}
			raw, err := json.Marshal(fields["code"])
			var code ktCloudStatus
			if err != nil || json.Unmarshal(raw, &code) != nil || code < 400 || code > 599 {
				return nil, ErrServerCreate
			}
			return nil, gophercloud.ErrUnexpectedResponseCode{Actual: int(code)}
		}
	}
	server, err := result.Extract()
	if err != nil || server == nil || server.ID == "" {
		return nil, ErrServerCreate
	}
	return server, nil
}

func (a *Adapter) refreshKTCloudNICs(ctx context.Context, request coreprovider.ProvisionRequest, vm coreprovider.VMSpec, nics provisionNICs, serverID string, resources []coreprovider.ResourceResult) (string, []coreprovider.ResourceResult, error) {
	server, err := a.lookupServer(ctx, serverID)
	if err != nil || server == nil || server.ID != serverID {
		return "", resources, ErrServerGet
	}
	for _, attachment := range server.AttachedVolumes {
		if attachment.ID != "" {
			resources = upsertResource(resources, ktResource(resourceIdentity(request.Generation, vm.VMKey+":root-volume"), coreprovider.ResourceTypeVolume, attachment.ID, "PRESENT"))
		}
	}
	if len(server.AttachedVolumes) != 1 {
		return "", resources, ErrReconcileLookup
	}
	root, err := volumes.Get(ctx, a.volume, server.AttachedVolumes[0].ID).Extract()
	if err != nil || root == nil || root.ID != server.AttachedVolumes[0].ID || root.VolumeImageMetadata["image_id"] != vm.ImageID || root.Size != 50 || len(root.Attachments) != 1 || root.Attachments[0].ServerID != serverID {
		return "", resources, ErrReconcileLookup
	}
	tier, exists, err := a.ktNetwork.tier(ctx, nics.managementTier.ID)
	if err != nil || !exists || tier.RefID != nics.managementTier.RefID || tier.Name != nics.managementTier.Name || tier.CIDR != a.provision.KTCloudManagementCIDR {
		return "", resources, ErrManagementIP
	}
	address, err := ktServerManagementAddress(*server, tier)
	return address, resources, err
}

func ktServerManagementAddress(server servers.Server, tier ktCloudTier) (string, error) {
	prefix, err := netip.ParsePrefix(tier.CIDR)
	if err != nil || tier.Name == "" || tier.RefID == "" {
		return "", ErrManagementIP
	}
	var address string
	for networkName, raw := range server.Addresses {
		if networkName != tier.Name {
			continue
		}
		b, e := json.Marshal(raw)
		if e != nil {
			return "", ErrManagementIP
		}
		var entries []struct {
			IP   string `json:"addr"`
			Type string `json:"OS-EXT-IPS:type"`
		}
		if json.Unmarshal(b, &entries) != nil {
			return "", ErrManagementIP
		}
		for _, entry := range entries {
			ip, e := netip.ParseAddr(entry.IP)
			if e != nil || !ip.Is4() || !prefix.Contains(ip) || entry.Type != "fixed" || ip == prefix.Addr() || ip == prefix.Addr().Next() {
				continue
			}
			if address != "" {
				return "", ErrManagementIP
			}
			address = ip.String()
		}
	}
	if address == "" {
		return "", ErrManagementIP
	}
	return address, nil
}
