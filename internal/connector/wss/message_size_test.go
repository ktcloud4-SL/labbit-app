package wss

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/mock"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

func sizeRequest(kind string) protocol.ProviderRequestMessage {
	return protocol.ProviderRequestMessage{BaseEnvelope: protocol.BaseEnvelope{
		Type: protocol.MessageTypeProviderRequest, MessageID: "size-request", RequestID: "size-http", SentAt: time.Now().UTC(),
		TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", TraceState: "vendor=value",
	}, Payload: protocol.ProviderRequestPayload{RequestType: kind, ProviderConnectionID: "provider-1"}}
}

func sizeProvider() *provider.MockProvider {
	return &provider.MockProvider{ConnectionID: "provider-1",
		ListImagesFunc: func(context.Context) ([]provider.Image, error) {
			items := make([]provider.Image, 5000)
			for i := range items {
				items[i] = provider.Image{ID: "00000000-0000-0000-0000-000000000001", Name: strings.Repeat("가<", 120), Status: "ACTIVE"}
			}
			return items, nil
		},
		ListFlavorsFunc: func(context.Context) ([]provider.Flavor, error) {
			items := make([]provider.Flavor, 5000)
			for i := range items {
				items[i] = provider.Flavor{ID: "flavor-1", Name: strings.Repeat("가<", 120), VCPUs: 1, RAMMiB: 1024, DiskGiB: 10}
			}
			return items, nil
		}}
}

func TestProviderResponseOversizedCatalogIsBoundedFailure(t *testing.T) {
	for _, kind := range []string{protocol.ProviderRequestListImages, protocol.ProviderRequestListFlavors} {
		t.Run(kind, func(t *testing.T) {
			request := sizeRequest(kind)
			var response protocol.ProviderResponseMessage
			h := NewHandler(sizeProvider(), SendMessageFunc(func(_ context.Context, message interface{}) error {
				response = message.(protocol.ProviderResponseMessage)
				return nil
			}))
			raw, _ := json.Marshal(request)
			if err := h.HandleMessage(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(response)
			if int64(len(encoded)) > protocol.MaxJSONMessageSize || response.Payload.Outcome != protocol.OutcomeFailed || response.Payload.Items != nil || response.Payload.Error == nil || response.Payload.Error.Code != "ERR_CONNECTOR_INTERNAL" {
				t.Fatalf("response bytes=%d outcome=%s items=%d error=%v", len(encoded), response.Payload.Outcome, len(response.Payload.Items), response.Payload.Error)
			}
			if response.ReplyToMessageID != request.MessageID || response.RequestID != request.RequestID || response.TraceParent != request.TraceParent || response.TraceState != request.TraceState {
				t.Fatal("response lost correlation/trace")
			}
		})
	}
}

func TestProviderResponseHugeCorrelationCannotBypassLimit(t *testing.T) {
	request := sizeRequest(protocol.ProviderRequestListImages)
	// The incoming message fits exactly, but the larger response envelope does
	// not. Required correlation must not be truncated to squeeze in a response.
	raw, _ := json.Marshal(request)
	request.MessageID += strings.Repeat("x", int(protocol.MaxJSONMessageSize)-len(raw))
	raw, _ = json.Marshal(request)
	if int64(len(raw)) != protocol.MaxJSONMessageSize {
		t.Fatal("invalid boundary fixture")
	}
	sent := false
	h := NewHandler(sizeProvider(), SendMessageFunc(func(context.Context, interface{}) error { sent = true; return nil }))
	if err := h.HandleMessage(context.Background(), raw); err == nil || sent {
		t.Fatal("unbounded fallback was sent or not rejected")
	}
}

func TestProviderResponseFinalJSONExactLimitAndOneByteOver(t *testing.T) {
	for _, kind := range []string{protocol.ProviderRequestListImages, protocol.ProviderRequestListFlavors} {
		t.Run(kind, func(t *testing.T) {
			response := protocol.ProviderResponseMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeProviderResponse, MessageID: "response", ReplyToMessageID: "request", SentAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)}, Payload: protocol.ProviderResponsePayload{RequestType: kind, Outcome: protocol.OutcomeSucceeded}}
			var item interface{} = protocol.ProviderImage{Kind: "IMAGE", ID: "i", Name: "가<&", Status: "ACTIVE"}
			if kind == protocol.ProviderRequestListFlavors {
				item = protocol.ProviderFlavor{Kind: "FLAVOR", ID: "f", Name: "가<&", VCPUs: 1, RAMMiB: 1024, DiskGiB: 10}
			}
			itemJSON, _ := json.Marshal(item)
			response.Payload.Items = []interface{}{item}
			oneJSON, _ := json.Marshal(response)
			count := 1 + (int(protocol.MaxJSONMessageSize)-len(oneJSON))/(len(itemJSON)+1)
			for i := 1; i < count; i++ {
				response.Payload.Items = append(response.Payload.Items, item)
			}
			encoded, _ := json.Marshal(response)
			padding := strings.Repeat("x", int(protocol.MaxJSONMessageSize)-len(encoded))
			if kind == protocol.ProviderRequestListImages {
				last := item.(protocol.ProviderImage)
				last.Name += padding
				response.Payload.Items[count-1] = last
			} else {
				last := item.(protocol.ProviderFlavor)
				last.Name += padding
				response.Payload.Items[count-1] = last
			}
			encoded, _ = json.Marshal(response)
			if int64(len(encoded)) != protocol.MaxJSONMessageSize {
				t.Fatal("invalid final response size fixture")
			}
			bounded, err := boundedProviderResponse(response)
			if err != nil || bounded.Payload.Outcome != protocol.OutcomeSucceeded || len(bounded.Payload.Items) != count {
				t.Fatal("exact limit catalog rejected/truncated")
			}
			if kind == protocol.ProviderRequestListImages {
				last := response.Payload.Items[count-1].(protocol.ProviderImage)
				last.Name += "x"
				response.Payload.Items[count-1] = last
			} else {
				last := response.Payload.Items[count-1].(protocol.ProviderFlavor)
				last.Name += "x"
				response.Payload.Items[count-1] = last
			}
			bounded, err = boundedProviderResponse(response)
			if err != nil || bounded.Payload.Outcome != protocol.OutcomeFailed || bounded.Payload.Items != nil {
				t.Fatal("one byte over catalog was not replaced with a bounded failure")
			}
		})
	}
}

