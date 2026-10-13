package openstackprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This fixture exercises only the NSM API boundary. It never substitutes for
// Connector VM/Control WSS acceptance and has an independent mutation gate.
func TestKTCloudD1DisposableNetworkIntegration(t *testing.T) {
	if os.Getenv("LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST") != "1" {
		t.Skip("set LABBIT_KTCLOUD_D1_NETWORK_MUTATION_TEST=1 explicitly for disposable NSM mutations")
	}
	ledgerPath := os.Getenv("LABBIT_KTCLOUD_TEST_LEDGER")
	prefix, err := netip.ParsePrefix(os.Getenv("LABBIT_KTCLOUD_TEST_TIER_CIDR"))
	source, sourceErr := netip.ParsePrefix(os.Getenv("LABBIT_KTCLOUD_TEST_SOURCE_CIDR"))
	if !filepath.IsAbs(ledgerPath) || err != nil || !prefix.Addr().Is4() || prefix.Bits() != 24 || prefix != prefix.Masked() || sourceErr != nil || !source.Addr().Is4() || source.Bits() != 32 {
		t.Fatal("explicit absolute ownership-ledger path, unused IPv4 /24 and source /32 are required")
	}
	journal, err := openKTFixtureLedger(ledgerPath)
	if err != nil {
		t.Fatal("fresh exclusive ownership ledger is required before cloud access")
	}
	t.Cleanup(func() { _ = journal.Close() })
	cfg := ConfigFromEnvironment()
	auth, _, _, err := loadConfig(cfg)
	if err != nil || !isKTCloudD1Identity(auth.IdentityEndpoint) {
		t.Fatal("verified-TLS KT D1 cloud configuration is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	a, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	n := a.ktNetwork
	baseline, err := n.tiers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := n.policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	connectorTier := ""
	for _, item := range baseline {
		if item.CIDR != "" {
			known, e := netip.ParsePrefix(item.CIDR)
			if e != nil || known.Overlaps(prefix) {
				t.Fatal("probe CIDR overlaps or Tier inventory is invalid")
			}
			if item.Name == "DMZ" && known.Contains(source.Addr()) && item.RefID != "" {
				connectorTier = item.ID
			}
		}
	}
	if connectorTier == "" {
		t.Fatal("source address must be in the existing DMZ Tier")
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal("fixture identity generation failed")
	}
	name := ktCloudManagementTierName("lbt145-network-"+hex.EncodeToString(random), 1, "workspace")
	t.Logf("KT Tier name length=%d (customer-reported deletion boundary: 50); name=%s", len(name), name)
	comment := name + ":ssh-policy"
	state := struct {
		Name            string `json:"name"`
		CIDR            string `json:"cidr"`
		State           string `json:"state"`
		TierID          string `json:"tierId,omitempty"`
		RefID           string `json:"refId,omitempty"`
		PolicyID        string `json:"policyId,omitempty"`
		PolicyJobID     string `json:"policyJobId,omitempty"`
		PolicyAttempted bool   `json:"policyAttempted"`
		Deleted         bool   `json:"deleted"`
	}{Name: name, CIDR: prefix.String()}
	save := func(stage string) {
		state.State = stage
		b, err := json.MarshalIndent(state, "", "  ")
		if err != nil || journal.Save(append(b, '\n')) != nil {
			t.Fatal("ownership ledger cannot be saved")
		}
	}
	save("PREFLIGHT_PASSED_NO_MUTATION")
	// Recovery is scoped to this fixture's random identity, absent at baseline.
	// Cleanup always uses confirmed actual UUIDs; no Create/Delete is retried.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
		defer stop()
		if err := requireKTFixtureCleanupOwnership(state.TierID, state.PolicyID, state.PolicyAttempted); err != nil {
			save("CLEANUP_OWNERSHIP_UNCONFIRMED_MANUAL_REVIEW_REQUIRED")
			t.Error(err)
			return
		}
		save("CLEANUP_CONFIRMED_IDS")
		if err := cleanupKTFixtureKnownResources(cleanupCtx, n, state.TierID, state.PolicyID, state.PolicyAttempted); err != nil {
			t.Error(err)
			return
		}
		finalTiers, e := n.tiers(cleanupCtx)
		if e != nil {
			t.Error(e)
			return
		}
		finalPolicies, e := n.policies(cleanupCtx)
		if e != nil {
			t.Error(e)
			return
		}
		for _, item := range finalTiers {
			if item.ID == state.TierID || item.Name == name {
				t.Error("test-owned Tier remains")
				return
			}
		}
		for _, item := range finalPolicies {
			if item.ID == state.PolicyID || item.Comment == comment {
				t.Error("test-owned policy remains")
				return
			}
		}
		state.Deleted = true
		save("CLEANUP_ABSENCE_CONFIRMED")
		t.Logf("NSM fixture residual=0; Tier count %d -> %d, policy count %d -> %d; no VM was created", len(baseline), len(finalTiers), len(policies), len(finalPolicies))
	})
	save("TIER_CREATE_ATTEMPTED")
	tier, ownedID, err := createKTFixtureTier(ctx, a, name, prefix.String())
	state.TierID = ownedID
	save("TIER_CREATE_RESULT_RECORDED")
	if err != nil {
		t.Fatal(err)
	}
	state.RefID = tier.RefID
	if state.TierID == state.RefID {
		t.Fatal("unexpected Tier UUID/refId identity; mapping requires investigation")
	}
	save("TIER_ACTIVE_DISTINCT_REF_CONFIRMED")
	state.PolicyAttempted = true
	save("POLICY_CREATE_ATTEMPTED")
	receipt, err := n.createPolicy(ctx, ktCloudFirewallSpec{Accept: true, Protocol: "TCP", Start: "22", End: "22", Source: []string{connectorTier}, Destination: []string{state.TierID}, SourceIPs: []string{source.String()}, Comment: comment})
	state.PolicyJobID = receipt.JobID
	save("POLICY_JOB_RECORDED")
	if err != nil {
		t.Fatal(err)
	}
	if !waitKTFixture(ctx, func() (bool, error) {
		status, e := n.job(ctx, state.PolicyJobID)
		if e != nil {
			return false, e
		}
		if status == "FAILED" || status == "FAILURE" {
			return false, fmt.Errorf("NSM policy job failed")
		}
		return status == "SUCCESS", nil
	}) {
		t.Fatal("policy job did not succeed")
	}
	if !waitKTFixture(ctx, func() (bool, error) {
		items, e := n.policies(ctx)
		if e != nil {
			return false, e
		}
		id, e := confirmedKTFixturePolicy(items, comment, ProvisionConfig{KTCloudConnectorTierID: connectorTier, SSHAllowedCIDR: source.String()}, state.TierID)
		if e != nil {
			return false, e
		}
		if id == "" {
			return false, nil
		}
		state.PolicyID = id
		return true, nil
	}) {
		t.Fatal("created policy mapping was not confirmed")
	}
	save("POLICY_MAPPING_CONFIRMED")
	t.Log("NSM create/list/job/delete API mapping PASS; Tier UUID and Nova network refId remain distinct")
}

