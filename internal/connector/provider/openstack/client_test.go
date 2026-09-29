package openstackprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigReadsNamedCloudWithoutExposingCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clouds.yaml")
	data := []byte(`clouds:
  labbit-test:
    auth:
      auth_url: https://identity.example.test/v3
      username: connector
      password: do-not-log-this
      project_name: labbit
      user_domain_name: Default
      project_domain_name: Default
    region_name: RegionOne
    interface: internal
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	auth, endpoint, tlsConfig, err := loadConfig(Config{File: path, CloudName: "labbit-test"})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if auth.IdentityEndpoint != "https://identity.example.test/v3" {
		t.Fatalf("unexpected identity endpoint: %q", auth.IdentityEndpoint)
	}
	if endpoint.Region != "RegionOne" || endpoint.Availability != "internal" {
		t.Fatalf("unexpected endpoint options: %+v", endpoint)
	}
	if tlsConfig != nil && tlsConfig.InsecureSkipVerify {
		t.Fatal("TLS verification was disabled")
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv(EnvProviderConfigFile, "  C:/openstack/clouds.yaml  ")
	t.Setenv(EnvCloudName, "  labbit-test  ")
	t.Setenv(EnvProviderConnection, " provider-connection-1 ")
	t.Setenv(EnvProjectID, " project-1 ")
	t.Setenv(EnvManagementNetwork, " management-network ")
	t.Setenv(EnvExternalNetwork, " external-network ")
	t.Setenv(EnvKeyPairName, " openstack2 ")
	t.Setenv(EnvSSHAllowedCIDR, " 172.16.8.1/32 ")
	t.Setenv(EnvLabSubnetCIDR, " 198.19.0.0/24 ")
	t.Setenv(EnvSSHUsername, " ubuntu ")
	t.Setenv(EnvSSHPrivateKeyFile, " C:/keys/labbit ")
	t.Setenv(EnvSSHKnownHostsFile, " C:/keys/known_hosts ")

	config := ConfigFromEnvironment()
	if config.File != "C:/openstack/clouds.yaml" || config.CloudName != "labbit-test" ||
		config.Provision.ProviderConnectionID != "provider-connection-1" || config.Provision.ProjectID != "project-1" || config.Provision.ManagementNetworkID != "management-network" ||
		config.Provision.ExternalNetworkID != "external-network" || config.Provision.KeyPairName != "openstack2" ||
		config.Provision.SSHAllowedCIDR != "172.16.8.1/32" || config.Provision.LabSubnetCIDR != "198.19.0.0/24" ||
		config.Provision.SSHUsername != "ubuntu" || config.Provision.SSHPrivateKeyFile != "C:/keys/labbit" ||
		config.Provision.SSHKnownHostsFile != "C:/keys/known_hosts" {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestLoadConfigRejectsUnsafeOrInvalidInputWithSafeErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, _, _, err := loadConfig(Config{})
		if !errors.Is(err, ErrConfigRequired) {
			t.Fatalf("error = %v, want ErrConfigRequired", err)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "clouds.yaml")
		if err := os.WriteFile(path, []byte("password: raw-secret\n: invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := loadConfig(Config{File: path, CloudName: "labbit-test"})
		if !errors.Is(err, ErrConfigInvalid) || err.Error() != ErrConfigInvalid.Error() {
			t.Fatalf("unsafe or unexpected error: %v", err)
		}
	})

	t.Run("insecure TLS", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "clouds.yaml")
		data := []byte(`clouds:
  labbit-test:
    auth:
      auth_url: https://identity.example.test/v3
      username: connector
      password: do-not-log-this
      project_name: labbit
      user_domain_name: Default
      project_domain_name: Default
    verify: false
`)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, _, err := loadConfig(Config{File: path, CloudName: "labbit-test"})
		if !errors.Is(err, ErrInsecureTLS) {
			t.Fatalf("error = %v, want ErrInsecureTLS", err)
		}
	})
}

func TestValidateConnectionRequiresAuthenticatedClient(t *testing.T) {
	if err := (*Adapter)(nil).ValidateConnection(context.Background()); !errors.Is(err, ErrClientUnavailable) {
		t.Fatalf("nil adapter error = %v", err)
	}

	adapter := newTestAdapter(t, nil)
	if err := adapter.ValidateConnection(context.Background()); err != nil {
		t.Fatalf("ValidateConnection() error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.ValidateConnection(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}
