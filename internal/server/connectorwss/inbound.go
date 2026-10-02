package connectorwss

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"github.com/ktcloud4-SL/labbit-app/internal/server/connector"
)

// decodeStatus는 inbound business message의 Schema 검증·decode 결과다.
type decodeStatus int

const (
	// decodeOK는 Schema를 만족하고 typed model로 표현할 수 있다.
	decodeOK decodeStatus = iota
	// decodeInvalid는 connector.schema.json을 만족하지 않는다.
	decodeInvalid
	// decodeUnrepresentable은 Schema는 만족하지만 wire에 상한이 없는 정수가 int64를 넘어 typed model로 표현할 수 없다.
	// Schema 위반이 아니므로 INVALID_MESSAGE로 분류하지 않는다.
	decodeUnrepresentable
)

// 이 file의 검증은 connector.schema.json의 OperationAck/Progress/Result, ReconcileResult message를 그대로 옮긴 것이다.
// HELLO/HEARTBEAT와 같은 규칙을 따른다.
//   - property 이름은 대소문자를 구분한다. 판단에 쓰는 값과 typed model에 담는 값은 모두 정확한 이름으로 조회한
//     RawMessage에서 직접 decode한다. struct decode는 대소문자만 다른 key가 정확한 key의 값을 덮어쓸 수 있어 쓰지 않는다.
//   - 정의되지 않은 field(대소문자만 다른 key 포함)는 무시한다(v1의 unknown optional field).
//   - traceparent/tracestate는 검증하지 않는다. 별도로 정상화하며 잘못되어도 업무 검증에 영향을 주지 않는다.
//   - generation은 Schema에 maximum이 없으므로 lexical하게 판정하고 int64 범위를 wire 제약으로 쓰지 않는다.

// inboundHeader는 checkEnvelope를 통과한 envelope에서 business correlation을 꺼낸다.
// operationId/labInstanceId/generation은 ACK/PROGRESS/RESULT/RECONCILE_RESULT 모두 required이고,
// replyToMessageId는 requireReplyTo일 때만 required다. 없어도 되는 message에서 있으면 이미 checkEnvelope가 형식을 확인했다.
func inboundHeader(envelope map[string]json.RawMessage, requireReplyTo bool) (connector.Inbound, map[string]json.RawMessage, decodeStatus) {
	messageID, payload, ok := checkEnvelope(envelope)
	if !ok {
		return connector.Inbound{}, nil, decodeInvalid
	}
	operationID, ok := jsonString(envelope["operationId"])
	if !ok || operationID == "" {
		return connector.Inbound{}, nil, decodeInvalid
	}
	labInstanceID, ok := jsonString(envelope["labInstanceId"])
	if !ok || labInstanceID == "" {
		return connector.Inbound{}, nil, decodeInvalid
	}
	generation, valid, inRange := parseGeneration(envelope["generation"])
	if !valid {
		return connector.Inbound{}, nil, decodeInvalid
	}
	if !inRange {
		generation = 0
	}

	var replyTo string
	if raw, present := envelope["replyToMessageId"]; present {
		replyTo, _ = jsonString(raw) // checkEnvelope가 비어 있지 않은 string임을 확인했다.
	} else if requireReplyTo {
		return connector.Inbound{}, nil, decodeInvalid
	}

	return connector.Inbound{
		MessageID:        messageID,
		ReplyToMessageID: replyTo,
		OperationID:      operationID,
		LabInstanceID:    labInstanceID,
		Generation:       generation,
		Trace:            envelopeTrace(envelope),
	}, payload, decodeOK
}

// envelopeTrace는 선택 Trace field를 표준 parser로 정상화한다. string이 아니거나 유효하지 않은 값은 버린다.
func envelopeTrace(envelope map[string]json.RawMessage) connector.TraceContext {
	traceparent, _ := jsonString(envelope["traceparent"])
	tracestate, _ := jsonString(envelope["tracestate"])
	return connector.NormalizeTrace(traceparent, tracestate)
}

