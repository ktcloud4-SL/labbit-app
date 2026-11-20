package app

import (
	"errors"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/maintenance"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workercontrol"
	"github.com/ktcloud4-SL/labbit-app/internal/testsupport/workercontrolfake"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintenanceConfigAndLegacyModeCannotSilentlyOpen(t *testing.T) {
	for _, legacy := range []string{"", "OPEN", "BLOCK_NEW", "private-invalid"} {
		t.Run(legacy, func(t *testing.T) {
			t.Setenv("LABBIT_ENVIRONMENT", "development")
			t.Setenv("LABBIT_RUNTIME_ROLES", "api")
			t.Setenv("LABBIT_DATABASE_DSN_FILE", "")
			t.Setenv("LABBIT_DATABASE_DSN", "postgres://fixture:dummy@127.0.0.1:1/not-used")
			t.Setenv("LABBIT_PUBLIC_ORIGIN", "http://localhost:5173")
			t.Setenv("LABBIT_VALKEY_ADDR", "")
			t.Setenv("VALKEY_ADDR", "")
			t.Setenv("LABBIT_WORKER_CONTROL_MODE", "")
			t.Setenv("LABBIT_WORKER_CONTROL_BACKEND", "")
			t.Setenv("LABBIT_MUTATION_ADMISSION_MODE", legacy)
			t.Setenv("LABBIT_MAINTENANCE_FILE", "fixture-mode")
			cfg, err := LoadConfig()
			if legacy != "" {
				if err == nil || strings.Contains(err.Error(), "private-invalid") {
					t.Fatalf("legacy=%v", err)
				}
				return
			}
			if err != nil || cfg.MaintenanceFile != "fixture-mode" {
				t.Fatalf("cfg=%+v/%v", cfg, err)
			}
		})
	}
}

func TestPersistentQueryOnlyAPIRouterKeepsMutationClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	scope := workercontrol.Scope{ID: "trial", Region: "region-fixture", Cluster: "cluster-fixture"}
	journal := workercontrolfake.New(workercontrol.State{Scope: scope, Revision: 2, Epoch: 2, Mode: workercontrol.ReconcileOnly})
	stack, err := newControlStack(nil, stackOptions{PublicOrigin: "http://localhost:5173", Maintenance: maintenance.New(path), WorkerControlMode: workercontrol.ReconcileOnly, WorkerControlSettings: workercontrol.Settings{Backend: "postgres", Scope: scope, Revision: 2, Epoch: 2}, WorkerControlStore: journal, WorkerControlPodID: "pod-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stack.Router.SendOperationCommand(t.Context(), connector.OperationCommand{}); !errors.Is(err, workercontrol.ErrPaused) {
		t.Fatal("direct mutation bypass", err)
	}
}

func TestReconcileOnlyRequiresPersistentAssemblyEvenWithoutWorker(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		if stack, err := newControlStack(nil, stackOptions{Worker: enabled, WorkerControlMode: workercontrol.ReconcileOnly}); stack != nil || !errors.Is(err, workercontrol.ErrInvalidBinding) {
			t.Fatalf("local query assembly=%v/%v", stack, err)
		}
	}
	if err := Run(t.Context(), Config{Roles: []string{"api"}, WorkerControlMode: workercontrol.ReconcileOnly}); !errors.Is(err, workercontrol.ErrInvalidBinding) {
		t.Fatal("queried DB before invalid config", err)
	}
}

func TestMaintenanceProductionAssemblyBlocksClaimWithoutDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(path, []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := maintenance.New(path)
	stack, err := newControlStack(nil, stackOptions{Worker: true, PublicOrigin: "http://localhost:5173", Maintenance: gate})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := stack.Provisions.RunOne(t.Context()); claimed || err != nil {
		t.Fatalf("closed claim=%v/%v", claimed, err)
	}
	if !gate.LocalResumeReady() {
		t.Fatal("closed idle gate not observed")
	}
}
