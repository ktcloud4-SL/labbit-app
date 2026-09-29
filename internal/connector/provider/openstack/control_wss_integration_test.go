package openstackprovider

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestOpenStackM2ControlWSSIntegration(t *testing.T) {
	fullLifecycle := os.Getenv("LABBIT_OPENSTACK_M3_WSS_TEST") == "1"
	if os.Getenv("LABBIT_OPENSTACK_M2_WSS_TEST") != "1" && !fullLifecycle {
		t.Skip("set LABBIT_OPENSTACK_M2_WSS_TEST=1 for Provision or LABBIT_OPENSTACK_M3_WSS_TEST=1 for the full WSS lifecycle")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	adapter, err := New(ctx, ConfigFromEnvironment())
	if err != nil {
		t.Fatalf("OpenStack adapter initialization failed: %v", err)
	}
	imageItems, err := adapter.ListImages(ctx)
	if err != nil {
		t.Fatalf("image discovery failed: %v", err)
	}
	flavorItems, err := adapter.ListFlavors(ctx)
	if err != nil {
		t.Fatalf("flavor discovery failed: %v", err)
	}
	networkItems, err := adapter.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("network discovery failed: %v", err)
	}
	image := exactImage(t, imageItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_IMAGE", "ubuntu"))
	flavor := exactFlavor(t, flavorItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_FLAVOR", "m1.small"))
	managementNetwork := exactNetwork(t, networkItems, environmentOrDefault("LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK", "sharednet1"))
	adapter.provision = normalizedProvisionConfig(ProvisionConfig{
		ProviderConnectionID: "local-integration",
		ProjectID:            adapter.provision.ProjectID,
		ManagementNetworkID:  managementNetwork.ID,
		ExternalNetworkID:    adapter.provision.ExternalNetworkID,
		KeyPairName:          environmentOrDefault("LABBIT_OPENSTACK_TEST_KEYPAIR", "openstack2"),
		SSHAllowedCIDR:       environmentOrDefault("LABBIT_OPENSTACK_TEST_SSH_CIDR", "172.16.8.1/32"),
		LabSubnetCIDR:        environmentOrDefault("LABBIT_OPENSTACK_TEST_LAB_CIDR", "198.20.0.0/24"),
		ActiveTimeout:        5 * time.Minute,
		SSHReadyTimeout:      3 * time.Minute,
		PollInterval:         2 * time.Second,
	})

	recorder := newRecordingProvider(adapter)
	mockSaaS := mock.NewMockSaaS("m2-control-test-token")
	defer mockSaaS.Close()
	client := wss.NewClient(wss.Config{
		BaseURL:       mockSaaS.URL(),
		Credential:    "m2-control-test-token",
		AllowInsecure: true,
	})
	if err := client.Dial(ctx); err != nil {
		t.Fatalf("Control WSS dial failed: %v", err)
	}
	defer client.Close()
	if _, err := client.SendHello(ctx); err != nil {
		t.Fatalf("HELLO failed: %v", err)
	}
	handler := wss.NewHandler(recorder, client)
	conn := client.Conn()
	if conn == nil {
		t.Fatal("Control WSS connection is nil after HELLO")
	}
	listenerDone := make(chan error, 1)
	go func() { listenerDone <- handler.Listen(ctx, conn) }()
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-listenerDone:
		case <-time.After(2 * time.Second):
			t.Error("Control WSS listener did not stop")
		}
	})

	runID := integrationRunID(t)
	messageID := "m2-wss-message-" + runID
	command := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type:          protocol.MessageTypeOperationCommand,
			MessageID:     messageID,
			SentAt:        time.Now().UTC(),
			RequestID:     "m2-wss-request-" + runID,
			OperationID:   "m2-wss-operation-" + runID,
			LabInstanceID: "m2-wss-lab-" + runID,
			Generation:    1,
		},
		Payload: protocol.OperationCommandPayload{
			MutationType: protocol.MutationTypeProvision,
			CreationSnapshot: &protocol.CreationSnapshot{
				ProviderConnectionID: "local-integration",
				VMs: []protocol.ResolvedVmSpec{{
					VMKey:         "workspace",
					Role:          "WORKSPACE",
					InstanceIndex: 0,
					ImageID:       image.ID,
					FlavorID:      flavor.ID,
					FlavorSpec: &protocol.ResolvedFlavorSpec{
						VCPUs: flavor.VCPUs, RAMMiB: flavor.RAMMiB, DiskGiB: flavor.DiskGiB,
					},
				}},
				WorkspaceVMKey:   "workspace",
				InternetOutbound: true,
			},
		},
	}
	if err := mockSaaS.SendRaw(command); err != nil {
		t.Fatalf("Mock SaaS command send failed: %v", err)
	}

	select {
	case <-recorder.done:
	case <-ctx.Done():
		t.Fatalf("Provider did not complete before timeout: %v", ctx.Err())
	}
	resources := recorder.resources()
	t.Cleanup(func() { cleanupM2Resources(t, adapter, resources) })

	ack, result := waitForOperationMessages(t, mockSaaS, messageID, 10*time.Second)
	if ack == nil || !ack.Payload.Accepted {
		t.Fatalf("accepted OPERATION_ACK was not received: %+v", ack)
	}
	if result == nil || result.Payload.Outcome != string(coreprovider.OutcomeSucceeded) {
		t.Fatalf("successful OPERATION_RESULT was not received: %+v", result)
	}
	if len(result.Payload.ProviderResources) != 10 || len(resources) != 10 {
		t.Fatalf("wire resources=%d recorded resources=%d, want 10", len(result.Payload.ProviderResources), len(resources))
	}
	t.Logf("Mock SaaS command reached real OpenStack and returned SUCCEEDED; tracked resources=%d", len(resources))
	if !fullLifecycle {
		return
	}

	oldRefs := wireResourceRefs(result.Payload.ProviderResources, 1)
	resetMessageID := "m3-wss-reset-message-" + runID
	reset := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type: protocol.MessageTypeOperationCommand, MessageID: resetMessageID, SentAt: time.Now().UTC(),
			OperationID: "m3-wss-reset-" + runID, LabInstanceID: command.LabInstanceID, Generation: 2,
		},
		Payload: protocol.OperationCommandPayload{MutationType: protocol.MutationTypeReset, CreationSnapshot: command.Payload.CreationSnapshot, ProviderResources: oldRefs},
	}
	if err := mockSaaS.SendRaw(reset); err != nil {
		t.Fatalf("Mock SaaS RESET send failed: %v", err)
	}
	resetAck, resetResult := waitForOperationMessages(t, mockSaaS, resetMessageID, 10*time.Minute)
	if resetAck == nil || !resetAck.Payload.Accepted || resetResult == nil || resetResult.Payload.Outcome != string(coreprovider.OutcomeSucceeded) {
		t.Fatalf("WSS RESET failed: ack=%+v result=%+v", resetAck, resetResult)
	}
	newResources := wireResourceResultsForGeneration(resetResult.Payload.ProviderResources, 2)
	if len(newResources) != 10 {
		t.Fatalf("WSS RESET generation 2 resources=%d, want 10", len(newResources))
	}
	resources = append(resources, newResources...)
	newRefs := wireResourceRefs(resetResult.Payload.ProviderResources, 2)

	reconcileMessageID := "m3-wss-reconcile-message-" + runID
	discover := true
	reconcile := protocol.ReconcileRequestMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type: protocol.MessageTypeReconcileRequest, MessageID: reconcileMessageID, SentAt: time.Now().UTC(),
			OperationID: "m3-wss-reconcile-" + runID, LabInstanceID: command.LabInstanceID, Generation: 2,
		},
		Payload: protocol.ReconcileRequestPayload{KnownResources: newRefs, DiscoverCandidates: &discover},
	}
	if err := mockSaaS.SendRaw(reconcile); err != nil {
		t.Fatalf("Mock SaaS RECONCILE send failed: %v", err)
	}
	reconcileResult := waitForReconcileMessage(t, mockSaaS, reconcileMessageID, 2*time.Minute)
	if reconcileResult == nil || reconcileResult.Payload.Error != nil || len(reconcileResult.Payload.Observations) != len(newRefs) {
		t.Fatalf("WSS live RECONCILE failed: %+v", reconcileResult)
	}
	for _, observation := range reconcileResult.Payload.Observations {
		if !observation.Exists || observation.Source != string(coreprovider.SourceKnownResource) {
			t.Fatalf("unexpected live WSS observation: %+v", observation)
		}
	}

	cleanupMessageID := "m3-wss-cleanup-message-" + runID
	cleanup := protocol.OperationCommandMessage{
		BaseEnvelope: protocol.BaseEnvelope{
			Type: protocol.MessageTypeOperationCommand, MessageID: cleanupMessageID, SentAt: time.Now().UTC(),
			OperationID: "m3-wss-cleanup-" + runID, LabInstanceID: command.LabInstanceID, Generation: 2,
		},
		Payload: protocol.OperationCommandPayload{MutationType: protocol.MutationTypeCleanup, ProviderResources: newRefs},
	}
	if err := mockSaaS.SendRaw(cleanup); err != nil {
		t.Fatalf("Mock SaaS CLEANUP send failed: %v", err)
	}
	cleanupAck, cleanupResult := waitForOperationMessages(t, mockSaaS, cleanupMessageID, 5*time.Minute)
	if cleanupAck == nil || !cleanupAck.Payload.Accepted || cleanupResult == nil || cleanupResult.Payload.Outcome != string(coreprovider.OutcomeSucceeded) {
		t.Fatalf("WSS CLEANUP failed: ack=%+v result=%+v", cleanupAck, cleanupResult)
	}

	postCleanupMessageID := "m3-wss-post-cleanup-reconcile-" + runID
	reconcile.MessageID = postCleanupMessageID
	reconcile.OperationID = "m3-wss-post-cleanup-reconcile-" + runID
	reconcile.SentAt = time.Now().UTC()
	if err := mockSaaS.SendRaw(reconcile); err != nil {
		t.Fatalf("post-cleanup RECONCILE send failed: %v", err)
	}
	postCleanup := waitForReconcileMessage(t, mockSaaS, postCleanupMessageID, 2*time.Minute)
	if postCleanup == nil || postCleanup.Payload.Error != nil || len(postCleanup.Payload.Observations) != len(newRefs) {
		t.Fatalf("post-cleanup WSS RECONCILE failed: %+v", postCleanup)
	}
	for _, observation := range postCleanup.Payload.Observations {
		if observation.Exists || observation.Source != string(coreprovider.SourceKnownResource) {
			t.Fatalf("resource remained after WSS CLEANUP: %+v", observation)
		}
	}
	t.Log("WSS Provision, Reset, Reconcile, Cleanup, and zero-residual verification succeeded")
}

