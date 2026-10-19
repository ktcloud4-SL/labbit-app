//go:build integration

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connectorwss"
	"github.com/ktcloud4-SL/labbit-app/internal/server/terminal/terminaltest"
)

func TestEnrollmentAPIWSSHeartbeatStatusAndExplicitRevoke(t *testing.T) {
	recorder, tracer := newRecordingTracer(t)
	e := newTerminalEnv(t, withTracer(tracer))
	terminaltest.Exec(t, e.conn, `UPDATE users SET organization_role='ADMIN' WHERE id=$1`, e.fixture.OutsiderID)
	admin := e.outsiderCookie
	issue := e.request("POST", "/api/v1/connectors/enrollment-tokens", admin, `{"name":"Enrolled Linux"}`)
	if issue.Status != 201 {
		t.Fatalf("issue status=%d", issue.Status)
	}
	issued := issue.json(t)
	token := issued["enrollmentToken"].(string)
	body, _ := json.Marshal(map[string]any{"enrollmentToken": token})
	enrollment := e.request("POST", "/api/v1/connector-enrollments", "", string(body), func(r *http.Request) { r.Header.Del("Origin") })
	if enrollment.Status != 201 {
		t.Fatalf("enroll status=%d", enrollment.Status)
	}
	out := enrollment.json(t)
	id := uuid.MustParse(out["connectorId"].(string))
	credential := out["connectorCredential"].(string)
	summary := func() map[string]any {
		t.Helper()
		r := e.request("GET", "/api/v1/connectors", admin, "")
		if r.Status != 200 {
			t.Fatalf("list status=%d", r.Status)
		}
		for _, raw := range r.json(t)["items"].([]any) {
			v := raw.(map[string]any)
			if v["id"] == id.String() {
				return v
			}
		}
		t.Fatal("enrolled identity missing")
		return nil
	}
	if v := summary(); v["connectionStatus"] != "NEVER_SEEN" {
		t.Fatal("registered without heartbeat !=NEVER_SEEN")
	}
	peer := terminaltest.NewConnector(t, e.server.URL, credential)
	peer.Start() // Real WebSocket contract peer, no Provider or Linux runtime involved.
	eventually(t, "HELLO metadata", 5*time.Second, func() bool { return summary()["lastRuntimeId"] != nil })
	if v := summary(); v["lastConnectorVersion"] == nil || v["connectionStatus"] != "NEVER_SEEN" {
		t.Fatal("HELLO treated as heartbeat")
	}
	peer.Heartbeat()
	eventually(t, "server heartbeat online", 5*time.Second, func() bool { return summary()["connectionStatus"] == "ONLINE" })
	var seen time.Time
	if err := e.conn.QueryRow(t.Context(), `SELECT last_seen_at FROM connectors WHERE id=$1`, id).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if time.Since(seen) > 5*time.Second {
		t.Fatal("heartbeat did not use server receipt time")
	}
	// Advance only the isolated fixture's persisted observation to represent 45s
	// elapsed, while unit tests cover the exact boundary with an injected clock.
	terminaltest.Exec(t, e.conn, `UPDATE connectors SET last_seen_at=now()-interval '46 seconds' WHERE id=$1`, id)
	if v := summary(); v["connectionStatus"] != "OFFLINE" {
		t.Fatal("stale heartbeat remained online")
	}
	peer.Heartbeat()
	eventually(t, "heartbeat returns online", 5*time.Second, func() bool { return summary()["connectionStatus"] == "ONLINE" })
	data := e.standaloneData(id, credential)
	e.addCredential(id, secondCredential)
	otherData := e.standaloneData(id, secondCredential)
	revoke := e.request("POST", "/api/v1/connectors/"+id.String()+"/revoke", admin, "")
	if revoke.Status != 204 {
		t.Fatalf("revoke status=%d", revoke.Status)
	}
	if code, ok := peer.ControlClosed(5 * time.Second); !ok || code != 4001 {
		t.Fatal("Control WSS survived revoke")
	}
	for _, d := range []standalone{data, otherData} {
		_ = d.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, err := d.conn.ReadMessage()
		var closed *websocket.CloseError
		if !errors.As(err, &closed) || closed.Code != 4001 {
			t.Fatal("Data WSS survived explicit revoke")
		}
	}
	v := summary()
	if v["registrationStatus"] != "REVOKED" || v["connectionStatus"] != "REVOKED" {
		t.Fatal("revoked status missing")
	}
	dialer := websocket.Dialer{Subprotocols: []string{"labbit.connector.v1"}, HandshakeTimeout: 5 * time.Second}
	control, resp, err := dialer.Dial("ws"+strings.TrimPrefix(e.server.URL, "http")+connectorwss.Path, http.Header{"Authorization": []string{"Bearer " + credential}})
	if control != nil {
		control.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("Control reauthentication accepted revoked identity")
	}
	resp.Body.Close()
	d, resp, err := e.dialData(credential)
	if d != nil {
		d.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatal("Data reauthentication accepted revoked identity")
	}
	resp.Body.Close()
	var allRevoked bool
	if err := e.conn.QueryRow(t.Context(), `SELECT bool_and(revoked_at IS NOT NULL) FROM connector_credentials WHERE connector_id=$1`, id).Scan(&allRevoked); err != nil || !allRevoked {
		t.Fatal("not all credentials revoked")
	}
	if strings.Contains(e.logs.String(), token) || strings.Contains(e.logs.String(), credential) {
		t.Fatal("enrollment secret logged")
	}
	for _, span := range recorder.Ended() {
		serialized := fmt.Sprintf("%v %v %v", span.Name(), span.Attributes(), span.Events())
		if strings.Contains(serialized, token) || strings.Contains(serialized, credential) {
			t.Fatal("enrollment secret traced")
		}
	}
}
