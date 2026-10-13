package openstackprovider

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

var (
	ErrServerAmbiguous = errors.New("OpenStack server name is ambiguous")
	ErrServerConflict  = errors.New("OpenStack server does not match the requested specification")
	ErrServerCreate    = errors.New("OpenStack server creation failed")
	ErrServerGet       = errors.New("OpenStack server lookup failed")
	ErrServerBoot      = errors.New("OpenStack server entered an error state")
	ErrServerWait      = errors.New("OpenStack server did not become active")
	ErrManagementIP    = errors.New("OpenStack management port has no usable IP address")
	ErrSSHNotReady     = errors.New("workspace VM SSH did not become ready")
)

type ServerSpec struct {
	Name       string
	ImageID    string
	FlavorID   string
	KeyPair    string
	PortIDs    []string
	NetworkIDs []string
	UserData   []byte
	Metadata   map[string]string
}

func (a *Adapter) EnsureServer(
	ctx context.Context,
	identity ResourceIdentity,
	spec ServerSpec,
) (coreprovider.ResourceResult, servers.Server, error) {
	if a == nil || a.compute == nil {
		return coreprovider.ResourceResult{}, servers.Server{}, ErrClientUnavailable
	}
	name := strings.TrimSpace(spec.Name)
	imageID := strings.TrimSpace(spec.ImageID)
	flavorID := strings.TrimSpace(spec.FlavorID)
	keyPair := strings.TrimSpace(spec.KeyPair)
	portIDs := normalizedOrderedIDs(spec.PortIDs)
	networkIDs := normalizedOrderedIDs(spec.NetworkIDs)
	if !validIdentity(identity) || name == "" || imageID == "" || flavorID == "" || keyPair == "" || (len(portIDs) == 0 && len(networkIDs) == 0) || (len(networkIDs) != 0 && (a.ktNetwork == nil || len(portIDs) != 0)) {
		return coreprovider.ResourceResult{}, servers.Server{}, ErrInvalidResourceSpec
	}

	items, err := a.listServerItems(ctx, servers.ListOpts{Name: name})
	if err != nil {
		return coreprovider.ResourceResult{}, servers.Server{}, safeContextError(ctx, ErrServerList)
	}
	exact := make([]servers.Server, 0, len(items))
	for _, item := range items {
		if item.Name == name {
			exact = append(exact, item)
		}
	}
	switch len(exact) {
	case 1:
		if len(networkIDs) != 0 {
			return coreprovider.ResourceResult{}, servers.Server{}, ErrResourceOwnership
		}
		detail, err := a.lookupServer(ctx, exact[0].ID)
		if err != nil || detail == nil {
			return coreprovider.ResourceResult{}, servers.Server{}, safeContextError(ctx, ErrServerGet)
		}
		attachedPortIDs, err := a.serverPortIDs(ctx, detail.ID)
		if err != nil {
			return coreprovider.ResourceResult{}, servers.Server{}, err
		}
		if !serverMatches(*detail, imageID, flavorID, keyPair, spec.Metadata) || !slices.Equal(normalizedIDs(attachedPortIDs), normalizedIDs(portIDs)) {
			return coreprovider.ResourceResult{}, servers.Server{}, ErrServerConflict
		}
		return coreprovider.ResourceResult{}, servers.Server{}, ErrResourceOwnership
	case 0:
		// Continue to create.
	default:
		return coreprovider.ResourceResult{}, servers.Server{}, ErrServerAmbiguous
	}

	networks := make([]servers.Network, 0, len(portIDs))
	for _, portID := range portIDs {
		networks = append(networks, servers.Network{Port: portID})
	}
	for _, networkID := range networkIDs {
		networks = append(networks, servers.Network{UUID: networkID})
	}
	serverOpts := servers.CreateOpts{Name: name, ImageRef: imageID, FlavorRef: flavorID, Networks: networks, UserData: append([]byte(nil), spec.UserData...), Metadata: cloneStringMap(spec.Metadata)}
	if len(networkIDs) != 0 {
		serverOpts.ImageRef = ""
		serverOpts.AvailabilityZone = "DX-M1"
		serverOpts.BlockDevice = []servers.BlockDevice{{SourceType: servers.SourceImage, UUID: imageID, DestinationType: servers.DestinationVolume, BootIndex: 0, VolumeSize: 50, DeleteOnTermination: true}}
	}
	createOpts := keypairs.CreateOptsExt{
		CreateOptsBuilder: serverOpts,
		KeyName:           keyPair,
	}
	var created *servers.Server
	if len(networkIDs) != 0 {
		created, err = a.createKTCloudServer(ctx, createOpts)
	} else {
		created, err = servers.Create(ctx, a.compute, createOpts, nil).Extract()
	}
	if err != nil || created == nil || created.ID == "" {
		return coreprovider.ResourceResult{}, servers.Server{}, safeMutationError(ctx, err, ErrServerCreate)
	}
	return serverResource(identity, *created), *created, nil
}

