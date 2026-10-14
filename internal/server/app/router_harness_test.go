//go:build backend_router_harness

package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

func c1TestPlan() c1Plan {
	baseline := map[string][]string{}
	for _, kind := range c1BaselineKinds {
		baseline[kind] = []string{}
	}
	dmzID, privateID, externalID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	baseline["KT_TIER"] = []string{dmzID, privateID, externalID}
	return c1Plan{
		Approval: "PROVISION_AND_EXACT_ID_CLEANUP", ExpiresAt: time.Now().Add(time.Minute), PodName: "mock-backend", PodUID: uuid.New(), ConnectorID: uuid.New(), SessionID: uuid.New(), ProjectID: uuid.New(),
		EvidenceSHA256: strings.Repeat("a", 64), ProviderRuntimeReady: true, LinuxImageVerified: true, RootGiB: 50, CostAndQuotaApproved: true, CleanupApproved: true, BaselineComplete: true,
		Baseline: baseline, ExistingTiers: []c1ExistingTier{
			{NetworkID: dmzID, RefID: uuid.NewString(), Name: "DMZ", CIDR: "172.25.0.0/24"},
			{NetworkID: privateID, RefID: uuid.NewString(), Name: "Private", CIDR: "172.25.1.0/24"},
			{NetworkID: externalID, Name: "external"},
		}, LabCIDR: "172.25.240.0/24", ManagementCIDR: "172.25.241.0/24",
		Snapshot: protocol.CreationSnapshot{ProviderConnectionID: uuid.NewString(), WorkspaceVMKey: "workspace", VMs: []protocol.ResolvedVmSpec{{VMKey: "workspace", Role: "workspace", ImageID: uuid.NewString(), FlavorID: uuid.NewString(), FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 1024, DiskGiB: 0}}}},
	}
}

func TestC1NSMExternalInventoryGate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*c1Plan)
		valid  bool
	}{
		{"DMZ-Private-external", func(*c1Plan) {}, true},
		{"order-independent-ID-mapping", func(p *c1Plan) {
			p.ExistingTiers[0], p.ExistingTiers[2] = p.ExistingTiers[2], p.ExistingTiers[0]
		}, true},
		{"regular-Tier-missing-CIDR", func(p *c1Plan) { p.ExistingTiers[0].CIDR = "" }, false},
		{"unattached-regular-Tier-missing-CIDR", func(p *c1Plan) {
			p.ExistingTiers[0].CIDR, p.ExistingTiers[0].RefID = "", ""
		}, false},
		{"regular-Tier-disguised-as-external", func(p *c1Plan) {
			p.ExistingTiers[0].Name, p.ExistingTiers[0].CIDR = "external", ""
		}, false},
		{"external-with-attachment", func(p *c1Plan) { p.ExistingTiers[2].RefID = uuid.NewString() }, false},
		{"external-with-fabricated-CIDR", func(p *c1Plan) { p.ExistingTiers[2].CIDR = "172.25.2.0/24" }, false},
		{"external-baseline-ID-omitted", func(p *c1Plan) { p.Baseline["KT_TIER"] = p.Baseline["KT_TIER"][:2] }, false},
		{"baseline-ID-replaced", func(p *c1Plan) { p.Baseline["KT_TIER"][2] = uuid.NewString() }, false},
		{"baseline-ID-duplicated", func(p *c1Plan) { p.Baseline["KT_TIER"][2] = p.Baseline["KT_TIER"][0] }, false},
		{"inventory-entry-omitted", func(p *c1Plan) { p.ExistingTiers = p.ExistingTiers[:2] }, false},
		{"inventory-ID-duplicated", func(p *c1Plan) { p.ExistingTiers[2].NetworkID = p.ExistingTiers[0].NetworkID }, false},
		{"foreign-inventory-ID", func(p *c1Plan) { p.ExistingTiers[2].NetworkID = uuid.NewString() }, false},
		{"physical-refId-used-as-networkId", func(p *c1Plan) { p.ExistingTiers[0].NetworkID = p.ExistingTiers[0].RefID }, false},
		{"lab-overlaps-DMZ", func(p *c1Plan) { p.LabCIDR = "172.25.0.0/24" }, false},
		{"management-overlaps-Private", func(p *c1Plan) { p.ManagementCIDR = "172.25.1.0/24" }, false},
		{"existing-supernet-overlap", func(p *c1Plan) { p.ExistingTiers[0].CIDR = "172.25.0.0/16" }, false},
		{"invalid-existing-CIDR", func(p *c1Plan) { p.ExistingTiers[0].CIDR = "not-a-cidr" }, false},
		{"noncanonical-existing-CIDR", func(p *c1Plan) { p.ExistingTiers[0].CIDR = "172.25.0.1/24" }, false},
		{"IPv6-existing-CIDR", func(p *c1Plan) { p.ExistingTiers[0].CIDR = "2001:db8::/64" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := c1TestPlan()
			tt.change(&p)
			before, err := json.Marshal(p.Baseline)
			if err != nil {
				t.Fatal(err)
			}
			root := c1TestRoot(t, p)
			loaded, _, err := c1ReadPlan(root)
			if err != nil {
				t.Fatal(err)
			}
			err = loaded.validate(time.Now(), p.PodName, p.PodUID)
			if (err == nil) != tt.valid {
				t.Fatalf("gate = %v, valid = %v", err, tt.valid)
			}
			after, err := json.Marshal(loaded.Baseline)
			if err != nil || string(before) != string(after) {
				t.Fatal("seven-kind exact-ID baseline altered")
			}
			if !tt.valid {
				h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
				if err := h.run(t.Context(), nil, root, p.PodName, p.PodUID); !errors.Is(err, errHarnessGate) {
					t.Fatalf("invalid inventory did not stop at approval gate: %v", err)
				}
				if _, err := root.Lstat("attempt.jsonl"); !os.IsNotExist(err) {
					t.Fatal("invalid inventory claimed mutation attempt")
				}
			}
		})
	}
}

