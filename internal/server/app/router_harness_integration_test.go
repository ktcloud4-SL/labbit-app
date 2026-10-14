//go:build backend_router_harness && integration

package app

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// Uses the same stack as app.Run, real DB authentication and real Connector
// WSS/Handler/Dispatcher. Only the Provider boundary is mocked; no KT access.
func TestC1ActualControlStackMockProvider(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "round-trip", true: "disconnect-during-provision"}[replace], func(t *testing.T) {
			dsn := postgrestest.NewDatabase(t)
			postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
			p := c1TestPlan()
			p.ConnectorID = seedConnector(t, dsn, e2eConnectorCredential, false, false)
			pool, err := postgres.OpenPool(t.Context(), dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			h := &routerHarness{events: &c1Events{ch: make(chan connector.Event, 32)}}
			stack, err := newControlStack(postgres.NewStore(pool), stackOptions{PublicOrigin: terminalTrustedOrigin, OperationSink: h.sink()})
			if err != nil {
				t.Fatal(err)
			}
			// Existing Connector test mode allows loopback WS; it does not disable
			// certificate validation for HTTPS. Production TLS stays unchanged.
			server := httptest.NewServer(applicationHandler(stack.routes()))
			t.Cleanup(server.Close)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := stack.shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			resources := c1TestResources()
			var provisions, cleanups, reconciles atomic.Int32
			entered, release := make(chan struct{}), make(chan struct{})
			mock := &provider.MockProvider{
				ProvisionFunc: func(ctx context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
					provisions.Add(1)
					close(entered)
					if replace {
						select {
						case <-release:
						case <-ctx.Done():
							return provider.OperationResult{}, ctx.Err()
						}
					}
					var owned []provider.ResourceResult
					for _, item := range resources {
						ref := item.ProviderResourceRef
						owned = append(owned, provider.ResourceResult{ResourceRef: provider.ResourceRef{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: r.Generation, LogicalName: ref.LogicalName}, ObservedState: "ACTIVE"})
					}
					return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: owned}, nil
				},
				CleanupFunc: func(_ context.Context, r provider.CleanupRequest) (provider.OperationResult, error) {
					cleanups.Add(1)
					var owned []provider.ResourceResult
					for _, ref := range r.ProviderResources {
						owned = append(owned, provider.ResourceResult{ResourceRef: ref, ObservedState: "DELETED"})
					}
					return provider.OperationResult{Outcome: provider.OutcomeSucceeded, ProviderResources: owned}, nil
				},
				ReconcileFunc: func(_ context.Context, r provider.ReconcileRequest) (provider.ReconcileResult, error) {
					reconciles.Add(1)
					if r.DiscoverCandidates {
						t.Error("candidate discovery enabled")
					}
					var observations []provider.ResourceObservation
					for _, ref := range r.KnownResources {
						state := "ACTIVE"
						exists := cleanups.Load() == 0
						if !exists {
							state = "ABSENT"
						}
						observations = append(observations, provider.ResourceObservation{ResourceType: ref.ResourceType, ProviderID: ref.ProviderID, Generation: ref.Generation, LogicalName: ref.LogicalName, Exists: exists, ObservedState: state, Source: provider.SourceKnownResource})
					}
					return provider.ReconcileResult{Observations: observations}, nil
				},
			}
			supervisor := wss.NewSupervisor(wss.Config{BaseURL: server.URL, Credential: e2eConnectorCredential, ConnectorVersion: "mock-c1", AllowInsecure: true}, wss.NewHandler(mock, nil), wss.BackoffPolicy{InitialInterval: time.Millisecond, MaxInterval: 10 * time.Millisecond, Multiplier: 2})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- supervisor.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("Supervisor shutdown timeout")
				}
			})
			eventually(t, "C1 authenticated protocol-ready", 5*time.Second, func() bool {
				return stack.Registry.WithReadyRoute(p.ConnectorID, func(s connector.Session, _ connector.Route) error { p.SessionID = s.ID; return nil }) == nil
			})
			root := c1TestRoot(t, p)
			runDone := make(chan error, 1)
			go func() { runDone <- h.run(ctx, stack, root, p.PodName, p.PodUID) }()
			if replace {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("Provision not received")
				}
				old := p.SessionID
				// The Handler dispatch currently blocks its read loop until Provider
				// returns. A second authenticated connection creates a real Session
				// replacement while that original mutation is still outstanding.
				replacement := wss.NewSupervisor(wss.Config{BaseURL: server.URL, Credential: e2eConnectorCredential, ConnectorVersion: "mock-replacement", AllowInsecure: true}, wss.NewHandler(mock, nil), wss.NewDefaultBackoffPolicy())
				replacementCtx, cancelReplacement := context.WithCancel(ctx)
				replacementDone := make(chan error, 1)
				go func() { replacementDone <- replacement.Run(replacementCtx) }()
				t.Cleanup(func() {
					cancelReplacement()
					select {
					case <-replacementDone:
					case <-time.After(5 * time.Second):
						t.Error("replacement shutdown timeout")
					}
				})
				eventually(t, "replacement protocol-ready", 5*time.Second, func() bool {
					return stack.Registry.WithReadyRoute(p.ConnectorID, func(s connector.Session, _ connector.Route) error {
						if s.ID == old {
							return errHarnessStop
						}
						return nil
					}) == nil
				})
				close(release)
			}
			select {
			case err := <-runDone:
				if (err != nil) != replace {
					t.Fatalf("run error=%v replacement=%v", err, replace)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("C1 harness did not terminate")
			}
			if provisions.Load() != 1 {
				t.Fatal("Provision retried")
			}
			if replace {
				if cleanups.Load() != 0 || reconciles.Load() != 0 {
					t.Fatal("commands continued on replacement")
				}
			} else if cleanups.Load() != 1 || reconciles.Load() != 2 {
				t.Fatal("single path incomplete")
			}
			if h.run(ctx, stack, root, p.PodName, p.PodUID) == nil {
				t.Fatal("ledger permitted repeat run")
			}
			if ops, recs := stack.Router.PendingCount(p.ConnectorID); ops != 0 || recs != 0 {
				t.Fatal("pending leak")
			}
			conn := postgrestest.Connect(t, dsn)
			for _, table := range []string{"operations", "operation_items", "lab_instances", "provider_resources"} {
				var count int
				if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("durable %s changed: count=%d err=%v", table, count, err)
				}
			}
		})
	}
}
