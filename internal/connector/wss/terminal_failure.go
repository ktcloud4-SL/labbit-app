package wss

import (
	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"time"
)

// TerminalOpenFailure carries lifecycle correlation and a sanitized cause only.
// Its session ID must be parsed as a UUID before being written to a log.
type TerminalOpenFailure struct {
	SessionID string
	Cause     error
}

func (e *TerminalOpenFailure) Error() string { return "terminal open failed" }
func (e *TerminalOpenFailure) Unwrap() error { return e.Cause }

func terminalOpenFailure(sessionID string, err error) error {
	return &TerminalOpenFailure{SessionID: sessionID, Cause: vmaccess.Diagnose(vmaccess.StageUnknown, err, time.Time{})}
}
