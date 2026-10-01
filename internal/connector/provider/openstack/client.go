package openstackprovider

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	openstackconfig "github.com/gophercloud/gophercloud/v2/openstack/config"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
)

var (
	ErrConfigRequired        = errors.New("OpenStack provider config file and cloud name are required")
	ErrConfigUnavailable     = errors.New("OpenStack provider config file is not accessible")
	ErrConfigInvalid         = errors.New("OpenStack provider config is invalid")
	ErrInsecureTLS           = errors.New("OpenStack provider config disables TLS certificate verification")
	ErrAuthentication        = errors.New("OpenStack authentication failed")
	ErrServiceUnavailable    = errors.New("OpenStack service catalog is unavailable")
	ErrClientUnavailable     = errors.New("OpenStack provider client is unavailable")
	ErrConnectionUnavailable = errors.New("OpenStack connection validation is unavailable")
	ErrMutationRejected      = errors.New("OpenStack rejected the mutation request")
)

// Config identifies a single cloud entry in the customer-local clouds.yaml.
// The file may contain credentials and must remain outside the repository.
type Config struct {
	File      string
	CloudName string
	Provision ProvisionConfig
}

// ProvisionConfig contains customer-local infrastructure choices. These are
// deployment inputs rather than Connector wire fields, so they remain outside
// CreationSnapshot and can differ for each Provider connection.
type ProvisionConfig struct {
	ProviderConnectionID      string
	ProjectID                 string
	ManagementNetworkID       string
	ManagementSecurityGroupID string
	ExternalNetworkID         string
	KeyPairName               string
	SSHAllowedCIDR            string
	LabSubnetCIDR             string
	SSHUsername               string
	SSHPrivateKeyFile         string
	SSHKnownHostsFile         string
	ActiveTimeout             time.Duration
	SSHReadyTimeout           time.Duration
	StartupReadyTimeout       time.Duration
	PollInterval              time.Duration
}

const (
	EnvProviderConfigFile      = "LABBIT_PROVIDER_CONFIG_FILE"
	EnvCloudName               = "OS_CLOUD"
	EnvProviderConnection      = "LABBIT_PROVIDER_CONNECTION_ID"
	EnvProjectID               = "OS_PROJECT_ID"
	EnvManagementNetwork       = "LABBIT_OPENSTACK_MANAGEMENT_NETWORK_ID"
	EnvManagementSecurityGroup = "LABBIT_OPENSTACK_MANAGEMENT_SECURITY_GROUP_ID"
	EnvExternalNetwork         = "LABBIT_OPENSTACK_EXTERNAL_NETWORK_ID"
	EnvKeyPairName             = "LABBIT_OPENSTACK_KEYPAIR_NAME"
	EnvSSHAllowedCIDR          = "LABBIT_OPENSTACK_SSH_ALLOWED_CIDR"
	EnvLabSubnetCIDR           = "LABBIT_OPENSTACK_LAB_SUBNET_CIDR"
	EnvSSHUsername             = "LABBIT_OPENSTACK_SSH_USERNAME"
	EnvSSHPrivateKeyFile       = "LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE"
	EnvSSHKnownHostsFile       = "LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE"
)

