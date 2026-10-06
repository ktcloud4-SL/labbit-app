package connectorwss

import (
	"encoding/json"
	"log/slog"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 이 file의 검증은 preview-control.schema.json의 PreviewOpenResult message를 그대로 옮긴 것이다.
// inbound.go와 같은 규칙을 따른다(정확한 property 이름, 정의되지 않은 field 무시, Trace는 검증하지 않고 별도 정상화).
// Schema의 BaseEnvelope는 previewSessionId, labInstanceId, generation을 모든 message에서 required로 둔다.

// previewHeader는 checkEnvelope를 통과한 envelope에서 PreviewSession correlation을 꺼낸다.
// PREVIEW_OPEN_RESULT는 replyToMessageId가 required다(requireReplyTo).
func previewHeader(envelope map[string]json.RawMessage, requireReplyTo bool) (connector.PreviewInbound, map[string]json.RawMessage, decodeStatus) {
	messageID, payload, ok := checkEnvelope(envelope)
	if !ok {
		return connector.PreviewInbound{}, nil, decodeInvalid
	}
	previewSessionID, ok := jsonString(envelope["previewSessionId"])
	if !ok || previewSessionID == "" {
		return connector.PreviewInbound{}, nil, decodeInvalid
	}
	labInstanceID, ok := jsonString(envelope["labInstanceId"])
	if !ok || labInstanceID == "" {
		return connector.PreviewInbound{}, nil, decodeInvalid
	}
	generation, valid, inRange := parseGeneration(envelope["generation"])
	if !valid {
		return connector.PreviewInbound{}, nil, decodeInvalid
	}
	if !inRange {
		generation = 0
	}

	var replyTo string
	if raw, present := envelope["replyToMessageId"]; present {
		replyTo, _ = jsonString(raw) // checkEnvelope가 비어 있지 않은 string임을 확인했다.
	} else if requireReplyTo {
		return connector.PreviewInbound{}, nil, decodeInvalid
	}

	return connector.PreviewInbound{
		MessageID:        messageID,
		ReplyToMessageID: replyTo,
		Correlation:      connector.PreviewCorrelation{PreviewSessionID: previewSessionID, LabInstanceID: labInstanceID, Generation: generation},
		Trace:            envelopeTrace(envelope),
	}, payload, decodeOK
}

func decodePreviewOpenResult(payload map[string]json.RawMessage) (connector.PreviewOpenResultPayload, decodeStatus) {
	outcome, ok := jsonString(payload["outcome"])
	if !ok || (outcome != connector.PreviewOutcomeSucceeded && outcome != connector.PreviewOutcomeFailed) {
		return connector.PreviewOpenResultPayload{}, decodeInvalid
	}
	out := connector.PreviewOpenResultPayload{Outcome: outcome}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return connector.PreviewOpenResultPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	return out, decodeOK
}

// routePreviewInbound는 type이 확인된 PREVIEW_OPEN_RESULT를 검증해 Router에 넘긴다. Router가 없으면 버린다.
//
// routeInbound와 같은 원칙을 따른다. Schema-invalid이면 어떤 pending에도 넘기지 않고 non-fatal ERROR만 보내며 연결을 유지한다.
// 넘기는 일은 이 Session이 아직 current인 동안에만 한다. 교체·revoke된 Session의 message는 routing하지 않는다.
func (h *Handler) routePreviewInbound(cc *controlConn, registration *connector.Registration, principal connector.Principal, log *slog.Logger, kind string, envelope map[string]json.RawMessage) {
	if h.router == nil {
		return
	}
	in, payload, status := previewHeader(envelope, kind == protocol.MessageTypePreviewOpenResult)

	var route func()
	if status == decodeOK {
		var decoded connector.PreviewOpenResultPayload
		decoded, status = decodePreviewOpenResult(payload)
		route = func() { h.router.RoutePreviewOpenResult(principal.ConnectorID, in, decoded) }
	}
	switch status {
	case decodeInvalid:
		log.Warn("Connector Control 응답 message 거절", "message_type", kind, "error_code", errorCodeInvalidMessage)
		sendProtocolError(cc, errorCodeInvalidMessage, kind+" does not match the connector contract", false)
		return
	case decodeUnrepresentable:
		route = func() { h.router.RoutePreviewUnrepresentable(principal.ConnectorID, kind, in) }
	}

	if in.Correlation.Generation < 1 {
		// schema-valid이지만 generation이 int64를 넘는다. 어떤 PreviewSession과도 맞지 않으므로 unmatched로 알린다.
		route = func() { h.router.RoutePreviewUnrepresentable(principal.ConnectorID, kind, in) }
	}

	current, _ := registration.IfCurrent(func() error {
		route()
		return nil
	})
	if !current {
		log.Debug("교체·revoke된 Session의 응답 message를 routing하지 않음", "message_type", kind)
	}
}
