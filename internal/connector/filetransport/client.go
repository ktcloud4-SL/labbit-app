package filetransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefile"
	"github.com/ktcloud4-SL/labbit-app/internal/jsonnum"
)

var errProtocol = errors.New("File protocol violation")
var errTooBig = errors.New("File message too large")

func (m *Manager) exchange(r *request, credential string, notify func(context.Context, bool) error) {
	notified := false
	defer func() {
		if !notified && r.ctx.Err() == nil && notify != nil {
			_ = notify(r.ctx, false)
		}
	}()
	dialer := *websocket.DefaultDialer
	if m.config.Dialer != nil {
		dialer = *m.config.Dialer
	}
	dialer.Subprotocols = []string{protocol.SubprotocolFileData}
	dialer.HandshakeTimeout = m.config.AttachTimeout
	headers := http.Header{"Authorization": []string{"Bearer " + credential}}
	conn, resp, err := dialer.DialContext(r.ctx, m.config.EndpointURL, headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return
	}
	r.mu.Lock()
	r.conn = conn
	canceled := r.ctx.Err() != nil
	r.mu.Unlock()
	defer conn.Close()
	if canceled || conn.Subprotocol() != protocol.SubprotocolFileData {
		return
	}
	stopCancel := context.AfterFunc(r.ctx, func() { _ = conn.Close() })
	defer stopCancel()
	// The global transport guard is the larger finite binary/JSON limit. Every
	// Text frame is independently limited to 1 MiB before JSON is decoded.
	limit := m.config.MaxFileBytes + 1
	if limit < protocol.MaxJSONMessageSize {
		limit = protocol.MaxJSONMessageSize
	}
	conn.SetReadLimit(limit)
	attach := protocol.FileDataAttachMessage{BaseEnvelope: responseEnvelope(r.open.BaseEnvelope, protocol.MessageTypeFileDataAttach), Payload: protocol.FileDataAttachPayload{RuntimeID: m.config.RuntimeID, TargetVMKey: r.target.VMKey, ProviderServerID: r.target.ServerID}}
	if m.writeJSON(r, attach) != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(m.config.AttachTimeout))
	attached, err := readJSON(conn)
	if err != nil || attached.Type != protocol.MessageTypeFileDataAttached || !matches(r, attached) || attached.ReplyToMessageID != attach.MessageID {
		reject(conn, err)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if r.ctx.Err() != nil {
		return
	}
	notified = true
	if notify != nil && notify(r.ctx, true) != nil {
		return
	}
	requestFrame, err := readJSON(conn)
	if err != nil || !matches(r, requestFrame) || requestFrame.Type != "FILE_"+r.open.Payload.Operation {
		reject(conn, err)
		return
	}
	p, ok := stringValue(requestFrame.Payload["path"])
	if !ok || utf8.RuneCountInString(p) > 4096 || (p == "" && r.open.Payload.Operation != "TREE") {
		reject(conn, errProtocol)
		return
	}
	var maxBytes, size int64
	var expected string
	var body []byte
	var precondition error
	if requestFrame.Type == protocol.MessageTypeFileRead {
		v, valid, inRange := jsonnum.Int64(requestFrame.Payload["maxBytes"])
		if !valid || (inRange && v < 1) || (!inRange && len(requestFrame.Payload["maxBytes"]) > 0 && requestFrame.Payload["maxBytes"][0] == '-') {
			reject(conn, errProtocol)
			return
		}
		maxBytes = v
		if !inRange || v > m.config.MaxFileBytes {
			maxBytes = m.config.MaxFileBytes
		}
	}
	if requestFrame.Type == protocol.MessageTypeFileSave {
		expected, ok = requiredString(requestFrame.Payload, "expectedRevision")
		if !ok || !validRevision(expected) {
			reject(conn, errProtocol)
			return
		}
		v, valid, inRange := jsonnum.Int64(requestFrame.Payload["size"])
		if !valid || (inRange && v < 0) || (!inRange && len(requestFrame.Payload["size"]) > 0 && requestFrame.Payload["size"][0] == '-') {
			reject(conn, errProtocol)
			return
		}
		size = v
		if !inRange || size > m.config.MaxFileBytes {
			precondition = workspacefile.ErrTooLarge
		}
		// Even a rejected declared size has exactly one following Binary frame.
		// Drain a finite body without allocating it; oversized wire messages are
		// still rejected by the connection's independent transport read limit.
		{
			kind, reader, err := conn.NextReader()
			if err != nil {
				return
			}
			if kind != websocket.BinaryMessage {
				reject(conn, errProtocol)
				return
			}
			if precondition != nil {
				var received int64
				received, err = io.Copy(io.Discard, reader)
				if err != nil {
					reject(conn, err)
					return
				}
				if !inRange || received != size {
					reject(conn, errProtocol)
					return
				}
			} else {
				body, err = io.ReadAll(io.LimitReader(reader, size+1))
				if err != nil {
					return
				}
				if int64(len(body)) != size {
					reject(conn, errProtocol)
					return
				}
			}
		}
	}
	// One reader owns the connection after the single request. It observes peer
	// disconnect/revoke immediately while SFTP is running and rejects extra frames.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_, _, err := conn.NextReader()
		if err == nil {
			reject(conn, errProtocol)
		}
		r.cancel()
	}()
	defer func() { _ = conn.Close(); <-readerDone }()
	if r.ctx.Err() != nil {
		return
	}
	out := struct {
		protocol.BaseEnvelope
		Payload map[string]any `json:"payload"`
	}{BaseEnvelope: responseEnvelope(requestFrame.BaseEnvelope, requestFrame.Type+"_RESULT"), Payload: map[string]any{"outcome": protocol.OutcomeSucceeded}}
	operationErr := precondition
	if operationErr == nil {
		operationErr = workspacefile.ValidatePath(p, r.open.Payload.Operation == "TREE")
	}
	var content []byte
	if operationErr == nil {
		switch r.open.Payload.Operation {
		case "TREE":
			entries, err := m.files.Tree(r.ctx, r.target, p)
			operationErr = err
			if entries == nil {
				entries = []workspacefile.Entry{}
			}
			out.Payload["entries"] = entries
		case "READ":
			f, err := m.files.Read(r.ctx, r.target, p, maxBytes)
			operationErr = err
			if err == nil && (int64(len(f.Content)) > maxBytes || !validRevision(f.Revision)) {
				operationErr = workspacefile.ErrInternal
			}
			content = f.Content
			out.Payload["revision"], out.Payload["size"] = f.Revision, len(content)
		case "SAVE":
			rev, err := m.files.Save(r.ctx, r.target, p, expected, body)
			operationErr = err
			if err == nil && !validRevision(rev) {
				operationErr = workspacefile.ErrInternal
			}
			out.Payload["revision"] = rev
		}
	}
	if r.ctx.Err() != nil {
		return
	}
	if operationErr != nil {
		out.Payload = failure(operationErr)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		reject(conn, errProtocol)
		return
	}
	if int64(len(encoded)) > protocol.MaxJSONMessageSize {
		operationErr = workspacefile.ErrTooLarge
		out.Payload = failure(workspacefile.ErrTooLarge)
		encoded, _ = json.Marshal(out)
		if int64(len(encoded)) > protocol.MaxJSONMessageSize {
			reject(conn, errTooBig)
			return
		}
	}
	if m.write(r, websocket.TextMessage, encoded) != nil {
		return
	}
	if operationErr == nil && r.open.Payload.Operation == "READ" {
		if m.write(r, websocket.BinaryMessage, content) != nil {
			return
		}
	}
	// The SaaS peer closes normally after consuming the single result/body.
	select {
	case <-readerDone:
	case <-r.ctx.Done():
	}
}