// ConfigFromEnvironment uses Labbit's provider-config path and OpenStack's
// standard cloud selector. It reads names and paths only; secret values stay
// inside the customer-local clouds.yaml/secure.yaml boundary.
func ConfigFromEnvironment() Config {
	return Config{
		File:      strings.TrimSpace(os.Getenv(EnvProviderConfigFile)),
		CloudName: strings.TrimSpace(os.Getenv(EnvCloudName)),
		Provision: ProvisionConfig{
			ProviderConnectionID:      strings.TrimSpace(os.Getenv(EnvProviderConnection)),
			ProjectID:                 strings.TrimSpace(os.Getenv(EnvProjectID)),
			ManagementNetworkID:       strings.TrimSpace(os.Getenv(EnvManagementNetwork)),
			ManagementSecurityGroupID: strings.TrimSpace(os.Getenv(EnvManagementSecurityGroup)),
			ExternalNetworkID:         strings.TrimSpace(os.Getenv(EnvExternalNetwork)),
			KeyPairName:               strings.TrimSpace(os.Getenv(EnvKeyPairName)),
			SSHAllowedCIDR:            strings.TrimSpace(os.Getenv(EnvSSHAllowedCIDR)),
			LabSubnetCIDR:             strings.TrimSpace(os.Getenv(EnvLabSubnetCIDR)),
			SSHUsername:               strings.TrimSpace(os.Getenv(EnvSSHUsername)),
			SSHPrivateKeyFile:         strings.TrimSpace(os.Getenv(EnvSSHPrivateKeyFile)),
			SSHKnownHostsFile:         strings.TrimSpace(os.Getenv(EnvSSHKnownHostsFile)),
		},
	}
}

// Adapter owns authenticated OpenStack clients and customer-local Provision
// settings. Credentials stay inside Gophercloud's ProviderClient.
type Adapter struct {
	provider     *gophercloud.ProviderClient
	image        *gophercloud.ServiceClient
	compute      *gophercloud.ServiceClient
	network      *gophercloud.ServiceClient
	provision    ProvisionConfig
	sshProbe     func(context.Context, string) error
	startupProbe func(context.Context, string, string) error
}

// ProviderConnectionID returns the SaaS ProviderConnection affinity configured
// for this customer-local Connector process.
func (a *Adapter) ProviderConnectionID() string {
	if a == nil {
		return ""
	}
	return a.provision.ProviderConnectionID
}

// New authenticates with Keystone and resolves the Glance, Nova, and Neutron
// endpoints needed by M1/M2. Returned errors are safe to log and never include
// credentials, tokens, provider response bodies, or endpoint details.
func New(ctx context.Context, cfg Config) (*Adapter, error) {
	auth, endpoint, tlsConfig, err := loadConfig(cfg)
	if err != nil {
		return nil, err
	}

	providerClient, err := openstackconfig.NewProviderClient(
		ctx,
		auth,
		openstackconfig.WithTLSConfig(tlsConfig),
	)
	if err != nil {
		return nil, safeContextError(ctx, ErrAuthentication)
	}

	imageClient, err := openstack.NewImageV2(providerClient, endpoint)
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	computeClient, err := openstack.NewComputeV2(providerClient, endpoint)
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	networkClient, err := openstack.NewNetworkV2(providerClient, endpoint)
	if err != nil {
		return nil, ErrServiceUnavailable
	}

	adapter := newAdapter(providerClient, imageClient, computeClient, networkClient)
	adapter.provision = normalizedProvisionConfig(cfg.Provision)
	return adapter, nil
}

func normalizedProvisionConfig(config ProvisionConfig) ProvisionConfig {
	config.ProviderConnectionID = strings.TrimSpace(config.ProviderConnectionID)
	config.ProjectID = strings.TrimSpace(config.ProjectID)
	config.ManagementNetworkID = strings.TrimSpace(config.ManagementNetworkID)
	config.ManagementSecurityGroupID = strings.TrimSpace(config.ManagementSecurityGroupID)
	config.ExternalNetworkID = strings.TrimSpace(config.ExternalNetworkID)
	config.KeyPairName = strings.TrimSpace(config.KeyPairName)
	config.SSHAllowedCIDR = strings.TrimSpace(config.SSHAllowedCIDR)
	config.LabSubnetCIDR = strings.TrimSpace(config.LabSubnetCIDR)
	config.SSHUsername = strings.TrimSpace(config.SSHUsername)
	config.SSHPrivateKeyFile = strings.TrimSpace(config.SSHPrivateKeyFile)
	config.SSHKnownHostsFile = strings.TrimSpace(config.SSHKnownHostsFile)
	if config.ActiveTimeout <= 0 {
		config.ActiveTimeout = 5 * time.Minute
	}
	if config.SSHReadyTimeout <= 0 {
		config.SSHReadyTimeout = 3 * time.Minute
	}
	if config.StartupReadyTimeout <= 0 {
		config.StartupReadyTimeout = 5 * time.Minute
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 2 * time.Second
	}
	return config
}

