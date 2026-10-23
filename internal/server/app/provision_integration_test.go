//go:build integration

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres"
	"github.com/ktcloud4-SL/labbit-app/internal/postgres/postgrestest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/labexecution/labexecutiontest"
	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

type provisionEnv struct {
	t                 *testing.T
	dsn               string
	db                *pgx.Conn
	store             *postgres.Store
	f                 *labexecutiontest.Fixture
	stack             *controlStack
	server            *httptest.Server
	cookie            string
	events            *routedEvents
	opID, executionID uuid.UUID
}

func newProvisionEnv(t *testing.T, timeout time.Duration, register ...bool) *provisionEnv {
	t.Helper()
	dsn := postgrestest.NewDatabase(t)
	postgrestest.Migrate(t, dsn, loadEmbeddedMigrations(t))
	db := postgrestest.Connect(t, dsn)
	f := labexecutiontest.New(t, db)
	hash := sha256.Sum256([]byte(e2eConnectorCredential))
	labexecutiontest.Exec(t, db, `INSERT INTO connector_credentials (id,connector_id,credential_hash) VALUES ($1,$2,$3)`, uuid.New(), f.ConnectorID, hash[:])
	pool, err := postgres.OpenPool(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewStore(pool)
	events := &routedEvents{}
	stack, err := newControlStack(store, stackOptions{PublicOrigin: publicOrigin, Worker: true, OperationSink: events,
		ProvisionLeaseDuration: 300 * time.Millisecond, ProvisionResultTimeout: timeout, ProvisionPollInterval: 10 * time.Millisecond, ProvisionDispatchRetryDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(applicationHandler(stack.routes()))
	t.Cleanup(func() {
		stack.close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stack.shutdown(ctx); err != nil {
			t.Error(err)
		}
		server.Close()
	})
	e := &provisionEnv{t: t, dsn: dsn, db: db, store: store, f: f, stack: stack, server: server, cookie: "__Host-labbit-session=" + terminaltest.LoginSession(t, db, f.InstructorID), events: events}
	if len(register) == 0 || register[0] {
		e.acceptAt(server.URL)
	}
	return e
}

func (e *provisionEnv) acceptAt(application string) {
	t, f := e.t, e.f
	t.Helper()
	body := fmt.Sprintf(`{"labSpecId":%q,"targetStudentIds":[%q,%q]}`, f.LabSpecID, f.StudentAID, f.StudentBID)
	resp := send(t, http.MethodPost, application+"/api/v1/classes/"+f.ClassID.String()+"/lab-executions", body,
		map[string]string{"Cookie": e.cookie, "Origin": publicOrigin, "Content-Type": "application/json", "Idempotency-Key": "provision-worker-http-key"})
	if resp.status != 202 {
		t.Fatalf("register=%d %s", resp.status, resp.body)
	}
	var accepted struct {
		OperationID string
		Target      struct{ ID string }
	}
	if err := json.Unmarshal([]byte(resp.body), &accepted); err != nil {
		t.Fatal(err)
	}
	e.opID = uuid.MustParse(accepted.OperationID)
	e.executionID = uuid.MustParse(accepted.Target.ID)
}

func (e *provisionEnv) startMock(mock *provider.MockProvider) {
	e.t.Helper()
	startRoutingConnector(e.t, &lifecycleEnv{url: e.server.URL, registry: e.stack.Registry, router: e.stack.Router, connectorID: e.f.ConnectorID}, mock)
}

func (e *provisionEnv) startWorker() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); e.stack.Provisions.Run(ctx) }()
	e.t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			e.t.Error("worker did not stop")
		}
	})
}

func (e *provisionEnv) operationStatus() string {
	e.t.Helper()
	o, err := e.store.OperationByID(e.t.Context(), e.opID)
	if err != nil {
		e.t.Fatal(err)
	}
	return o.Status
}

func (e *provisionEnv) assertHTTPMatchesDatabase() {
	e.t.Helper()
	resp := send(e.t, http.MethodGet, e.server.URL+"/api/v1/operations/"+e.opID.String(), "", map[string]string{"Cookie": e.cookie})
	if resp.status != 200 {
		e.t.Fatalf("GET operation=%d %s", resp.status, resp.body)
	}
	var got struct {
		ID, Type, Status, Stage          string
		StartedAt, FinishedAt, UpdatedAt time.Time
	}
	if err := json.Unmarshal([]byte(resp.body), &got); err != nil {
		e.t.Fatal(err)
	}
	db, err := e.store.OperationByID(e.t.Context(), e.opID)
	if err != nil || got.ID != db.ID.String() || got.Type != db.Type || got.Status != db.Status || got.Stage != db.Stage || !got.UpdatedAt.Equal(db.UpdatedAt) {
		e.t.Fatalf("HTTP=%+v DB=%+v %v", got, db, err)
	}
	if db.FinishedAt != nil && !got.FinishedAt.Equal(*db.FinishedAt) {
		e.t.Fatalf("finish=%v %v", got.FinishedAt, db.FinishedAt)
	}
	for _, value := range []string{labexecutiontest.ImageMarker, labexecutiontest.FlavorMarker, labexecutiontest.ScriptMarker, e.f.ProviderConnectionID.String(), "provider-id-"} {
		if strings.Contains(resp.body, value) {
			e.t.Fatalf("HTTP leaked %q", value)
		}
	}
}

