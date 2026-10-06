package realtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

// wireCase는 Schema의 한 message 정의에 대한 검증 기대값이다.
type wireCase struct {
	name string
	// schema는 terminal-live.schema.json 또는 terminal-data.schema.json의 $defs 이름이다.
	schema string
	json   string
	// valid는 그 Schema 정의를 만족하는지다. 디코더의 판정이 이와 같아야 한다.
	valid bool
}

const (
	browserAttachDef = "TerminalAttachMessage"
	browserResizeDef = "TerminalResizeMessage"
	dataAttachDef    = "TerminalDataAttachMessage"
	dataEndedDef     = "TerminalDataEndedMessage"
	dataErrorDef     = "ErrorMessage"
)

// defTypes는 Schema 정의가 요구하는 type const다.
var defTypes = map[string]string{
	browserAttachDef: "TERMINAL_ATTACH",
	browserResizeDef: "TERMINAL_RESIZE",
	dataAttachDef:    "TERMINAL_DATA_ATTACH",
	dataEndedDef:     "TERMINAL_DATA_ENDED",
	dataErrorDef:     "ERROR",
}

const (
	attachBase = `{"type":"TERMINAL_ATTACH","messageId":"m1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"s1","payload":{"sessionToken":"t","cols":80,"rows":24}}`
	resizeBase = `{"type":"TERMINAL_RESIZE","messageId":"m1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"s1","payload":{"cols":80,"rows":24}}`
	dataHeader = `"messageId":"m1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"s1","labInstanceId":"l1","generation":1`
)

func dataAttachJSON(rest string) string {
	return `{"type":"TERMINAL_DATA_ATTACH",` + dataHeader + rest + `}`
}

func dataEndedJSON(payload string) string {
	return `{"type":"TERMINAL_DATA_ENDED",` + dataHeader + `,"payload":` + payload + `}`
}

func dataErrorJSON(payload string) string {
	return `{"type":"ERROR",` + dataHeader + `,"payload":` + payload + `}`
}