func c1TestResources() []protocol.ProviderResourceResult {
	var refs []protocol.ProviderResourceResult
	for label, kind := range c1ResourceLabels {
		refs = append(refs, protocol.ProviderResourceResult{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: kind, ProviderID: uuid.NewString(), Generation: 1, LogicalName: label}, ObservedState: "ACTIVE"})
	}
	return refs
}

func c1TestRoot(t *testing.T, p c1Plan) *os.Root {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := c1OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	c1TestWritePlan(t, root, p)
	return root
}

func c1TestWritePlan(t *testing.T, root *os.Root, p c1Plan) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := root.OpenFile("approval.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// Real Router/Registry with an in-memory protocol peer at the transport
// boundary. PostgreSQL-authenticated actual WSS is exercised separately.
func c1TestPeer(t *testing.T, p *c1Plan, response func(string, connector.Correlation) protocol.OperationResultPayload, observation func(*protocol.ResourceObservation), writeError bool) (*routerHarness, *controlStack, *atomic.Int32) {
	t.Helper()
	h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
	registry := connector.NewRegistry()
	router, err := connector.NewRouter(connector.RouterOptions{Registry: registry, Sink: h.sink()})
	if err != nil {
		t.Fatal(err)
	}
	stack := &controlStack{Registry: registry, Router: router}
	principal := connector.Principal{ConnectorID: p.ConnectorID, CredentialID: uuid.New()}
	reg := registry.Register(principal, nil)
	p.SessionID = reg.Session().ID
	mutations := &atomic.Int32{}
	if !reg.MarkReady(func(data []byte) error {
		var envelope protocol.BaseEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		c := connector.Correlation{OperationID: envelope.OperationID, LabInstanceID: envelope.LabInstanceID, Generation: envelope.Generation}
		in := connector.Inbound{MessageID: uuid.NewString(), ReplyToMessageID: envelope.MessageID, OperationID: c.OperationID, LabInstanceID: c.LabInstanceID, Generation: c.Generation}
		if envelope.Type == protocol.MessageTypeOperationCommand {
			mutations.Add(1)
			if writeError {
				return errors.New("SENSITIVE-TRANSPORT-SENTINEL")
			}
			var command protocol.OperationCommandMessage
			if err := json.Unmarshal(data, &command); err != nil {
				return err
			}
			router.RouteOperationAck(p.ConnectorID, in, protocol.OperationAckPayload{Accepted: true})
			in.MessageID = uuid.NewString()
			router.RouteOperationResult(p.ConnectorID, in, response(command.Payload.MutationType, c))
		} else {
			var req protocol.ReconcileRequestMessage
			if err := json.Unmarshal(data, &req); err != nil {
				return err
			}
			if req.Payload.DiscoverCandidates == nil || *req.Payload.DiscoverCandidates {
				return errors.New("candidate discovery enabled")
			}
			var observations []protocol.ResourceObservation
			for _, ref := range req.Payload.KnownResources {
				o := protocol.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Source: "KNOWN_RESOURCE", Exists: mutations.Load() == 1, ObservedState: "ACTIVE"}
				if !o.Exists {
					o.ObservedState = "ABSENT"
				}
				if observation != nil {
					observation(&o)
				}
				observations = append(observations, o)
			}
			router.RouteReconcileResult(p.ConnectorID, in, protocol.ReconcileResultPayload{Observations: observations})
		}
		return nil
	}) {
		t.Fatal("not ready")
	}
	return h, stack, mutations
}

