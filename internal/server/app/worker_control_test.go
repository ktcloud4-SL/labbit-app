package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
)

func TestLoadConfigWorkerControlMode(t *testing.T) {
	for _, tt := range []struct {
		raw, roles string
		want       workercontrol.Mode
		invalid    bool
	}{
		{"", "api,worker", workercontrol.Active, false},
		{"ACTIVE", "api", workercontrol.Active, false},
		{"PAUSE_NEW", "api,worker,realtime", workercontrol.PauseNew, false},
		{"RECONCILE_ONLY", "api,worker", workercontrol.ReconcileOnly, false},
		{"PAUSE_NEW", "worker", workercontrol.PauseNew, false},
		{"private-invalid-mode", "api", 0, true},
		{"private-invalid-mode", "worker", 0, true},
		{"private-invalid-mode", "realtime", workercontrol.Active, false},
	} {
		t.Run(tt.raw+"/"+tt.roles, func(t *testing.T) {
			t.Setenv("LABBIT_ENVIRONMENT", "development")
			t.Setenv("LABBIT_RUNTIME_ROLES", tt.roles)
			t.Setenv("LABBIT_SHUTDOWN_GRACE", "5s")
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			t.Setenv("LABBIT_DATABASE_DSN", "postgres://labbit:dummy@127.0.0.1:1/not-used")
			t.Setenv("LABBIT_PUBLIC_ORIGIN", "http://localhost:5173")
			t.Setenv("LABBIT_VALKEY_ADDR", "")
			t.Setenv("VALKEY_ADDR", "")
			t.Setenv("LABBIT_MUTATION_ADMISSION_MODE", "")
			t.Setenv("LABBIT_WORKER_CONTROL_MODE", tt.raw)
			if tt.raw == "RECONCILE_ONLY" {
				t.Setenv("LABBIT_WORKER_CONTROL_BACKEND", "postgres")
				t.Setenv("LABBIT_WORKER_CONTROL_ID", "trial")
				t.Setenv("LABBIT_WORKER_CONTROL_REGION", "region-fixture")
				t.Setenv("LABBIT_WORKER_CONTROL_CLUSTER", "cluster-fixture")
				t.Setenv("LABBIT_WORKER_CONTROL_REVISION", "1")
				t.Setenv("LABBIT_WORKER_CONTROL_EPOCH", "1")
				t.Setenv("LABBIT_POD_ID", "pod-fixture")
			}
			cfg, err := LoadConfig() // No runtime/DB/HTTP startup.
			if tt.invalid {
				if !errors.Is(err, workercontrol.ErrInvalidMode) || strings.Contains(err.Error(), tt.raw) {
					t.Fatalf("parse=%v", err)
				}
			} else if err != nil || cfg.WorkerControlMode != tt.want {
				t.Fatalf("mode=%v err=%v", cfg.WorkerControlMode, err)
			}
		})
	}
}

func TestInvalidWorkerControlModeFailsBeforeRuntimeInitialization(t *testing.T) {
	for _, roles := range [][]string{{"api"}, {"worker"}, {"api", "worker"}} {
		if err := Run(t.Context(), Config{Roles: roles, WorkerControlMode: workercontrol.Mode(255)}); !errors.Is(err, workercontrol.ErrInvalidMode) {
			t.Fatalf("run=%v", err)
		}
	}
	if s, err := newControlStack(nil, stackOptions{WorkerControlMode: workercontrol.Mode(255)}); s != nil || !errors.Is(err, workercontrol.ErrInvalidMode) {
		t.Fatalf("stack=%v err=%v", s, err)
	}
}

