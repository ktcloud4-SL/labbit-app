package openstackprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	coreprovider "github.com/ktcloud4-SL/labbit-app/internal/connector/provider"
)

type readinessStage string

const (
	stageServerActive  readinessStage = "server_active"
	stageManagementSSH readinessStage = "management_ssh"
	stageStartup       readinessStage = "startup"
	stageWorkspacePTY  readinessStage = "workspace_pty"
)

// This budget is specific to KT D1 Server boot, including RESET's new server.
// ActiveTimeout continues to bound deletion and other Provider waits.
func (a *Adapter) serverActiveTimeout(config ProvisionConfig) time.Duration {
	if a.ktNetwork != nil {
		return 6 * time.Minute
	}
	return config.ActiveTimeout
}

func (a *Adapter) runReadinessStage(ctx context.Context, correlation coreprovider.Correlation, vmKey, serverID string, stage readinessStage, budget time.Duration, run func(context.Context) error) error {
	started := time.Now()
	stageCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	err := run(stageCtx)
	if a.diagnosticLogger == nil {
		return err
	}
	status := "succeeded"
	switch {
	case ctx.Err() != nil:
		status = "parent_canceled"
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded), stageCtx.Err() == context.DeadlineExceeded:
		status = "timeout"
	case errors.Is(err, ErrServerGet):
		status = "lookup_failed"
	case errors.Is(err, ErrServerBoot):
		status = "server_error"
	default:
		status = "failed"
	}
	attrs := []any{"stage", string(stage), "status", status, "duration_ms", time.Since(started).Milliseconds(), "budget_ms", budget.Milliseconds(), "generation", correlation.Generation}
	// Only canonical UUIDs are readable. VM keys and all other opaque values
	// are hashed; no arbitrary text or underlying error reaches the logger.
	for _, field := range []struct{ name, value string }{
		{"operation_id", correlation.OperationID},
		{"lab_instance_id", correlation.LabInstanceID},
		{"provider_connection_id", a.provision.ProviderConnectionID},
		{"server_id", serverID},
	} {
		if field.value == "" {
			continue
		}
		if id, parseErr := uuid.Parse(field.value); parseErr == nil && id != uuid.Nil && id.String() == field.value {
			attrs = append(attrs, field.name, id.String())
		} else {
			sum := sha256.Sum256([]byte(field.value))
			attrs = append(attrs, field.name+"_sha256", hex.EncodeToString(sum[:]))
		}
	}
	if vmKey != "" {
		sum := sha256.Sum256([]byte(vmKey))
		attrs = append(attrs, "vm_key_sha256", hex.EncodeToString(sum[:]))
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
	}
	a.diagnosticLogger.Log(ctx, level, "VM readiness stage completed", attrs...)
	return err
}