func TestC1SingleShotAndLedgerBlocksRestart(t *testing.T) {
	p := c1TestPlan()
	resources := c1TestResources()
	h, stack, mutations := c1TestPeer(t, &p, func(string, connector.Correlation) protocol.OperationResultPayload {
		return protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: resources}
	}, nil, false)
	root := c1TestRoot(t, p)
	if err := h.run(t.Context(), stack, root, p.PodName, p.PodUID); err != nil {
		t.Fatal(err)
	}
	if mutations.Load() != 2 {
		t.Fatal("expected Provision and Cleanup once each")
	}
	data, _ := root.ReadFile("attempt.jsonl")
	if !strings.Contains(string(data), "EXACT_ID_ABSENCE_CONFIRMED_INVENTORY_COMPARISON_PENDING") {
		t.Fatal("missing final evidence")
	}
	if err := h.run(t.Context(), stack, root, p.PodName, p.PodUID); !errors.Is(err, errHarnessLedger) {
		t.Fatal("existing completed ledger did not block")
	}
	after, _ := root.ReadFile("attempt.jsonl")
	if string(after) != string(data) || mutations.Load() != 2 {
		t.Fatal("restart overwrote ledger or mutated")
	}
	if ops, recs := stack.Router.PendingCount(p.ConnectorID); ops != 0 || recs != 0 {
		t.Fatal("pending leak")
	}
}

