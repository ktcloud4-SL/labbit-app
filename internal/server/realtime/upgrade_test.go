package realtime_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktcloud4-SL/labbit-app/internal/server/realtime"
)

// Browser Upgrade는 WebSocket connection이 되기 전에 Origin, Cookie, subprotocol, 현재 인증을 거절한다.
func TestBrowserUpgradeRejectedBeforeUpgrade(t *testing.T) {
	e := newEnv(t)

	tests := []struct {
		name      string
		cookie    string
		origin    string
		protocols []string
		want      int
	}{
		{name: "no cookie", cookie: "", origin: trustedOrigin, want: http.StatusUnauthorized},
		{name: "invalid or expired login session", cookie: "unknown-session-cookie", origin: trustedOrigin, want: http.StatusUnauthorized},
		{name: "wrong origin", cookie: ownerCookie, origin: "https://evil.test", want: http.StatusForbidden},
		{name: "missing origin", cookie: ownerCookie, origin: "", want: http.StatusForbidden},
		{name: "origin with path", cookie: ownerCookie, origin: trustedOrigin + "/app", want: http.StatusForbidden},
		{name: "wrong subprotocol", cookie: ownerCookie, origin: trustedOrigin, protocols: []string{realtime.DataSubprotocol}, want: http.StatusBadRequest},
		{name: "live subprotocol is not terminal", cookie: ownerCookie, origin: trustedOrigin, protocols: []string{"labbit.live.v1"}, want: http.StatusBadRequest},
		{name: "no subprotocol", cookie: ownerCookie, origin: trustedOrigin, protocols: []string{}, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, resp, err := e.dialBrowser(tt.cookie, tt.origin, tt.protocols...)
			if err == nil {
				t.Fatal("Upgrade가 성공함")
			}
			if resp == nil || resp.StatusCode != tt.want {
				t.Fatalf("status = %v, want %d (err = %v)", resp, tt.want, err)
			}
		})
	}
}

// Session token을 URL query로 받지 않는다. query의 값은 인증으로 인정하지 않는다.
func TestBrowserUpgradeIgnoresSessionTokenInQuery(t *testing.T) {
	e := newEnv(t)
	header := http.Header{"Origin": []string{trustedOrigin}}
	dialer := websocket.Dialer{Subprotocols: []string{realtime.BrowserSubprotocol}, HandshakeTimeout: 5 * time.Second}
	_, resp, err := dialer.Dial(e.wsURL(realtime.BrowserPath)+"?token="+ownerCookie+"&"+realtime.SessionCookieName+"="+ownerCookie, header)
	if err == nil {
		t.Fatal("query의 token으로 Upgrade가 성공함")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestUpgradeEndpointsRequireWebSocketUpgrade(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{realtime.BrowserPath, realtime.DataPath} {
		resp, err := http.Get(e.server.URL + path)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET %s status = %d, want 400", path, resp.StatusCode)
		}
	}
}

// Connector Terminal Data Upgrade는 Connector credential과 subprotocol을 WebSocket connection이 되기 전에 확인한다.
func TestDataUpgradeRejectedBeforeUpgrade(t *testing.T) {
	e := newEnv(t)

	tests := []struct {
		name       string
		credential string
		protocols  []string
		authHeader string
		want       int
	}{
		{name: "no authorization", want: http.StatusUnauthorized},
		{name: "bad credential", credential: "not-a-known-credential", want: http.StatusUnauthorized},
		{name: "non-bearer scheme", authHeader: "Basic " + connectorCred, want: http.StatusUnauthorized},
		{name: "control subprotocol is not terminal data", credential: connectorCred, protocols: []string{"labbit.connector.v1"}, want: http.StatusBadRequest},
		{name: "browser subprotocol is not terminal data", credential: connectorCred, protocols: []string{realtime.BrowserSubprotocol}, want: http.StatusBadRequest},
		{name: "no subprotocol", credential: connectorCred, protocols: []string{}, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			switch {
			case tt.authHeader != "":
				header.Set("Authorization", tt.authHeader)
			case tt.credential != "":
				header.Set("Authorization", "Bearer "+tt.credential)
			}
			protocols := tt.protocols
			if protocols == nil {
				protocols = []string{realtime.DataSubprotocol}
			}
			dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 5 * time.Second}
			_, resp, err := dialer.Dial(e.wsURL(realtime.DataPath), header)
			if err == nil {
				t.Fatal("Upgrade가 성공함")
			}
			if resp == nil || resp.StatusCode != tt.want {
				t.Fatalf("status = %v, want %d (err = %v)", resp, tt.want, err)
			}
		})
	}
}

