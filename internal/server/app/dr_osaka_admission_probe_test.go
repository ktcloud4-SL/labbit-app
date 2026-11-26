package app

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOsakaAdmissionPrivateRoutes(t *testing.T) {
	mode := "closed"
	calls := 0
	h := withOsakaAdmissionProbe(http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if _, err := r.Cookie("__Host-labbit-session"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == "GET" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}), func() string { return mode }, false)
	id := "7cf00925-c340-4317-92af-72a763f4ccdd"
	reset := "/api/v1/lab-instances/" + id + "/reset"
	for _, tc := range []struct {
		method, path, state, name, body string
		cookie                          bool
		want, reached                   int
	}{
		{"GET", "/api/v1/me", "closed", reconciliationClient, "", true, 200, 1},
		{"POST", "/api/v1/classes/" + id + "/lab-executions", "closed", reconciliationClient, "{}", true, 503, 1},
		{"POST", reset, "closed", reconciliationClient, "", true, 503, 1},
		{"POST", "/api/v1/lab-executions/" + id + "/cleanup", "closed", reconciliationClient, "", true, 503, 1},
		{"POST", reset, "closed", reconciliationClient, "", false, 401, 1},
		{"POST", reset, "closed", "wrong-client", "", true, 403, 0},
		{"POST", reset, "open", reconciliationClient, "", true, 409, 0},
		{"POST", reset, "invalid", reconciliationClient, "", true, 412, 0},
		{"POST", reset + "?redirect=x", "closed", reconciliationClient, "", true, 404, 0},
		{"POST", "/api/v1/lab-instances/invalid/reset", "closed", reconciliationClient, "", true, 400, 0},
		{"POST", reset, "closed", reconciliationClient, strings.Repeat("a", 4097), true, 400, 0},
		{"POST", "/api/v1/classes", "closed", reconciliationClient, "", true, 404, 0},
	} {
		mode, calls = tc.state, 0
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{
			{DNSNames: []string{tc.name}},
		}}}
		if tc.cookie {
			req.AddCookie(&http.Cookie{Name: "__Host-labbit-session", Value: "fixture"})
		}
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		if resp.Code != tc.want || calls != tc.reached {
			t.Fatalf("%s %s (%s, %s): status=%d calls=%d, want %d/%d", tc.method, tc.path, tc.state, tc.name, resp.Code, calls, tc.want, tc.reached)
		}
	}
}

func TestOsakaAdmissionStandbyOnlyAcceptsAuthenticatedGet(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/me" {
			t.Fatalf("unexpected API invocation: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	h := withOsakaAdmissionProbe(http.NotFoundHandler(), api, func() string { return "closed" }, true)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/me", http.StatusOK},
		{"HEAD", "/api/v1/me", http.StatusPreconditionFailed},
		{"POST", "/api/v1/lab-instances/7cf00925-c340-4317-92af-72a763f4ccdd/reset", http.StatusNotFound},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{
			{DNSNames: []string{reconciliationClient}},
		}}}
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		if resp.Code != tc.status {
			t.Fatalf("%s %s: status=%d, want %d", tc.method, tc.path, resp.Code, tc.status)
		}
	}
}
