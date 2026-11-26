package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

func TestStandbyReconciliationIsProbeOnly(t *testing.T) {
	mode := filepath.Join(t.TempDir(), "mode")
	writeMode := func(value string) {
		t.Helper()
		if err := os.WriteFile(mode, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeMode("closed")
	gate := maintenance.New(mode)
	local, err := workercontrol.New(workercontrol.PauseNew)
	if err != nil {
		t.Fatal(err)
	}
	stack := &controlStack{WorkerControl: local}
	cfg := Config{PodAddr: "10.20.4.1:8081", DRReconciliationTLSDir: t.TempDir(),
		DRReconciliationMode: "standby-probe", WorkerControlMode: workercontrol.PauseNew,
		WorkerControlSettings: workercontrol.Settings{Backend: "local", AdmissionPolicy: "MAINTENANCE"}}
	if host, err := reconciliationAddress(cfg, stack, gate, false); host != "10.20.4.1" || err != nil {
		t.Fatalf("Osaka closed probe address = %v, %v", host, err)
	}
	reject := cfg
	reject.PodAddr = "10.10.4.1:8081"
	if _, err := reconciliationAddress(reject, stack, gate, false); err == nil {
		t.Fatal("non-Osaka private address was allowed")
	}
	reject = cfg
	reject.WorkerControlMode = workercontrol.Active
	if _, err := reconciliationAddress(reject, stack, gate, false); err == nil {
		t.Fatal("active local Worker was allowed")
	}
	reject = cfg
	reject.DRReconciliationMode = ""
	if _, err := reconciliationAddress(reject, stack, gate, false); err == nil {
		t.Fatal("full reconciliation started on local mode")
	}

	handler := reconciliationHandler(reconciliationIdentity{}, gate.LocalResumeReady, nil, nil, nil)
	for _, tc := range []struct {
		method, path, file string
		trusted            bool
		want               int
	}{
		{"HEAD", reconciliationPath, "closed", true, 204},
		{"HEAD", reconciliationPath, "open", true, 409},
		{"HEAD", reconciliationPath, "", true, 409},
		{"HEAD", reconciliationPath, "closed", false, 403},
		{"POST", reconciliationPath, "closed", true, 409},
		{"POST", reconciliationOwnerPath, "closed", true, 409},
	} {
		writeMode(tc.file)
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.trusted {
			req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{DNSNames: []string{reconciliationClient}}}}}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("%s %s mode %q returned %d, want %d", tc.method, tc.path, tc.file, w.Code, tc.want)
		}
	}
}

func TestReconciliationListenerIsOptIn(t *testing.T) {
	server, listener, err := reconciliationListener(Config{}, nil, nil, nil, nil)
	if err != nil || server != nil || listener != nil {
		t.Fatal("disabled collector has a listener")
	}
	_, _, err = reconciliationListener(Config{DRReconciliationTLSDir: t.TempDir()}, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("enabled collector without persistent Osaka control was accepted")
	}
}

func TestReconciliationProbeRequiresVerifiedClientCertificate(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{reconciliationClient},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Minute),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, &cert, &cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	ca := x509.NewCertPool()
	ca.AddCert(leaf)
	boot := uuid.New()
	status := func(context.Context) (workercontrol.ProcessReadback, bool) {
		return workercontrol.ProcessReadback{
			Binding:         workercontrol.Binding{Mode: workercontrol.PauseNew, BootID: boot},
			AdmissionClosed: true,
		}, true
	}
	srv := httptest.NewUnstartedServer(reconciliationHandler(reconciliationIdentity{}, nil, nil, status, nil))
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca}
	srv.StartTLS()
	defer srv.Close()

	if res, err := srv.Client().Head(srv.URL + reconciliationPath); err == nil {
		res.Body.Close()
		t.Fatal("anonymous client reached the reconciliation listener")
	}
	transport := srv.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.TLSClientConfig.Certificates = []tls.Certificate{{Certificate: [][]byte{raw}, PrivateKey: key}}
	client := http.Client{Transport: transport}
	res, err := client.Head(srv.URL + reconciliationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("authenticated HEAD returned %d", res.StatusCode)
	}
	if res, err := srv.Client().Get(srv.URL + reconciliationStatusPath); err == nil {
		res.Body.Close()
		t.Fatal("anonymous client reached the status endpoint")
	}
	readback, err := client.Get(srv.URL + reconciliationStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	defer readback.Body.Close()
	var observed workercontrol.ProcessReadback
	if readback.StatusCode != http.StatusOK ||
		json.NewDecoder(readback.Body).Decode(&observed) != nil || observed.Binding.BootID != boot {
		t.Fatal("authenticated GET did not return the current process boot ID")
	}
}