// wireCorpus는 accept/reject 경계 사례다. 같은 표를 Python jsonschema로 Schema와 대조해 디코더가 Schema보다 엄격하거나 느슨하지 않음을 확인한다.
var wireCorpus = []wireCase{
	// --- Browser TERMINAL_ATTACH ---
	{"attach: base", browserAttachDef, attachBase, true},
	{"attach: unknown envelope property is ignored", browserAttachDef, strings.Replace(attachBase, `"messageId"`, `"extra":{"a":1},"messageId"`, 1), true},
	{"attach: unknown payload property is allowed", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":80,"extra":true`, 1), true},
	{"attach: optional ids present", browserAttachDef, strings.Replace(attachBase, `"messageId"`, `"replyToMessageId":"r","requestId":"q","liveSessionId":"l","messageId"`, 1), true},
	{"attach: cols 1.0 and rows 1e2 are integers", browserAttachDef, strings.Replace(strings.Replace(attachBase, `"cols":80`, `"cols":1.0`, 1), `"rows":24`, `"rows":1e2`, 1), true},
	{"attach: cols 1e30 is an integer without maximum", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":1e30`, 1), true},
	{"attach: cols 100E-2 is 1", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":100E-2`, 1), true},
	{"attach: sentAt with fraction and offset", browserAttachDef, strings.Replace(attachBase, `2026-10-01T09:00:00Z`, `2026-10-01T18:00:00.123456+09:00`, 1), true},
	{"attach: type wrong case", browserAttachDef, strings.Replace(attachBase, `TERMINAL_ATTACH`, `terminal_attach`, 1), false},
	{"attach: messageId missing", browserAttachDef, strings.Replace(attachBase, `"messageId":"m1",`, ``, 1), false},
	{"attach: messageId empty", browserAttachDef, strings.Replace(attachBase, `"m1"`, `""`, 1), false},
	{"attach: messageId not a string", browserAttachDef, strings.Replace(attachBase, `"m1"`, `1`, 1), false},
	{"attach: sentAt missing", browserAttachDef, strings.Replace(attachBase, `"sentAt":"2026-10-01T09:00:00Z",`, ``, 1), false},
	{"attach: sentAt null", browserAttachDef, strings.Replace(attachBase, `"2026-10-01T09:00:00Z"`, `null`, 1), false},
	{"attach: sentAt not a date-time", browserAttachDef, strings.Replace(attachBase, `2026-10-01T09:00:00Z`, `yesterday`, 1), false},
	{"attach: terminalSessionId missing", browserAttachDef, strings.Replace(attachBase, `"terminalSessionId":"s1",`, ``, 1), false},
	{"attach: terminalSessionId empty", browserAttachDef, strings.Replace(attachBase, `"s1"`, `""`, 1), false},
	{"attach: replyToMessageId empty", browserAttachDef, strings.Replace(attachBase, `"messageId"`, `"replyToMessageId":"","messageId"`, 1), false},
	{"attach: requestId empty", browserAttachDef, strings.Replace(attachBase, `"messageId"`, `"requestId":"","messageId"`, 1), false},
	{"attach: liveSessionId empty", browserAttachDef, strings.Replace(attachBase, `"messageId"`, `"liveSessionId":"","messageId"`, 1), false},
	{"attach: payload missing", browserAttachDef, `{"type":"TERMINAL_ATTACH","messageId":"m1","sentAt":"2026-10-01T09:00:00Z","terminalSessionId":"s1"}`, false},
	{"attach: payload not an object", browserAttachDef, strings.Replace(attachBase, `{"sessionToken":"t","cols":80,"rows":24}`, `"x"`, 1), false},
	{"attach: sessionToken missing", browserAttachDef, strings.Replace(attachBase, `"sessionToken":"t",`, ``, 1), false},
	{"attach: sessionToken empty", browserAttachDef, strings.Replace(attachBase, `"t"`, `""`, 1), false},
	{"attach: sessionToken not a string", browserAttachDef, strings.Replace(attachBase, `"t"`, `5`, 1), false},
	{"attach: cols missing", browserAttachDef, strings.Replace(attachBase, `"cols":80,`, ``, 1), false},
	{"attach: rows missing", browserAttachDef, strings.Replace(attachBase, `,"rows":24`, ``, 1), false},
	{"attach: cols zero", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":0`, 1), false},
	{"attach: rows negative", browserAttachDef, strings.Replace(attachBase, `"rows":24`, `"rows":-1`, 1), false},
	{"attach: cols fraction", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":1.5`, 1), false},
	{"attach: cols 1e-1", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":1e-1`, 1), false},
	{"attach: cols string", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":"80"`, 1), false},
	{"attach: cols null", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":null`, 1), false},
	{"attach: cols bool", browserAttachDef, strings.Replace(attachBase, `"cols":80`, `"cols":true`, 1), false},

	// --- Browser TERMINAL_RESIZE ---
	{"resize: base", browserResizeDef, resizeBase, true},
	{"resize: cols 1.0 and rows 1e2", browserResizeDef, strings.Replace(strings.Replace(resizeBase, `"cols":80`, `"cols":1.0`, 1), `"rows":24`, `"rows":1e2`, 1), true},
	{"resize: unknown envelope property is ignored", browserResizeDef, strings.Replace(resizeBase, `"messageId"`, `"extra":1,"messageId"`, 1), true},
	{"resize: unknown payload property is rejected", browserResizeDef, strings.Replace(resizeBase, `"cols":80`, `"cols":80,"extra":1`, 1), false},
	{"resize: terminalSessionId missing", browserResizeDef, strings.Replace(resizeBase, `"terminalSessionId":"s1",`, ``, 1), false},
	{"resize: terminalSessionId empty", browserResizeDef, strings.Replace(resizeBase, `"s1"`, `""`, 1), false},
	{"resize: cols zero", browserResizeDef, strings.Replace(resizeBase, `"cols":80`, `"cols":0`, 1), false},
	{"resize: rows string", browserResizeDef, strings.Replace(resizeBase, `"rows":24`, `"rows":"24"`, 1), false},
	{"resize: rows missing", browserResizeDef, strings.Replace(resizeBase, `,"rows":24`, ``, 1), false},
	{"resize: payload empty", browserResizeDef, strings.Replace(resizeBase, `{"cols":80,"rows":24}`, `{}`, 1), false},

	// --- Connector TERMINAL_DATA_ATTACH ---
	{"data attach: base", dataAttachDef, dataAttachJSON(`,"payload":{"runtimeId":"r"}`), true},
	{"data attach: unknown payload property", dataAttachDef, dataAttachJSON(`,"payload":{"runtimeId":"r","extra":[1]}`), true},
	{"data attach: requestId and liveSessionId are not constrained by the data schema", dataAttachDef, dataAttachJSON(`,"requestId":"","liveSessionId":7,"payload":{"runtimeId":"r"}`), true},
	{"data attach: replyToMessageId present", dataAttachDef, dataAttachJSON(`,"replyToMessageId":"x","payload":{"runtimeId":"r"}`), true},
	{"data attach: replyToMessageId empty", dataAttachDef, dataAttachJSON(`,"replyToMessageId":"","payload":{"runtimeId":"r"}`), false},
	{"data attach: generation 1.0", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":1.0`, 1), true},
	{"data attach: generation 1e0", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":1e0`, 1), true},
	{"data attach: generation beyond int64", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":1e30`, 1), true},
	{"data attach: generation zero", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":0`, 1), false},
	{"data attach: generation string", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":"1"`, 1), false},
	{"data attach: generation missing", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `,"generation":1`, ``, 1), false},
	{"data attach: labInstanceId missing", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"labInstanceId":"l1",`, ``, 1), false},
	{"data attach: labInstanceId empty", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"l1"`, `""`, 1), false},
	{"data attach: terminalSessionId missing", dataAttachDef, strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"terminalSessionId":"s1",`, ``, 1), false},
	{"data attach: runtimeId missing", dataAttachDef, dataAttachJSON(`,"payload":{}`), false},
	{"data attach: runtimeId empty", dataAttachDef, dataAttachJSON(`,"payload":{"runtimeId":""}`), false},
	{"data attach: runtimeId not a string", dataAttachDef, dataAttachJSON(`,"payload":{"runtimeId":1}`), false},
	{"data attach: payload missing", dataAttachDef, dataAttachJSON(``), false},

	// --- Connector TERMINAL_DATA_ENDED ---
	{"ended: reason only", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED"}`), true},
	{"ended: exit code", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":7}`), true},
	{"ended: negative exit code", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":-1}`), true},
	{"ended: exit code 1.0", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":1.0}`), true},
	{"ended: exit code beyond int64", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":1e30}`), true},
	{"ended: error", dataEndedDef, dataEndedJSON(`{"reason":"SSH_DISCONNECTED","error":{"code":"SSH_LOST","message":"gone"}}`), true},
	{"ended: error without message", dataEndedDef, dataEndedJSON(`{"reason":"SSH_DISCONNECTED","error":{"code":"SSH_LOST"}}`), true},
	{"ended: error with extra property", dataEndedDef, dataEndedJSON(`{"reason":"SSH_DISCONNECTED","error":{"code":"SSH_LOST","extra":1}}`), true},
	{"ended: unknown payload property", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","extra":1}`), true},
	{"ended: reason missing", dataEndedDef, dataEndedJSON(`{}`), false},
	{"ended: reason empty", dataEndedDef, dataEndedJSON(`{"reason":""}`), false},
	{"ended: reason not a string", dataEndedDef, dataEndedJSON(`{"reason":3}`), false},
	{"ended: exit code fraction", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":1.5}`), false},
	{"ended: exit code string", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":"1"}`), false},
	{"ended: exit code null", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":null}`), false},
	{"ended: error not an object", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","error":"x"}`), false},
	{"ended: error without code", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","error":{}}`), false},
	{"ended: error code empty", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","error":{"code":""}}`), false},
	{"ended: error message not a string", dataEndedDef, dataEndedJSON(`{"reason":"PTY_EXITED","error":{"code":"X","message":5}}`), false},
	{"ended: payload empty string", dataEndedDef, dataEndedJSON(`"x"`), false},

	// --- Connector ERROR ---
	{"error: code only", dataErrorDef, dataErrorJSON(`{"code":"PROTOCOL_ERROR"}`), true},
	{"error: code message fatal", dataErrorDef, dataErrorJSON(`{"code":"PROTOCOL_ERROR","message":"x","fatal":true}`), true},
	{"error: code missing", dataErrorDef, dataErrorJSON(`{}`), false},
	{"error: code empty", dataErrorDef, dataErrorJSON(`{"code":""}`), false},
	{"error: message not a string", dataErrorDef, dataErrorJSON(`{"code":"X","message":1}`), false},
	{"error: fatal not a boolean", dataErrorDef, dataErrorJSON(`{"code":"X","fatal":"yes"}`), false},
	{"error: fatal null", dataErrorDef, dataErrorJSON(`{"code":"X","fatal":null}`), false},
}

// decoderAccepts는 message를 해당 Schema 정의에 대응하는 디코더로 판정한다. 디코더는 type으로 분기하므로 type이 정의의 const와
// 다르면 그 정의를 만족하지 않는 것으로 본다.
func decoderAccepts(schema, raw string) bool {
	want := defTypes[schema]
	switch schema {
	case browserAttachDef, browserResizeDef:
		msg, err := decodeBrowserMessage([]byte(raw))
		return err == nil && msg.Type == want
	default:
		msg, err := decodeDataMessage([]byte(raw))
		return err == nil && msg.Type == want
	}
}

func TestDecodersMatchSchemaCorpus(t *testing.T) {
	for _, tt := range wireCorpus {
		t.Run(tt.name, func(t *testing.T) {
			if got := decoderAccepts(tt.schema, tt.json); got != tt.valid {
				t.Fatalf("디코더 판정 = %v, Schema 기대 = %v\n%s", got, tt.valid, tt.json)
			}
		})
	}
}

func TestDecodeBrowserMessageValues(t *testing.T) {
	msg, err := decodeBrowserMessage([]byte(strings.Replace(attachBase, `"cols":80`, `"cols":1.0`, 1)))
	if err != nil {
		t.Fatalf("decodeBrowserMessage() error = %v", err)
	}
	if msg.Type != "TERMINAL_ATTACH" || msg.MessageID != "m1" || msg.TerminalSessionID != "s1" || msg.Token != "t" {
		t.Fatalf("msg = %+v", msg)
	}
	// cols/rows는 값을 바꾸지 않은 원문이다.
	if string(msg.Cols) != "1.0" || string(msg.Rows) != "24" {
		t.Fatalf("Cols/Rows = %s/%s, want raw 1.0/24", msg.Cols, msg.Rows)
	}

	// Browser가 보낼 수 없는 type이다.
	for _, typ := range []string{"TERMINAL_ATTACHED", "TERMINAL_SESSION_ENDED", "LIVE_SUBSCRIBE", "ERROR", "SOMETHING"} {
		_, err := decodeBrowserMessage([]byte(strings.Replace(attachBase, "TERMINAL_ATTACH", typ, 1)))
		if !errors.Is(err, errUnsupportedType) {
			t.Fatalf("type %s: error = %v, want errUnsupportedType", typ, err)
		}
	}
	for _, raw := range []string{``, `null`, `[]`, `"x"`, `{}`, `{"type":1}`, `{"type":null}`} {
		if _, err := decodeBrowserMessage([]byte(raw)); !errors.Is(err, errMalformed) {
			t.Fatalf("%q: error = %v, want errMalformed", raw, err)
		}
	}
}

func TestDecodeDataMessageValues(t *testing.T) {
	msg, err := decodeDataMessage([]byte(dataEndedJSON(`{"reason":"PTY_EXITED","exitCode":7}`)))
	if err != nil {
		t.Fatalf("decodeDataMessage() error = %v", err)
	}
	if msg.Type != "TERMINAL_DATA_ENDED" || msg.TerminalSessionID != "s1" || msg.LabInstanceID != "l1" || msg.Generation != 1 ||
		msg.Reason != "PTY_EXITED" || msg.ExitCode == nil || *msg.ExitCode != 7 {
		t.Fatalf("msg = %+v", msg)
	}

	// 알 수 없거나 표현할 수 없는 exit code를 0으로 만들지 않는다.
	for _, payload := range []string{`{"reason":"PTY_EXITED"}`, `{"reason":"PTY_EXITED","exitCode":1e30}`} {
		msg, err := decodeDataMessage([]byte(dataEndedJSON(payload)))
		if err != nil || msg.ExitCode != nil {
			t.Fatalf("%s: ExitCode = %v, error = %v, want nil", payload, msg.ExitCode, err)
		}
	}

	// generation이 int64를 넘는 것은 Schema 위반이 아니다. 어떤 TerminalSession과도 맞지 않도록 0으로 표현한다.
	msg, err = decodeDataMessage([]byte(strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":1e30`, 1)))
	if err != nil || msg.Generation != 0 {
		t.Fatalf("generation = %d, error = %v, want 0 and no error", msg.Generation, err)
	}

	// Schema 위반이어도 읽을 수 있었던 correlation은 돌려준다(ERROR에 되돌려 줄 수 있는지 판단하기 위해서다).
	msg, err = decodeDataMessage([]byte(dataAttachJSON(`,"payload":{}`)))
	if !errors.Is(err, errMalformed) || msg.TerminalSessionID != "s1" || msg.LabInstanceID != "l1" || msg.Generation != 1 {
		t.Fatalf("msg = %+v, error = %v, want correlation with errMalformed", msg, err)
	}
	msg, err = decodeDataMessage([]byte(strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), `"generation":1`, `"generation":0`, 1)))
	if !errors.Is(err, errMalformed) || msg.Generation != 0 {
		t.Fatalf("generation 0: msg = %+v, error = %v", msg, err)
	}

	// Connector가 보낼 일이 없는 type이다.
	for _, typ := range []string{"TERMINAL_DATA_ATTACHED", "TERMINAL_DATA_RESIZE", "TERMINAL_DATA_CLOSE", "SOMETHING"} {
		_, err := decodeDataMessage([]byte(strings.Replace(dataAttachJSON(`,"payload":{"runtimeId":"r"}`), "TERMINAL_DATA_ATTACH", typ, 1)))
		if !errors.Is(err, errUnsupportedType) {
			t.Fatalf("type %s: error = %v, want errUnsupportedType", typ, err)
		}
	}
}

// Relay가 만드는 message는 Schema의 required field를 모두 가진다. Schema 원문은 contracts/에 있으므로 여기서는 구조만 확인한다.
func TestOutgoingMessagesCarryRequiredEnvelopeFields(t *testing.T) {
	c := correlation{TerminalSessionID: "s1", LabInstanceID: "l1", Generation: 3}
	zero := int64(0)
	tests := []struct {
		name string
		data []byte
		want []string
	}{
		{"browser attached", browserAttached("s1", "r1", true, noTrace), []string{"type", "messageId", "sentAt", "terminalSessionId", "replyToMessageId", "payload"}},
		{"browser ended", browserEnded("s1", End{Reason: "PTY_EXITED", ExitCode: &zero}), []string{"type", "messageId", "sentAt", "terminalSessionId", "payload"}},
		{"browser error", browserError("FORBIDDEN", "m", true, "", noTrace), []string{"type", "messageId", "sentAt", "payload"}},
		{"data attached", dataAttached(c, "r1", false, noTrace), []string{"type", "messageId", "sentAt", "terminalSessionId", "labInstanceId", "generation", "replyToMessageId", "payload"}},
		{"data resize", dataResize(c, []byte("80"), []byte("24"), noTrace), []string{"type", "messageId", "sentAt", "terminalSessionId", "labInstanceId", "generation", "payload"}},
		{"data close", dataClose(c, "SESSION_CLOSED", noTrace), []string{"type", "messageId", "sentAt", "terminalSessionId", "labInstanceId", "generation", "payload"}},
		{"data error", dataError(c, "PROTOCOL_ERROR", "m", true, "", noTrace), []string{"type", "messageId", "sentAt", "terminalSessionId", "labInstanceId", "generation", "payload"}},
	}
	for _, tt := range tests {
		members, ok := jsonObject(tt.data)
		if !ok {
			t.Fatalf("%s: JSON object가 아님: %s", tt.name, tt.data)
		}
		for _, name := range tt.want {
			if _, ok := members[name]; !ok {
				t.Errorf("%s: %s가 없음: %s", tt.name, name, tt.data)
			}
		}
		if !validTimestamp(members["sentAt"]) {
			t.Errorf("%s: sentAt이 date-time이 아님: %s", tt.name, members["sentAt"])
		}
	}

	// historyAvailable은 항상 false다(history/replay 없음). exitCode 0은 알려진 code이므로 생략하지 않는다.
	if !strings.Contains(string(browserAttached("s1", "r", false, noTrace)), `"historyAvailable":false`) ||
		!strings.Contains(string(dataAttached(c, "r", true, noTrace)), `"historyAvailable":false`) {
		t.Error("ATTACHED가 historyAvailable=false를 싣지 않음")
	}
	if !strings.Contains(string(browserEnded("s1", End{Reason: "PTY_EXITED", ExitCode: &zero})), `"exitCode":0`) {
		t.Error("알려진 exit code 0이 생략됨")
	}
	if strings.Contains(string(browserEnded("s1", End{Reason: "PTY_EXITED"})), "exitCode") {
		t.Error("알 수 없는 exit code가 전달됨")
	}
	// cols/rows 원문은 값이 바뀌지 않고 전달된다.
	if !strings.Contains(string(dataResize(c, []byte("1e2"), []byte("1.0"), noTrace)), `"cols":1e2,"rows":1.0`) {
		t.Error("TERMINAL_DATA_RESIZE가 cols/rows 원문을 바꿈")
	}
}

// 계약의 상수와 구현의 상수가 같아야 한다. 계약 상수는 기존 protocol package가 원본이다.
func TestConstantsMatchContract(t *testing.T) {
	if DataSubprotocol != protocol.SubprotocolTerminalData {
		t.Errorf("DataSubprotocol = %q, want %q", DataSubprotocol, protocol.SubprotocolTerminalData)
	}
	if maxJSONTextBytes != protocol.MaxJSONMessageSize {
		t.Errorf("maxJSONTextBytes = %d, want %d", maxJSONTextBytes, protocol.MaxJSONMessageSize)
	}
	if closeTooBig != protocol.CloseMessageTooBig {
		t.Errorf("closeTooBig = %d, want %d", closeTooBig, protocol.CloseMessageTooBig)
	}
	if BrowserSubprotocol != "labbit.terminal.v1" || BrowserPath != "/realtime/v1/terminal" || DataPath != "/connector/v1/terminal-data" {
		t.Errorf("endpoint 상수가 계약과 다름: %s %s %s", BrowserSubprotocol, BrowserPath, DataPath)
	}
	if DefaultGrace.Seconds() != 60 {
		t.Errorf("DefaultGrace = %v, want 60s", DefaultGrace)
	}
	// 종료 원인과 close code 대응은 realtime README §9다.
	for reason, want := range map[string]int{
		"SESSION_CLOSED": 1000, "PTY_EXITED": 1000, "SSH_DISCONNECTED": 1000, "UNKNOWN_FUTURE_REASON": 1000,
		"SESSION_EXPIRED": 4003, "LAB_RESET": 4006, "LAB_CLEANUP": 4006, "SOURCE_TERMINAL_ENDED": 4006, "SERVICE_RESTARTING": 1012,
	} {
		if got := closeCodeFor(reason); got != want {
			t.Errorf("closeCodeFor(%q) = %d, want %d", reason, got, want)
		}
	}
}

func TestSanitizeReason(t *testing.T) {
	for in, want := range map[string]string{
		"PTY_EXITED": "PTY_EXITED", "SSH_DISCONNECTED": "SSH_DISCONNECTED", "A": "A",
		"":                              "UNKNOWN",
		"lowercase":                     "UNKNOWN",
		"HAS SPACE":                     "UNKNOWN",
		"1STARTS_WITH_DIGIT":            "UNKNOWN",
		"line\nbreak":                   "UNKNOWN",
		"shell said: secret-output":     "UNKNOWN",
		strings.Repeat("A", 64):         strings.Repeat("A", 64),
		strings.Repeat("A", 65):         "UNKNOWN",
		fmt.Sprintf("%s\x00", "NUL"):    "UNKNOWN",
		"SAFE_CODE_WITH_NUMBERS_123":    "SAFE_CODE_WITH_NUMBERS_123",
		"한글":                            "UNKNOWN",
		"PTY_EXITED; DROP TABLE users;": "UNKNOWN",
	} {
		if got := SanitizeReason(in); got != want {
			t.Errorf("SanitizeReason(%q) = %q, want %q", in, got, want)
		}
	}
}
