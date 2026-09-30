package realtime

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// 이 file은 JSON Text control frame의 검증과 생성이다. 검증은 JSON Schema(contracts/realtime/terminal-live.schema.json,
// contracts/connector/terminal-data.schema.json)를 그대로 옮긴 것이며 Schema보다 엄격하거나 느슨하지 않다.
//
//   - property 이름은 대소문자를 구분한다. 판단에 쓰는 값은 정확한 이름으로 조회한 RawMessage에서 직접 decode한다.
//     struct decode는 대소문자만 다른 key가 정확한 key의 값을 덮어쓸 수 있어 쓰지 않는다.
//   - 정의되지 않은 field(대소문자만 다른 key 포함)는 무시한다(additionalProperties: true인 message).
//   - traceparent/tracestate는 업무 검증에서 제외하고 표준 parser로 따로 정상화한다(traceOf). 유효한 값만 message의 Trace로 보존하고
//     타입·길이·W3C 유효성이 잘못된 값은 그 관측 field만 버린다. Terminal은 관측 metadata 때문에 업무 message를 거절하지 않는다.
//   - cols/rows/generation 같은 integer는 JSON Schema 2020-12의 integer 규칙(1.0, 1e2도 정수)을 lexical하게 판정하고
//     Schema에 없는 상한을 만들지 않는다.

var (
	// errMalformed는 JSON 형식이거나 Schema의 required/type/minimum을 만족하지 않는 message다.
	errMalformed = errors.New("realtime: message가 계약을 만족하지 않음")
	// errUnsupportedType은 이 경계에서 받을 수 없는 type이다.
	errUnsupportedType = errors.New("realtime: 지원하지 않는 message type")
)

// 계약의 message type이다.
const (
	typeTerminalAttach       = "TERMINAL_ATTACH"
	typeTerminalAttached     = "TERMINAL_ATTACHED"
	typeTerminalResize       = "TERMINAL_RESIZE"
	typeTerminalSessionEnded = "TERMINAL_SESSION_ENDED"
	typeError                = "ERROR"

	typeDataAttach   = "TERMINAL_DATA_ATTACH"
	typeDataAttached = "TERMINAL_DATA_ATTACHED"
	typeDataResize   = "TERMINAL_DATA_RESIZE"
	typeDataClose    = "TERMINAL_DATA_CLOSE"
	typeDataEnded    = "TERMINAL_DATA_ENDED"
)

// Browser에 보내는 ERROR code다(contracts/realtime/README.md §9). INTERNAL_ERROR는 새 code이며 Client는 unknown code fallback을 가진다.
const (
	codeAuthRequired       = "AUTH_REQUIRED"
	codeInvalidSessionTok  = "INVALID_SESSION_TOKEN"
	codeForbidden          = "FORBIDDEN"
	codeSessionNotFound    = "SESSION_NOT_FOUND"
	codeSessionExpired     = "SESSION_EXPIRED"
	codeConnectorUnavail   = "CONNECTOR_UNAVAILABLE"
	codeProtocolError      = "PROTOCOL_ERROR"
	codeSlowConsumer       = "SLOW_CONSUMER"
	codeLabMutation        = "LAB_MUTATION"
	codeServiceRestarting  = "SERVICE_RESTARTING"
	codeInternalError      = "INTERNAL_ERROR"
	codeDataInvalidSession = "INVALID_SESSION"
	codeDataStaleGen       = "STALE_GENERATION"
)

func jsonObject(raw []byte) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return nil, false
	}
	return members, true
}

// jsonString은 raw가 JSON string일 때만 그 값을 반환한다. 없는 field(빈 raw)와 null은 string이 아니다.
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || string(raw) == "null" {
		return "", false
	}
	return s, true
}

// nonEmptyString은 raw가 비어 있지 않은 JSON string일 때만 그 값을 반환한다(ResourceId, MessageId: minLength 1).
func nonEmptyString(raw json.RawMessage) (string, bool) {
	s, ok := jsonString(raw)
	return s, ok && s != ""
}