func TestWorkerControlProductionAssemblySharesExecutorAndRouterGate(t *testing.T) {
	s, err := newControlStack(nil, stackOptions{Worker: true, PublicOrigin: "http://localhost:5173"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Provisions == nil {
		t.Fatal("worker not constructed")
	}
	if err := s.Provisions.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Empty command would normally fail validation. The shared latch blocks it
	// first, without touching Registry/DB/transport or exposing a mutation path.
	if _, err := s.Router.SendOperationCommand(t.Context(), connector.OperationCommand{}); !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatalf("Router used independent gate: %v", err)
	}
	if _, err := s.Router.SendReconcileRequest(t.Context(), connector.ReconcileRequest{}); !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatalf("reconcile used independent gate: %v", err)
	}
	if claimed, err := s.Provisions.RunOne(t.Context()); claimed || !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatal("paused production attempted DB claim")
	}
}

func TestDurableWorkerControlProductionAssemblyBlocksPeersWithoutDatabase(t *testing.T) {
	scope := workercontrol.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}
	journal := workercontrolfake.New(workercontrol.State{Scope: scope, Revision: 1, Epoch: 1, Mode: workercontrol.Active})
	opts := stackOptions{Worker: true, PublicOrigin: "http://localhost:5173", WorkerControlSettings: workercontrol.Settings{Backend: "postgres", Scope: scope, Revision: 1, Epoch: 1}, WorkerControlStore: journal, WorkerControlPodID: "pod-a"}
	a, err := newControlStack(nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.WorkerControlPodID = "pod-b"
	b, err := newControlStack(nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Provisions.Pause(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Router.SendOperationCommand(t.Context(), connector.OperationCommand{}); !errors.Is(err, workercontrol.ErrStale) {
		t.Fatalf("peer Router bypass=%v", err)
	}
	if claimed, e := b.Provisions.RunOne(t.Context()); claimed || !errors.Is(e, workercontrol.ErrUnavailable) {
		t.Fatalf("peer claim bypass=%v/%v", claimed, e)
	}
	// Every stack construction gets a fresh process incarnation without querying DB.
	opts.WorkerControlStore = nil
	if _, e := newControlStack(nil, opts); !errors.Is(e, workercontrol.ErrInvalidBinding) {
		t.Fatal("persistent config fell back to local")
	}
}
func TestDurableWorkerControlPartialRuntimeSettingsFailBeforeDatabase(t *testing.T) {
	for _, settings := range []workercontrol.Settings{{Backend: "postgres"}, {Backend: "local", Revision: 1}, {Backend: "private-invalid"}} {
		if err := Run(t.Context(), Config{Roles: []string{"api"}, WorkerControlSettings: settings}); !errors.Is(err, workercontrol.ErrInvalidBinding) {
			t.Fatal(err)
		}
	}
	for _, backend := range []string{"postgres", "local", "unknown"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("LABBIT_ENVIRONMENT", "development")
			t.Setenv("LABBIT_RUNTIME_ROLES", "api,worker")
			t.Setenv("LABBIT_PUBLIC_ORIGIN", "http://localhost:5173")
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			t.Setenv("LABBIT_DATABASE_DSN", "postgres://dummy:fixture@127.0.0.1:1/not-used")
			t.Setenv("LABBIT_VALKEY_ADDR", "")
			t.Setenv("VALKEY_ADDR", "")
			t.Setenv("LABBIT_WORKER_CONTROL_BACKEND", backend)
			t.Setenv("LABBIT_WORKER_CONTROL_ID", "trial")
			t.Setenv("LABBIT_WORKER_CONTROL_REGION", "region-fixture")
			t.Setenv("LABBIT_WORKER_CONTROL_CLUSTER", "cluster-fixture")
			t.Setenv("LABBIT_WORKER_CONTROL_REVISION", "9")
			t.Setenv("LABBIT_WORKER_CONTROL_EPOCH", "2")
			t.Setenv("LABBIT_POD_ID", "pod-a")
			cfg, err := LoadConfig()
			if backend == "postgres" {
				if err != nil || !cfg.WorkerControlSettings.Enabled() {
					t.Fatal(err)
				}
			} else if !errors.Is(err, workercontrol.ErrInvalidBinding) {
				t.Fatal("partial persistent settings downgraded")
			}
		})
	}
}
