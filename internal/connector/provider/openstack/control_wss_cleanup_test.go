package openstackprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
)

func TestControlWSSFailedResetTracksPartialGenerationForTeardown(t *testing.T) {
	for _, outcome := range []coreprovider.Outcome{coreprovider.OutcomeFailed, coreprovider.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			old := coreprovider.ResourceResult{ResourceRef: coreprovider.ResourceRef{
				ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "g1-network", Generation: 1, LogicalName: "lab-network",
			}}
			partial := []coreprovider.ResourceResult{
				{ResourceRef: coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "g2-network", Generation: 2, LogicalName: "lab-network"}},
				{ResourceRef: coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypePort, ProviderID: "g2-port", Generation: 2, LogicalName: "workspace:management"}},
			}
			recorder := newRecordingProvider(&coreprovider.MockProvider{
				ProvisionFunc: func(context.Context, coreprovider.ProvisionRequest) (coreprovider.OperationResult, error) {
					return coreprovider.OperationResult{Outcome: coreprovider.OutcomeSucceeded, ProviderResources: []coreprovider.ResourceResult{old}}, nil
				},
				ResetFunc: func(context.Context, coreprovider.ResetRequest) (coreprovider.OperationResult, error) {
					return coreprovider.OperationResult{Outcome: outcome, ProviderResources: partial}, nil
				},
			})
			_, _ = recorder.Provision(context.Background(), coreprovider.ProvisionRequest{})
			var wireResult *protocol.OperationResultMessage
			handler := wss.NewHandler(recorder, wss.SendMessageFunc(func(_ context.Context, message interface{}) error {
				if result, ok := message.(protocol.OperationResultMessage); ok {
					wireResult = &result
				}
				return nil
			}))
			command := protocol.OperationCommandMessage{
				BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeOperationCommand, MessageID: "failed-reset", SentAt: time.Now().UTC(), OperationID: "reset-operation", LabInstanceID: "test-lab", Generation: 2},
				Payload: protocol.OperationCommandPayload{
					MutationType: protocol.MutationTypeReset,
					CreationSnapshot: &protocol.CreationSnapshot{
						ProviderConnectionID: "provider-connection-1", WorkspaceVMKey: "workspace",
						VMs: []protocol.ResolvedVmSpec{{VMKey: "workspace", Role: "WORKSPACE", InstanceIndex: 0, ImageID: "image-ubuntu", FlavorID: "flavor-small", FlavorSpec: &protocol.ResolvedFlavorSpec{VCPUs: 1, RAMMiB: 2048, DiskGiB: 20}}},
					},
					ProviderResources: []protocol.ProviderResourceRef{{ResourceType: old.ResourceType, ProviderID: old.ProviderID, Generation: 1, LogicalName: old.LogicalName}},
				},
			}
			raw, err := json.Marshal(command)
			if err != nil {
				t.Fatal(err)
			}
			if err := handler.HandleMessage(context.Background(), raw); err != nil {
				t.Fatalf("failed Reset was not handled: %v", err)
			}
			if wireResult == nil || wireResult.Payload.Outcome != string(outcome) {
				t.Fatalf("wire result=%+v, want %s", wireResult, outcome)
			}
			deleted := map[string]bool{}
			adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
				if id != "g1-network" && id != "g2-network" && id != "g2-port" {
					t.Errorf("teardown tried to mutate an untracked resource: %s", id)
					response.WriteHeader(http.StatusForbidden)
					return
				}
				if request.Method == http.MethodDelete {
					deleted[id] = true
					response.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(response, request)
			}))
			cleanupM2Resources(t, adapter, recorder.resources())
			for _, id := range []string{"g1-network", "g2-network", "g2-port"} {
				if !deleted[id] {
					t.Errorf("teardown omitted %s after Reset outcome %s", id, outcome)
				}
			}
		})
	}
}

func TestRecordingProviderFailedProvisionTracksPartialResourcesForTeardown(t *testing.T) {
	for _, outcome := range []coreprovider.Outcome{coreprovider.OutcomeFailed, coreprovider.OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			partial := []coreprovider.ResourceResult{
				{ResourceRef: coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypeNetwork, ProviderID: "g1-network", Generation: 1, LogicalName: "lab-network"}},
				{ResourceRef: coreprovider.ResourceRef{ResourceType: coreprovider.ResourceTypePort, ProviderID: "g1-port", Generation: 1, LogicalName: "workspace:management"}},
			}
			recorder := newRecordingProvider(&coreprovider.MockProvider{
				ProvisionFunc: func(context.Context, coreprovider.ProvisionRequest) (coreprovider.OperationResult, error) {
					return coreprovider.OperationResult{Outcome: outcome, ProviderResources: partial}, nil
				},
			})
			result, err := recorder.Provision(context.Background(), coreprovider.ProvisionRequest{})
			if err != nil || result.Outcome != outcome {
				t.Fatalf("unexpected partial Provision result: outcome=%s error=%v", result.Outcome, err)
			}
			deleted := map[string]bool{}
			adapter := newTestAdapter(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				id := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
				if id != "g1-network" && id != "g1-port" {
					t.Errorf("teardown tried to mutate an untracked resource: %s", id)
					response.WriteHeader(http.StatusForbidden)
					return
				}
				if request.Method == http.MethodDelete {
					deleted[id] = true
					response.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(response, request)
			}))
			cleanupM2Resources(t, adapter, recorder.resources())
			for _, id := range []string{"g1-network", "g1-port"} {
				if !deleted[id] {
					t.Errorf("teardown omitted %s after Provision outcome %s", id, outcome)
				}
			}
		})
	}
}