func matches(r *request, f frame) bool {
	return f.FileRequestID == r.open.FileRequestID && f.LabInstanceID == r.open.LabInstanceID && f.Generation == r.open.Generation
}
func readJSON(conn *websocket.Conn) (frame, error) {
	kind, reader, err := conn.NextReader()
	if err != nil {
		return frame{}, err
	}
	if kind != websocket.TextMessage {
		return frame{}, errProtocol
	}
	data, err := io.ReadAll(io.LimitReader(reader, protocol.MaxJSONMessageSize+1))
	if err != nil {
		return frame{}, err
	}
	if int64(len(data)) > protocol.MaxJSONMessageSize {
		return frame{}, errTooBig
	}
	f, ok := parse(data)
	if !ok {
		return frame{}, errProtocol
	}
	return f, nil
}
func (m *Manager) writeJSON(r *request, value any) error {
	data, err := json.Marshal(value)
	if err != nil || int64(len(data)) > protocol.MaxJSONMessageSize {
		return errTooBig
	}
	return m.write(r, websocket.TextMessage, data)
}
func (m *Manager) write(r *request, kind int, data []byte) error {
	if r.ctx.Err() != nil {
		return r.ctx.Err()
	}
	_ = r.conn.SetWriteDeadline(time.Now().Add(m.config.WriteTimeout))
	return r.conn.WriteMessage(kind, data)
}
func reject(conn *websocket.Conn, err error) {
	code := websocket.ClosePolicyViolation
	if errors.Is(err, errTooBig) || errors.Is(err, websocket.ErrReadLimit) {
		code = websocket.CloseMessageTooBig
	}
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "invalid File frame"), time.Now().Add(100*time.Millisecond))
	_ = conn.Close()
}
func validRevision(rev string) bool {
	if len(rev) < 1 || len(rev) > 128 {
		return false
	}
	for _, c := range rev {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func failure(err error) map[string]any {
	code := workspacefile.ErrInternal
	for _, candidate := range []workspacefile.Error{workspacefile.ErrNotFound, workspacefile.ErrNotAFile, workspacefile.ErrNotADirectory, workspacefile.ErrTooLarge, workspacefile.ErrRevisionConflict, workspacefile.ErrPermissionDenied, workspacefile.ErrInvalidPath, workspacefile.ErrUnavailable, workspacefile.ErrInternal} {
		if errors.Is(err, candidate) {
			code = candidate
			break
		}
	}
	return map[string]any{"outcome": protocol.OutcomeFailed, "error": protocol.SafeError{Code: string(code)}}
}