func mockProvisionResult(r provider.ProvisionRequest, outcome provider.Outcome) provider.OperationResult {
	resources := []provider.ResourceResult{{ResourceRef: provider.ResourceRef{ResourceType: "KT_TIER", ProviderID: "tier-exact-" + r.LabInstanceID, Generation: r.Generation, LogicalName: "lab-tier"}, ObservedState: "ACTIVE"}}
	for _, vm := range r.CreationSnapshot.VMs {
		resources = append(resources, provider.ResourceResult{ResourceRef: provider.ResourceRef{ResourceType: "SERVER", ProviderID: "provider-id-" + r.LabInstanceID + "/" + vm.VMKey, Generation: r.Generation, LogicalName: vm.VMKey}, ObservedState: "ACTIVE"})
	}
	return provider.OperationResult{Outcome: outcome, ProviderResources: resources}
}

func TestProvisionControlStackClaimAckResultAndHTTP(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	requests := make(chan provider.ProvisionRequest, 3)
	release := make(chan struct{})
	e.startMock(&provider.MockProvider{ProvisionFunc: func(ctx context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		requests <- r
		select {
		case <-release:
		case <-ctx.Done():
			return provider.OperationResult{}, ctx.Err()
		}
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})
	// Mutable definitions change AFTER acceptance; dispatch must use the stored snapshot.
	labexecutiontest.Exec(t, e.db, `UPDATE provider_image_mappings SET provider_image_id='changed-after-acceptance' WHERE id=$1`, e.f.ImageMappingID)
	labexecutiontest.Exec(t, e.db, `UPDATE lab_specs SET startup_script='changed-script',revision=revision+1 WHERE id=$1`, e.f.LabSpecID)
	done := make(chan error, 1)
	go func() {
		claimed, err := e.stack.Provisions.RunOne(t.Context())
		if !claimed && err == nil {
			err = fmt.Errorf("no claim")
		}
		done <- err
	}()
	var request provider.ProvisionRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch missing")
	}
	if request.OperationID != e.opID.String() || request.Generation != 1 || request.CreationSnapshot.ProviderConnectionID != e.f.ProviderConnectionID.String() || request.CreationSnapshot.StartupScript.Content != labexecutiontest.StartupScript {
		t.Fatalf("dispatch correlation/snapshot=%+v", request.Correlation)
	}
	for _, vm := range request.CreationSnapshot.VMs {
		if vm.ImageID != labexecutiontest.ImageMarker || vm.FlavorID != labexecutiontest.FlavorMarker {
			t.Fatal("mutable mapping used")
		}
	}
	eventually(t, "ACK persisted without completion", 5*time.Second, func() bool {
		items, err := e.store.OperationItems(t.Context(), e.opID)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range items {
			if i.LabInstanceID.String() == request.LabInstanceID {
				return i.Status == "RUNNING" && i.Stage == "PROVISIONING" && i.FinishedAt == nil
			}
		}
		return false
	})
	if e.operationStatus() != "RUNNING" {
		t.Fatal("ACK completed operation")
	}
	e.assertHTTPMatchesDatabase()
	// Provider wait must not hold item/parent/product row locks.
	tx, err := e.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT id FROM operation_items WHERE operation_id=$1 FOR UPDATE NOWAIT`, e.opID); err != nil {
		t.Fatalf("external I/O holds transaction: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if claimed, err := e.stack.Provisions.RunOne(t.Context()); !claimed || err != nil {
			t.Fatalf("sibling=%v %v", claimed, err)
		}
	}
	if e.operationStatus() != "SUCCEEDED" {
		t.Fatalf("parent=%s", e.operationStatus())
	}
	e.assertHTTPMatchesDatabase()
	var resources, ready, provisioning int
	err = e.db.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM provider_resources WHERE provider_connection_id=$1 AND generation=1 AND lifecycle_status='PRESENT' AND created_by_operation_item_id IS NOT NULL),
 (SELECT count(*) FROM lab_instances WHERE status='READY'),(SELECT count(*) FROM lab_instances WHERE status='PROVISIONING')`, e.f.ProviderConnectionID).Scan(&resources, &ready, &provisioning)
	if err != nil || resources != 12 || ready != 0 || provisioning != 3 {
		t.Fatalf("resources/readiness=%d/%d/%d %v", resources, ready, provisioning, err)
	}
	var exact string
	if err := e.db.QueryRow(t.Context(), `SELECT provider_id FROM provider_resources WHERE lab_instance_id=$1 AND resource_type='SERVER' AND logical_name='vm-1'`, uuid.MustParse(request.LabInstanceID)).Scan(&exact); err != nil || exact != "provider-id-"+request.LabInstanceID+"/vm-1" {
		t.Fatalf("exact ID=%q %v", exact, err)
	}
}

func TestProvisionControlStackIndependentFailureAndUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown=%v", unknown), func(t *testing.T) {
			e := newProvisionEnv(t, 5*time.Second)
			var calls atomic.Int32
			e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
				n := calls.Add(1)
				outcome := provider.OutcomeSucceeded
				if n == 1 {
					outcome = provider.OutcomeFailed
				}
				if n == 2 && unknown {
					outcome = provider.OutcomeUnknown
				}
				return mockProvisionResult(r, outcome), nil
			}})
			e.startWorker()
			want := "FAILED"
			if unknown {
				want = "RECONCILING"
			}
			eventually(t, "all independent outcomes persisted", 5*time.Second, func() bool { return e.operationStatus() == want })
			e.assertHTTPMatchesDatabase()
			items, err := e.store.OperationItems(t.Context(), e.opID)
			if err != nil {
				t.Fatal(err)
			}
			succeeded := 0
			for _, i := range items {
				if i.Status == "SUCCEEDED" {
					succeeded++
				}
				if i.Status == "PENDING" || i.Status == "RUNNING" {
					t.Fatalf("sibling stranded: %+v", i)
				}
			}
			wantSuccess := 2
			if unknown {
				wantSuccess = 1
			}
			if succeeded != wantSuccess || calls.Load() != 3 {
				t.Fatalf("successes=%d calls=%d", succeeded, calls.Load())
			}
			var resources, errors int
			if err := e.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM provider_resources),(SELECT count(*) FROM lab_instances WHERE status='ERROR')`).Scan(&resources, &errors); err != nil || resources != 12 || errors != 1 {
				t.Fatalf("partial results=%d errors=%d %v", resources, errors, err)
			}
		})
	}
}

func (e *provisionEnv) peer(credential string) *wss.Client {
	e.t.Helper()
	c := newConnectorClient(e.server.URL, credential)
	e.t.Cleanup(func() { _ = c.Close() })
	if err := c.Dial(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
	if _, err := c.SendHello(e.t.Context()); err != nil {
		e.t.Fatal(err)
	}
	return c
}

func TestProvisionControlStackReplacementCorrelationDuplicateAndLateResult(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	peer := e.peer(e2eConnectorCredential)
	done := make(chan error, 1)
	go func() { _, err := e.stack.Provisions.RunOne(t.Context()); done <- err }()
	var cmd protocol.OperationCommandMessage
	_ = peer.Conn().SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := peer.Conn().ReadJSON(&cmd); err != nil {
		t.Fatal(err)
	}
	msg := protocol.OperationResultMessage{BaseEnvelope: protocol.BaseEnvelope{Type: "OPERATION_RESULT", MessageID: uuid.NewString(), SentAt: time.Now(), ReplyToMessageID: cmd.MessageID, OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: cmd.Generation},
		Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED", ProviderResources: []protocol.ProviderResourceResult{{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: "SERVER", ProviderID: "exact-wire-ID", Generation: 1, LogicalName: "vm-1"}, ObservedState: "ACTIVE"}}}}
	// A separately authenticated Connector cannot write this item's result.
	const otherCredential = "provision-other-connector-dummy"
	seedConnector(t, e.dsn, otherCredential, false, false)
	other := e.peer(otherCredential)
	if err := other.SendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"operation", "instance", "generation", "reply"} {
		bad := msg
		bad.MessageID = uuid.NewString()
		switch kind {
		case "operation":
			bad.OperationID = uuid.NewString()
		case "instance":
			bad.LabInstanceID = uuid.NewString()
		case "generation":
			bad.Generation = 2
		case "reply":
			bad.ReplyToMessageID = uuid.NewString()
		}
		if err := peer.SendMessage(t.Context(), bad); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "five unmatched messages", 5*time.Second, func() bool { return len(e.events.all()) >= 5 })
	if e.operationStatus() != "RUNNING" {
		t.Fatal("wrong correlation completed item")
	}
	old, _ := e.stack.Registry.Current(e.f.ConnectorID)
	replacement := e.peer(e2eConnectorCredential)
	current, _ := e.stack.Registry.Current(e.f.ConnectorID)
	if current.ID == old.ID {
		t.Fatal("session was not replaced")
	}
	if n, _ := e.stack.Router.PendingCount(e.f.ConnectorID); n != 1 {
		t.Fatalf("pending=%d", n)
	}
	if err := replacement.SendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before, _ := e.store.OperationByID(t.Context(), e.opID)
	msg.MessageID = uuid.NewString()
	if err := replacement.SendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	eventually(t, "duplicate terminal result rejected", 5*time.Second, func() bool { return len(e.events.all()) >= 7 })
	after, _ := e.store.OperationByID(t.Context(), e.opID)
	if !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatal("duplicate changed durable state")
	}
	items, _ := e.store.OperationItems(t.Context(), e.opID)
	for _, i := range items {
		if i.LabInstanceID.String() != cmd.LabInstanceID && i.Status != "PENDING" {
			t.Fatal("wrong item changed")
		}
	}
	e.assertHTTPMatchesDatabase()
}

func TestProvisionControlStackResponseLossDoesNotRetryOnReplacement(t *testing.T) {
	e := newProvisionEnv(t, 100*time.Millisecond)
	peer := e.peer(e2eConnectorCredential)
	done := make(chan error, 1)
	go func() { _, err := e.stack.Provisions.RunOne(t.Context()); done <- err }()
	var cmd protocol.OperationCommandMessage
	_ = peer.Conn().SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := peer.Conn().ReadJSON(&cmd); err != nil {
		t.Fatal(err)
	}
	ack := protocol.OperationAckMessage{BaseEnvelope: protocol.BaseEnvelope{Type: "OPERATION_ACK", MessageID: uuid.NewString(), SentAt: time.Now(), ReplyToMessageID: cmd.MessageID, OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: cmd.Generation}, Payload: protocol.OperationAckPayload{Accepted: true}}
	if err := peer.SendMessage(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	items, _ := e.store.OperationItems(t.Context(), e.opID)
	for _, i := range items {
		if i.LabInstanceID.String() == cmd.LabInstanceID && (i.Status != "RECONCILING" || i.FinishedAt != nil) {
			t.Fatalf("lost response=%+v", i)
		}
	}
	replacement := e.peer(e2eConnectorCredential)
	if n, _ := e.stack.Router.PendingCount(e.f.ConnectorID); n != 0 {
		t.Fatalf("timed-out pending=%d", n)
	}
	late := protocol.OperationResultMessage{BaseEnvelope: ack.BaseEnvelope, Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED", ProviderResources: []protocol.ProviderResourceResult{}}}
	late.Type = "OPERATION_RESULT"
	late.MessageID = uuid.NewString()
	if err := replacement.SendMessage(t.Context(), late); err != nil {
		t.Fatal(err)
	}
	eventually(t, "late result unmatched", 5*time.Second, func() bool {
		for _, event := range e.events.all() {
			if u, ok := event.(connector.UnmatchedEvent); ok && u.MessageType == "OPERATION_RESULT" {
				return true
			}
		}
		return false
	})
	items, _ = e.store.OperationItems(t.Context(), e.opID)
	for _, i := range items {
		if i.LabInstanceID.String() == cmd.LabInstanceID && i.Status != "RECONCILING" {
			t.Fatal("late result changed item")
		}
	}
	// A restarted store still cannot claim the lost response Item. The two
	// untouched siblings are distinct claims, not a replay of this mutation.
	for range 2 {
		c, err := e.store.ClaimProvisionOperationItem(t.Context(), uuid.New(), time.Minute)
		if err != nil || c.Item.LabInstanceID.String() == cmd.LabInstanceID {
			t.Fatalf("blind retry claim=%+v %v", c.Item, err)
		}
	}
	if _, err := e.store.ClaimProvisionOperationItem(t.Context(), uuid.New(), time.Minute); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("reclaim=%v", err)
	}
	go func() { _, err := e.stack.Provisions.RunOne(t.Context()); done <- err }()
	var request protocol.ReconcileRequestMessage
	if err := replacement.Conn().ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Type != "RECONCILE_REQUEST" || request.LabInstanceID != cmd.LabInstanceID || len(request.Payload.KnownResources) != 0 {
		t.Fatal("lost response replayed or invented ownership")
	}
	response := protocol.ReconcileResultMessage{BaseEnvelope: protocol.BaseEnvelope{Type: "RECONCILE_RESULT", MessageID: uuid.NewString(), SentAt: time.Now(), ReplyToMessageID: request.MessageID, OperationID: request.OperationID, LabInstanceID: request.LabInstanceID, Generation: request.Generation}, Payload: protocol.ReconcileResultPayload{Observations: []protocol.ResourceObservation{{ResourceType: "SERVER", ProviderID: "unowned-lost-result", Exists: true, Source: "DISCOVERED_CANDIDATE"}}}}
	if err := replacement.SendMessage(t.Context(), response); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("candidate adopted: %d %v", count, err)
	}
	var stage string
	if err := e.db.QueryRow(t.Context(), `SELECT stage FROM operation_items WHERE operation_id=$1 AND lab_instance_id=$2`, e.opID, cmd.LabInstanceID).Scan(&stage); err != nil || stage != "HOLD" {
		t.Fatalf("loss stage=%s %v", stage, err)
	}

	// Losing a Reconcile response also remains HOLD; it cannot authorize mutation.
	labexecutiontest.Exec(t, e.db, `UPDATE operation_items SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE operation_id=$1 AND lab_instance_id=$2`, e.opID, cmd.LabInstanceID)
	go func() { _, err := e.stack.Provisions.RunOne(t.Context()); done <- err }()
	if err := replacement.Conn().ReadJSON(&request); err != nil {
		t.Fatal(err)
	}
	if request.Type != "RECONCILE_REQUEST" {
		t.Fatal("reconciliation timeout replayed mutation")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, pending := e.stack.Router.PendingCount(e.f.ConnectorID); pending != 0 {
		t.Fatalf("reconciliation pending leaked: %d", pending)
	}

}

func TestProvisionRuntimeOwnsControlAndRunsRegisteredWork(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second, false)
	admin, application := startServerWithApplication(t, "development", "api,worker", e.dsn, "")
	waitForStatus(t, admin+"/readyz", http.StatusOK)
	client := newConnectorClient(application, e2eConnectorCredential)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Dial(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendHello(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.acceptAt(application)
	seen := map[string]bool{}
	for range 3 {
		var cmd protocol.OperationCommandMessage
		_ = client.Conn().SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := client.Conn().ReadJSON(&cmd); err != nil {
			t.Fatal(err)
		}
		if cmd.Type != "OPERATION_COMMAND" || cmd.OperationID != e.opID.String() || seen[cmd.LabInstanceID] {
			t.Fatalf("unexpected/replayed command=%+v", cmd.BaseEnvelope)
		}
		seen[cmd.LabInstanceID] = true
		resources := []protocol.ProviderResourceResult{}
		for _, vm := range cmd.Payload.CreationSnapshot.VMs {
			resources = append(resources, protocol.ProviderResourceResult{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: "SERVER", ProviderID: "runtime-exact-" + cmd.LabInstanceID + "/" + vm.VMKey, LogicalName: vm.VMKey, Generation: 1}, ObservedState: "ACTIVE"})
		}
		result := protocol.OperationResultMessage{BaseEnvelope: protocol.BaseEnvelope{Type: "OPERATION_RESULT", MessageID: uuid.NewString(), ReplyToMessageID: cmd.MessageID, SentAt: time.Now(), OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: 1}, Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED", ProviderResources: resources}}
		if err := client.SendMessage(t.Context(), result); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "runtime results durable", 5*time.Second, func() bool { return e.operationStatus() == "SUCCEEDED" })
	resp := send(t, http.MethodGet, application+"/api/v1/operations/"+e.opID.String(), "", map[string]string{"Cookie": e.cookie})
	if resp.status != 200 || !strings.Contains(resp.body, `"status":"SUCCEEDED"`) {
		t.Fatalf("runtime operation=%d %s", resp.status, resp.body)
	}
}

func TestProvisionRuntimeDoesNotClaimUnsupportedRolesOrIncompatibleSchema(t *testing.T) {
	for _, scenario := range []string{"worker-only", "api-only", "incompatible-schema"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, 5*time.Second)
			roles := "api"
			want := http.StatusOK
			if scenario == "worker-only" {
				roles = "worker"
				want = http.StatusServiceUnavailable
			}
			if scenario == "incompatible-schema" {
				roles = "api,worker"
				want = http.StatusServiceUnavailable
				labexecutiontest.Exec(t, e.db, `UPDATE labbit_schema_migrations SET checksum='incompatible-test-checksum' WHERE version=7`)
			}
			admin, _ := startServerWithApplication(t, "development", roles, e.dsn, "")
			waitForStatus(t, admin+"/livez", http.StatusOK)
			waitForStatus(t, admin+"/readyz", want)
			// Covers the first and a subsequent production polling interval.
			time.Sleep(1100 * time.Millisecond)
			items, err := e.store.OperationItems(t.Context(), e.opID)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range items {
				if i.Status != "PENDING" || i.StartedAt != nil {
					t.Fatalf("disabled runtime claimed: %+v", i)
				}
			}
		})
	}
}

func (e *provisionEnv) runOneMustClaim() {
	e.t.Helper()
	if claimed, err := e.stack.Provisions.RunOne(e.t.Context()); !claimed || err != nil {
		e.t.Fatalf("RunOne=%v %v", claimed, err)
	}
}

func (e *provisionEnv) assertUnsentDeferred() {
	e.t.Helper()
	var eligible, instances, resources int
	var last time.Time
	if err := e.db.QueryRow(e.t.Context(), `SELECT
 (SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='PENDING' AND stage='DEFERRED'
 AND lease_owner IS NULL AND lease_expires_at IS NULL AND finished_at IS NULL AND dispatch_not_before>clock_timestamp()),
 (SELECT count(*) FROM lab_instances WHERE lab_execution_id=$2 AND status='PENDING'),
 (SELECT count(*) FROM provider_resources),
 (SELECT max(dispatch_not_before) FROM operation_items WHERE operation_id=$1)`, e.opID, e.executionID).Scan(&eligible, &instances, &resources, &last); err != nil {
		e.t.Fatal(err)
	}
	if eligible != 3 || instances != 3 || resources != 0 || e.operationStatus() != "PENDING" {
		e.t.Fatalf("deferred=%d instances=%d resources=%d parent=%s", eligible, instances, resources, e.operationStatus())
	}
	if n, _ := e.stack.Router.PendingCount(e.f.ConnectorID); n != 0 {
		e.t.Fatalf("unsent command pending=%d", n)
	}
	for range 5 {
		if claimed, err := e.stack.Provisions.RunOne(e.t.Context()); claimed || err != nil {
			e.t.Fatalf("hot deferred claim=%v %v", claimed, err)
		}
	}
	var after time.Time
	if err := e.db.QueryRow(e.t.Context(), `SELECT max(dispatch_not_before) FROM operation_items WHERE operation_id=$1`, e.opID).Scan(&after); err != nil || !last.Equal(after) {
		e.t.Fatalf("polling re-deferred before deadline=%v %v", after, err)
	}
	e.assertHTTPMatchesDatabase()
}

func TestProvisionControlStackDisconnectedThenReadyDispatchesExactlyOnce(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	for range 3 {
		e.runOneMustClaim()
	}
	e.assertUnsentDeferred()
	var calls, duplicates atomic.Int32
	var seen sync.Map
	e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
		calls.Add(1)
		if _, loaded := seen.LoadOrStore(r.LabInstanceID, true); loaded {
			duplicates.Add(1)
		}
		return mockProvisionResult(r, provider.OutcomeSucceeded), nil
	}})
	e.startWorker()
	eventually(t, "deferred Items finish after ready Connector", 5*time.Second, func() bool { return e.operationStatus() == "SUCCEEDED" })
	if calls.Load() != 3 || duplicates.Load() != 0 {
		t.Fatalf("Provider calls=%d duplicates=%d", calls.Load(), duplicates.Load())
	}
	e.assertHTTPMatchesDatabase()
	var resources int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources WHERE provider_connection_id=$1 AND generation=1
 AND lifecycle_status='PRESENT' AND created_by_operation_item_id IS NOT NULL`, e.f.ProviderConnectionID).Scan(&resources); err != nil || resources != 12 {
		t.Fatalf("resources=%d %v", resources, err)
	}
}

