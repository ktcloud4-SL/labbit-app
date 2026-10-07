package openstackprovider

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
)

const ktCloudD1Identity = "https://api.ucloudbiz.olleh.com/d1/identity"

func isKTCloudD1Identity(endpoint string) bool {
	return strings.TrimRight(endpoint, "/") == ktCloudD1Identity
}

// KT D1 exposes Keystone v3 token operations without the /v3 suffix and its
// advertised Nova/Glance catalog URLs are not the public Gateway API routes.
// Keep SDK request/response mapping, locking and 401 handling; specialize only
// endpoint construction. Neutron is still resolved from the catalog and must
// not be confused with the separately identified KT Tier API.
func newKTCloudD1Adapter(ctx context.Context, cfg Config, auth gophercloud.AuthOptions, endpoint gophercloud.EndpointOpts, tlsConfig *tls.Config) (*Adapter, error) {
	client, err := openstack.NewClient(auth.IdentityEndpoint)
	if err != nil {
		return nil, ErrAuthentication
	}
	if tlsConfig != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsConfig
		client.HTTPClient.Transport = transport
	}
	identityEndpoint := gophercloud.NormalizeURL(auth.IdentityEndpoint)
	authContext, cancel := context.WithTimeout(ctx, authenticationTimeout)
	err = authenticateKTCloudD1(authContext, client, identityEndpoint, auth)
	cancel()
	if err != nil {
		return nil, safeContextError(ctx, ErrAuthentication)
	}
	if auth.AllowReauth {
		// Match the SDK throwaway-client pattern: refresh has no token and
		// cannot recursively reauthenticate. ProviderClient coordinates races.
		fresh := *client
		fresh.SetThrowaway(true)
		fresh.ReauthFunc = nil
		if err := fresh.SetTokenAndAuthResult(nil); err != nil {
			return nil, ErrAuthentication
		}
		refreshAuth := auth
		refreshAuth.AllowReauth = false
		client.ReauthFunc = boundedReauthentication(func(refreshContext context.Context) error {
			if err := authenticateKTCloudD1(refreshContext, &fresh, identityEndpoint, refreshAuth); err != nil {
				return err
			}
			client.CopyTokenFrom(&fresh)
			return nil
		})
	}

	network, err := openstack.NewNetworkV2(client, endpoint)
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	gateway := strings.TrimSuffix(strings.TrimRight(identityEndpoint, "/"), "/identity")
	image := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: gateway + "/image/", Type: "image"}
	compute := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: gateway + "/server/", Type: "compute"}
	adapter := newAdapter(client, image, compute, network)
	adapter.identity = &gophercloud.ServiceClient{ProviderClient: client, Endpoint: identityEndpoint, Type: "identity"}
	adapter.ktNetwork = &ktCloudD1Network{client: &gophercloud.ServiceClient{ProviderClient: client, Endpoint: gateway + "/nsm/v1/", Type: "network"}}
	adapter.provision = normalizedProvisionConfig(cfg.Provision)
	if adapter.provision.ProjectID != "" {
		adapter.volume = &gophercloud.ServiceClient{ProviderClient: client, Endpoint: gateway + "/volume/" + adapter.provision.ProjectID + "/", Type: "volumev3"}
	}
	return adapter, nil
}

type ktCloudTokenResult interface {
	gophercloud.AuthResult
	ExtractServiceCatalog() (*tokens.ServiceCatalog, error)
}

func authenticateKTCloudD1(ctx context.Context, client *gophercloud.ProviderClient, endpoint string, auth gophercloud.AuthOptions) error {
	identity := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: endpoint, Type: "identity"}
	var result ktCloudTokenResult
	if auth.TokenID != "" && (auth.Scope == nil || *auth.Scope == (gophercloud.AuthScope{})) {
		client.SetToken(auth.TokenID)
		result = tokens.Get(ctx, identity, auth.TokenID)
	} else {
		result = tokens.Create(ctx, identity, &auth)
	}
	if err := client.SetTokenAndAuthResult(result); err != nil {
		return err
	}
	catalog, err := result.ExtractServiceCatalog()
	if err != nil {
		return err
	}
	client.EndpointLocator = func(options gophercloud.EndpointOpts) (string, error) {
		return openstack.V3EndpointURL(catalog, options)
	}
	return nil
}
