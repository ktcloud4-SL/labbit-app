package openstackprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
	name := "labbit-lbt145-network-" + hex.EncodeToString(random)
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
		b, _ := json.MarshalIndent(state, "", "  ")
		if os.WriteFile(ledgerPath, append(b, '\n'), 0600) != nil {
			t.Fatal("ownership ledger cannot be saved")
		}
	}
	save("PREFLIGHT_PASSED_NO_MUTATION")
	// Recovery is scoped to this fixture's random identity, absent at baseline.
	// Cleanup always uses confirmed actual UUIDs; no Create/Delete is retried.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
		defer stop()
		if state.TierID == "" {
			items, e := n.tiers(cleanupCtx)
			if e != nil {
				t.Error("unknown create inventory unavailable")
				return
			}
			for _, item := range items {
				if item.Name == name && item.CIDR == prefix.String() {
					if state.TierID != "" {
						t.Error("ambiguous fixture Tier ownership")
						return
					}
					state.TierID = item.ID
				}
			}
		}
		if state.PolicyAttempted && state.PolicyID == "" {
			items, e := n.policies(cleanupCtx)
			if e != nil {
				t.Error("policy inventory unavailable; tracked ledger requires reconciliation")
				return
			}
			for _, item := range items {
				if item.Comment == comment {
					if state.PolicyID != "" {
						t.Error("ambiguous fixture policy ownership")
						return
					}
					state.PolicyID = item.ID
				}
			}
		}
		save("CLEANUP_CONFIRMED_IDS")
		if state.PolicyID != "" {
			if err := n.deletePolicy(cleanupCtx, state.PolicyID); err != nil {
				t.Error(err)
				return
			}
			if !waitKTFixture(cleanupCtx, func() (bool, error) {
				items, e := n.policies(cleanupCtx)
				if e != nil {
					return false, e
				}
				for _, item := range items {
					if item.ID == state.PolicyID {
						return false, nil
					}
				}
				return true, nil
			}) {
				t.Error("policy deletion not confirmed")
				return
			}
		}
		if state.TierID != "" {
			if err := n.deleteTier(cleanupCtx, state.TierID); err != nil {
				t.Error(err)
				return
			}
			if !waitKTFixture(cleanupCtx, func() (bool, error) { _, exists, e := n.tier(cleanupCtx, state.TierID); return !exists, e }) {
				t.Error("Tier deletion not confirmed")
				return
			}
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
	receipt, err := n.createTier(ctx, name, prefix.String())
	state.TierID = receipt.Data.ID
	save("TIER_CREATE_RESPONSE_RECORDED")
	if err != nil {
		t.Fatal(err)
	}
	if !waitKTFixture(ctx, func() (bool, error) {
		item, exists, e := n.tier(ctx, state.TierID)
		if e != nil {
			return false, e
		}
		if exists && strings.EqualFold(item.Status, "ACTIVE") && item.RefID != "" {
			state.RefID = item.RefID
			return true, nil
		}
		return false, nil
	}) {
		t.Fatal("Tier did not become ACTIVE with a physical refId")
	}
	if state.TierID == state.RefID {
		t.Fatal("unexpected Tier UUID/refId identity; mapping requires investigation")
	}
	save("TIER_ACTIVE_DISTINCT_REF_CONFIRMED")
	state.PolicyAttempted = true
	save("POLICY_CREATE_ATTEMPTED")
	receipt, err = n.createPolicy(ctx, ktCloudFirewallSpec{Accept: true, Protocol: "TCP", Start: "22", End: "22", Source: []string{connectorTier}, Destination: []string{state.TierID}, SourceIPs: []string{source.String()}, Comment: comment})
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
		matches := 0
		for _, item := range items {
			if item.Comment == comment {
				state.PolicyID = item.ID
				matches++
				if len(item.Source) != 1 || item.Source[0].ID != connectorTier || len(item.Destination) != 1 || item.Destination[0].ID != state.TierID || len(item.Services) != 1 || item.Services[0].Protocol != "TCP" || item.Services[0].Start != "22" || item.Services[0].End != "22" {
					return false, fmt.Errorf("policy mapping differs from the requested SSH rule")
				}
			}
		}
		if matches > 1 {
			return false, fmt.Errorf("ambiguous fixture policy")
		}
		return matches == 1, nil
	}) {
		t.Fatal("created policy mapping was not confirmed")
	}
	save("POLICY_MAPPING_CONFIRMED")
	t.Log("NSM create/list/job/delete API mapping PASS; Tier UUID and Nova network refId remain distinct")
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
