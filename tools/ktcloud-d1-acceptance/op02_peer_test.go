package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func testOP02Peer(t *testing.T) (*op02Peer, *websocket.Dialer, string) {
	t.Helper()
	p := newOP02Peer("synthetic-fixture-token")
	s := httptest.NewTLSServer(p.handler(http.NotFoundHandler()))
	t.Cleanup(s.Close)
	t.Cleanup(p.close)
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	d := &websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Subprotocols: []string{protocol.SubprotocolFileData}}
	return p, d, "wss" + strings.TrimPrefix(s.URL, "https") + "/connector/v1/file-data"
}
func fixtureFilePlan(p *op02Peer) *dataPlan {
	env := protocol.BaseEnvelope{MessageID: "fixture-open", FileRequestID: "fixture", LabInstanceID: "fixture-lab", Generation: 1, SentAt: time.Now().UTC()}
	plan := newDataPlan(env, "owned-server")
	plan.operation = "READ"
	plan.payload = readPayload("file")
	p.files.Store("fixture", plan)
	return plan
}
func fixtureAttach(t *testing.T, c *websocket.Conn, plan *dataPlan, generation int64) {
	t.Helper()
	env := plan.envelope
	env.Type = protocol.MessageTypeFileDataAttach
	env.MessageID = "attach"
	env.Generation = generation
	if c.WriteJSON(protocol.FileDataAttachMessage{BaseEnvelope: env, Payload: protocol.FileDataAttachPayload{RuntimeID: "fixture-runtime", TargetVMKey: "workspace", ProviderServerID: plan.server}}) != nil {
		t.Fatal("fixture ATTACH failed")
	}
}
func TestOP02PeerRejectsUnauthorizedSubprotocolAndTarget(t *testing.T) {
	p, d, url := testOP02Peer(t)
	plan := fixtureFilePlan(p)
	for _, tc := range []struct {
		name, token, subprotocol string
		status                   int
	}{{"credential", "wrong", protocol.SubprotocolFileData, 401}, {"subprotocol", "synthetic-fixture-token", protocol.SubprotocolTerminalData, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *d
			copy.Subprotocols = []string{tc.subprotocol}
			c, response, err := copy.Dial(url, http.Header{"Authorization": []string{"Bearer " + tc.token}})
			if c != nil {
				c.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.status {
				t.Fatal("unauthorized peer Upgrade accepted")
			}
			response.Body.Close()
		})
	}
	c, _, err := d.Dial(url, http.Header{"Authorization": []string{"Bearer synthetic-fixture-token"}})
	if err != nil {
		t.Fatal("trusted TLS Upgrade failed")
	}
	defer c.Close()
	fixtureAttach(t, c, plan, 2)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("wrong generation received ATTACHED")
	}
	if plan.dials.Load() != 0 {
		t.Fatal("wrong target was bound")
	}
}
func TestOP02PeerRequiresExactlyOneEmptyReadBinaryAndNormalClose(t *testing.T) {
	for _, tc := range []struct {
		name          string
		binary        int
		failed, valid bool
	}{{"one-empty", 1, false, true}, {"missing-empty", 0, false, false}, {"two-empty", 2, false, false}, {"failed-no-body", 0, true, true}, {"failed-body", 1, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, url := testOP02Peer(t)
			plan := fixtureFilePlan(p)
			c, _, err := d.Dial(url, http.Header{"Authorization": []string{"Bearer synthetic-fixture-token"}})
			if err != nil {
				t.Fatal("Upgrade failed")
			}
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			fixtureAttach(t, c, plan, 1)
			for i := 0; i < 2; i++ {
				if _, _, err = c.ReadMessage(); err != nil {
					t.Fatal("ATTACHED/request missing")
				}
			}
			env := plan.envelope
			env.Type = protocol.MessageTypeFileReadResult
			env.MessageID = "result"
			env.ReplyToMessageID = "fixture-request"
			payload := map[string]any{"outcome": "SUCCEEDED", "revision": "revision", "size": 0}
			code := ""
			if tc.failed {
				code = "NOT_FOUND"
				payload = map[string]any{"outcome": "FAILED", "error": map[string]any{"code": code}}
			}
			_ = c.WriteJSON(struct {
				protocol.BaseEnvelope
				Payload map[string]any `json:"payload"`
			}{env, payload})
			for i := 0; i < tc.binary; i++ {
				_ = c.WriteMessage(websocket.BinaryMessage, nil)
			}
			if tc.binary == 0 && !tc.failed {
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1000, "missing"), time.Now().Add(time.Second))
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					if _, _, err := c.ReadMessage(); err != nil {
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, body, resultErr := fileResult(ctx, plan, code)
			if (resultErr == nil) != tc.valid || (tc.valid && len(body) != 0) {
				t.Fatal("File frame count/close assertion incorrect")
			}
			c.Close()
			<-done
		})
	}
}
func TestOP02TreeAssertionsRejectEmptyWrongKindsSymlinksAndDuplicates(t *testing.T) {
	valid := []map[string]string{{"name": "file", "kind": "file"}, {"name": "empty", "kind": "file"}, {"name": "dir", "kind": "directory"}}
	for _, tc := range []struct {
		name    string
		entries []map[string]string
		valid   bool
	}{{"correct", valid, true}, {"empty", []map[string]string{}, false}, {"missing", valid[:2], false}, {"wrong-kind", append(append([]map[string]string{}, valid[:2]...), map[string]string{"name": "dir", "kind": "file"}), false}, {"symlink", append(append([]map[string]string{}, valid...), map[string]string{"name": "link", "kind": "file"}), false}, {"duplicate", append(append([]map[string]string{}, valid...), valid[0]), false}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.entries)
			if (validateFixtureTree(raw) == nil) != tc.valid {
				t.Fatal("Tree acceptance false positive")
			}
		})
	}
}