func TestC1StopsBeforeCleanupOnUncertainty(t *testing.T) {
	for _, scenario := range []string{"unknown", "failed", "partial", "baseline", "candidate", "generation", "absent", "write-failed", "no-result"} {
		t.Run(scenario, func(t *testing.T) {
			p := c1TestPlan()
			resources := c1TestResources()
			if scenario == "baseline" {
				p.Baseline[resources[0].ResourceType] = append(p.Baseline[resources[0].ResourceType], resources[0].ProviderID)
				if resources[0].ResourceType == "KT_TIER" {
					p.ExistingTiers = append(p.ExistingTiers, c1ExistingTier{NetworkID: resources[0].ProviderID, RefID: uuid.NewString(), Name: "existing-lab", CIDR: "172.25.2.0/24"})
				}
			}
			response := func(string, connector.Correlation) protocol.OperationResultPayload {
				outcome := protocol.OutcomeSucceeded
				if scenario == "unknown" {
					outcome = protocol.OutcomeUnknown
				}
				if scenario == "failed" {
					outcome = protocol.OutcomeFailed
				}
				if scenario == "partial" {
					resources = resources[:1]
				}
				return protocol.OperationResultPayload{Outcome: outcome, ProviderResources: resources, Error: nil}
			}
			observe := func(o *protocol.ResourceObservation) {
				switch scenario {
				case "candidate":
					o.Source = "DISCOVERED_CANDIDATE"
				case "generation":
					o.Generation = 2
				case "absent":
					o.Exists = false
					o.ObservedState = "ABSENT"
				}
			}
			h, stack, calls := c1TestPeer(t, &p, response, observe, scenario == "write-failed")
			if scenario == "no-result" {
				stack.Registry.Register(connector.Principal{ConnectorID: p.ConnectorID, CredentialID: uuid.New()}, nil).MarkReady(func([]byte) error { calls.Add(1); return nil })
				s, _ := stack.Registry.Current(p.ConnectorID)
				p.SessionID = s.ID
			}
			root := c1TestRoot(t, p)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if err := h.run(ctx, stack, root, p.PodName, p.PodUID); err == nil {
				t.Fatal("uncertain run passed")
			}
			if calls.Load() != 1 {
				t.Fatalf("mutations=%d", calls.Load())
			}
			data, _ := root.ReadFile("attempt.jsonl")
			if strings.Contains(string(data), "SENSITIVE-TRANSPORT-SENTINEL") || !strings.Contains(string(data), "STOPPED_MANUAL_RECONCILE_REQUIRED") {
				t.Fatal("unsafe/missing failure evidence")
			}
			if scenario == "unknown" && !strings.Contains(string(data), resources[0].ProviderID) {
				t.Fatal("UNKNOWN confirmed ID lost")
			}
		})
	}
}

func TestC1GateFailClosed(t *testing.T) {
	for _, scenario := range []string{"runtime", "baseline", "cost", "cleanup", "cidr", "image", "root", "outbound", "expiry", "pod", "session", "missing-field"} {
		t.Run(scenario, func(t *testing.T) {
			p := c1TestPlan()
			root := c1TestRoot(t, p)
			switch scenario {
			case "runtime":
				p.ProviderRuntimeReady = false
			case "baseline":
				delete(p.Baseline, "PUBLIC_IP")
			case "cost":
				p.CostAndQuotaApproved = false
			case "cleanup":
				p.CleanupApproved = false
			case "cidr":
				p.ManagementCIDR = p.LabCIDR
			case "image":
				p.LinuxImageVerified = false
			case "root":
				p.RootGiB = 20
			case "outbound":
				p.Snapshot.InternetOutbound = true
			case "expiry":
				p.ExpiresAt = time.Now().Add(-time.Second)
			case "pod":
				p.PodName = "other-pod"
			case "session":
				p.SessionID = uuid.Nil
			case "missing-field":
				p.EvidenceSHA256 = ""
			}
			c1TestWritePlan(t, root, p)
			h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
			if err := h.run(t.Context(), nil, root, "mock-backend", p.PodUID); !errors.Is(err, errHarnessGate) {
				t.Fatalf("gate=%v", err)
			}
			if _, err := root.Lstat("attempt.jsonl"); !os.IsNotExist(err) {
				t.Fatal("invalid approval claimed attempt")
			}
		})
	}
}

func TestC1NoConnectionOrWrongSessionCannotMutate(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "replacement"}[connected], func(t *testing.T) {
			p := c1TestPlan()
			h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
			reg := connector.NewRegistry()
			var calls atomic.Int32
			if connected {
				reg.Register(connector.Principal{ConnectorID: p.ConnectorID, CredentialID: uuid.New()}, nil).MarkReady(func([]byte) error { calls.Add(1); return nil })
			}
			router, _ := connector.NewRouter(connector.RouterOptions{Registry: reg, Sink: h.sink()})
			root := c1TestRoot(t, p)
			if h.run(t.Context(), &controlStack{Registry: reg, Router: router}, root, p.PodName, p.PodUID) == nil || calls.Load() != 0 {
				t.Fatal("no exact route allowed command")
			}
		})
	}
}