// validTimestamp는 raw가 RFC 3339 date-time 형식의 JSON string일 때만 true다. null은 string이 아니므로 거절한다.
// RFC 3339는 'T'와 'Z'의 소문자도 허용하므로 대문자로 맞춘 뒤 해석한다.
func validTimestamp(raw json.RawMessage) bool {
	s, ok := jsonString(raw)
	if !ok {
		return false
	}
	_, err := time.Parse(time.RFC3339Nano, strings.ToUpper(s))
	return err == nil
}

// traceOf는 message의 선택 Trace field(traceparent, tracestate)를 표준 parser로 정상화한다(contracts/connector/README.md §9, D-25).
// string이 아니거나 유효하지 않은 값은 버린다. traceparent가 없거나 유효하지 않으면 tracestate도 쓰지 않고, traceparent만 유효하면
// tracestate만 버린다. 업무 검증과 독립이므로 이 값 때문에 message를 거절하지 않으며 원문을 기록하지도 않는다. 가짜 Trace ID를 만들지 않는다.
func traceOf(members map[string]json.RawMessage) tracecontext.Context {
	traceparent, _ := jsonString(members["traceparent"])
	tracestate, _ := jsonString(members["tracestate"])
	return tracecontext.Normalize(traceparent, tracestate)
}

// withTrace는 유효한 Trace Context가 있으면 그 trace_id를 log에 붙인다. 없으면 log를 그대로 반환하고 가짜 값을 만들지 않는다.
func withTrace(log *slog.Logger, t tracecontext.Context) *slog.Logger {
	if id := t.TraceID(); id != "" {
		return log.With("trace_id", id)
	}
	return log
}

// optionalNonEmpty는 member가 없으면 true, 있으면 비어 있지 않은 string일 때만 true다.
func optionalNonEmpty(members map[string]json.RawMessage, name string) bool {
	raw, present := members[name]
	if !present {
		return true
	}
	_, ok := nonEmptyString(raw)
	return ok
}

// 선택 Envelope field 중 Schema가 비어 있지 않은 string(ResourceId, MessageId: minLength 1)으로 제약하는 것이다.
// Browser(terminal-live.schema.json)와 Connector Data(terminal-data.schema.json)의 BaseEnvelope가 서로 다르다.
var (
	browserOptionalIDs = []string{"replyToMessageId", "requestId", "terminalSessionId", "liveSessionId"}
	dataOptionalIDs    = []string{"replyToMessageId"}
)

// envelope는 messageId, sentAt, payload(object)와 message의 Schema가 제약하는 선택 Envelope field를 확인한다.
// 성공하면 messageId와 payload를 반환한다.
func envelope(members map[string]json.RawMessage, optionalIDs []string) (messageID string, payload map[string]json.RawMessage, ok bool) {
	messageID, ok = nonEmptyString(members["messageId"])
	if !ok || !validTimestamp(members["sentAt"]) {
		return "", nil, false
	}
	for _, name := range optionalIDs {
		if !optionalNonEmpty(members, name) {
			return "", nil, false
		}
	}
	payload, ok = jsonObject(members["payload"])
	if !ok {
		return "", nil, false
	}
	return messageID, payload, true
}

// browserMessage는 Schema 검증을 통과한 Browser → Relay JSON control message다.
type browserMessage struct {
	Type              string
	MessageID         string
	TerminalSessionID string
	// Token은 TERMINAL_ATTACH에서만 채운다.
	Token AttachToken
	// Cols와 Rows는 TERMINAL_ATTACH와 TERMINAL_RESIZE에서 채우며 검증된 양의 정수 JSON number 원문이다.
	Cols, Rows json.RawMessage
	// Trace는 message의 유효한 Trace Context다. 없거나 유효하지 않으면 zero value다.
	Trace tracecontext.Context
}

