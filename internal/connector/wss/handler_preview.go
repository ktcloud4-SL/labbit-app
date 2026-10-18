package wss

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/preview"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// Each OPEN keeps the sender, credential and cancellation scope of the Control
// connection which delivered it. A reconnect cannot lend it new authority.
func (h *Handler) handlePreviewOpen(ctx context.Context, _ protocol.BaseEnvelope, raw []byte, epoch uint64, asynchronous bool) error {
	var msg protocol.PreviewOpenMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return errors.New("invalid PREVIEW_OPEN")
	}
	h.mu.RLock()
	mgr, cfg, owner, sender := h.previewMgr, h.previewCfg, h.previewOwner, h.sender
	if epoch != h.fileOwnerEpoch || owner == nil || owner.Err() != nil {
		h.mu.RUnlock()
		return context.Canceled
	}
	if dial, ok := sender.(interface{ DialCredential() string }); ok {
		cfg.Credential = dial.DialCredential()
		cfg.CredentialFile = ""
		cfg.CredentialProvider = nil
	}
	h.mu.RUnlock()
	result := func(code, message string) error {
		if owner.Err() != nil {
			return context.Canceled
		}
		outcome := protocol.OutcomeSucceeded
		var safe *protocol.SafeError
		if code != "" {
			outcome = protocol.OutcomeFailed
			safe = &protocol.SafeError{Code: code, Message: message}
		}
		if sender == nil {
			return errors.New("preview Control sender unavailable")
		}
		// A TCP/Data close must not cancel a shared Control write in progress.
		// The captured sender still prevents delivery through a new Control.
		notifyCtx, cancelNotify := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancelNotify()
		return sender.SendMessage(notifyCtx, protocol.PreviewOpenResultMessage{
			BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypePreviewOpenResult, MessageID: generateUUID(), ReplyToMessageID: msg.MessageID, SentAt: time.Now().UTC(), PreviewSessionID: msg.PreviewSessionID, LabInstanceID: msg.LabInstanceID, Generation: msg.Generation},
			Payload:      protocol.PreviewOpenResultPayload{Outcome: outcome, Error: safe},
		})
	}
	fail := func(code, message string) error {
		_ = result(code, message)
		return errors.New(message)
	}
	if msg.MessageID == "" || msg.PreviewSessionID == "" || msg.LabInstanceID == "" || msg.Generation < 1 {
		return fail(protocol.PreviewErrorCodeUnavailable, "missing required preview correlation")
	}
	if msg.Payload.TargetVmKey == "" || msg.Payload.ProviderServerID == "" || msg.Payload.TargetPort <= 0 || msg.Payload.TargetPort > 65535 || msg.Payload.TargetPort == 22 {
		return fail(protocol.PreviewErrorCodePortRejected, "preview target or port rejected")
	}
	if mgr == nil || cfg.EndpointURL == "" {
		return fail(protocol.PreviewErrorCodeUnavailable, "preview transport unavailable")
	}
	ctx, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(owner, cancel)
	// Bound workers without blocking the Control reader behind an unresponsive
	// VM or Gateway. This internal bound is not an application port policy.
	if asynchronous {
		select {
		case h.previewOpenSlots <- struct{}{}:
		default:
			stopOwner()
			cancel()
			return fail(protocol.PreviewErrorCodeUnavailable, "preview transport busy")
		}
	}
	releaseSlot := func() {
		if asynchronous {
			<-h.previewOpenSlots
		}
	}
	// Admission and trust retirement share the authority lock. An old worker
	// cannot register a pending tunnel after its Control owner was retired.
	h.mu.RLock()
	var attempt *preview.OpenAttempt
	var err error
	if epoch != h.fileOwnerEpoch || owner.Err() != nil {
		err = context.Canceled
	} else {
		attempt, err = mgr.PrepareOpen(ctx, msg.Payload, msg.BaseEnvelope)
	}
	h.mu.RUnlock()
	if err != nil {
		releaseSlot()
		stopOwner()
		cancel()
		return fail(protocol.PreviewErrorCodeUnavailable, "preview attempt rejected")
	}
	work := func() error {
		defer releaseSlot()
		session, retry, err := attempt.Open(ctx)
		if err != nil {
			stopOwner()
			defer cancel()
			code, message := protocol.PreviewErrorCodeUnavailable, "cannot open preview tunnel"
			switch {
			case errors.Is(err, preview.ErrPortRejected):
				code, message = protocol.PreviewErrorCodePortRejected, "port is not allowed"
			case errors.Is(err, preview.ErrAppNotRunning):
				code, message = protocol.PreviewErrorCodeAppNotRunning, "application is not running on target port"
			case errors.Is(err, preview.ErrVMUnreachable):
				code, message = protocol.PreviewErrorCodeVMUnreachable, "workspace VM is unreachable"
			}
			return fail(code, message)
		}
		if retry {
			stopOwner()
			defer cancel()
			return result("", "")
		}
		session.OnAttemptClosed(msg.MessageID, func() { stopOwner(); cancel() })
		client := preview.NewDataWSSClient(cfg)
		if err := client.DialAndAttach(ctx, session, msg.MessageID); err != nil {
			mgr.CloseAttempt(session, msg.MessageID, protocol.PreviewReasonOpenFailed)
			return fail(protocol.PreviewErrorCodeUnavailable, "preview data attach failed")
		}
		if err := result("", ""); err != nil {
			mgr.CloseAttempt(session, msg.MessageID, protocol.PreviewReasonOpenFailed)
			return errors.New("preview open result delivery failed")
		}
		// The completed Data tunnel outlives ordinary Control reconnects.
		// Explicit trust retirement still closes it through the manager.
		stopOwner()
		return nil
	}
	if asynchronous {
		go func() {
			if err := work(); err != nil {
				if onError := h.OnError(); onError != nil {
					onError(err)
				}
			}
		}()
		return nil
	}
	return work()
}

func (h *Handler) handlePreviewClose(_ context.Context, _ protocol.BaseEnvelope, raw []byte, epoch uint64) error {
	var msg protocol.PreviewCloseMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("invalid PREVIEW_CLOSE")
	}
	if msg.MessageID == "" || msg.PreviewSessionID == "" || msg.LabInstanceID == "" || msg.Generation < 1 || msg.Payload.Reason == "" {
		return errors.New("missing required PREVIEW_CLOSE correlation")
	}
	h.mu.RLock()
	mgr := h.previewMgr
	if epoch != h.fileOwnerEpoch {
		h.mu.RUnlock()
		return context.Canceled
	}
	if mgr != nil {
		closeSession, err := mgr.RetireMatchingSession(msg.PreviewSessionID, msg.LabInstanceID, msg.Generation, msg.Payload.Reason)
		h.mu.RUnlock()
		closeSession()
		return err
	}
	h.mu.RUnlock()
	return nil
}
