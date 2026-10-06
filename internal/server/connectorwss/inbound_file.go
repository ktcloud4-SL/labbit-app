package connectorwss

import (
	"encoding/json"
	"log/slog"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// 이 file의 검증은 file-control.schema.json의 FileOpenResult message를 그대로 옮긴 것이다.
// inbound.go와 같은 규칙을 따른다(정확한 property 이름, 정의되지 않은 field 무시, Trace는 검증하지 않고 별도 정상화).
// Schema의 BaseEnvelope는 fileRequestId, labInstanceId, generation을 모든 message에서 required로 둔다.

// fileHeader는 checkEnvelope를 통과한 envelope에서 File 요청 correlation을 꺼낸다.
// FILE_OPEN_RESULT는 replyToMessageId가 required다(requireReplyTo).
func fileHeader(envelope map[string]json.RawMessage, requireReplyTo bool) (connector.FileInbound, map[string]json.RawMessage, decodeStatus) {
	messageID, payload, ok := checkEnvelope(envelope)
	if !ok {
		return connector.FileInbound{}, nil, decodeInvalid
	}
	fileRequestID, ok := jsonString(envelope["fileRequestId"])
	if !ok || fileRequestID == "" {
		return connector.FileInbound{}, nil, decodeInvalid
	}
	labInstanceID, ok := jsonString(envelope["labInstanceId"])
	if !ok || labInstanceID == "" {
		return connector.FileInbound{}, nil, decodeInvalid
	}
	generation, valid, inRange := parseGeneration(envelope["generation"])
	if !valid {
		return connector.FileInbound{}, nil, decodeInvalid
	}
	if !inRange {
		generation = 0
	}

	var replyTo string
	if raw, present := envelope["replyToMessageId"]; present {
		replyTo, _ = jsonString(raw) // checkEnvelope가 비어 있지 않은 string임을 확인했다.
	} else if requireReplyTo {
		return connector.FileInbound{}, nil, decodeInvalid
	}

	return connector.FileInbound{
		MessageID:        messageID,
		ReplyToMessageID: replyTo,
		Correlation:      connector.FileCorrelation{FileRequestID: fileRequestID, LabInstanceID: labInstanceID, Generation: generation},
		Trace:            envelopeTrace(envelope),
	}, payload, decodeOK
}

func decodeFileOpenResult(payload map[string]json.RawMessage) (connector.FileOpenResultPayload, decodeStatus) {
	outcome, ok := jsonString(payload["outcome"])
	if !ok || (outcome != connector.FileOutcomeSucceeded && outcome != connector.FileOutcomeFailed) {
		return connector.FileOpenResultPayload{}, decodeInvalid
	}
	out := connector.FileOpenResultPayload{Outcome: outcome}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return connector.FileOpenResultPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	return out, decodeOK
}

// routeFileInbound는 type이 확인된 FILE_OPEN_RESULT를 검증해 Router에 넘긴다. Router가 없으면 버린다.
//
// routeInbound와 같은 원칙을 따른다. Schema-invalid이면 어떤 pending에도 넘기지 않고 non-fatal ERROR만 보내며 연결을 유지한다.
// 넘기는 일은 이 Session이 아직 current인 동안에만 한다. 교체·revoke된 Session의 message는 routing하지 않는다.
func (h *Handler) routeFileInbound(cc *controlConn, registration *connector.Registration, principal connector.Principal, log *slog.Logger, kind string, envelope map[string]json.RawMessage) {
	if h.router == nil {
		return
	}
	in, payload, status := fileHeader(envelope, kind == protocol.MessageTypeFileOpenResult)

	var route func()
	if status == decodeOK {
		var decoded connector.FileOpenResultPayload
		decoded, status = decodeFileOpenResult(payload)
		route = func() { h.router.RouteFileOpenResult(principal.ConnectorID, in, decoded) }
	}
	switch status {
	case decodeInvalid:
		log.Warn("Connector Control 응답 message 거절", "message_type", kind, "error_code", errorCodeInvalidMessage)
		sendProtocolError(cc, errorCodeInvalidMessage, kind+" does not match the connector contract", false)
		return
	case decodeUnrepresentable:
		route = func() { h.router.RouteFileUnrepresentable(principal.ConnectorID, kind, in) }
	}

	if in.Correlation.Generation < 1 {
		// schema-valid이지만 generation이 int64를 넘는다. 어떤 File 요청과도 맞지 않으므로 unmatched로 알린다.
		route = func() { h.router.RouteFileUnrepresentable(principal.ConnectorID, kind, in) }
	}

	current, _ := registration.IfCurrent(func() error {
		route()
		return nil
	})
	if !current {
		log.Debug("교체·revoke된 Session의 응답 message를 routing하지 않음", "message_type", kind)
	}
}

// helloCapabilities는 validateHello를 통과한 HELLO에서 선언한 capability를 꺼낸다. 없으면 nil이다.
func helloCapabilities(data []byte) []string {
	envelope, ok := jsonObject(data)
	if !ok {
		return nil
	}
	payload, ok := jsonObject(envelope["payload"])
	if !ok {
		return nil
	}
	raw, present := payload["capabilities"]
	if !present {
		return nil
	}
	var capabilities []string
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		return nil
	}
	return capabilities
}