func (a *Adapter) serverPortIDs(ctx context.Context, serverID string) ([]string, error) {
	if a == nil || a.network == nil {
		return nil, ErrClientUnavailable
	}
	pages, err := ports.List(a.network, ports.ListOpts{DeviceID: serverID}).AllPages(ctx)
	if err != nil {
		return nil, safeContextError(ctx, ErrPortList)
	}
	items, err := ports.ExtractPorts(pages)
	if err != nil {
		return nil, ErrPortList
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.ID)
	}
	return result, nil
}

func (a *Adapter) waitServerActive(ctx context.Context, identity ResourceIdentity, serverID string) (coreprovider.ResourceResult, error) {
	if a == nil || a.compute == nil {
		return coreprovider.ResourceResult{}, ErrClientUnavailable
	}
	ticker := time.NewTicker(a.provision.PollInterval)
	defer ticker.Stop()
	for {
		item, err := a.lookupServer(ctx, serverID)
		if err != nil || item == nil || item.ID != serverID {
			return coreprovider.ResourceResult{}, safeContextError(ctx, ErrServerGet)
		}
		resource := serverResource(identity, *item)
		switch resource.ObservedState {
		case "ACTIVE":
			return resource, nil
		case "ERROR":
			return resource, ErrServerBoot
		}
		select {
		case <-ctx.Done():
			return resource, errors.Join(ErrServerWait, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (a *Adapter) waitSSHReady(ctx context.Context, port ports.Port, serverID string) error {
	address, err := managementAddress(port)
	if err != nil {
		return err
	}
	return a.waitSSHReadyAddress(ctx, address, serverID)
}

func (a *Adapter) waitSSHReadyAddress(ctx context.Context, address, serverID string) error {
	probe := a.sshProbe
	if probe == nil {
		hostIdentity, identityErr := sshHostKeyIdentity(a.provision.ProviderConnectionID, serverID)
		if identityErr != nil {
			return ErrSSHNotReady
		}
		probe = func(probeContext context.Context, probeAddress string) error {
			if err := a.runSSHCommand(probeContext, probeAddress, hostIdentity, "true"); err != nil {
				return ErrSSHNotReady
			}
			return nil
		}
	}
	ticker := time.NewTicker(a.provision.PollInterval)
	defer ticker.Stop()
	for {
		if err := probe(ctx, net.JoinHostPort(address, "22")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrSSHNotReady, ctx.Err())
		case <-ticker.C:
		}
	}
}

func managementAddress(port ports.Port) (string, error) {
	for _, fixedIP := range port.FixedIPs {
		address := net.ParseIP(strings.TrimSpace(fixedIP.IPAddress))
		if address != nil && address.To4() != nil {
			return address.String(), nil
		}
	}
	return "", ErrManagementIP
}

func serverResource(identity ResourceIdentity, item servers.Server) coreprovider.ResourceResult {
	return coreprovider.ResourceResult{
		ResourceRef: coreprovider.ResourceRef{
			ResourceType: coreprovider.ResourceTypeServer,
			ProviderID:   item.ID,
			Generation:   identity.Generation,
			LogicalName:  identity.LogicalName,
		},
		ObservedState: normalizeStatus(item.Status),
	}
}

func metadataContains(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func serverMatches(item servers.Server, imageID, flavorID, keyPair string, metadata map[string]string) bool {
	actualImageID, imageOK := item.Image["id"].(string)
	actualFlavorID, flavorOK := item.Flavor["id"].(string)
	return imageOK && flavorOK && actualImageID == imageID && actualFlavorID == flavorID && item.KeyName == keyPair && metadataContains(item.Metadata, metadata)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func normalizedOrderedIDs(values []string) []string {
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
	return result
}