func decodeOperationAck(payload map[string]json.RawMessage) (protocol.OperationAckPayload, decodeStatus) {
	accepted, ok := jsonBool(payload["accepted"])
	if !ok {
		return protocol.OperationAckPayload{}, decodeInvalid
	}
	out := protocol.OperationAckPayload{Accepted: accepted}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return protocol.OperationAckPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	return out, decodeOK
}

func decodeOperationProgress(payload map[string]json.RawMessage) (protocol.OperationProgressPayload, decodeStatus) {
	stage, ok := jsonString(payload["stage"])
	if !ok || stage == "" {
		return protocol.OperationProgressPayload{}, decodeInvalid
	}
	return protocol.OperationProgressPayload{Stage: stage}, decodeOK
}

func decodeOperationResult(payload map[string]json.RawMessage) (protocol.OperationResultPayload, decodeStatus) {
	outcome, ok := jsonString(payload["outcome"])
	if !ok || (outcome != protocol.OutcomeSucceeded && outcome != protocol.OutcomeFailed && outcome != protocol.OutcomeUnknown) {
		return protocol.OperationResultPayload{}, decodeInvalid
	}
	items, ok := jsonArray(payload["providerResources"])
	if !ok {
		return protocol.OperationResultPayload{}, decodeInvalid
	}

	out := protocol.OperationResultPayload{Outcome: outcome, ProviderResources: make([]protocol.ProviderResourceResult, 0, len(items))}
	unrepresentable := false
	for _, raw := range items {
		item, ok := jsonObject(raw)
		if !ok {
			return protocol.OperationResultPayload{}, decodeInvalid
		}
		resourceType, ok1 := jsonString(item["resourceType"])
		providerID, ok2 := jsonString(item["providerId"])
		if !ok1 || resourceType == "" || !ok2 || providerID == "" {
			return protocol.OperationResultPayload{}, decodeInvalid
		}
		generation, valid, inRange := parseGeneration(item["generation"]) // ProviderResourceRef.generation은 required
		if !valid {
			return protocol.OperationResultPayload{}, decodeInvalid
		}
		unrepresentable = unrepresentable || !inRange
		logicalName, ok3 := optionalString(item, "logicalName")
		observedState, ok4 := optionalString(item, "observedState")
		if !ok3 || !ok4 {
			return protocol.OperationResultPayload{}, decodeInvalid
		}
		out.ProviderResources = append(out.ProviderResources, protocol.ProviderResourceResult{
			ProviderResourceRef: protocol.ProviderResourceRef{ResourceType: resourceType, ProviderID: providerID, Generation: generation, LogicalName: logicalName},
			ObservedState:       observedState,
		})
	}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return protocol.OperationResultPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	if unrepresentable {
		return protocol.OperationResultPayload{}, decodeUnrepresentable
	}
	return out, decodeOK
}

