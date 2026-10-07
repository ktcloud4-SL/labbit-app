package openstackprovider

import (
	"context"
	"os"
	"testing"
	"time"
)

// This gate is deliberately independent of regular unit tests and the existing
// OpenStack mutation gates. It verifies the public OP-01 constructor and SDK
// queries; it does not claim lifecycle acceptance or create cloud resources.
func TestKTCloudD1ReadOnlyIntegration(t *testing.T) {
	if os.Getenv("LABBIT_KTCLOUD_D1_INTEGRATION") != "1" {
		t.Skip("set LABBIT_KTCLOUD_D1_INTEGRATION=1 explicitly for real KT D1 read-only checks")
	}
	cfg := ConfigFromEnvironment()
	auth, _, _, err := loadConfig(cfg)
	if err != nil || !isKTCloudD1Identity(auth.IdentityEndpoint) {
		t.Fatal("KT D1 integration requires an explicit, verified-TLS D1 cloud configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ValidateConnection(ctx); err != nil {
		t.Fatal(err)
	}
	servers, err := a.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flavors, err := a.ListFlavors(ctx)
	if err != nil || len(flavors) == 0 {
		t.Fatalf("flavor query: %v", err)
	}
	images, err := a.ListImages(ctx)
	if err != nil || len(images) == 0 {
		t.Fatalf("image query: %v", err)
	}
	t.Logf("OP-01 New/ValidateConnection/SDK query PASS; servers=%d flavors=%d images=%d; no lifecycle mutation", len(servers), len(flavors), len(images))
}