func TestProvisionControlStackPreHelloDefersUntilHelloAck(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	peer := newConnectorClient(e.server.URL, e2eConnectorCredential)
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.Dial(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "authenticated but not protocol-ready route", 5*time.Second, func() bool {
		return errors.Is(e.stack.Registry.WithReadyRoute(e.f.ConnectorID, func(connector.Session, connector.Route) error { return nil }), connector.ErrNotReady)
	})
	for range 3 {
		e.runOneMustClaim()
	}
	e.assertUnsentDeferred()
	if _, err := peer.SendHello(t.Context()); err != nil {
		t.Fatalf("unexpected pre-HELLO command or missing HELLO_ACK: %v", err)
	}
	e.startWorker()
	seen := map[string]bool{}
	for range 3 {
		var cmd protocol.OperationCommandMessage
		_ = peer.Conn().SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := peer.Conn().ReadJSON(&cmd); err != nil {
			t.Fatal(err)
		}
		if cmd.Type != "OPERATION_COMMAND" || cmd.OperationID != e.opID.String() || seen[cmd.LabInstanceID] {
			t.Fatalf("duplicate/wrong command=%+v", cmd.BaseEnvelope)
		}
		seen[cmd.LabInstanceID] = true
		resources := []protocol.ProviderResourceResult{}
		for _, vm := range cmd.Payload.CreationSnapshot.VMs {
			resources = append(resources, protocol.ProviderResourceResult{ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: "SERVER", ProviderID: "deferred-exact-" + cmd.LabInstanceID + "/" + vm.VMKey, Generation: cmd.Generation, LogicalName: vm.VMKey}, ObservedState: "ACTIVE"})
		}
		result := protocol.OperationResultMessage{BaseEnvelope: protocol.BaseEnvelope{Type: "OPERATION_RESULT", MessageID: uuid.NewString(), SentAt: time.Now(), ReplyToMessageID: cmd.MessageID, OperationID: cmd.OperationID, LabInstanceID: cmd.LabInstanceID, Generation: cmd.Generation}, Payload: protocol.OperationResultPayload{Outcome: "SUCCEEDED", ProviderResources: resources}}
		if err := peer.SendMessage(t.Context(), result); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "post-HELLO results persisted", 5*time.Second, func() bool { return e.operationStatus() == "SUCCEEDED" })
	if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil {
		t.Fatalf("completed replay=%v %v", claimed, err)
	}
	e.assertHTTPMatchesDatabase()
}

