package wss

import (
	"context"
	"errors"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/filetransport"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func (h *Handler) SetFileManager(manager *filetransport.Manager, credential string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fileMgr, h.fileCredential = manager, credential
}
func (h *Handler) handleFileOpen(ctx context.Context, raw []byte, epoch uint64) error {
	open, valid := filetransport.ParseOpen(raw)
	if !valid {
		return errors.New("invalid File control message")
	}
	h.mu.RLock()
	if epoch != h.fileOwnerEpoch {
		h.mu.RUnlock()
		return nil
	}
	manager, sender, credential := h.fileMgr, h.sender, h.fileCredential
	owner := h.fileOwner
	if owner == nil {
		owner = context.Background()
	}
	requestCtx, cancelRequest := context.WithCancel(owner)
	h.mu.RUnlock()
	stopProcess := context.AfterFunc(ctx, cancelRequest)
	if ctx.Err() != nil {
		cancelRequest()
	}
	// Cleanup follows this particular File request, not an eventual new sender.
	cleanup := func() { stopProcess(); cancelRequest() }
	if source, ok := sender.(interface{ DialCredential() string }); ok {
		credential = source.DialCredential()
	}
	// Capture this Control sender; a late result must never use h.Sender() from
	// a new connection. File cancellation must not cancel a Control write and
	// thereby close that shared connection, so notification has its own deadline.
	notify := func(requestCtx context.Context, succeeded bool) error {
		if sender == nil || requestCtx.Err() != nil {
			return filetransport.ErrUnavailable
		}
		notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), 2*time.Second)
		defer cancel()
		payload := protocol.FileOpenResultPayload{Outcome: protocol.OutcomeSucceeded}
		if !succeeded {
			payload.Outcome = protocol.OutcomeFailed
			payload.Error = &protocol.SafeError{Code: "UNAVAILABLE"}
		}
		return sender.SendMessage(notifyCtx, protocol.FileOpenResultMessage{BaseEnvelope: protocol.BaseEnvelope{Type: protocol.MessageTypeFileOpenResult, MessageID: generateUUID(), SentAt: time.Now().UTC(), ReplyToMessageID: open.MessageID, FileRequestID: open.FileRequestID, LabInstanceID: open.LabInstanceID, Generation: open.Generation, RequestID: open.RequestID, TraceParent: open.TraceParent, TraceState: open.TraceState}, Payload: payload})
	}
	if manager == nil || sender == nil {
		defer cleanup()
		return notify(ctx, false)
	}
	reused, err := manager.Open(requestCtx, open, credential, notify, cleanup)
	alive := requestCtx.Err() == nil
	if err != nil || reused {
		cleanup()
	}
	if err != nil && alive {
		return notify(ctx, false)
	}
	return nil
}
func (h *Handler) handleFileClose(raw []byte, epoch uint64) error {
	close, valid := filetransport.ParseClose(raw)
	if !valid {
		return errors.New("invalid File control message")
	}
	h.mu.RLock()
	if epoch != h.fileOwnerEpoch {
		h.mu.RUnlock()
		return nil
	}
	manager := h.fileMgr
	h.mu.RUnlock()
	if manager != nil {
		manager.Close(close.BaseEnvelope)
	}
	return nil
}
