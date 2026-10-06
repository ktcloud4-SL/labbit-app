package realtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ktcloud4-SL/labbit-app/internal/server/tracecontext"
)

// contracts/connector/README.md §9(D-25)와 contracts/realtime/README.md: Terminal JSON control message의 optional traceparent/tracestate는
// 표준 parser로 정상화해 유효한 값만 보존하고, 유효하지 않은 값은 그 관측 field만 버린다. 업무 message는 Trace 때문에 거절되지 않는다.

const (
	tpSampled   = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tpUnsampled = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	tsValid     = "vendor=opaque,other=value"
	traceIDHex  = "4bf92f3577b34da6a3ce929d0e0e4736"
)

// traceVariant는 message에 넣는 Trace field(JSON member 조각)와 정상화 뒤에 남아야 하는 값이다.
type traceVariant struct {
	name string
	// members는 message 최상위에 추가할 JSON member들이다. 앞에 쉼표를 붙이지 않는다. 비어 있으면 Trace field가 없는 message다.
	members               string
	wantParent, wantState string
}

var traceVariants = []traceVariant{
	{"none", ``, "", ""},
	{"valid sampled", `"traceparent":"` + tpSampled + `"`, tpSampled, ""},
	{"valid with tracestate", `"traceparent":"` + tpSampled + `","tracestate":"` + tsValid + `"`, tpSampled, tsValid},
	// 미샘플링 Context도 그대로 전파하며 sampled=1로 바꾸지 않는다.
	{"valid not sampled stays not sampled", `"traceparent":"` + tpUnsampled + `","tracestate":"` + tsValid + `"`, tpUnsampled, tsValid},
	{"tracestate is normalized by the standard parser", `"traceparent":"` + tpSampled + `","tracestate":"vendor=a,,other=b"`, tpSampled, "vendor=a,other=b"},

	// tracestate만 잘못되면 tracestate만 버린다.
	{"invalid tracestate only", `"traceparent":"` + tpSampled + `","tracestate":"not valid"`, tpSampled, ""},
	{"tracestate too long", `"traceparent":"` + tpSampled + `","tracestate":"vendor=` + strings.Repeat("a", 1024) + `"`, tpSampled, ""},
	{"tracestate is a number", `"traceparent":"` + tpSampled + `","tracestate":1`, tpSampled, ""},
	{"tracestate is an object", `"traceparent":"` + tpSampled + `","tracestate":{"a":"b"}`, tpSampled, ""},
	{"tracestate is null", `"traceparent":"` + tpSampled + `","tracestate":null`, tpSampled, ""},

	// traceparent가 없거나 잘못되면 tracestate도 쓰지 않는다.
	{"tracestate without traceparent", `"tracestate":"` + tsValid + `"`, "", ""},
	{"malformed traceparent", `"traceparent":"not-a-traceparent","tracestate":"` + tsValid + `"`, "", ""},
	{"all zero trace id", `"traceparent":"00-00000000000000000000000000000000-00f067aa0ba902b7-01","tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent empty", `"traceparent":"","tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent too long", `"traceparent":"` + tpSampled + strings.Repeat("0", 512) + `","tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent is a number", `"traceparent":1,"tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent is an array", `"traceparent":["` + tpSampled + `"],"tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent is an object", `"traceparent":{"v":"` + tpSampled + `"},"tracestate":"` + tsValid + `"`, "", ""},
	{"traceparent is null", `"traceparent":null,"tracestate":"` + tsValid + `"`, "", ""},
	// property 이름은 대소문자를 구분한다. 다른 대소문자의 key는 Trace field가 아니다.
	{"property name case differs", `"Traceparent":"` + tpSampled + `","TRACESTATE":"` + tsValid + `"`, "", ""},
}

// withMembers는 JSON object 문자열의 첫 member 앞에 members를 넣는다.
func withMembers(obj, members string) string {
	if members == "" {
		return obj
	}
	return strings.Replace(obj, `{`, `{`+members+`,`, 1)
}

// 네 종류의 Terminal control message(Browser ATTACH/RESIZE, Connector DATA_ATTACH/DATA_ENDED) 모두에서 같은 규칙이 적용된다.
// 어떤 Trace 값이든 업무 message는 그대로 해석되고, 유효한 값만 Trace로 남는다.
func TestDecodersNormalizeTraceAndNeverRejectForIt(t *testing.T) {
	type decoded struct {
		trace tracecontext.Context
		err   error
		ok    bool // 업무 field가 그대로 해석되었는지
	}
	decoders := map[string]func(raw string) decoded{
		"browser TERMINAL_ATTACH": func(raw string) decoded {
			m, err := decodeBrowserMessage([]byte(raw))
			return decoded{m.Trace, err, m.Type == "TERMINAL_ATTACH" && m.MessageID == "m1" && m.TerminalSessionID == "s1" && string(m.Cols) == "80"}
		},
		"browser TERMINAL_RESIZE": func(raw string) decoded {
			m, err := decodeBrowserMessage([]byte(raw))
			return decoded{m.Trace, err, m.Type == "TERMINAL_RESIZE" && m.MessageID == "m1" && string(m.Rows) == "24"}
		},
		"data TERMINAL_DATA_ATTACH": func(raw string) decoded {
			m, err := decodeDataMessage([]byte(raw))
			return decoded{m.Trace, err, m.Type == "TERMINAL_DATA_ATTACH" && m.MessageID == "m1" && m.RuntimeID == "r"}
		},
		"data TERMINAL_DATA_ENDED": func(raw string) decoded {
			m, err := decodeDataMessage([]byte(raw))
			return decoded{m.Trace, err, m.Type == "TERMINAL_DATA_ENDED" && m.Reason == "PTY_EXITED"}
		},
	}
	bases := map[string]string{
		"browser TERMINAL_ATTACH":   attachBase,
		"browser TERMINAL_RESIZE":   resizeBase,
		"data TERMINAL_DATA_ATTACH": dataAttachJSON(`,"payload":{"runtimeId":"r"}`),
		"data TERMINAL_DATA_ENDED":  dataEndedJSON(`{"reason":"PTY_EXITED"}`),
	}
	for name, decode := range decoders {
		for _, v := range traceVariants {
			t.Run(name+"/"+v.name, func(t *testing.T) {
				raw := withMembers(bases[name], v.members)
				got := decode(raw)
				if got.err != nil || !got.ok {
					t.Fatalf("Trace 때문에 업무 message가 거절되거나 잘못 해석됨: error = %v, 업무 field 정상 = %v, message = %s", got.err, got.ok, raw)
				}
				if got.trace.Traceparent != v.wantParent || got.trace.Tracestate != v.wantState {
					t.Fatalf("Trace = %+v, want {%q %q}", got.trace, v.wantParent, v.wantState)
				}
				if v.wantParent == "" && got.trace.Valid() {
					t.Fatal("유효한 traceparent가 없는데 Valid()임")
				}
			})
		}
	}
}

// Trace 원문은 Trace 밖으로 새지 않는다. 잘못된 값은 message 구조체 어디에도 남지 않는다.
func TestInvalidTraceLeavesNoTraceInDecodedMessage(t *testing.T) {
	raw := withMembers(attachBase, `"traceparent":"INVALID-TRACE-MARKER","tracestate":"INVALID-STATE-MARKER"`)
	msg, err := decodeBrowserMessage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if dumped, _ := json.Marshal(msg); strings.Contains(string(dumped), "INVALID-") {
		t.Fatalf("잘못된 Trace 원문이 해석 결과에 남음: %s", dumped)
	}
	if msg.Trace != (tracecontext.Context{}) {
		t.Fatalf("Trace = %+v, want zero value(가짜 Trace를 만들지 않는다)", msg.Trace)
	}
}

// 업무 검증이 실패한 message도 읽을 수 있었던 Trace는 돌려준다. Connector에 보내는 ERROR 응답에 같은 Trace를 실을 수 있게 하기 위해서다.
func TestMalformedDataMessageStillReturnsItsValidTrace(t *testing.T) {
	raw := withMembers(dataAttachJSON(`,"payload":{}`), `"traceparent":"`+tpSampled+`"`)
	msg, err := decodeDataMessage([]byte(raw))
	if err == nil {
		t.Fatal("payload가 없는 attach가 거절되지 않음")
	}
	if msg.Trace.Traceparent != tpSampled {
		t.Fatalf("Trace = %+v, want %q", msg.Trace, tpSampled)
	}
}

// 송신 control message는 유효한 Trace Context만 싣는다. 호출자가 잘못된 값을 넘겨도 wire에는 나가지 않는다.
func TestOutgoingControlMessagesCarryOnlyValidNormalizedTrace(t *testing.T) {
	c := correlation{TerminalSessionID: "s1", LabInstanceID: "l1", Generation: 3}
	valid := tracecontext.Context{Traceparent: tpUnsampled, Tracestate: tsValid}
	builders := map[string]func(tracecontext.Context) []byte{
		"browser attached": func(tr tracecontext.Context) []byte { return browserAttached("s1", "r1", true, tr) },
		"browser error":    func(tr tracecontext.Context) []byte { return browserError("FORBIDDEN", "m", true, "r1", tr) },
		"browser ended":    func(tr tracecontext.Context) []byte { return browserEnded("s1", End{Reason: "PTY_EXITED", Trace: tr}) },
		"data attached":    func(tr tracecontext.Context) []byte { return dataAttached(c, "r1", false, tr) },
		"data resize":      func(tr tracecontext.Context) []byte { return dataResize(c, []byte("80"), []byte("24"), tr) },
		"data close":       func(tr tracecontext.Context) []byte { return dataClose(c, "SESSION_CLOSED", tr) },
		"data error":       func(tr tracecontext.Context) []byte { return dataError(c, "PROTOCOL_ERROR", "m", true, "r1", tr) },
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			members, ok := jsonObject(build(valid))
			if !ok {
				t.Fatal("JSON object가 아님")
			}
			if got, _ := jsonString(members["traceparent"]); got != tpUnsampled {
				t.Fatalf("traceparent = %q, want %q(미샘플링 그대로)", got, tpUnsampled)
			}
			if got, _ := jsonString(members["tracestate"]); got != tsValid {
				t.Fatalf("tracestate = %q, want %q", got, tsValid)
			}

			// Trace가 없으면 field 자체가 없다. 빈 문자열이나 가짜 값을 싣지 않는다.
			for label, none := range map[string]tracecontext.Context{
				"zero":                      {},
				"invalid traceparent":       {Traceparent: "garbage", Tracestate: tsValid},
				"tracestate without parent": {Tracestate: tsValid},
			} {
				members, _ := jsonObject(build(none))
				for _, key := range []string{"traceparent", "tracestate"} {
					if _, present := members[key]; present {
						t.Fatalf("%s: %s가 wire에 나감: %s", label, key, members[key])
					}
				}
			}

			// traceparent만 유효하고 tracestate가 잘못된 값이면 tracestate만 나가지 않는다.
			members, _ = jsonObject(build(tracecontext.Context{Traceparent: tpSampled, Tracestate: "not valid"}))
			if got, _ := jsonString(members["traceparent"]); got != tpSampled {
				t.Fatalf("traceparent = %q, want %q", got, tpSampled)
			}
			if _, present := members["tracestate"]; present {
				t.Fatal("잘못된 tracestate가 wire에 나감")
			}
		})
	}
}

// log에는 원문이 아니라 정상화된 trace_id만 붙는다. Trace가 없으면 log를 바꾸지 않는다.
func TestTraceIDOfNormalizedContext(t *testing.T) {
	if got := tracecontext.Normalize(tpSampled, tsValid).TraceID(); got != traceIDHex {
		t.Fatalf("TraceID() = %q, want %q", got, traceIDHex)
	}
	if got := traceOf(map[string]json.RawMessage{"traceparent": json.RawMessage(`"garbage"`)}).TraceID(); got != "" {
		t.Fatalf("잘못된 traceparent의 TraceID() = %q, want 없음", got)
	}
}
