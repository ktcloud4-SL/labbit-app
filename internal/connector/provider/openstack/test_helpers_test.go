package openstackprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func newTestAdapter(t *testing.T, handler http.Handler) *Adapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	providerClient := &gophercloud.ProviderClient{
		HTTPClient: *server.Client(),
		TokenID:    "test-token",
	}
	return newAdapter(
		providerClient,
		&gophercloud.ServiceClient{ProviderClient: providerClient, Endpoint: server.URL + "/image/v2/"},
		&gophercloud.ServiceClient{ProviderClient: providerClient, Endpoint: server.URL + "/compute/v2/"},
		&gophercloud.ServiceClient{ProviderClient: providerClient, Endpoint: server.URL + "/network/v2.0/"},
	)
}

func writeJSON(t *testing.T, response http.ResponseWriter, status int, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