func TestReconciliationHandler(t *testing.T) {
	identity := reconciliationIdentity{ConnectorID: uuid.New(), ProjectID: strings.Repeat("a", 32), ConnectionID: uuid.New()}
	valid := reconciliationRequest{
		Scope: connector.ReconciliationInventoryScope{RunID: "run", RequestSHA: strings.Repeat("a", 64),
			Region: "ap-northeast-3", ConnectorID: identity.ConnectorID, ProjectID: identity.ProjectID, ConnectionID: identity.ConnectionID},
		Binding: workercontrol.Binding{Scope: workercontrol.Scope{ID: "backend", Region: "ap-northeast-3", Cluster: "osaka"},
			Revision: 3, Epoch: 4, Mode: workercontrol.ReconcileOnly, PodID: "backend-a", BootID: uuid.New()},
		SessionID: uuid.New(), Deadline: time.Now().Add(15 * time.Second).UTC(),
		Operations: []connector.ReconciliationInventoryOperation{},
	}
	for _, tc := range []struct {
		name, method, principal string
		change                  func(*reconciliationRequest)
		open, opensDuringRead   bool
		ownerLookup, badLookup  bool
		status, calls           int
	}{
		{name: "protected probe", method: "HEAD", principal: reconciliationClient, status: 204},
		{name: "no trusted client", method: "POST", status: 403},
		{name: "wrong trusted client", method: "POST", principal: "unrelated.labbit.internal", status: 403},
		{name: "wildcard trusted client", method: "POST", principal: "*.osaka.labbit.internal", status: 403},
		{name: "read exact target", method: "POST", principal: reconciliationClient, status: 200, calls: 1},
		{name: "null operations", method: "POST", principal: reconciliationClient, status: 400, change: func(r *reconciliationRequest) {
			r.Operations = nil
		}},
		{name: "open admission", method: "POST", principal: reconciliationClient, open: true, status: 409},
		{name: "changed admission", method: "POST", principal: reconciliationClient, opensDuringRead: true, status: 409, calls: 1},
		{name: "wrong project", method: "POST", principal: reconciliationClient, status: 400, change: func(r *reconciliationRequest) {
			r.Scope.ProjectID = strings.Repeat("b", 32)
		}},
		{name: "expired", method: "POST", principal: reconciliationClient, status: 409, change: func(r *reconciliationRequest) {
			r.Deadline = time.Now().Add(-time.Second)
		}},
		{name: "future", method: "POST", principal: reconciliationClient, status: 409, change: func(r *reconciliationRequest) {
			r.Deadline = time.Now().Add(time.Minute)
		}},
		{name: "owner lookup", method: "POST", principal: reconciliationClient, ownerLookup: true, status: 200, calls: 1},
		{name: "paused owner lookup", method: "POST", principal: reconciliationClient, ownerLookup: true, status: 200, calls: 1, change: func(r *reconciliationRequest) {
			r.Binding.Mode = workercontrol.PauseNew
		}},
		{name: "paused inventory rejected", method: "POST", principal: reconciliationClient, status: 400, change: func(r *reconciliationRequest) {
			r.Binding.Mode = workercontrol.PauseNew
		}},
		{name: "owner lookup without mTLS", method: "POST", ownerLookup: true, status: 403},
		{name: "owner lookup while open", method: "POST", principal: reconciliationClient, ownerLookup: true, open: true, status: 409},
		{name: "owner lookup gate changes", method: "POST", principal: reconciliationClient, ownerLookup: true, opensDuringRead: true, status: 409, calls: 1},
		{name: "owner lookup with inventory fields", method: "POST", principal: reconciliationClient, ownerLookup: true, badLookup: true, status: 400},
		{name: "owner lookup with wrong project", method: "POST", principal: reconciliationClient, ownerLookup: true, status: 400, change: func(r *reconciliationRequest) {
			r.Scope.ProjectID = strings.Repeat("b", 32)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			if tc.change != nil {
				tc.change(&req)
			}
			path, payload := reconciliationPath, any(req)
			if tc.ownerLookup {
				path = reconciliationOwnerPath
				if !tc.badLookup {
					payload = reconciliationOwnerRequest{Scope: req.Scope, Binding: req.Binding, Deadline: req.Deadline}
					req.SessionID, req.Operations = uuid.Nil, nil
				}
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(tc.method, path, bytes.NewReader(body))
			if tc.principal != "" {
				r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{
					{{DNSNames: []string{tc.principal}}},
				}}
			} else {
				r.TLS = nil
			}
			open, calls := tc.open, 0
			w := httptest.NewRecorder()
			reconciliationHandler(identity, func() bool { return !open },
				func(ctx context.Context, request reconciliationRequest) (connector.ReconciliationInventory, error) {
					calls++
					deadline, ok := ctx.Deadline()
					if !ok || !deadline.Equal(req.Deadline) || request.Scope != req.Scope ||
						request.Binding != req.Binding || request.SessionID != req.SessionID ||
						(request.Operations == nil) != (req.Operations == nil) {
						t.Fatal("scope or deadline was replaced")
					}
					open = tc.opensDuringRead
					return connector.ReconciliationInventory{Scope: request.Scope}, nil
				}, nil, nil).ServeHTTP(w, r)
			if w.Code != tc.status || calls != tc.calls || (w.Code != http.StatusOK && w.Body.Len() != 0) {
				t.Fatalf("status=%d calls=%d", w.Code, calls)
			}
		})
	}
}

