package connectorwss

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// Executor.Run has four independent workers. Exercise their equivalent wire
// dispatch through the real Backend Router and Connector, including one parent
// Operation with distinct Items. A rejected ACK would become FAILED/REJECTED
// in Executor.rejected, so all four must be accepted before the first finishes.
func TestActualConnectorAdmitsFourWorkerItemsWhileProviderBlocked(t *testing.T) {
	h := newRoutedHarness(t, realConnectorHeartbeat)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, active atomic.Int32
	rig := startConnector(t, h, testCredential, h.principal.ConnectorID, &provider.MockProvider{
		ProvisionFunc: func(ctx context.Context, r provider.ProvisionRequest) (provider.OperationResult, error) {
			if active.Add(1) != 1 {
				t.Error("Provider mutations overlapped")
			}
			defer active.Add(-1)
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return succeeded(r.Correlation, "srv-"+r.LabInstanceID), nil
		},
	})
	sent := make(map[string]string)
	for i := range 4 {
		lab := fmt.Sprintf("lab-%d", i)
		sent[lab] = send(t, h, provisionCommand(h.principal.ConnectorID, "shared-operation", lab, 1)).MessageID
		if i == 0 {
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first Provider call did not start")
			}
		}
	}
	for _, event := range h.events.waitCount(t, 4) {
		ack, ok := event.(connector.OperationAckEvent)
		if !ok || !ack.Payload.Accepted || ack.Payload.Error != nil || ack.RequestMessageID != sent[ack.Correlation.LabInstanceID] {
			t.Fatalf("valid worker Item rejected or miscorrelated: %+v", event)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("Provider calls before release=%d, want 1", calls.Load())
	}
	close(release)
	for _, event := range h.events.waitCount(t, 8)[4:] {
		result, ok := event.(connector.OperationResultEvent)
		if !ok || result.Payload.Outcome != protocol.OutcomeSucceeded || result.Correlation.OperationID != "shared-operation" || result.RequestMessageID != sent[result.Correlation.LabInstanceID] {
			t.Fatalf("admitted Item did not complete correctly: %+v", event)
		}
		delete(sent, result.Correlation.LabInstanceID)
	}
	if calls.Load() != 4 || len(sent) != 0 || pending(h, h.principal.ConnectorID) != 0 {
		t.Fatalf("calls=%d remaining=%v pending=%d", calls.Load(), sent, pending(h, h.principal.ConnectorID))
	}
	wantNoHandlerErrors(t, rig)
}