// decodeBrowserMessage는 Browser가 보낼 수 있는 TERMINAL_ATTACH와 TERMINAL_RESIZE를 검증한다.
// 그 외 type은 errUnsupportedType이고, Schema를 만족하지 않으면 errMalformed다.
func decodeBrowserMessage(data []byte) (browserMessage, error) {
	members, ok := jsonObject(data)
	if !ok {
		return browserMessage{}, errMalformed
	}
	kind, ok := jsonString(members["type"])
	if !ok {
		return browserMessage{}, errMalformed
	}
	if kind != typeTerminalAttach && kind != typeTerminalResize {
		return browserMessage{}, errUnsupportedType
	}
	messageID, payload, ok := envelope(members, browserOptionalIDs)
	if !ok {
		return browserMessage{}, errMalformed
	}
	// 두 message 모두 terminalSessionId가 required다.
	sessionID, ok := nonEmptyString(members["terminalSessionId"])
	if !ok {
		return browserMessage{}, errMalformed
	}
	msg := browserMessage{Type: kind, MessageID: messageID, TerminalSessionID: sessionID, Trace: traceOf(members)}

	if kind == typeTerminalAttach {
		token, ok := nonEmptyString(payload["sessionToken"])
		if !ok {
			return browserMessage{}, errMalformed
		}
		msg.Token = AttachToken(token)
	} else {
		// TerminalResizePayload는 additionalProperties: false다.
		for name := range payload {
			if name != "cols" && name != "rows" {
				return browserMessage{}, errMalformed
			}
		}
	}
	if !jsonnum.PositiveInteger(payload["cols"]) || !jsonnum.PositiveInteger(payload["rows"]) {
		return browserMessage{}, errMalformed
	}
	msg.Cols, msg.Rows = payload["cols"], payload["rows"]
	return msg, nil
}

// dataMessage는 Schema 검증을 통과한 Connector → Relay Terminal Data JSON control message다.
type dataMessage struct {
	Type              string
	MessageID         string
	TerminalSessionID string
	LabInstanceID     string
	// Generation은 Schema를 만족하지만 int64로 표현할 수 없으면 0이다. 어떤 TerminalSession과도 맞지 않는다.
	Generation int64
	// RuntimeID는 TERMINAL_DATA_ATTACH에서 채운다.
	RuntimeID string
	// Reason과 ExitCode는 TERMINAL_DATA_ENDED에서 채운다. ExitCode는 알려 주지 않았거나 표현할 수 없으면 nil이다.
	Reason   string
	ExitCode *int64
	// ErrorCode는 ERROR에서 채운다.
	ErrorCode string
	// Trace는 message의 유효한 Trace Context다. 없거나 유효하지 않으면 zero value다. errMalformed여도 읽을 수 있으면 채운다.
	Trace tracecontext.Context
}