type recordingProvider struct {
	coreprovider.Provider
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	last coreprovider.OperationResult
}

func newRecordingProvider(delegate coreprovider.Provider) *recordingProvider {
	return &recordingProvider{Provider: delegate, done: make(chan struct{})}
}

func (r *recordingProvider) Provision(ctx context.Context, request coreprovider.ProvisionRequest) (coreprovider.OperationResult, error) {
	result, err := r.Provider.Provision(ctx, request)
	r.mu.Lock()
	r.last = result
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
	return result, err
}

func (r *recordingProvider) resources() []coreprovider.ResourceResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	resources := make([]coreprovider.ResourceResult, len(r.last.ProviderResources))
	copy(resources, r.last.ProviderResources)
	return resources
}

func waitForOperationMessages(t *testing.T, mockSaaS *mock.MockSaaS, replyTo string, timeout time.Duration) (*protocol.OperationAckMessage, *protocol.OperationResultMessage) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var ack *protocol.OperationAckMessage
	var result *protocol.OperationResultMessage
	for time.Now().Before(deadline) {
		for _, raw := range mockSaaS.ReceivedMessages() {
			var envelope protocol.BaseEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil || envelope.ReplyToMessageID != replyTo {
				continue
			}
			switch envelope.Type {
			case protocol.MessageTypeOperationAck:
				var message protocol.OperationAckMessage
				if json.Unmarshal(raw, &message) == nil {
					ack = &message
				}
			case protocol.MessageTypeOperationResult:
				var message protocol.OperationResultMessage
				if json.Unmarshal(raw, &message) == nil {
					result = &message
				}
			}
		}
		if ack != nil && result != nil {
			return ack, result
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ack, result
}

func waitForReconcileMessage(t *testing.T, mockSaaS *mock.MockSaaS, replyTo string, timeout time.Duration) *protocol.ReconcileResultMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, raw := range mockSaaS.ReceivedMessages() {
			var envelope protocol.BaseEnvelope
			if json.Unmarshal(raw, &envelope) != nil || envelope.Type != protocol.MessageTypeReconcileResult || envelope.ReplyToMessageID != replyTo {
				continue
			}
			var message protocol.ReconcileResultMessage
			if json.Unmarshal(raw, &message) == nil {
				return &message
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func wireResourceRefs(resources []protocol.ProviderResourceResult, generation int64) []protocol.ProviderResourceRef {
	result := make([]protocol.ProviderResourceRef, 0, len(resources))
	for _, resource := range resources {
		if resource.Generation == generation {
			result = append(result, resource.ProviderResourceRef)
		}
	}
	return result
}

func wireResourceResultsForGeneration(resources []protocol.ProviderResourceResult, generation int64) []coreprovider.ResourceResult {
	result := make([]coreprovider.ResourceResult, 0, len(resources))
	for _, resource := range resources {
		if resource.Generation != generation {
			continue
		}
		result = append(result, coreprovider.ResourceResult{
			ResourceRef: coreprovider.ResourceRef{
				ResourceType: resource.ResourceType, ProviderID: resource.ProviderID, Generation: resource.Generation, LogicalName: resource.LogicalName,
			},
			ObservedState: resource.ObservedState,
		})
	}
	return result
}