func TestProvisionControlStackAmbiguousWriteNeverDefersOrReplays(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	principal, err := connector.NewService(e.store).Authenticate(t.Context(), connector.Credential(e2eConnectorCredential))
	if err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	registration := e.stack.Registry.Register(principal, nil)
	t.Cleanup(func() { registration.Release() })
	registration.MarkReady(func([]byte) error { writes.Add(1); return errors.New("mock partial write failure") })
	for range 3 {
		e.runOneMustClaim()
	}
	if e.operationStatus() != "RECONCILING" || writes.Load() != 3 {
		t.Fatalf("uncertain parent=%s writes=%d", e.operationStatus(), writes.Load())
	}
	var uncertain int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RECONCILING' AND dispatch_not_before IS NULL AND finished_at IS NULL`, e.opID).Scan(&uncertain); err != nil || uncertain != 3 {
		t.Fatalf("uncertain=%d %v", uncertain, err)
	}
	replacement := e.stack.Registry.Register(principal, nil)
	t.Cleanup(func() { replacement.Release() })
	var reconciles atomic.Int32
	replacement.MarkReady(func(data []byte) error {
		var request protocol.ReconcileRequestMessage
		if err := json.Unmarshal(data, &request); err != nil {
			return err
		}
		if request.Type != "RECONCILE_REQUEST" {
			writes.Add(1)
			return errors.New("unexpected mutation replay")
		}
		reconciles.Add(1)
		if !e.stack.Router.RouteReconcileResult(e.f.ConnectorID, connector.Inbound{MessageID: uuid.NewString(), ReplyToMessageID: request.MessageID,
			OperationID: request.OperationID, LabInstanceID: request.LabInstanceID, Generation: request.Generation}, protocol.ReconcileResultPayload{Observations: []protocol.ResourceObservation{}}) {
			t.Error("reconciliation response rejected")
		}
		return nil
	})
	for range 3 {
		e.runOneMustClaim()
	}
	for range 2 {
		if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil {
			t.Fatalf("HOLD claim=%v %v", claimed, err)
		}
	}
	if writes.Load() != 3 || reconciles.Load() != 3 {
		t.Fatalf("mutation/reconciliation writes=%d/%d", writes.Load(), reconciles.Load())
	}
	e.assertHTTPMatchesDatabase()
}

