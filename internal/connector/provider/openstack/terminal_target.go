package openstackprovider

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var _ coreprovider.ServerAddressResolver = (*Adapter)(nil)

// ResolveServerAddress adapts the main Terminal boundary to Provider-owned
// management NIC lookup. It never trusts Nova floating/lab addresses or an IP
// supplied by Control. The caller owns envelope LabInstance/generation affinity;
// this inherited interface supplies only the VM key and resolved Server ID.
func (a *Adapter) ResolveServerAddress(ctx context.Context, targetVmKey, serverID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	targetVmKey, serverID = strings.TrimSpace(targetVmKey), strings.TrimSpace(serverID)
	if !adapterAvailable(a) || targetVmKey == "" || serverID == "" || (a.ktNetwork == nil && a.provision.ManagementNetworkID == "") || a.provision.ProjectID == "" {
		return "", ErrInvalidResourceSpec
	}
	server, err := a.lookupServer(ctx, serverID)
	if err != nil {
		return "", safeContextError(ctx, ErrServerGet)
	}
	generation, err := strconv.ParseInt(server.Metadata["labbit_generation"], 10, 64)
	labID := strings.TrimSpace(server.Metadata["labbit_lab_instance_id"])
	if err != nil || generation < 1 || labID == "" || strings.TrimSpace(server.Metadata["labbit_operation_id"]) == "" ||
		server.ID != serverID || server.TenantID != a.provision.ProjectID || server.Metadata["labbit_vm_key"] != targetVmKey ||
		server.Name != provisionBaseName(labID, generation)+"-"+safeName(targetVmKey, 28) || normalizeStatus(server.Status) != "ACTIVE" {
		return "", ErrResourceOwnership
	}
	if a.ktNetwork != nil {
		expectedName := provisionBaseName(labID, generation) + "-" + safeName(targetVmKey, 28) + "-management-tier"
		items, err := a.ktNetwork.tiers(ctx)
		if err != nil {
			return "", ErrManagementIP
		}
		matches := []ktCloudTier{}
		for _, tier := range items {
			if tier.Name == expectedName && tier.CIDR == a.provision.KTCloudManagementCIDR && !tier.Shared && normalizeStatus(tier.Status) == "ACTIVE" {
				matches = append(matches, tier)
			}
		}
		if len(matches) != 1 {
			return "", ErrManagementIP
		}
		return ktServerManagementAddress(*server, matches[0])
	}
	pages, err := ports.List(a.network, ports.ListOpts{DeviceID: serverID, NetworkID: a.provision.ManagementNetworkID}).AllPages(ctx)
	if err != nil {
		return "", safeContextError(ctx, ErrPortList)
	}
	items, err := ports.ExtractPorts(pages)
	if err != nil {
		return "", ErrPortList
	}
	if len(items) != 1 {
		return "", ErrManagementIP
	}
	port := items[0]
	if port.DeviceID != serverID || port.NetworkID != a.provision.ManagementNetworkID || port.TenantID != a.provision.ProjectID ||
		!strings.HasPrefix(port.DeviceOwner, "compute:") {
		return "", ErrResourceOwnership
	}
	// Multiple usable IPv4 addresses are ambiguous; do not pick an arbitrary one.
	var address string
	for _, fixedIP := range port.FixedIPs {
		ip := net.ParseIP(strings.TrimSpace(fixedIP.IPAddress))
		if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
			continue
		}
		if address != "" {
			return "", ErrManagementIP
		}
		address = ip.String()
	}
	if address == "" {
		return "", ErrManagementIP
	}
	return address, nil
}

// SSHHostKeyCallback verifies an already enrolled ProviderConnection/Server pin.
// Terminal access does not enroll unknown keys or fall back to IP-based trust.
func (a *Adapter) SSHHostKeyCallback(ctx context.Context, serverID string) (ssh.HostKeyCallback, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil {
		return nil, ErrSSHNotReady
	}
	identity, err := sshHostKeyIdentity(a.provision.ProviderConnectionID, serverID)
	if err != nil {
		return nil, ErrSSHNotReady
	}
	knownHostsMu.Lock()
	verify, err := knownhosts.New(a.provision.SSHKnownHostsFile)
	knownHostsMu.Unlock()
	if err != nil {
		return nil, ErrSSHNotReady
	}
	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
		if err := verify(identity, remote, key); err != nil {
			return ErrSSHNotReady
		}
		return nil
	}, nil
}
