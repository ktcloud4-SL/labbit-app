package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/valkeyowner"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
)

const reconciliationPath = "/dr-control/reconciliation"
const reconciliationOwnerPath = reconciliationPath + "/owner"
const reconciliationStatusPath = reconciliationPath + "/status"
const standbyStatusPath = reconciliationPath + "/standby-status"
const reconciliationClient = "dr-runner.osaka.labbit.internal"
const reconciliationMaxBody = 128 * 1024

var reconciliationProject = regexp.MustCompile(`^[a-f0-9]{32}$`)

type reconciliationIdentity struct {
	ConnectorID  uuid.UUID `json:"connector_id"`
	ProjectID    string    `json:"provider_project_id"`
	ConnectionID uuid.UUID `json:"connection_id"`
}

type reconciliationRequest struct {
	Scope      connector.ReconciliationInventoryScope       `json:"scope"`
	Binding    workercontrol.Binding                        `json:"binding"`
	SessionID  uuid.UUID                                    `json:"session_id"`
	Deadline   time.Time                                    `json:"valid_until_utc"`
	Operations []connector.ReconciliationInventoryOperation `json:"operations"`
}

type reconciliationOwnerRequest struct {
	Scope    connector.ReconciliationInventoryScope `json:"scope"`
	Binding  workercontrol.Binding                  `json:"binding"`
	Deadline time.Time                              `json:"valid_until_utc"`
}

type inventoryReader func(context.Context, reconciliationRequest) (connector.ReconciliationInventory, error)