func TestProvisionControlStackInvalidCommandFailsWithoutRequeue(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	// Replace the fixture snapshot before dispatch with a malformed stored input.
	// Production's immutable UPDATE trigger stays enabled; Router validation is real.
	labexecutiontest.Exec(t, e.db, `WITH original AS (DELETE FROM creation_snapshots WHERE lab_execution_id=$1 RETURNING *)
 INSERT INTO creation_snapshots (id,organization_id,lab_execution_id,source_lab_spec_id,source_lab_spec_revision,
 provider_connection_id,schema_version,snapshot,created_at)
 SELECT id,organization_id,lab_execution_id,source_lab_spec_id,source_lab_spec_revision,provider_connection_id,
 schema_version,jsonb_set(snapshot,'{vms}','[]'::jsonb),created_at FROM original`, e.executionID)
	for range 3 {
		e.runOneMustClaim()
	}
	if e.operationStatus() != "FAILED" {
		t.Fatalf("invalid command parent=%s", e.operationStatus())
	}
	for range 3 {
		if claimed, err := e.stack.Provisions.RunOne(t.Context()); claimed || err != nil {
			t.Fatalf("invalid command requeued=%v %v", claimed, err)
		}
	}
	var failed, resources int
	if err := e.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='FAILED'
 AND error_code='provision_command_invalid' AND dispatch_not_before IS NULL AND finished_at IS NOT NULL),
 (SELECT count(*) FROM provider_resources)`, e.opID).Scan(&failed, &resources); err != nil || failed != 3 || resources != 0 {
		t.Fatalf("failed=%d resources=%d %v", failed, resources, err)
	}
	if n, _ := e.stack.Router.PendingCount(e.f.ConnectorID); n != 0 {
		t.Fatalf("invalid pending=%d", n)
	}
	e.assertHTTPMatchesDatabase()
}

func TestProvisionControlStackShutdownPreservesSentRunningItem(t *testing.T) {
	e := newProvisionEnv(t, 5*time.Second)
	principal, err := connector.NewService(e.store).Authenticate(t.Context(), connector.Credential(e2eConnectorCredential))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registration := e.stack.Registry.Register(principal, nil)
	t.Cleanup(func() { registration.Release() })
	registration.MarkReady(func([]byte) error { cancel(); return nil })
	if claimed, err := e.stack.Provisions.RunOne(ctx); !claimed || !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown=%v %v", claimed, err)
	}
	var running int
	if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM operation_items WHERE operation_id=$1 AND status='RUNNING'
 AND stage='DISPATCHING' AND lease_owner IS NOT NULL AND finished_at IS NULL AND dispatch_not_before IS NULL`, e.opID).Scan(&running); err != nil || running != 1 {
		t.Fatalf("shutdown running=%d %v", running, err)
	}
}

