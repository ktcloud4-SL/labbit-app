package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func transportTestOpen() protocol.PreviewOpenMessage {
	return protocol.PreviewOpenMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpen, MessageID: "open", SentAt: time.Now().UTC(), PreviewSessionID: "session", LabInstanceID: "lab", Generation: 1}, Payload: protocol.PreviewOpenPayload{TargetVmKey: "workspace", ProviderServerID: "server", TargetPort: 3000}}
}
func transportTestAttach(open protocol.PreviewOpenMessage) protocol.PreviewAttachMessage {
	return protocol.PreviewAttachMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewAttach, MessageID: "attach", SentAt: time.Now().UTC(), ReplyToMessageID: open.MessageID, PreviewSessionID: open.PreviewSessionID, LabInstanceID: open.LabInstanceID, Generation: open.Generation}, Payload: protocol.PreviewAttachPayload{RuntimeID: "runtime", TargetVmKey: open.Payload.TargetVmKey, ProviderServerID: open.Payload.ProviderServerID, TargetPort: open.Payload.TargetPort}}
}

func transportTestPeer(t *testing.T) (*previewTransportPeer, string, *websocket.Dialer) {
	t.Helper()
	g := newPreviewTransportPeer("synthetic-token")
	s := httptest.NewTLSServer(g.handler(http.NotFoundHandler()))
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	d := &websocket.Dialer{Subprotocols: []string{protocol.SubprotocolPreviewData}, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	t.Cleanup(func() { g.close(); s.Close() })
	return g, "wss" + strings.TrimPrefix(s.URL, "https") + "/connector/v1/preview-data", d
}

func TestPreviewTransportPeerExactBinding(t *testing.T) {
	mutants := map[string]func(*protocol.PreviewAttachMessage){
		"type":       func(a *protocol.PreviewAttachMessage) { a.Type = "OTHER" },
		"message":    func(a *protocol.PreviewAttachMessage) { a.MessageID = "" },
		"sentAt":     func(a *protocol.PreviewAttachMessage) { a.SentAt = time.Time{} },
		"reply":      func(a *protocol.PreviewAttachMessage) { a.ReplyToMessageID = "other" },
		"session":    func(a *protocol.PreviewAttachMessage) { a.PreviewSessionID = "other" },
		"lab":        func(a *protocol.PreviewAttachMessage) { a.LabInstanceID = "other" },
		"generation": func(a *protocol.PreviewAttachMessage) { a.Generation = 2 },
		"runtime":    func(a *protocol.PreviewAttachMessage) { a.Payload.RuntimeID = "" },
		"vm":         func(a *protocol.PreviewAttachMessage) { a.Payload.TargetVmKey = "other" },
		"server":     func(a *protocol.PreviewAttachMessage) { a.Payload.ProviderServerID = "other" },
		"port":       func(a *protocol.PreviewAttachMessage) { a.Payload.TargetPort = 3001 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			g, url, d := transportTestPeer(t)
			open := transportTestOpen()
			p := g.plan(open, false)
			c, _, err := d.Dial(url, http.Header{"Authorization": []string{"Bearer synthetic-token"}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			a := transportTestAttach(open)
			mutate(&a)
			if err = c.WriteJSON(a); err != nil {
				t.Fatal(err)
			}
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _, err = c.ReadMessage()
			if !websocket.IsCloseError(err, 1008) || p.attaches.Load() != 0 || !g.invalid.Load() {
				t.Fatal("invalid binding was not rejected")
			}
		})
	}
}

func TestPreviewTransportPeerUpgradeAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, auth, protocol string
		status               int
	}{
		{"missing credential", "", protocol.SubprotocolPreviewData, 401},
		{"wrong credential", "Bearer other", protocol.SubprotocolPreviewData, 401},
		{"missing protocol", "Bearer synthetic-token", "", 400},
		{"wrong protocol", "Bearer synthetic-token", protocol.SubprotocolControl, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, url, d := transportTestPeer(t)
			d.Subprotocols = []string{tc.protocol}
			c, response, err := d.Dial(url, http.Header{"Authorization": []string{tc.auth}})
			if c != nil {
				c.Close()
			}
			if response != nil {
				defer response.Body.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatal("upgrade guard failed")
			}
		})
	}
}

