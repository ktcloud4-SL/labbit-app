package filetransport

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/server/jsonnum"
	"github.com/ktcloud4-SL/labbit-app/internal/server/workspacefile"
)

// 이 file은 contracts/connector/file-data.schema.json의 JSON control frame이다. inbound(Connector → SaaS) 검증은 Schema를 그대로
// 옮긴 것이며 Schema보다 엄격하거나 느슨하지 않다. Schema의 property 이름은 대소문자를 구분하므로 판단에 쓰는 값은 모두 정확한 이름으로
// 조회한 RawMessage에서 직접 decode한다. 정의되지 않은 field와 traceparent/tracestate는 검증하지 않고 무시한다.

// Data WSS message type이다(file-data.schema.json).
const (
	typeAttach     = "FILE_DATA_ATTACH"
	typeAttached   = "FILE_DATA_ATTACHED"
	typeTree       = "FILE_TREE"
	typeRead       = "FILE_READ"
	typeSave       = "FILE_SAVE"
	typeTreeResult = "FILE_TREE_RESULT"
	typeReadResult = "FILE_READ_RESULT"
	typeSaveResult = "FILE_SAVE_RESULT"
	typeError      = "ERROR"
)

// Connector가 FAILED 결과의 error.code에 쓰는 값이다(file-data.schema.json의 SafeError.code 설명).
const (
	codeNotFound         = "NOT_FOUND"
	codeNotAFile         = "NOT_A_FILE"
	codeNotADirectory    = "NOT_A_DIRECTORY"
	codeTooLarge         = "TOO_LARGE"
	codeRevisionConflict = "REVISION_CONFLICT"
	codePermissionDenied = "PERMISSION_DENIED"
	codeInvalidPath      = "INVALID_PATH"
)

const (
	outcomeSucceeded = "SUCCEEDED"
	outcomeFailed    = "FAILED"
)

// revisionPattern은 file-data.schema.json의 Revision pattern이다.
var revisionPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// outboundEnvelope는 SaaS → Connector frame의 BaseEnvelope다.
type outboundEnvelope struct {
	Type             string    `json:"type"`
	MessageID        string    `json:"messageId"`
	SentAt           time.Time `json:"sentAt"`
	ReplyToMessageID string    `json:"replyToMessageId,omitempty"`
	FileRequestID    string    `json:"fileRequestId"`
	LabInstanceID    string    `json:"labInstanceId"`
	Generation       int64     `json:"generation"`
	Payload          any       `json:"payload"`
}

type (
	attachedPayload struct{}
	treePayload     struct {
		Path string `json:"path"`
	}
	readPayload struct {
		Path     string `json:"path"`
		MaxBytes int64  `json:"maxBytes"`
	}
	savePayload struct {
		Path             string `json:"path"`
		ExpectedRevision string `json:"expectedRevision"`
		Size             int64  `json:"size"`
	}
)

// inboundEnvelope는 Schema 검증을 통과한 Connector → SaaS frame의 공통 부분이다.
type inboundEnvelope struct {
	Type             string
	MessageID        string
	ReplyToMessageID string
	FileRequestID    string
	LabInstanceID    string
	// Generation은 1 이상이다. int64로 표현할 수 없는 Schema-valid 값은 0이며 어떤 요청과도 맞지 않는다.
	Generation int64
	Payload    map[string]json.RawMessage
}

// parseEnvelope는 BaseEnvelope의 required 조건(type, messageId, sentAt, fileRequestId, labInstanceId, generation, payload)과 선택 field
// 제약을 확인한다. 하나라도 어긋나면 ok가 false다.
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
		if env.ReplyToMessageID, ok = jsonString(raw); !ok || env.ReplyToMessageID == "" {
			return inboundEnvelope{}, false
		}
	}
	if env.FileRequestID, ok = jsonString(members["fileRequestId"]); !ok || env.FileRequestID == "" {
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

// attachInfo는 FILE_DATA_ATTACH의 payload다.
type attachInfo struct {
	RuntimeID        string
	TargetVMKey      string
	ProviderServerID string
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
	return info, true
}

// resultOutcome은 *_RESULT payload의 공통 부분이다. FAILED이면 Error code가 있다(Schema의 else: required error).
type resultOutcome struct {
	Succeeded bool
	// ErrorCode는 FAILED일 때 Connector가 보낸 error.code다. 값은 길이가 제한되지 않은 원문이므로 log에 남기지 않는다.
	ErrorCode string
}

func decodeOutcome(payload map[string]json.RawMessage) (resultOutcome, bool) {
	outcome, ok := jsonString(payload["outcome"])
	if !ok {
		return resultOutcome{}, false
	}
	switch outcome {
	case outcomeSucceeded:
		return resultOutcome{Succeeded: true}, true
	case outcomeFailed:
		obj, ok := jsonObject(payload["error"])
		if !ok {
			return resultOutcome{}, false
		}
		code, ok := jsonString(obj["code"])
		if !ok || code == "" {
			return resultOutcome{}, false
		}
		if raw, present := obj["message"]; present {
			if _, ok := jsonString(raw); !ok {
				return resultOutcome{}, false
			}
		}
		return resultOutcome{ErrorCode: code}, true
	default:
		return resultOutcome{}, false
	}
}

// decodeTreeEntries는 SUCCEEDED FILE_TREE_RESULT의 entries(required array, 원소는 name: minLength 1, kind: file|directory)를 읽는다.
func decodeTreeEntries(payload map[string]json.RawMessage) ([]workspacefile.Entry, bool) {
	raw, present := payload["entries"]
	if !present {
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, false
	}
	entries := make([]workspacefile.Entry, 0, len(items))
	for _, item := range items {
		obj, ok := jsonObject(item)
		if !ok {
			return nil, false
		}
		name, ok := jsonString(obj["name"])
		if !ok || name == "" {
			return nil, false
		}
		kind, ok := jsonString(obj["kind"])
		if !ok || (kind != string(workspacefile.KindFile) && kind != string(workspacefile.KindDirectory)) {
			return nil, false
		}
		entries = append(entries, workspacefile.Entry{Name: name, Kind: workspacefile.EntryKind(kind)})
	}
	return entries, true
}

// decodeRevision은 SUCCEEDED 결과의 revision(required, Revision pattern)을 읽는다.
func decodeRevision(payload map[string]json.RawMessage) (workspacefile.Revision, bool) {
	revision, ok := jsonString(payload["revision"])
	if !ok || !revisionPattern.MatchString(revision) {
		return "", false
	}
	return workspacefile.Revision(revision), true
}

// decodeReadSize는 SUCCEEDED FILE_READ_RESULT의 size(required, integer, minimum 0)를 읽는다. Schema에 maximum이 없으므로
// int64를 넘는 값도 유효하며 그 경우 inRange가 false다(어떤 한도보다도 크다).
func decodeReadSize(payload map[string]json.RawMessage) (size int64, inRange, ok bool) {
	raw := payload["size"]
	value, valid, inRange := jsonnum.Int64(raw)
	if !valid {
		return 0, false, false
	}
	if inRange {
		return value, true, value >= 0
	}
	// int64를 넘는 정수다. 음수는 minimum 0 위반이다.
	return 0, false, len(raw) > 0 && raw[0] != '-'
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