// 인증은 Upgrade 전에 끝난다. 거절된 요청은 session 상태나 Control에 attach를 만들지 않는다.
func TestRejectedUpgradesDoNotTouchSessions(t *testing.T) {
	e := newEnv(t)
	s := e.newSession()

	_, _, _ = e.dialBrowser("", trustedOrigin)
	_, _, _ = e.dialData("")
	_, _, _ = e.dialData("bad-credential")

	if e.relay.DataBound(s.ID) {
		t.Fatal("거절된 Upgrade 뒤 data channel이 bind됨")
	}
	attached, detached, closed, ended := e.control.snapshot()
	if len(attached)+len(detached)+len(closed)+len(ended) != 0 {
		t.Fatalf("Control 호출 = attached %v detached %v closed %v ended %v, want none", attached, detached, closed, ended)
	}
}

func TestRelayShutdownRejectsNewUpgrades(t *testing.T) {
	e := newEnv(t)
	e.relay.Close()

	_, resp, err := e.dialBrowser(ownerCookie, trustedOrigin)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Browser Upgrade = %v, %v, want 503", resp, err)
	}
	_, resp, err = e.dialData(connectorCred)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Data Upgrade = %v, %v, want 503", resp, err)
	}
	if err := e.relay.Expect(realtime.Expected{TerminalSessionID: "x", ConnectorID: connectorID1, LabInstanceID: "y", Generation: 1}); err != realtime.ErrRelayClosed {
		t.Fatalf("Expect() after Close = %v, want ErrRelayClosed", err)
	}
}

func TestExpectValidatesInput(t *testing.T) {
	e := newEnv(t)
	valid := realtime.Expected{TerminalSessionID: "s", ConnectorID: connectorID1, LabInstanceID: "l", Generation: 1}

	for name, mod := range map[string]func(*realtime.Expected){
		"empty session":    func(x *realtime.Expected) { x.TerminalSessionID = "" },
		"empty connector":  func(x *realtime.Expected) { x.ConnectorID = "" },
		"empty lab":        func(x *realtime.Expected) { x.LabInstanceID = "" },
		"generation zero":  func(x *realtime.Expected) { x.Generation = 0 },
		"generation below": func(x *realtime.Expected) { x.Generation = -1 },
	} {
		x := valid
		mod(&x)
		if err := e.relay.Expect(x); err != realtime.ErrInvalidExpected {
			t.Fatalf("%s: Expect() = %v, want ErrInvalidExpected", name, err)
		}
	}
	if err := e.relay.Expect(valid); err != nil {
		t.Fatalf("Expect(valid) error = %v", err)
	}
	if err := e.relay.Expect(valid); err != realtime.ErrDuplicateSession {
		t.Fatalf("Expect(duplicate) = %v, want ErrDuplicateSession", err)
	}
}

func TestOptionsRequireDependencies(t *testing.T) {
	ok := realtime.Options{
		Control:     newFakeControl(),
		Connectors:  newFakeConnectors(),
		AllowOrigin: func(string) bool { return true },
	}
	if _, err := realtime.New(ok); err != nil {
		t.Fatalf("New(valid) error = %v", err)
	}
	for name, mod := range map[string]func(*realtime.Options){
		"control":                func(o *realtime.Options) { o.Control = nil },
		"connectors":             func(o *realtime.Options) { o.Connectors = nil },
		"origin":                 func(o *realtime.Options) { o.AllowOrigin = nil },
		"negative grace":         func(o *realtime.Options) { o.Grace = -time.Second },
		"negative write timeout": func(o *realtime.Options) { o.WriteTimeout = -1 },
	} {
		o := ok
		mod(&o)
		if _, err := realtime.New(o); err == nil {
			t.Fatalf("%s: New()가 성공함", name)
		}
	}
	if strings.TrimSpace(realtime.SessionCookieName) != "__Host-labbit-session" {
		t.Fatalf("Cookie 이름 = %q", realtime.SessionCookieName)
	}
}