func createKTFixtureTier(ctx context.Context, a *Adapter, name, cidr string) (ktCloudTier, string, error) {
	// Reuse production ownership confirmation, including preservation of a
	// matching BUILD ref after an uncertain observation. A receipt ID alone
	// must never authorize the fixture's deferred cleanup.
	tier, resources, err := a.createKTCloudTier(ctx, resourceIdentity(1, "fixture-tier"), name, cidr, nil)
	ownedID := ""
	if len(resources) == 1 {
		ownedID = resources[0].ProviderID
	}
	return tier, ownedID, err
}

func waitKTFixture(ctx context.Context, check func() (bool, error)) bool {
	for {
		done, err := check()
		if err != nil {
			return false
		}
		if done {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

func cleanupKTFixtureKnownResources(ctx context.Context, n *ktCloudD1Network, tierID, policyID string, policyAttempted bool) error {
	if err := requireKTFixtureCleanupOwnership(tierID, policyID, policyAttempted); err != nil {
		return err
	}
	if policyID != "" {
		if err := n.deletePolicy(ctx, policyID); err != nil {
			return err
		}
		if !waitKTFixture(ctx, func() (bool, error) {
			items, e := n.policies(ctx)
			if e != nil {
				return false, e
			}
			for _, item := range items {
				if item.ID == policyID {
					return false, nil
				}
			}
			return true, nil
		}) {
			return errors.New("policy deletion not confirmed")
		}
	}
	if tierID != "" {
		if err := n.deleteTier(ctx, tierID); err != nil {
			return err
		}
		if !waitKTFixture(ctx, func() (bool, error) { _, exists, e := n.tier(ctx, tierID); return !exists, e }) {
			return errors.New("Tier deletion not confirmed")
		}
	}
	return nil
}

func confirmedKTFixturePolicy(items []ktCloudFirewallPolicy, comment string, config ProvisionConfig, tierID string) (string, error) {
	matches := []ktCloudFirewallPolicy{}
	for _, item := range items {
		if item.Comment == comment {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	if len(matches) != 1 || matches[0].ID == "" || !ktSSHPolicyMatches(matches[0], config, tierID) {
		return "", fmt.Errorf("fixture policy ownership mapping uncertain")
	}
	return matches[0].ID, nil
}