func TestC1LedgerExclusiveAndReplacement(t *testing.T) {
	root := c1TestRoot(t, c1TestPlan())
	var wg sync.WaitGroup
	winners := make(chan *c1Ledger, 16)
	for range 16 {
		wg.Go(func() {
			if l, err := c1Claim(root); err == nil {
				winners <- l
			}
		})
	}
	wg.Wait()
	close(winners)
	if len(winners) != 1 {
		t.Fatal("multiple processes claimed attempt")
	}
	l := <-winners
	defer l.file.Close()
	if err := l.record(c1Entry{Stage: "INTENT"}); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("attempt.jsonl", "preserved.jsonl"); err != nil {
		t.Fatal(err)
	}
	f, err := root.OpenFile("attempt.jsonl", os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if l.record(c1Entry{Stage: "MUTATION"}) == nil {
		t.Fatal("replaced ledger accepted")
	}
}

func TestC1ApprovalRejectsSymlinkAndRawFields(t *testing.T) {
	p := c1TestPlan()
	root := c1TestRoot(t, p)
	if err := root.Rename("approval.json", "original.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root.Name(), "original.json"), filepath.Join(root.Name(), "approval.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1ReadPlan(root); err == nil {
		t.Fatal("symlink approval accepted")
	}
	root.Remove("approval.json")
	f, _ := root.OpenFile("approval.json", os.O_CREATE|os.O_WRONLY, 0600)
	f.WriteString(`{"credential":"SENSITIVE-APPROVAL-SENTINEL"}`)
	f.Close()
	if _, _, err := c1ReadPlan(root); err == nil || strings.Contains(err.Error(), "SENSITIVE") {
		t.Fatal("raw field accepted/exposed")
	}
}

func TestC1PreviousOrUnprotectedLedgerNeverAdopted(t *testing.T) {
	for _, state := range []string{"", "UNKNOWN", "COMPLETED"} {
		t.Run("previous-"+state, func(t *testing.T) {
			p := c1TestPlan()
			root := c1TestRoot(t, p)
			f, err := root.OpenFile("attempt.jsonl", os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(state)
			f.Close()
			h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
			if err := h.run(t.Context(), nil, root, p.PodName, p.PodUID); !errors.Is(err, errHarnessLedger) {
				t.Fatal("previous ledger accepted")
			}
			data, _ := root.ReadFile("attempt.jsonl")
			if string(data) != state {
				t.Fatal("previous ledger overwritten")
			}
		})
	}
	root := c1TestRoot(t, c1TestPlan())
	if err := os.Chmod(filepath.Join(root.Name(), "approval.json"), 0640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c1ReadPlan(root); err == nil {
		t.Fatal("unprotected approval accepted")
	}
	if _, err := c1OpenRoot(""); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestC1CorrelationMismatchCannotSupplyCleanupIDs(t *testing.T) {
	p := c1TestPlan()
	resources := c1TestResources()
	for _, field := range []string{"connector", "operation", "lab", "generation", "reply", "request", "message"} {
		t.Run(field, func(t *testing.T) {
			h, stack, calls := c1TestPeer(t, &p, func(string, connector.Correlation) protocol.OperationResultPayload {
				return protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: resources}
			}, nil, false)
			// Replace the route by a protocol peer that sends a valid ACK followed by
			// a typed RESULT with exactly one incorrect correlation component.
			reg := stack.Registry.Register(connector.Principal{ConnectorID: p.ConnectorID, CredentialID: uuid.New()}, nil)
			p.SessionID = reg.Session().ID
			reg.MarkReady(func(data []byte) error {
				calls.Add(1)
				var cmd protocol.OperationCommandMessage
				if err := json.Unmarshal(data, &cmd); err != nil {
					return err
				}
				in := connector.Inbound{MessageID: uuid.NewString(), ReplyToMessageID: cmd.MessageID, OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: cmd.Generation}
				stack.Router.RouteOperationAck(p.ConnectorID, in, protocol.OperationAckPayload{Accepted: true})
				// Direct typed delivery isolates the Harness consumer contract as well
				// as the existing Router's independent mismatch tests.
				e := connector.OperationResultEvent{Routed: connector.Routed{ConnectorID: p.ConnectorID, Correlation: connector.Correlation{OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: cmd.Generation}, RequestMessageID: cmd.MessageID, RequestID: cmd.RequestID, MessageID: uuid.NewString()}, Payload: protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: resources}}
				switch field {
				case "connector":
					e.ConnectorID = uuid.New()
				case "operation":
					e.Correlation.OperationID = uuid.NewString()
				case "lab":
					e.Correlation.LabInstanceID = uuid.NewString()
				case "generation":
					e.Correlation.Generation = 2
				case "reply":
					e.RequestMessageID = uuid.NewString()
				case "request":
					e.RequestID = uuid.NewString()
				case "message":
					e.MessageID = "not-a-message-id"
				}
				h.events.ch <- e
				return nil
			})
			root := c1TestRoot(t, p)
			if h.run(t.Context(), stack, root, p.PodName, p.PodUID) == nil || calls.Load() != 1 {
				t.Fatal("wrong correlation allowed cleanup")
			}
		})
	}
}

func TestC1StoppedResultRetainsConfirmedIDs(t *testing.T) {
	p := c1TestPlan()
	root := c1TestRoot(t, p)
	ledger, err := c1Claim(root)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.file.Close()
	c := connector.Correlation{OperationID: uuid.NewString(), LabInstanceID: uuid.NewString(), Generation: 1}
	sent := connector.SentMessage{MessageID: uuid.NewString()}
	resources := c1TestResources()
	e := connector.OperationResultEvent{Routed: connector.Routed{ConnectorID: p.ConnectorID, Correlation: c, RequestID: c.OperationID, RequestMessageID: sent.MessageID, MessageID: uuid.NewString()}, Payload: protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: resources}}
	if err := c1StoppedResult(ledger, e, p, c, sent); err != nil {
		t.Fatal(err)
	}
	data, _ := root.ReadFile("attempt.jsonl")
	if !strings.Contains(string(data), resources[0].ProviderID) {
		t.Fatal("already received ID lost on stop")
	}
}

func TestC1OperatorSubmissionRequiredAtRuntime(t *testing.T) {
	t.Setenv("LABBIT_C1_HARNESS", "")
	if newRouterHarness().sink() != nil {
		t.Fatal("tag alone armed harness")
	}
	p := c1TestPlan()
	p.PodName, _ = os.Hostname()
	t.Setenv("LABBIT_C1_POD_UID", p.PodUID.String())
	resources := c1TestResources()
	h, stack, calls := c1TestPeer(t, &p, func(string, connector.Correlation) protocol.OperationResultPayload {
		return protocol.OperationResultPayload{Outcome: protocol.OutcomeSucceeded, ProviderResources: resources}
	}, nil, false)
	root := c1TestRoot(t, p)
	if err := root.Remove("approval.json"); err != nil {
		t.Fatal(err)
	}
	h.dir = root.Name()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.start(ctx, stack, slog.New(slog.DiscardHandler))
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("startup mutated without operator submission")
	}
	c1TestWritePlan(t, root, p)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := root.ReadFile("attempt.jsonl")
		if strings.Contains(string(data), "EXACT_ID_ABSENCE_CONFIRMED_INVENTORY_COMPARISON_PENDING") {
			h.start(ctx, stack, slog.New(slog.DiscardHandler))
			if calls.Load() != 2 {
				t.Fatal("startup hook repeated mutation")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operator submission did not run the one-shot path")
}