func loadConfig(cfg Config) (gophercloud.AuthOptions, gophercloud.EndpointOpts, *tls.Config, error) {
	file := strings.TrimSpace(cfg.File)
	cloudName := strings.TrimSpace(cfg.CloudName)
	if file == "" || cloudName == "" {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, nil, ErrConfigRequired
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, nil, ErrConfigUnavailable
	}

	auth, endpoint, tlsConfig, err := clouds.Parse(
		clouds.WithLocations(file),
		clouds.WithCloudName(cloudName),
	)
	if err != nil {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, nil, ErrConfigInvalid
	}
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		return gophercloud.AuthOptions{}, gophercloud.EndpointOpts{}, nil, ErrInsecureTLS
	}
	return auth, endpoint, tlsConfig, nil
}

func newAdapter(
	providerClient *gophercloud.ProviderClient,
	imageClient *gophercloud.ServiceClient,
	computeClient *gophercloud.ServiceClient,
	networkClient *gophercloud.ServiceClient,
) *Adapter {
	return &Adapter{
		provider:  providerClient,
		image:     imageClient,
		compute:   computeClient,
		network:   networkClient,
		provision: normalizedProvisionConfig(ProvisionConfig{}),
	}
}

// ValidateConnection checks the current token against Keystone rather than
// treating a cached token as proof of connectivity. It is read-only and does
// not claim that Nova, Neutron, or Glance are healthy. SDK reauthentication, when
// configured, is preserved; a refreshed subject token is checked once more, bounded by
// the same deadline. Provider response bodies and endpoints never escape here.
func (a *Adapter) ValidateConnection(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if a == nil || a.provider == nil || strings.TrimSpace(a.provider.Token()) == "" || strings.TrimSpace(a.provider.IdentityBase) == "" {
		return ErrClientUnavailable
	}
	identity, err := openstack.NewIdentityV3(a.provider, gophercloud.EndpointOpts{})
	if err != nil {
		return ErrClientUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token := a.provider.Token()
	valid, err := tokens.Validate(ctx, identity, token)
	// Gophercloud retries a 401 with a new X-Auth-Token, but its original
	// X-Subject-Token is unchanged. Validate the refreshed token explicitly.
	if current := a.provider.Token(); current != token && strings.TrimSpace(current) != "" {
		valid, err = tokens.Validate(ctx, identity, current)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	var reauthError *gophercloud.ErrUnableToReauthenticate
	var afterReauth *gophercloud.ErrErrorAfterReauthentication
	if errors.As(err, &afterReauth) {
		err = afterReauth.ErrOriginal
	}
	if errors.As(err, &reauthError) || gophercloud.ResponseCodeIs(err, 401) || gophercloud.ResponseCodeIs(err, 403) || (err == nil && !valid) {
		return ErrAuthentication
	}
	if err != nil {
		return ErrConnectionUnavailable
	}
	return nil
}

func safeContextError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

func safeMutationError(ctx context.Context, providerError, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if isDefiniteMutationRejection(providerError) {
		return errors.Join(fallback, ErrMutationRejected)
	}
	return fallback
}

// isDefiniteMutationRejection contains only statuses for which the Provider
// can safely conclude that OpenStack did not accept the mutation. In
// particular, 408 and 429 can be generated by an intermediary after the
// upstream service has already applied the request, so they must reconcile as
// UNKNOWN rather than being reported as a definite failure.
func isDefiniteMutationRejection(providerError error) bool {
	for _, status := range []int{400, 401, 403, 404, 405, 409, 412, 422} {
		if gophercloud.ResponseCodeIs(providerError, status) {
			return true
		}
	}
	return false
}
