//go:build integration

package httpapi_test

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

func connectorJSON(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("secret response cacheable")
	}
	var obj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestConnectorEnrollmentHTTPContractAndRecovery(t *testing.T) {
	s := newStack(t)
	cookie := issuedToken(t, s.login("alice", password))
	send := func(method, path, body string) *httptest.ResponseRecorder {
		return s.send(method, path, body, withCookie(cookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
	}
	issued := connectorJSON(t, send("POST", "/api/v1/connectors/enrollment-tokens", `{"name":"고객사 Linux 연결기 🐰"}`), 201)
	token := issued["enrollmentToken"].(string)
	id := issued["tokenId"].(string)
	expires, err := time.Parse(time.RFC3339Nano, issued["expiresAt"].(string))
	if err != nil || expires.Sub(s.clock.now) != connector.EnrollmentTokenLifetime {
		t.Fatal("TTL differs from contract")
	}
	list := connectorJSON(t, send("GET", "/api/v1/connectors", ""), 200)
	if len(list["items"].([]any)) != 0 {
		t.Fatal("issued token counted as Connector")
	}
	body := fmt.Sprintf(`{"enrollmentToken":%q}`, token)
	// No Cookie, Origin or Referer: only the one-time Token authenticates.
	out := connectorJSON(t, s.send("POST", "/api/v1/connector-enrollments", body, withHeader("Content-Type", "application/json")), 201)
	connectorID, credential := out["connectorId"].(string), out["connectorCredential"].(string)
	if connectorID == "" || credential == "" || token == credential {
		t.Fatal("invalid enrollment result")
	}
	list = connectorJSON(t, send("GET", "/api/v1/connectors", ""), 200)
	item := list["items"].([]any)[0].(map[string]any)
	if item["id"] != connectorID || item["name"] != "고객사 Linux 연결기 🐰" || item["registrationStatus"] != "REGISTERED" || item["connectionStatus"] != "NEVER_SEEN" || item["lastSeenAt"] != nil || item["lastConnectorVersion"] != nil || item["lastRuntimeId"] != nil || item["revokedAt"] != nil {
		t.Fatal("new Connector response differs from contract")
	}
	replay := connectorJSON(t, s.send("POST", "/api/v1/connector-enrollments", body, withHeader("Content-Type", "application/json")), 401)
	if replay["code"] != "invalid_enrollment_token" {
		t.Fatal("unexpected replay code")
	}
	if r := send("DELETE", "/api/v1/connectors/enrollment-tokens/"+id, ""); r.Code != 409 {
		t.Fatal("consumed cancellation !=409")
	}
	// The first enrollment response can be discarded. Admin lists/revokes the
	// never-connected identity and issues a fresh Token instead of replaying it.
	for range 2 {
		if r := send("POST", "/api/v1/connectors/"+connectorID+"/revoke", ""); r.Code != 204 {
			t.Fatal("revoke failed")
		}
	}
	if n := s.count(t, `SELECT count(*) FROM connector_credentials WHERE connector_id=$1 AND revoked_at IS NOT NULL`, connectorID); n != 1 {
		t.Fatal("Credential not revoked")
	}
	list = connectorJSON(t, send("GET", "/api/v1/connectors", ""), 200)
	item = list["items"].([]any)[0].(map[string]any)
	if item["registrationStatus"] != "REVOKED" || item["connectionStatus"] != "REVOKED" || item["revokedAt"] == nil {
		t.Fatal("revoked response mismatch")
	}
	newToken := connectorJSON(t, send("POST", "/api/v1/connectors/enrollment-tokens", `{"name":"Replacement"}`), 201)
	newBody, _ := json.Marshal(map[string]any{"enrollmentToken": newToken["enrollmentToken"]})
	replacement := connectorJSON(t, s.send("POST", "/api/v1/connector-enrollments", string(newBody), withHeader("Content-Type", "application/json")), 201)
	if replacement["connectorId"] == connectorID || replacement["connectorCredential"] == credential {
		t.Fatal("replacement reused identity")
	}
	cancelled := connectorJSON(t, send("POST", "/api/v1/connectors/enrollment-tokens", `{"name":"Cancel"}`), 201)
	for range 2 {
		if r := send("DELETE", "/api/v1/connectors/enrollment-tokens/"+cancelled["tokenId"].(string), ""); r.Code != 204 {
			t.Fatal("cancellation failed")
		}
	}
	cancelledBody, _ := json.Marshal(map[string]any{"enrollmentToken": cancelled["enrollmentToken"]})
	if r := s.send("POST", "/api/v1/connector-enrollments", string(cancelledBody), withHeader("Content-Type", "application/json")); r.Code != 401 {
		t.Fatal("cancelled token accepted")
	}
	for _, table := range []string{"connector_enrollment_tokens", "connector_credentials", "connectors"} {
		var data string
		if err := s.pool.QueryRow(t.Context(), `SELECT json_agg(t)::text FROM `+table+` t`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data, token) || strings.Contains(data, credential) {
			t.Fatal("secret persisted")
		}
	}
	if strings.Contains(s.logs.String(), token) || strings.Contains(s.logs.String(), credential) {
		t.Fatal("secret logged")
	}
}

func TestConnectorManagementHTTPAuthorizationAndCSRF(t *testing.T) {
	s := newStack(t)
	cookie := issuedToken(t, s.login("alice", password))
	headers := []func(*http.Request){withCookie(cookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	issued := connectorJSON(t, s.send("POST", "/api/v1/connectors/enrollment-tokens", `{"name":"Org one"}`, headers...), 201)
	tokenID := issued["tokenId"].(string)
	body, _ := json.Marshal(map[string]any{"enrollmentToken": issued["enrollmentToken"]})
	created := connectorJSON(t, s.send("POST", "/api/v1/connector-enrollments", string(body), withHeader("Content-Type", "application/json")), 201)
	id := created["connectorId"].(string)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/v1/connectors", ""},
		{"POST", "/api/v1/connectors/enrollment-tokens", `{"name":"test"}`},
		{"DELETE", "/api/v1/connectors/enrollment-tokens/" + tokenID, ""},
		{"POST", "/api/v1/connectors/" + id + "/revoke", ""},
	}
	for _, route := range routes {
		r := s.send(route.method, route.path, route.body, withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
		if r.Code != 401 {
			t.Fatalf("anonymous %s=%d", route.path, r.Code)
		}
		if route.method != "GET" {
			for _, origin := range []string{"", "https://evil.test", "null"} {
				r := s.send(route.method, route.path, route.body, withCookie(cookie), withHeader("Origin", origin), withHeader("Content-Type", "application/json"))
				if r.Code != 403 {
					t.Fatalf("Origin protection weakened: %s", route.path)
				}
			}
		}
	}
	// A valid Session alone cannot perform Token-only enrollment.
	r := s.send("POST", "/api/v1/connector-enrollments", `{"enrollmentToken":"invalid"}`, withCookie(cookie), withHeader("Content-Type", "application/json"))
	if r.Code != 401 {
		t.Fatal("Cookie authorized installer")
	}
	s.exec(t, `UPDATE users SET organization_role='MEMBER' WHERE id=$1`, s.userID)
	for _, route := range routes {
		if r := s.send(route.method, route.path, route.body, headers...); r.Code != 403 {
			t.Fatalf("MEMBER allowed %s=%d", route.path, r.Code)
		}
	}
	// Make a distinct Organization ADMIN Session with no access to org one's IDs.
	org, user := uuid.New(), uuid.New()
	s.exec(t, `INSERT INTO organizations (id,name) VALUES ($1,'foreign')`, org)
	s.exec(t, `INSERT INTO users (id,organization_id,organization_role) VALUES ($1,$2,'ADMIN')`, user, org)
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	secret := base64.RawURLEncoding.EncodeToString(raw[:])
	digest := sha256.Sum256(raw[:])
	s.exec(t, `INSERT INTO auth_sessions (id,user_id,token_hash,created_at,expires_at) VALUES ($1,$2,$3,$4,$5)`, uuid.New(), user, digest[:], s.clock.now, s.clock.now.Add(time.Hour))
	foreignHeaders := []func(*http.Request){withCookie(secret), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	for _, route := range routes[2:] {
		if r := s.send(route.method, route.path, route.body, foreignHeaders...); r.Code != 404 {
			t.Fatal("cross-org mutation allowed")
		}
	}
	list := connectorJSON(t, s.send("GET", "/api/v1/connectors", "", foreignHeaders...), 200)
	if len(list["items"].([]any)) != 0 {
		t.Fatal("cross-org list leak")
	}
}

func TestConnectorHTTPRejectsMalformedBodiesAndExpiredTokens(t *testing.T) {
	s := newStack(t)
	cookie := issuedToken(t, s.login("alice", password))
	headers := []func(*http.Request){withCookie(cookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json")}
	for _, body := range []string{`{}`, `null`, `[]`, `{"name":null}`, `{"Name":"x"}`, `{"name":"x","organizationId":"foreign"}`, `{"name":"x","name":"y"}`, `{"name":" "}`, `{"name":"x"}{}`, `{"name":"` + strings.Repeat("x", 201) + `"}`} {
		if r := s.send("POST", "/api/v1/connectors/enrollment-tokens", body, headers...); r.Code != 400 {
			t.Fatal("malformed name accepted")
		}
	}
	for _, body := range []string{`{}`, `null`, `{"enrollmentToken":null}`, `{"EnrollmentToken":"x"}`, `{"enrollmentToken":""}`, `{"enrollmentToken":"x","enrollmentToken":"y"}`, `{"enrollmentToken":"x","organizationId":"foreign"}`, `{"enrollmentToken":"` + strings.Repeat("x", 257) + `"}`} {
		if r := s.send("POST", "/api/v1/connector-enrollments", body, withHeader("Content-Type", "application/json")); r.Code != 400 {
			t.Fatal("malformed token body accepted")
		}
	}
	issued := connectorJSON(t, s.send("POST", "/api/v1/connectors/enrollment-tokens", `{"name":"expires"}`, headers...), 201)
	body, _ := json.Marshal(map[string]any{"enrollmentToken": issued["enrollmentToken"]})
	if r := s.send("POST", "/api/v1/connector-enrollments?token=forbidden", string(body), withHeader("Content-Type", "application/json")); r.Code != 400 {
		t.Fatal("query allowed")
	}
	s.clock.now = s.clock.now.Add(connector.EnrollmentTokenLifetime)
	if r := s.send("POST", "/api/v1/connector-enrollments", string(body), withHeader("Content-Type", "application/json")); r.Code != 401 {
		t.Fatal("expired accepted")
	}
}

func TestConnectorEnrollmentTokenRejectsNULNames(t *testing.T) {
	s := newStack(t)
	cookie := issuedToken(t, s.login("alice", password))
	for _, name := range []string{"\x00", "\x00a", "a\x00", "고객\x00연결기"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"name": name})
			if err != nil {
				t.Fatal(err)
			}
			response := s.send("POST", "/api/v1/connectors/enrollment-tokens", string(body),
				withCookie(cookie), withHeader("Origin", trustedOrigin), withHeader("Content-Type", "application/json"))
			problem := connectorJSON(t, response, http.StatusBadRequest)
			if problem["code"] != "invalid_request" {
				t.Fatalf("error code = %v, want invalid_request", problem["code"])
			}
			if n := s.count(t, `SELECT count(*) FROM connector_enrollment_tokens`); n != 0 {
				t.Fatalf("NUL name created enrollment token: count=%d", n)
			}
		})
	}
}