func TestPreviewTransportLateAttachAfterFailedOpenCannotPass(t *testing.T) {
	g, url, d := transportTestPeer(t)
	open := transportTestOpen()
	p := g.plan(open, true)
	c, _, err := d.Dial(url, http.Header{"Authorization": []string{"Bearer synthetic-token"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// A failure result could already have been consumed when this arrives.
	if p.attaches.Load() != 0 {
		t.Fatal("unexpected attach before send")
	}
	if err = c.WriteJSON(transportTestAttach(open)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = c.ReadMessage()
	if !websocket.IsCloseError(err, 1008) || p.attaches.Load() != 0 || !g.invalid.Load() || g.drained() {
		t.Fatal("late attach could produce a false PASS")
	}
}

func TestPreviewTransportPeerOverflowAndShutdownAreJoined(t *testing.T) {
	g, url, d := transportTestPeer(t)
	open := transportTestOpen()
	p := g.plan(open, false)
	c, _, err := d.Dial(url, http.Header{"Authorization": []string{"Bearer synthetic-token"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.WriteJSON(transportTestAttach(open)); err != nil {
		t.Fatal(err)
	}
	var ack protocol.PreviewAttachedMessage
	if err = c.ReadJSON(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.ReplyToMessageID != "attach" {
		t.Fatal("ack binding")
	}
	for range 40 {
		if c.WriteMessage(websocket.BinaryMessage, []byte("response")) != nil {
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if p.closed(ctx, false) == nil || !p.overflow.Load() {
		t.Fatal("overflow was silently accepted")
	}
	g.close()
	g.mu.Lock()
	remaining := len(g.connections)
	g.mu.Unlock()
	if remaining != 0 {
		t.Fatal("peer shutdown did not join handlers")
	}
}

func TestPreviewTransportResultsAndHTTPAreExact(t *testing.T) {
	open := transportTestOpen()
	result := protocol.PreviewOpenResultMessage{BaseEnvelope: open.BaseEnvelope}
	result.Type = protocol.MessageTypePreviewOpenResult
	result.ReplyToMessageID = open.MessageID
	if !previewResultMatches(result, open) {
		t.Fatal("valid result rejected")
	}
	result.PreviewSessionID = "other"
	if previewResultMatches(result, open) {
		t.Fatal("wrong Preview session accepted")
	}
	want := append([]byte("LBT175-RESPONSE:"), previewRequestBody...)
	valid := append([]byte(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(want))), want...)
	for _, tc := range []struct {
		name string
		raw  []byte
		good bool
	}{
		{"exact", valid, true}, {"truncated", valid[:len(valid)-1], false},
		{"status", bytes.Replace(valid, []byte("200 OK"), []byte("503 Unavailable"), 1), false},
		{"wrong body", bytes.Replace(valid, []byte("LBT175-RESPONSE:"), []byte("WRONG0-RESPONSE:"), 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validatePreviewTransportHTTP(tc.raw); (err == nil) != tc.good {
				t.Fatalf("exact HTTP assertion: %v", err)
			}
		})
	}
}

func TestPreviewTransportWaitsForTCPPublicationAndRejectsDuplicateFailure(t *testing.T) {
	calls := 0
	if err := waitPreviewTCP(context.Background(), 1, func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, nil
		}
		return 1, nil
	}); err != nil || calls != 3 {
		t.Fatal("delayed application accept was not awaited")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitPreviewTCP(ctx, 1, func(context.Context) (int, error) { return 0, nil }) == nil {
		t.Fatal("never-observed TCP count passed")
	}
	open := transportTestOpen()
	result := protocol.PreviewOpenResultMessage{BaseEnvelope: open.BaseEnvelope, Payload: protocol.PreviewOpenResultPayload{Outcome: protocol.OutcomeSucceeded}}
	result.Type = protocol.MessageTypePreviewOpenResult
	result.ReplyToMessageID = open.MessageID
	raw, _ := json.Marshal(result)
	if !previewSucceeded(raw, open) {
		t.Fatal("success rejected")
	}
	result.Payload.Outcome = protocol.OutcomeFailed
	raw, _ = json.Marshal(result)
	if previewSucceeded(raw, open) {
		t.Fatal("duplicate failed result accepted")
	}
	result.Payload.Outcome = protocol.OutcomeSucceeded
	result.PreviewSessionID = "other"
	raw, _ = json.Marshal(result)
	if previewSucceeded(raw, open) {
		t.Fatal("duplicate wrong session accepted")
	}
}

func TestPreviewTransportScopeAndRecoveryIsolation(t *testing.T) {
	for _, tc := range []struct {
		transport, smoke, op02 bool
		generations            int
		recovery, wantError    bool
	}{
		{true, false, false, 2, false, false}, {true, true, false, 2, false, true}, {true, false, true, 2, false, true},
		{true, false, false, 1, false, true}, {true, false, false, 2, true, true}, {false, false, false, 2, true, false},
	} {
		if err := validatePreviewTransportScope(tc.transport, tc.smoke, tc.op02, tc.generations, tc.recovery); (err != nil) != tc.wantError {
			t.Fatal("transport scope guard failed")
		}
	}
}
