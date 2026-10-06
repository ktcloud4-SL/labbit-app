package preview

import (
	"encoding/json"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
)

// 이 file은 contracts/connector/preview-data.schema.json의 JSON control frame이다. inbound(Connector → SaaS) 검증은 Schema를 그대로
// 옮긴 것이며 Schema보다 엄격하거나 느슨하지 않다. Schema의 property 이름은 대소문자를 구분하므로 판단에 쓰는 값은 모두 정확한 이름으로
// 조회한 RawMessage에서 직접 decode한다. 정의되지 않은 field와 traceparent/tracestate는 검증하지 않고 무시한다.

// Data WSS message type이다(preview-data.schema.json).
const (
	typeAttach   = "PREVIEW_ATTACH"
	typeAttached = "PREVIEW_ATTACHED"
)

// outboundEnvelope는 SaaS → Connector frame의 BaseEnvelope다.
type outboundEnvelope struct {
	Type             string    `json:"type"`
	MessageID        string    `json:"messageId"`
	SentAt           time.Time `json:"sentAt"`
	ReplyToMessageID string    `json:"replyToMessageId,omitempty"`
	PreviewSessionID string    `json:"previewSessionId"`
	LabInstanceID    string    `json:"labInstanceId"`
	Generation       int64     `json:"generation"`
	Payload          any       `json:"payload"`
}

type attachedPayload struct{}

// inboundEnvelope는 Schema 검증을 통과한 Connector → SaaS frame의 공통 부분이다.
type inboundEnvelope struct {
	Type             string
	MessageID        string
	PreviewSessionID string
	LabInstanceID    string
	// Generation은 1 이상이다. int64로 표현할 수 없는 Schema-valid 값은 0이며 어떤 PreviewSession과도 맞지 않는다.
	Generation int64
	Payload    map[string]json.RawMessage
}

// parseEnvelope는 BaseEnvelope의 required 조건(type, messageId, sentAt, previewSessionId, labInstanceId, generation, payload)과 선택 field
// 제약(replyToMessageId)을 확인한다. 하나라도 어긋나면 ok가 false다.
func parseEnvelope(data []byte) (inboundEnvelope, bool) {
	members, ok := jsonObject(data)
	if !ok {
		return inboundEnvelope{}, false
	}
	var env inboundEnvelope
	if env.Type, ok = jsonString(members["type"]); !ok {
		return inboundEnvelope{}, false
	}
	if env.MessageID, ok = jsonString(members["messageId"]); !ok || env.MessageID == "" {
		return inboundEnvelope{}, false
	}
	if !validTimestamp(members["sentAt"]) {
		return inboundEnvelope{}, false
	}
	if raw, present := members["replyToMessageId"]; present {
		if s, ok := jsonString(raw); !ok || s == "" {
			return inboundEnvelope{}, false
		}
	}
	if env.PreviewSessionID, ok = jsonString(members["previewSessionId"]); !ok || env.PreviewSessionID == "" {
		return inboundEnvelope{}, false
	}
	if env.LabInstanceID, ok = jsonString(members["labInstanceId"]); !ok || env.LabInstanceID == "" {
		return inboundEnvelope{}, false
	}
	if !jsonnum.PositiveInteger(members["generation"]) {
		return inboundEnvelope{}, false
	}
	if value, _, inRange := jsonnum.Int64(members["generation"]); inRange {
		env.Generation = value
	}
	if env.Payload, ok = jsonObject(members["payload"]); !ok {
		return inboundEnvelope{}, false
	}
	return env, true
}

// attachInfo는 PREVIEW_ATTACH의 payload다.
type attachInfo struct {
	RuntimeID        string
	TargetVMKey      string
	ProviderServerID string
	// TargetPort는 1~65535다. 범위를 벗어나면 decodeAttach가 거절한다.
	TargetPort int
}

func decodeAttach(payload map[string]json.RawMessage) (attachInfo, bool) {
	var info attachInfo
	var ok bool
	if info.RuntimeID, ok = jsonString(payload["runtimeId"]); !ok || info.RuntimeID == "" {
		return attachInfo{}, false
	}
	if info.TargetVMKey, ok = jsonString(payload["targetVmKey"]); !ok || info.TargetVMKey == "" {
		return attachInfo{}, false
	}
	if info.ProviderServerID, ok = jsonString(payload["providerServerId"]); !ok || info.ProviderServerID == "" {
		return attachInfo{}, false
	}
	// Schema: targetPort는 required integer, minimum 1, maximum 65535다.
	raw := payload["targetPort"]
	if !jsonnum.PositiveInteger(raw) {
		return attachInfo{}, false
	}
	port, _, inRange := jsonnum.Int64(raw)
	if !inRange || port < 1 || port > 65535 {
		return attachInfo{}, false
	}
	info.TargetPort = int(port)
	return info, true
}

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

// validTimestamp는 raw가 RFC 3339 date-time string이고 zero time이 아닐 때만 true다.
func validTimestamp(raw json.RawMessage) bool {
	var t time.Time
	return json.Unmarshal(raw, &t) == nil && !t.IsZero()
}
