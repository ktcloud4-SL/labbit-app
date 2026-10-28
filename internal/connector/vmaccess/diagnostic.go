package vmaccess

import (
	"context"
	"errors"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// FailureStage contains only local, fixed categories. Never use an external
// error string, address, command, response body or credential as a stage.
type FailureStage uint8

const (
	StageUnknown FailureStage = iota
	StageResolveServer
	StageResolveManagement
	StageHostPin
	StagePrivateKey
	StageDial
	StageHandshake
	StageChannelOpen
	StagePTYRequest
	StageShellStart
	StageDataAttach
)

func (s FailureStage) String() string {
	switch s {
	case StageResolveServer:
		return "resolve_server"
	case StageResolveManagement:
		return "resolve_management"
	case StageHostPin:
		return "host_pin"
	case StagePrivateKey:
		return "private_key"
	case StageDial:
		return "dial"
	case StageHandshake:
		return "handshake"
	case StageChannelOpen:
		return "channel_open"
	case StagePTYRequest:
		return "pty_request"
	case StageShellStart:
		return "shell_start"
	case StageDataAttach:
		return "data_attach"
	default:
		return "unknown"
	}
}

type FailureInfo struct {
	Stage                  FailureStage
	Timeout                bool
	ChannelReason          uint32
	ElapsedMS              int64
	SSHPhase               SSHPhase
	HandshakeReturnedError bool
	ContextEnd             string
	ContextClosedSocket    bool
	TransportTimeout       bool
	SharedWaiter           bool
	CallerContextEnd       string
	CallerElapsedMS        int64
}

// SSHPhase is the last observed checkpoint, not proof of the exact blocked
// library operation. Identification can be malformed; after host verification
// the library can await the userauth service before invoking public-key auth.
// It contains no peer text or terminal content.
type SSHPhase uint8

const (
	SSHPhaseUnknown SSHPhase = iota
	SSHVersionExchange
	SSHKeyExchange
	SSHHostVerification
	SSHAuthentication
	SSHComplete
)

func (p SSHPhase) String() string {
	switch p {
	case SSHVersionExchange:
		return "version_exchange"
	case SSHKeyExchange:
		return "key_exchange"
	case SSHHostVerification:
		return "host_verification"
	case SSHAuthentication:
		return "authentication"
	case SSHComplete:
		return "complete"
	default:
		return "unknown"
	}
}

var errSetupTimeout = errors.New("VM access setup deadline")
var errTransportRetired = errors.New("VM access transport retired")

func contextEnd(ctx context.Context) string {
	switch {
	case ctx.Err() == nil:
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return "deadline_timer_pending"
		}
		return "active"
	case errors.Is(context.Cause(ctx), errTransportRetired):
		return "transport_retired"
	case errors.Is(context.Cause(ctx), errSetupTimeout):
		return "setup_deadline"
	case errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		return "request_deadline"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "request_deadline"
	default:
		return "request_canceled"
	}
}

func acquireDiagnostic(err error, ctx context.Context, started time.Time, shared bool) error {
	d := Diagnose(StageUnknown, err, time.Time{}).(*diagnosticError)
	d.info.SharedWaiter = shared
	d.info.CallerContextEnd = contextEnd(ctx)
	d.info.CallerElapsedMS = time.Since(started).Milliseconds()
	return d
}

type diagnosticError struct {
	info FailureInfo
	kind error
}

func (e *diagnosticError) Error() string { return e.kind.Error() }
func (e *diagnosticError) Unwrap() error { return e.kind }

// Diagnose discards the original error, retaining only safe classifications.
// Existing typed diagnostics keep the innermost failing stage.
func Diagnose(stage FailureStage, cause error, started time.Time) error {
	var prior *diagnosticError
	hasPrior := errors.As(cause, &prior)
	kind := ErrUnavailable
	if errors.Is(cause, ErrInvalidTarget) {
		kind = ErrInvalidTarget
	}
	if errors.Is(cause, ErrClosed) {
		kind = errors.Join(kind, ErrClosed)
	}
	if errors.Is(cause, context.Canceled) {
		kind = errors.Join(kind, context.Canceled)
	}
	var ne net.Error
	timedOut := errors.Is(cause, context.DeadlineExceeded) || (errors.As(cause, &ne) && ne.Timeout())
	if timedOut {
		kind = errors.Join(kind, context.DeadlineExceeded)
	}
	var reason uint32
	var ce *ssh.OpenChannelError
	if errors.As(cause, &ce) && ce.Reason >= ssh.Prohibited && ce.Reason <= ssh.ResourceShortage {
		reason = uint32(ce.Reason)
	}
	elapsed := int64(0)
	if !started.IsZero() {
		elapsed = time.Since(started).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
	}
	info := FailureInfo{Stage: stage, Timeout: timedOut, ChannelReason: reason, ElapsedMS: elapsed}
	if hasPrior {
		info = prior.info
		info.Timeout = info.Timeout || timedOut
	}
	return &diagnosticError{info: info, kind: kind}
}

func Diagnostic(err error) (FailureInfo, bool) {
	var e *diagnosticError
	if errors.As(err, &e) {
		return e.info, true
	}
	return FailureInfo{}, false
}