func reconciliationHandler(identity reconciliationIdentity, closed func() bool, read inventoryReader,
	status func(context.Context) (workercontrol.ProcessReadback, bool), standby http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path != reconciliationPath && r.URL.Path != reconciliationOwnerPath &&
			r.URL.Path != reconciliationStatusPath && r.URL.Path != standbyStatusPath) || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if !isReconciliationClient(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == standbyStatusPath {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if standby == nil {
				http.NotFound(w, r)
				return
			}
			standby.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == reconciliationStatusPath {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if status == nil {
				w.WriteHeader(http.StatusConflict)
				return
			}
			snapshot, ok := status(r.Context())
			if !ok || !snapshot.AdmissionClosed || snapshot.Control.Unverified ||
				(snapshot.Binding.Mode != workercontrol.PauseNew && snapshot.Binding.Mode != workercontrol.ReconcileOnly &&
					snapshot.Binding.Mode != workercontrol.Active) {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(snapshot)
			return
		}
		if r.Method == http.MethodHead && r.URL.Path == reconciliationPath {
			if read == nil && closed != nil && !closed() {
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if read == nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		var req reconciliationRequest
		input := json.NewDecoder(http.MaxBytesReader(w, r.Body, reconciliationMaxBody))
		input.DisallowUnknownFields()
		if r.URL.Path == reconciliationOwnerPath {
			var owner reconciliationOwnerRequest
			if input.Decode(&owner) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			req.Scope, req.Binding, req.Deadline = owner.Scope, owner.Binding, owner.Deadline
		} else if input.Decode(&req) != nil || req.SessionID == uuid.Nil || req.Operations == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if input.Decode(new(any)) != io.EOF ||
			req.Binding.Validate() != nil || req.Binding.Scope.Region != "ap-northeast-3" ||
			(req.Binding.Mode != workercontrol.ReconcileOnly &&
				!(req.Binding.Mode == workercontrol.PauseNew && r.URL.Path == reconciliationOwnerPath)) ||
			req.Scope.Region != req.Binding.Scope.Region ||
			req.Scope.ConnectorID != identity.ConnectorID || req.Scope.ProjectID != identity.ProjectID ||
			req.Scope.ConnectionID != identity.ConnectionID {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		if !now.Before(req.Deadline) || req.Deadline.After(now.Add(30*time.Second)) || !closed() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		ctx, cancel := context.WithDeadline(r.Context(), req.Deadline)
		defer cancel()
		report, err := read(ctx, req)
		if err != nil || ctx.Err() != nil || !closed() {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(report)
	})
}

// Only the dedicated TLS listener exposes the Collector. It is never mounted
// on the application, peer or health/metrics listener.
func reconciliationListener(cfg Config, stack *controlStack, gate *maintenance.Gate, locator *valkeyowner.Locator, api http.Handler) (*http.Server, net.Listener, error) {
	if cfg.DRReconciliationTLSDir == "" && cfg.DRReconciliationMode == "" {
		return nil, nil, nil
	}
	fail := errors.New("Osaka reconciliation listener configuration invalid")
	ip, err := reconciliationAddress(cfg, stack, gate, locator != nil)
	if err != nil {
		return nil, nil, fail
	}
	dir := cfg.DRReconciliationTLSDir
	var identity reconciliationIdentity
	var read inventoryReader
	var status func(context.Context) (workercontrol.ProcessReadback, bool)
	var standby http.HandlerFunc
	if cfg.DRReconciliationMode != "standby-probe" {
		data, err := os.ReadFile(filepath.Join(dir, "scope.json"))
		if err != nil || len(data) > 1024 || json.Unmarshal(data, &identity) != nil ||
			identity.ConnectorID == uuid.Nil || identity.ConnectionID == uuid.Nil ||
			!reconciliationProject.MatchString(identity.ProjectID) {
			return nil, nil, fail
		}
		read = func(ctx context.Context, req reconciliationRequest) (connector.ReconciliationInventory, error) {
			if req.SessionID == uuid.Nil {
				return stack.Router.ObserveReconciliationOwner(ctx, req.Scope, req.Binding, locator)
			}
			return stack.Router.CollectReconciliationInventory(ctx, req.Scope, req.Binding, req.SessionID, req.Operations, locator)
		}
		status = func(ctx context.Context) (workercontrol.ProcessReadback, bool) {
			return durableStatus(ctx, stack.WorkerControl, gate)
		}
	} else {
		standby = func(w http.ResponseWriter, _ *http.Request) {
			writeStandbyStatus(w, stack.WorkerControl, gate)
		}
	}
	tlsConfig, err := reconciliationTLS(dir)
	if err != nil {
		return nil, nil, fail
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(ip, "9443"))
	if err != nil {
		return nil, nil, fail
	}
	handler := reconciliationHandler(identity, func() bool {
		mode, active := gate.Readback()
		return mode == "closed" && active["http"] == 0
	}, read, status, standby)
	standbyOnly := cfg.DRReconciliationMode == "standby-probe"
	handler = withOsakaAdmissionProbe(handler, api, func() string {
		state, _ := gate.Readback()
		return state
	}, standbyOnly)
	handler = withMutationGuardReadback(handler, stack.WorkerControl, gate, standbyOnly)
	return &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second,
			ReadTimeout: 4 * time.Second, WriteTimeout: 29 * time.Second, IdleTimeout: 3 * time.Second},
		tls.NewListener(listener, tlsConfig), nil
}

func reconciliationAddress(cfg Config, stack *controlStack, gate *maintenance.Gate, hasLocator bool) (string, error) {
	host, _, err := net.SplitHostPort(cfg.PodAddr)
	addr := net.ParseIP(host)
	fail := errors.New("Osaka reconciliation listener configuration invalid")
	if err != nil || !addr.IsPrivate() || stack == nil ||
		stack.WorkerControl == nil || gate == nil || !gate.Configured() ||
		!filepath.IsAbs(cfg.DRReconciliationTLSDir) {
		return "", fail
	}
	settings := cfg.WorkerControlSettings
	switch cfg.DRReconciliationMode {
	case "standby-probe":
		ip := addr.To4()
		mode, _ := gate.Readback()
		if ip == nil || ip[0] != 10 || ip[1] != 20 || mode != "closed" || cfg.WorkerControlMode != workercontrol.PauseNew ||
			settings.Backend != "local" || settings.AdmissionPolicy != "MAINTENANCE" ||
			stack.WorkerControl.Persistent() {
			return "", fail
		}
	case "":
		if !hasLocator || !cfg.ValkeyTLS || settings.Scope.Region != "ap-northeast-3" ||
			settings.AdmissionPolicy != "APPROVED_CONTROL" || !stack.WorkerControl.Persistent() {
			return "", fail
		}
	default:
		return "", fail
	}
	return host, nil
}

func reconciliationTLS(dir string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(filepath.Join(dir, "runner-ca.crt"))
	pool := x509.NewCertPool()
	if err != nil || !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid reconciliation client CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: pool, Certificates: []tls.Certificate{cert}}, nil
}
