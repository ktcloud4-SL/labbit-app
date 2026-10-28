package app

import (
	"errors"
	"github.com/google/uuid"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/wss"
	"log/slog"
)

func logControlError(logger *slog.Logger, connectorID string, err error) {
	attrs := []any{"connector_id", connectorID}
	if info, ok := vmaccess.Diagnostic(err); ok {
		attrs = append(attrs, accessFailureAttrs(info)...)
	}
	var open *wss.TerminalOpenFailure
	if errors.As(err, &open) {
		if id, parseErr := uuid.Parse(open.SessionID); parseErr == nil {
			attrs = append(attrs, "terminal_session_id", id.String())
		}
	}
	// Raw errors may include SSH peer messages, URLs with tokens or provider
	// response bodies. Log only fixed categories and validated lifecycle IDs.
	logger.Warn("Connector Control 메시지 처리 실패", attrs...)
}

func accessFailureAttrs(info vmaccess.FailureInfo) []any {
	return []any{"failure_stage", info.Stage.String(), "failure_timeout", info.Timeout, "ssh_channel_reason", info.ChannelReason, "failure_elapsed_ms", info.ElapsedMS,
		"ssh_phase", info.SSHPhase.String(), "ssh_returned_error", info.HandshakeReturnedError,
		"ssh_context_end", info.ContextEnd, "ssh_context_closed_socket", info.ContextClosedSocket, "ssh_io_timeout", info.TransportTimeout,
		"ssh_shared_waiter", info.SharedWaiter, "caller_context_end", info.CallerContextEnd, "caller_elapsed_ms", info.CallerElapsedMS}
}

func logVMAccessFailure(logger *slog.Logger, connectorID string, target vmaccess.Target, info vmaccess.FailureInfo) {
	attrs := append([]any{"connector_id", connectorID}, accessFailureAttrs(info)...)
	if id, err := uuid.Parse(target.LabInstanceID); err == nil {
		attrs = append(attrs, "lab_instance_id", id.String())
	}
	if target.Generation > 0 {
		attrs = append(attrs, "generation", target.Generation)
	}
	logger.Warn("VM SSH acquisition failed", attrs...)
}