// Covers the production serializer, authenticated WSS receiver, Router,
// Executor and PostgreSQL writer. Provider proof is simulated, not Cloud E2E.
func TestReadinessProofSurvivesControlWSSAndReachesPostgres(t *testing.T) {
	for _, scenario := range []string{"ready", "legacy", "stale", "incomplete", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			e := newProvisionEnv(t, 5*time.Second)
			e.startMock(&provider.MockProvider{ProvisionFunc: func(_ context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
				result := mockProvisionResult(r, provider.OutcomeSucceeded)
				result.Readiness = &provider.Readiness{WorkspaceVMKey: r.CreationSnapshot.WorkspaceVMKey, ServerID: "provider-id-" + r.LabInstanceID + "/" + r.CreationSnapshot.WorkspaceVMKey, Generation: r.Generation}
				switch scenario {
				case "legacy":
					result.Readiness = nil
				case "stale":
					result.Readiness.Generation++
				case "incomplete":
					result.ProviderResources = result.ProviderResources[:1]
				case "unknown":
					result.Outcome = provider.OutcomeUnknown
				}
				return result, nil
			}})
			e.startWorker()
			wantOperation := "SUCCEEDED"
			if scenario == "stale" || scenario == "incomplete" || scenario == "unknown" {
				wantOperation = "RECONCILING"
			}
			eventually(t, "proof result persisted", 5*time.Second, func() bool { return e.operationStatus() == wantOperation })
			var ready, total int
			if err := e.db.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE status='READY'),count(*) FROM lab_instances WHERE lab_execution_id=$1`, e.executionID).Scan(&ready, &total); err != nil {
				t.Fatal(err)
			}
			if scenario == "ready" && ready != total || scenario != "ready" && ready != 0 {
				t.Fatalf("false readiness: ready=%d total=%d", ready, total)
			}
			var execution string
			if err := e.db.QueryRow(t.Context(), `SELECT status FROM lab_executions WHERE id=$1`, e.executionID).Scan(&execution); err != nil {
				t.Fatal(err)
			}
			if scenario == "ready" && execution != "ACTIVE" {
				t.Fatalf("execution=%s", execution)
			}
			if scenario == "stale" {
				var resources int
				if err := e.db.QueryRow(t.Context(), `SELECT count(*) FROM provider_resources WHERE lab_instance_id IN(SELECT id FROM lab_instances WHERE lab_execution_id=$1)`, e.executionID).Scan(&resources); err != nil || resources == 0 {
					t.Fatalf("owned observations lost: n=%d err=%v", resources, err)
				}
			}
		})
	}
}