func decodeReconcileResult(payload map[string]json.RawMessage) (protocol.ReconcileResultPayload, decodeStatus) {
	items, ok := jsonArray(payload["observations"])
	if !ok {
		return protocol.ReconcileResultPayload{}, decodeInvalid
	}

	out := protocol.ReconcileResultPayload{Observations: make([]protocol.ResourceObservation, 0, len(items))}
	unrepresentable := false
	for _, raw := range items {
		item, ok := jsonObject(raw)
		if !ok {
			return protocol.ReconcileResultPayload{}, decodeInvalid
		}
		resourceType, ok1 := jsonString(item["resourceType"])
		providerID, ok2 := jsonString(item["providerId"])
		exists, ok3 := jsonBool(item["exists"])
		source, ok4 := jsonString(item["source"])
		if !ok1 || resourceType == "" || !ok2 || providerID == "" || !ok3 || !ok4 {
			return protocol.ReconcileResultPayload{}, decodeInvalid
		}
		if source != "KNOWN_RESOURCE" && source != "DISCOVERED_CANDIDATE" {
			return protocol.ReconcileResultPayload{}, decodeInvalid
		}
		var generation int64
		if rawGeneration, present := item["generation"]; present { // ResourceObservation.generation은 optional
			value, valid, inRange := parseGeneration(rawGeneration)
			if !valid {
				return protocol.ReconcileResultPayload{}, decodeInvalid
			}
			generation = value
			unrepresentable = unrepresentable || !inRange
		}
		observedState, ok5 := optionalString(item, "observedState")
		logicalName, ok6 := optionalString(item, "logicalName")
		if !ok5 || !ok6 {
			return protocol.ReconcileResultPayload{}, decodeInvalid
		}
		out.Observations = append(out.Observations, protocol.ResourceObservation{
			ResourceType: resourceType, ProviderID: providerID, Generation: generation,
			Exists: exists, ObservedState: observedState, Source: source, LogicalName: logicalName,
		})
	}
	if raw, present := payload["error"]; present {
		safe, ok := decodeSafeError(raw)
		if !ok {
			return protocol.ReconcileResultPayload{}, decodeInvalid
		}
		out.Error = safe
	}
	if unrepresentable {
		return protocol.ReconcileResultPayload{}, decodeUnrepresentable
	}
	return out, decodeOK
}

// decodeSafeError는 SafeError(code: 비어 있지 않은 string required, message: string optional)를 decode한다.
func decodeSafeError(raw json.RawMessage) (*protocol.SafeError, bool) {
	obj, ok := jsonObject(raw)
	if !ok {
		return nil, false
	}
	code, ok := jsonString(obj["code"])
	if !ok || code == "" {
		return nil, false
	}
	message, ok := optionalString(obj, "message")
	if !ok {
		return nil, false
	}
	return &protocol.SafeError{Code: code, Message: message}, true
}

// jsonBool은 raw가 JSON boolean일 때만 그 값을 반환한다. 없는 field와 null은 boolean이 아니다.
func jsonBool(raw json.RawMessage) (bool, bool) {
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil || string(raw) == "null" {
		return false, false
	}
	return b, true
}

// jsonArray는 raw가 JSON array일 때만 그 원소를 반환한다. 없는 field와 null은 array가 아니다. 빈 array는 비어 있지 않은 slice가 아니어도 ok다.
func jsonArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, false
	}
	return items, true
}

// optionalString은 member가 없으면 ("", true), 있으면 JSON string일 때만 그 값과 true를 반환한다.
func optionalString(members map[string]json.RawMessage, name string) (string, bool) {
	raw, present := members[name]
	if !present {
		return "", true
	}
	return jsonString(raw)
}

// parseGeneration은 integerAtLeastOne의 lexical 판정에 더해 값이 int64에 들어가는지 알려준다.
// valid는 Schema(integer, minimum 1)를 만족하는지, inRange는 valid이면서 int64로 표현되는지다.
// valid이고 inRange가 아니면 값은 0이다. Schema에 maximum이 없으므로 int64 초과를 invalid로 분류하지 않는다.
func parseGeneration(raw json.RawMessage) (value int64, valid, inRange bool) {
	digits, zeros, valid, huge := lexicalPositiveInteger(raw)
	if !valid {
		return 0, false, false
	}
	// int64는 최대 19자리다. 지수가 너무 큰 값(huge)도 그보다 크다.
	if huge || int64(len(digits))+zeros > 19 {
		return 0, true, false
	}
	parsed, err := strconv.ParseInt(digits+strings.Repeat("0", int(zeros)), 10, 64)
	if err != nil {
		return 0, true, false
	}
	return parsed, true, true
}

// integerAtLeastOne은 raw가 1 이상의 정수 값인 JSON number일 때만 true다(generation: integer, minimum 1).
// Schema에 maximum이 없고 JSON Schema 2020-12의 integer는 1.0, 1e2처럼 소수부가 0인 표기도 포함한다.
func integerAtLeastOne(raw json.RawMessage) bool {
	_, _, valid, _ := lexicalPositiveInteger(raw)
	return valid
}