func TestPrivateReconciliationStatus(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name, principal, method string
		mode                    workercontrol.Mode
		closed, tracked         bool
		want                    int
	}{
		{"paused replacement", reconciliationClient, "GET", workercontrol.PauseNew, true, true, 200},
		{"reconcile replacement", reconciliationClient, "GET", workercontrol.ReconcileOnly, true, true, 200},
		{"standby", reconciliationClient, "GET", workercontrol.PauseNew, true, false, 409},
		{"active, admission closed", reconciliationClient, "GET", workercontrol.Active, true, true, 200},
		{"active, admission open", reconciliationClient, "GET", workercontrol.Active, false, true, 409},
		{"open", reconciliationClient, "GET", workercontrol.PauseNew, false, true, 409},
		{"wrong principal", "unrelated", "GET", workercontrol.PauseNew, true, true, 403},
		{"mutation unsupported", reconciliationClient, "POST", workercontrol.PauseNew, true, true, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, reconciliationStatusPath, nil)
			r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{DNSNames: []string{tc.principal}}}}}
			var status func(context.Context) (workercontrol.ProcessReadback, bool)
			if tc.tracked {
				status = func(context.Context) (workercontrol.ProcessReadback, bool) {
					return workercontrol.ProcessReadback{
						Binding:         workercontrol.Binding{Mode: tc.mode, BootID: id},
						AdmissionClosed: tc.closed,
					}, true
				}
			}
			w := httptest.NewRecorder()
			reconciliationHandler(reconciliationIdentity{}, nil, nil, status, nil).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if tc.want == 200 {
				var result workercontrol.ProcessReadback
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Binding.BootID != id {
					t.Fatal("missing replacement BootID")
				}
			} else if w.Body.Len() > 0 {
				t.Fatal("denial disclosed status")
			}
		})
	}
}
