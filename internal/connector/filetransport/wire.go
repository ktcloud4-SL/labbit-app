// Package filetransport connects one outbound File Data WSS to one rooted
// Workspace SFTP operation. There is no reconnect, retry, content log or store.
package filetransport

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/jsonnum"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type frame struct {
	protocol.BaseEnvelope
	Payload map[string]json.RawMessage
}

func object(raw []byte) (map[string]json.RawMessage, bool) {
	var v map[string]json.RawMessage
	err := json.Unmarshal(raw, &v)
	return v, err == nil && v != nil
}
func stringValue(raw []byte) (string, bool) {
	var v string
	err := json.Unmarshal(raw, &v)
	return v, err == nil && string(raw) != "null"
}
func requiredString(p map[string]json.RawMessage, key string) (string, bool) {
	v, ok := stringValue(p[key])
	return v, ok && v != ""
}

// JSON Schema integer accepts decimal/exponent spellings too. The shared
// lexical parser never computes an attacker-controlled power of ten.
func integer(raw []byte) (int64, bool) {
	v, valid, inRange := jsonnum.Int64(raw)
	return v, valid && inRange
}
func parse(raw []byte) (frame, bool) {
	if int64(len(raw)) > protocol.MaxJSONMessageSize || !utf8.Valid(raw) {
		return frame{}, false
	}
	p, ok := object(raw)
	if !ok {
		return frame{}, false
	}
	var f frame
	if f.Type, ok = requiredString(p, "type"); !ok {
		return frame{}, false
	}
	if f.MessageID, ok = requiredString(p, "messageId"); !ok {
		return frame{}, false
	}
	if json.Unmarshal(p["sentAt"], &f.SentAt) != nil || f.SentAt.IsZero() {
		return frame{}, false
	}
	if f.FileRequestID, ok = requiredString(p, "fileRequestId"); !ok {
		return frame{}, false
	}
	if f.LabInstanceID, ok = requiredString(p, "labInstanceId"); !ok {
		return frame{}, false
	}
	if f.Generation, ok = integer(p["generation"]); !ok || f.Generation < 1 {
		return frame{}, false
	}
	if f.Payload, ok = object(p["payload"]); !ok {
		return frame{}, false
	}
	for key, dest := range map[string]*string{"replyToMessageId": &f.ReplyToMessageID, "requestId": &f.RequestID} {
		if _, present := p[key]; present {
			if *dest, ok = requiredString(p, key); !ok {
				return frame{}, false
			}
		}
	}
	parent, parentOK := stringValue(p["traceparent"])
	state, _ := stringValue(p["tracestate"])
	if parentOK && len(parent) <= 512 {
		carrier := propagation.MapCarrier{"traceparent": parent}
		if len(state) <= 1024 {
			carrier["tracestate"] = state
		}
		sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), carrier))
		if sc.IsValid() {
			f.TraceParent = parent
			f.TraceState = sc.TraceState().String()
		}
	}
	return f, true
}

func ParseOpen(raw []byte) (protocol.FileOpenMessage, bool) {
	f, ok := parse(raw)
	if !ok || f.Type != protocol.MessageTypeFileOpen {
		return protocol.FileOpenMessage{}, false
	}
	var p protocol.FileOpenPayload
	if p.Operation, ok = requiredString(f.Payload, "operation"); !ok || (p.Operation != "TREE" && p.Operation != "READ" && p.Operation != "SAVE") {
		return protocol.FileOpenMessage{}, false
	}
	if p.TargetVMKey, ok = requiredString(f.Payload, "targetVmKey"); !ok {
		return protocol.FileOpenMessage{}, false
	}
	if p.ProviderServerID, ok = requiredString(f.Payload, "providerServerId"); !ok {
		return protocol.FileOpenMessage{}, false
	}
	return protocol.FileOpenMessage{BaseEnvelope: f.BaseEnvelope, Payload: p}, true
}
func ParseClose(raw []byte) (protocol.FileCloseMessage, bool) {
	f, ok := parse(raw)
	if !ok || f.Type != protocol.MessageTypeFileClose {
		return protocol.FileCloseMessage{}, false
	}
	reason, ok := requiredString(f.Payload, "reason")
	if !ok {
		return protocol.FileCloseMessage{}, false
	}
	return protocol.FileCloseMessage{BaseEnvelope: f.BaseEnvelope, Payload: protocol.FileClosePayload{Reason: reason}}, true
}
func responseEnvelope(in protocol.BaseEnvelope, kind string) protocol.BaseEnvelope {
	return protocol.BaseEnvelope{Type: kind, MessageID: newID(), SentAt: time.Now().UTC(), ReplyToMessageID: in.MessageID, FileRequestID: in.FileRequestID, LabInstanceID: in.LabInstanceID, Generation: in.Generation, RequestID: in.RequestID, TraceParent: in.TraceParent, TraceState: in.TraceState}
}
