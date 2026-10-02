package connectorwss

import (
	"encoding/json"
	"log/slog"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
)

// 이 file의 검증은 terminal-control.schema.json의 TerminalOpenResult, TerminalEnded message를 그대로 옮긴 것이다.
// inbound.go와 같은 규칙을 따른다(정확한 property 이름, 정의되지 않은 field 무시, Trace는 검증하지 않고 별도 정상화).
// Schema의 BaseEnvelope는 terminalSessionId, labInstanceId, generation을 모든 message에서 required로 둔다.

// terminalHeader는 checkEnvelope를 통과한 envelope에서 TerminalSession correlation을 꺼낸다.
// TERMINAL_OPEN_RESULT만 replyToMessageId가 required다(requireReplyTo).
func terminalHeader(envelope map[string]json.RawMessage, requireReplyTo bool) (connector.TerminalInbound, map[string]json.RawMessage, decodeStatus) {
	messageID, payload, ok := checkEnvelope(envelope)
	if !ok {
		return connector.TerminalInbound{}, nil, decodeInvalid
	}
	terminalSessionID, ok := jsonString(envelope["terminalSessionId"])
	if !ok || terminalSessionID == "" {
		return connector.TerminalInbound{}, nil, decodeInvalid
	}
	labInstanceID, ok := jsonString(envelope["labInstanceId"])
	if !ok || labInstanceID == "" {
		return connector.TerminalInbound{}, nil, decodeInvalid
	}
	generation, valid, inRange := parseGeneration(envelope["generation"])
	if !valid {
		return connector.TerminalInbound{}, nil, decodeInvalid
	}
	if !inRange {
		generation = 0
	}

	var replyTo string
	if raw, present := envelope["replyToMessageId"]; present {
		replyTo, _ = jsonString(raw) // checkEnvelope가 비어 있지 않은 string임을 확인했다.
	} else if requireReplyTo {
		return connector.TerminalInbound{}, nil, decodeInvalid
	}

	return connector.TerminalInbound{
		MessageID:        messageID,
		ReplyToMessageID: replyTo,
		Correlation:      connector.TerminalCorrelation{TerminalSessionID: terminalSessionID, LabInstanceID: labInstanceID, Generation: generation},
		Trace:            envelopeTrace(envelope),
	}, payload, decodeOK
}

func decodeTerminalOpenResult(payload map[string]json.RawMessage) (connector.TerminalOpenResultPayload, decodeStatus) {
	outcome, ok := jsonString(payload["outcome"])
	if !ok || (outcome != connector.TerminalOutcomeSucceeded && outcome != connector.TerminalOutcomeFailed) {
		return connector.TerminalOpenResultPayload{}, decodeInvalid
	}
	out := connector.TerminalOpenResultPayload{Outcome: outcome}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return connector.TerminalOpenResultPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	return out, decodeOK
}

func decodeTerminalEnded(payload map[string]json.RawMessage) (connector.TerminalEndedPayload, decodeStatus) {
	reason, ok := jsonString(payload["reason"])
	if !ok || reason == "" {
		return connector.TerminalEndedPayload{}, decodeInvalid
	}
	out := connector.TerminalEndedPayload{Reason: reason}
	if raw, present := payload["exitCode"]; present {
		value, valid, inRange := jsonnum.Int64(raw)
		if !valid {
			return connector.TerminalEndedPayload{}, decodeInvalid
		}
		// Schema에 범위 제한이 없으므로 int64를 넘는 exit code는 Schema 위반이 아니다. 알 수 없는 값으로 취급하고 0으로 만들지 않는다.
		if inRange {
			out.ExitCode = &value
		}
	}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return connector.TerminalEndedPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	return out, decodeOK
}

// routeTerminalInbound는 type이 확인된 TERMINAL_OPEN_RESULT/TERMINAL_ENDED를 검증해 Router에 넘긴다. Router가 없으면 버린다.
//
// routeInbound와 같은 원칙을 따른다. Schema-invalid이면 어떤 pending에도 넘기지 않고 non-fatal ERROR만 보내며 연결을 유지한다.
// 넘기는 일은 이 Session이 아직 current인 동안에만 한다. 교체·revoke된 Session의 message는 routing하지 않는다.
func (h *Handler) routeTerminalInbound(cc *controlConn, registration *connector.Registration, principal connector.Principal, log *slog.Logger, kind string, envelope map[string]json.RawMessage) {
	if h.router == nil {
		return
	}
	in, payload, status := terminalHeader(envelope, kind == protocol.MessageTypeTerminalOpenResult)

	var route func()
	if status == decodeOK {
		switch kind {
		case protocol.MessageTypeTerminalOpenResult:
			var decoded connector.TerminalOpenResultPayload
			decoded, status = decodeTerminalOpenResult(payload)
			route = func() { h.router.RouteTerminalOpenResult(principal.ConnectorID, in, decoded) }
		default: // protocol.MessageTypeTerminalEnded
			var decoded connector.TerminalEndedPayload
			decoded, status = decodeTerminalEnded(payload)
			route = func() { h.router.RouteTerminalEnded(principal.ConnectorID, in, decoded) }
		}
	}
	switch status {
	case decodeInvalid:
		log.Warn("Connector Control 응답 message 거절", "message_type", kind, "error_code", errorCodeInvalidMessage)
		sendProtocolError(cc, errorCodeInvalidMessage, kind+" does not match the connector contract", false)
		return
	case decodeUnrepresentable:
		route = func() { h.router.RouteTerminalUnrepresentable(principal.ConnectorID, kind, in) }
	}

	if in.Correlation.Generation < 1 {
		// schema-valid이지만 generation이 int64를 넘는다. 어떤 TerminalSession과도 맞지 않으므로 unmatched로 알린다.
		route = func() { h.router.RouteTerminalUnrepresentable(principal.ConnectorID, kind, in) }
	}

	current, _ := registration.IfCurrent(func() error {
		route()
		return nil
	})
	if !current {
		log.Debug("교체·revoke된 Session의 응답 message를 routing하지 않음", "message_type", kind)
	}
}