// lexicalPositiveInteger는 값을 계산하지 않고 숫자 문법만 lexical하게 판정한다. 유효숫자열 D와 지수로 값은 D × 10^e이고,
// D의 앞 0을 버리고 뒤 0을 e로 옮기면 D는 0으로 끝나지 않으므로 e >= 0일 때만 정수다(D가 0이면 값이 0이다).
// 지수는 부호와 자릿수로만 비교해 1e999999999처럼 큰 값을 만들지 않는다. big.Int로 지수를 읽으면
// 1 MiB 지수 문자열에서 처리 시간이 자릿수의 제곱으로 늘 수 있어 쓰지 않는다.
//
// valid이면 값 = digits(0으로 끝나지 않고 앞 0이 없는 십진 문자열) × 10^zeros다. 지수가 너무 커서 zeros를 표현할 수 없으면
// huge이고 digits/zeros는 쓰지 않는다. 숫자가 아니거나 문법에 맞지 않거나 1 미만이면 valid가 아니다.
func lexicalPositiveInteger(raw json.RawMessage) (digits string, zeros int64, valid, huge bool) {
	digitsAt := func(i int) int {
		n := 0
		for i+n < len(raw) && raw[i+n] >= '0' && raw[i+n] <= '9' {
			n++
		}
		return n
	}

	i := 0
	intLen := digitsAt(i)
	if intLen == 0 || (intLen > 1 && raw[i] == '0') { // 음수('-')와 number가 아닌 값도 여기서 거절한다.
		return "", 0, false, false
	}
	intPart := string(raw[i : i+intLen])
	i += intLen

	var fracPart string
	if i < len(raw) && raw[i] == '.' {
		i++
		fracLen := digitsAt(i)
		if fracLen == 0 {
			return "", 0, false, false
		}
		fracPart = string(raw[i : i+fracLen])
		i += fracLen
	}

	var expDigits string
	expNegative := false
	if i < len(raw) && (raw[i] == 'e' || raw[i] == 'E') {
		i++
		if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
			expNegative = raw[i] == '-'
			i++
		}
		expLen := digitsAt(i)
		if expLen == 0 {
			return "", 0, false, false
		}
		expDigits = string(raw[i : i+expLen])
		i += expLen
	}
	if i != len(raw) {
		return "", 0, false, false
	}

	all := strings.TrimLeft(intPart+fracPart, "0")
	if all == "" {
		return "", 0, false, false
	}
	mantissa := strings.TrimRight(all, "0")
	trailingZeros := len(all) - len(mantissa)
	// 값 = mantissa × 10^(exp - shift)
	shift := int64(len(fracPart) - trailingZeros)

	expDigits = strings.TrimLeft(expDigits, "0")
	if len(expDigits) > 18 {
		// 지수의 크기가 shift(입력 길이 이하)보다 항상 크므로 부호만 본다.
		if expNegative {
			return "", 0, false, false
		}
		return "", 0, true, true
	}
	var exp int64
	if expDigits != "" {
		exp, _ = strconv.ParseInt(expDigits, 10, 64)
	}
	if expNegative {
		exp = -exp
	}
	if exp < shift {
		return "", 0, false, false
	}
	return mantissa, exp - shift, true, false
}

// safeCodePattern은 Connector가 보낸 ERROR code를 로그에 남길 수 있는 형태다. 기계 판독용 상수 형태만 허용한다.
var safeCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// safeErrorCode는 inbound ERROR의 code를 log용으로 반환한다. 형태가 다르면 고정 값이다. message는 읽지 않는다.
func safeErrorCode(envelope map[string]json.RawMessage) string {
	payload, ok := jsonObject(envelope["payload"])
	if !ok {
		return "INVALID"
	}
	code, ok := jsonString(payload["code"])
	if !ok || !safeCodePattern.MatchString(code) {
		return "INVALID"
	}
	return code
}