// decodeDataMessage는 Connector가 보낼 수 있는 TERMINAL_DATA_ATTACH, TERMINAL_DATA_ENDED, ERROR를 검증한다.
// 그 외 type은 errUnsupportedType이고, Schema를 만족하지 않으면 errMalformed다.
// errMalformed여도 읽을 수 있었던 correlation(terminalSessionId, labInstanceId, generation)은 반환한다. 호출자는 그 값을 ERROR에
// 되돌려 줄 수 있는지만 판단하며 업무 처리에는 쓰지 않는다.
func decodeDataMessage(data []byte) (dataMessage, error) {
	members, ok := jsonObject(data)
	if !ok {
		return dataMessage{}, errMalformed
	}
	kind, ok := jsonString(members["type"])
	if !ok {
		return dataMessage{}, errMalformed
	}
	if kind != typeDataAttach && kind != typeDataEnded && kind != typeError {
		return dataMessage{}, errUnsupportedType
	}
	msg := dataMessage{Type: kind, Trace: traceOf(members)}

	// terminal-data.schema.json의 BaseEnvelope는 세 message 모두 terminalSessionId, labInstanceId, generation을 required로 둔다.
	correlated := true
	msg.TerminalSessionID, ok = nonEmptyString(members["terminalSessionId"])
	correlated = correlated && ok
	msg.LabInstanceID, ok = nonEmptyString(members["labInstanceId"])
	correlated = correlated && ok
	if jsonnum.PositiveInteger(members["generation"]) {
		// Schema에 maximum이 없으므로 int64를 넘는 값도 Schema 위반이 아니다. 그 값은 어떤 TerminalSession의 generation과도 맞지 않는다.
		msg.Generation, _, _ = jsonnum.Int64(members["generation"])
	} else {
		correlated = false
	}
	if !correlated {
		// 읽을 수 없는 값은 비운다. 일부만 읽혔어도 호출자는 세 값이 모두 있을 때만 사용한다.
		return msg, errMalformed
	}

	messageID, payload, ok := envelope(members, dataOptionalIDs)
	if !ok {
		return msg, errMalformed
	}
	msg.MessageID = messageID

	switch kind {
	case typeDataAttach:
		runtimeID, ok := nonEmptyString(payload["runtimeId"])
		if !ok {
			return msg, errMalformed
		}
		msg.RuntimeID = runtimeID
	case typeDataEnded:
		reason, ok := nonEmptyString(payload["reason"])
		if !ok {
			return msg, errMalformed
		}
		msg.Reason = reason
		if raw, present := payload["exitCode"]; present {
			value, valid, inRange := jsonnum.Int64(raw)
			if !valid {
				return msg, errMalformed
			}
			if inRange {
				msg.ExitCode = &value
			}
		}
		if raw, present := payload["error"]; present && !validSafeError(raw) {
			return msg, errMalformed
		}
	default: // typeError
		code, ok := nonEmptyString(payload["code"])
		if !ok || !optionalString(payload, "message") || !optionalBool(payload, "fatal") {
			return msg, errMalformed
		}
		msg.ErrorCode = code
	}
	return msg, nil
}

// optionalString은 member가 없으면 true, 있으면 JSON string일 때만 true다.
func optionalString(members map[string]json.RawMessage, name string) bool {
	raw, present := members[name]
	if !present {
		return true
	}
	_, ok := jsonString(raw)
	return ok
}

// optionalBool은 member가 없으면 true, 있으면 JSON boolean일 때만 true다. null은 boolean이 아니다.
func optionalBool(members map[string]json.RawMessage, name string) bool {
	raw, present := members[name]
	if !present {
		return true
	}
	var b bool
	return string(raw) != "null" && json.Unmarshal(raw, &b) == nil
}

// validSafeError는 SafeError(code: 비어 있지 않은 string required, message: string optional)의 Schema 제약을 확인한다.
func validSafeError(raw json.RawMessage) bool {
	obj, ok := jsonObject(raw)
	if !ok {
		return false
	}
	_, ok = nonEmptyString(obj["code"])
	return ok && optionalString(obj, "message")
}

// 아래는 Relay가 만드는 message다. terminalSessionId 같은 correlation은 Relay가 이미 알고 있는 값만 싣는다.

type outEnvelope struct {
	Type              string    `json:"type"`
	MessageID         string    `json:"messageId"`
	SentAt            time.Time `json:"sentAt"`
	ReplyToMessageID  string    `json:"replyToMessageId,omitempty"`
	TerminalSessionID string    `json:"terminalSessionId,omitempty"`
	LabInstanceID     string    `json:"labInstanceId,omitempty"`
	Generation        int64     `json:"generation,omitempty"`
	// Traceparent와 Tracestate는 control event의 W3C Trace Context다. PTY Binary frame에는 붙이지 않는다(Binary는 이 envelope를 쓰지 않는다).
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
	Payload     any    `json:"payload"`
}

// noTrace는 전파할 Trace Context가 없는 control message다. 가짜 Trace를 만들지 않는다.
var noTrace tracecontext.Context

