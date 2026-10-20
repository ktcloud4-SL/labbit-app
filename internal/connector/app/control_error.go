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
		attrs = append(attrs, "failure_stage", info.Stage.String(), "failure_timeout", info.Timeout, "ssh_channel_reason", info.ChannelReason, "failure_elapsed_ms", info.ElapsedMS)
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