func TestClientSendMessageFinalJSONSizeBoundaryKeepsSocket(t *testing.T) {
	server := mock.NewMockSaaS("size-token")
	server.OnConnected = func(conn *websocket.Conn) { conn.SetReadLimit(protocol.MaxJSONMessageSize) }
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL(), Credential: "size-token", AllowInsecure: true})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Dial(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendHello(ctx); err != nil {
		t.Fatal(err)
	}
	connection := client.Conn()
	message := map[string]string{"type": "TEST", "payload": "가<&"}
	base, _ := json.Marshal(message)
	message["payload"] += strings.Repeat("x", int(protocol.MaxJSONMessageSize)-len(base))
	exact, _ := json.Marshal(message)
	if int64(len(exact)) != protocol.MaxJSONMessageSize {
		t.Fatal("invalid encoded byte fixture")
	}
	if err := client.SendMessage(ctx, message); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	message["payload"] += "x"
	if err := client.SendMessage(ctx, message); err == nil {
		t.Fatal("one byte over accepted")
	}
	if client.Conn() != connection {
		t.Fatal("local size rejection closed socket")
	}
	if err := client.SendMessage(ctx, sizeHeartbeat("size-heartbeat")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range server.ReceivedMessages() {
			if strings.Contains(string(raw), "size-heartbeat") {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("peer did not receive heartbeat after local rejection")
}

func TestProviderResponseSocketSurvivesLargeCatalogThenSmallQuery(t *testing.T) {
	server := mock.NewMockSaaS("catalog-token")
	server.OnConnected = func(conn *websocket.Conn) { conn.SetReadLimit(protocol.MaxJSONMessageSize) }
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL(), Credential: "catalog-token", AllowInsecure: true})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Dial(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendHello(ctx); err != nil {
		t.Fatal(err)
	}
	p := sizeProvider()
	h := NewHandler(p, client)
	raw, _ := json.Marshal(sizeRequest(protocol.ProviderRequestListImages))
	if err := h.HandleMessage(ctx, raw); err != nil {
		t.Fatal(err)
	}
	p.ListImagesFunc = func(context.Context) ([]provider.Image, error) {
		return []provider.Image{{ID: "small", Name: "small", Status: "ACTIVE"}}, nil
	}
	if err := h.HandleMessage(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := client.SendMessage(ctx, sizeHeartbeat("catalog-heartbeat")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		failed, success, heartbeat := false, false, false
		for _, raw := range server.ReceivedMessages() {
			var message protocol.ProviderResponseMessage
			_ = json.Unmarshal(raw, &message)
			if message.Type == protocol.MessageTypeProviderResponse {
				failed = failed || message.Payload.Outcome == protocol.OutcomeFailed
				success = success || message.Payload.Outcome == protocol.OutcomeSucceeded
			}
			heartbeat = heartbeat || strings.Contains(string(raw), "catalog-heartbeat")
		}
		if failed && success && heartbeat {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bounded failure/small success/heartbeat not all received")
}

func sizeHeartbeat(id string) protocol.HeartbeatMessage {
	return protocol.HeartbeatMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeHeartbeat, MessageID: id, SentAt: time.Now().UTC()}, Payload: protocol.HeartbeatPayload{ObservedAt: time.Now().UTC()}}
}

func TestProviderResponseEmptyOrQueryFailureDoesNotLeakItems(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "failure"}[fail], func(t *testing.T) {
			p := sizeProvider()
			p.ListImagesFunc = func(context.Context) ([]provider.Image, error) {
				if fail {
					return []provider.Image{{ID: "partial", Name: "must-not-leak"}}, errors.New("raw-provider-secret")
				}
				return nil, nil
			}
			h := NewHandler(p, SendMessageFunc(func(_ context.Context, message interface{}) error {
				response := message.(protocol.ProviderResponseMessage)
				if len(response.Payload.Items) != 0 {
					t.Fatal("unexpected items")
				}
				raw, _ := json.Marshal(response)
				if strings.Contains(string(raw), "raw-provider-secret") || strings.Contains(string(raw), "must-not-leak") {
					t.Fatal("query leaked raw detail")
				}
				return nil
			}))
			raw, _ := json.Marshal(sizeRequest(protocol.ProviderRequestListImages))
			if err := h.HandleMessage(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
		})
	}
}
