package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestControlFailureLogContainsOnlySafeDiagnostics(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	secret := "raw-token-password-provider-body-terminal-content"
	for _, id := range []string{"3fdc3246-0bae-40fe-a4f8-8d156bf6fe5b", secret} {
		diagnostic := vmaccess.Diagnose(vmaccess.StageDataAttach, errors.Join(errors.New(secret), context.DeadlineExceeded), time.Now())
		logControlError(logger, "connector", fmt.Errorf("%s: %w", secret, &wss.TerminalOpenFailure{SessionID: id, Cause: diagnostic}))
	}
	logControlError(logger, "connector", errors.New(secret))
	got := out.String()
	if strings.Contains(got, secret) || !strings.Contains(got, `"failure_stage":"data_attach"`) || !strings.Contains(got, `"failure_timeout":true`) || strings.Count(got, `"terminal_session_id"`) != 1 {
		t.Fatalf("unexpected safe log: %s", got)
	}
}

func TestVMAccessFailureLogValidatesTargetAndKeepsSafeCheckpoint(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	secret := "private-endpoint-key-vm-command"
	info, _ := vmaccess.Diagnostic(vmaccess.Diagnose(vmaccess.StageHandshake, errors.Join(errors.New(secret), context.DeadlineExceeded), time.Now()))
	for _, id := range []string{"3fdc3246-0bae-40fe-a4f8-8d156bf6fe5b", secret} {
		logVMAccessFailure(logger, "connector", vmaccess.Target{LabInstanceID: id, Generation: 1, VMKey: secret, ServerID: secret}, info)
	}
	got := out.String()
	if strings.Contains(got, secret) || strings.Count(got, `"lab_instance_id"`) != 1 || !strings.Contains(got, `"failure_stage":"handshake"`) || !strings.Contains(got, `"failure_timeout":true`) {
		t.Fatalf("unsafe or missing VM acquisition metadata: %s", got)
	}
}