// marshalOut은 e를 JSON으로 만든다. t가 유효할 때만 traceparent/tracestate를 싣는다. 호출자가 넘긴 값이라도 표준 parser로 다시
// 정상화하므로 유효하지 않은 값이 wire에 나가지 않는다.
func marshalOut(e outEnvelope, t tracecontext.Context) []byte {
	t = tracecontext.Normalize(t.Traceparent, t.Tracestate)
	e.Traceparent, e.Tracestate = t.Traceparent, t.Tracestate
	e.MessageID = uuid.NewString()
	e.SentAt = time.Now().UTC()
	data, err := json.Marshal(e)
	if err != nil {
		// 고정된 구조와 이미 검증된 값만 담으므로 발생하지 않는다.
		panic("realtime: message 직렬화 실패")
	}
	return data
}

// errorPayload는 Browser와 Connector 양쪽 ERROR의 payload다. message에는 고정된 설명만 싣는다.
type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Fatal   bool   `json:"fatal,omitempty"`
}

// 아래 builder의 trace는 그 message가 응답하거나 이어 가는 control event의 Trace Context다. 관계가 없으면 noTrace다.

func browserError(code, message string, fatal bool, replyTo string, trace tracecontext.Context) []byte {
	return marshalOut(outEnvelope{
		Type: typeError, ReplyToMessageID: replyTo,
		Payload: errorPayload{Code: code, Message: message, Fatal: fatal},
	}, trace)
}

func browserAttached(sessionID, replyTo string, resumed bool, trace tracecontext.Context) []byte {
	return marshalOut(outEnvelope{
		Type: typeTerminalAttached, TerminalSessionID: sessionID, ReplyToMessageID: replyTo,
		Payload: struct {
			Resumed          bool `json:"resumed"`
			HistoryAvailable bool `json:"historyAvailable"`
		}{Resumed: resumed, HistoryAvailable: false},
	}, trace)
}

// browserEnded는 종료 원인(end)의 Trace Context를 싣는다.
func browserEnded(sessionID string, end End) []byte {
	return marshalOut(outEnvelope{
		Type: typeTerminalSessionEnded, TerminalSessionID: sessionID,
		Payload: struct {
			Reason   string `json:"reason"`
			ExitCode *int64 `json:"exitCode,omitempty"`
		}{Reason: end.Reason, ExitCode: end.ExitCode},
	}, end.Trace)
}

// correlation은 Terminal Data message가 공통으로 싣는 TerminalSession 식별자다.
type correlation struct {
	TerminalSessionID string
	LabInstanceID     string
	Generation        int64
}

func (c correlation) envelope(kind, replyTo string, payload any, trace tracecontext.Context) []byte {
	return marshalOut(outEnvelope{
		Type: kind, ReplyToMessageID: replyTo,
		TerminalSessionID: c.TerminalSessionID, LabInstanceID: c.LabInstanceID, Generation: c.Generation,
		Payload: payload,
	}, trace)
}

func dataAttached(c correlation, replyTo string, resumed bool, trace tracecontext.Context) []byte {
	return c.envelope(typeDataAttached, replyTo, struct {
		Resumed          bool `json:"resumed"`
		HistoryAvailable bool `json:"historyAvailable"`
	}{Resumed: resumed, HistoryAvailable: false}, trace)
}

// dataResize의 cols/rows는 검증된 JSON number 원문을 그대로 전달한다. 값을 다시 만들지 않으므로 Schema에 없는 상한이 생기지 않는다.
func dataResize(c correlation, cols, rows json.RawMessage, trace tracecontext.Context) []byte {
	return c.envelope(typeDataResize, "", struct {
		Cols json.RawMessage `json:"cols"`
		Rows json.RawMessage `json:"rows"`
	}{Cols: cols, Rows: rows}, trace)
}

func dataClose(c correlation, reason string, trace tracecontext.Context) []byte {
	return c.envelope(typeDataClose, "", struct {
		Reason string `json:"reason"`
	}{Reason: reason}, trace)
}

func dataError(c correlation, code, message string, fatal bool, replyTo string, trace tracecontext.Context) []byte {
	return c.envelope(typeError, replyTo, errorPayload{Code: code, Message: message, Fatal: fatal}, trace)
}
