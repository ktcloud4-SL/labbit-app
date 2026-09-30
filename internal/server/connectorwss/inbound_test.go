package connectorwss

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// generation은 Schema에서 maximum이 없는 integer(minimum 1)다. lexical하게 판정하고 값이 int64에 들어가는지만 따로 알려준다.
func TestParseGeneration(t *testing.T) {
	tests := []struct {
		raw     string
		want    int64
		valid   bool
		inRange bool
	}{
		{"1", 1, true, true},
		{"7", 7, true, true},
		{"2147483647", 2147483647, true, true}, // DB integer 최댓값
		{"9223372036854775807", 9223372036854775807, true, true},
		{"1.0", 1, true, true},
		{"1e0", 1, true, true},
		{"1E0", 1, true, true},
		{"10e-1", 1, true, true},
		{"100e-2", 1, true, true},
		{"0.1e1", 1, true, true},
		{"1.5e1", 15, true, true},
		{"1.50e1", 15, true, true},
		{"12e2", 1200, true, true},
		{"1e18", 1_000_000_000_000_000_000, true, true},
		{"9e18", 9_000_000_000_000_000_000, true, true},
		// Schema-valid이지만 int64를 넘는다. invalid가 아니다.
		{"9223372036854775808", 0, true, false},
		{"1e19", 0, true, false},
		{"123456789012345678901234567890", 0, true, false},
		{"1e400", 0, true, false},
		{"1e999999999999999999999", 0, true, false},
		{"1" + strings.Repeat("0", 5000), 0, true, false},
		// Schema-invalid
		{"0", 0, false, false},
		{"0.0", 0, false, false},
		{"-1", 0, false, false},
		{"01", 0, false, false},
		{"1.5", 0, false, false},
		{"1e-1", 0, false, false},
		{"1e-999999999999999999999", 0, false, false},
		{".5", 0, false, false},
		{"1.", 0, false, false},
		{"1e", 0, false, false},
		{"", 0, false, false},
		{`"1"`, 0, false, false},
		{"null", 0, false, false},
		{"true", 0, false, false},
		{"[1]", 0, false, false},
		{"NaN", 0, false, false},
	}
	for _, tt := range tests {
		name := tt.raw
		if len(name) > 40 {
			name = name[:40] + "..."
		}
		t.Run(name, func(t *testing.T) {
			got, valid, inRange := parseGeneration(json.RawMessage(tt.raw))
			if got != tt.want || valid != tt.valid || inRange != tt.inRange {
				t.Fatalf("parseGeneration(%q) = %d, %v, %v, want %d, %v, %v", name, got, valid, inRange, tt.want, tt.valid, tt.inRange)
			}
			if integerAtLeastOne(json.RawMessage(tt.raw)) != tt.valid {
				t.Fatalf("integerAtLeastOne(%q) != valid(%v)", name, tt.valid)
			}
		})
	}
}

// 지수가 매우 커도 값을 만들지 않고 즉시 판정한다(1 MiB message 안의 큰 지수로 CPU/메모리를 쓰지 못한다).
func TestParseGenerationDoesNotMaterializeHugeNumbers(t *testing.T) {
	huge := json.RawMessage("1e" + strings.Repeat("9", 1<<19))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, valid, inRange := parseGeneration(huge); !valid || inRange {
			t.Errorf("valid %v, inRange %v, want true, false", valid, inRange)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("매우 큰 지수의 판정이 끝나지 않음")
	}
}

// 판단에 쓰는 typed payload는 정확한 property 이름에서 decode한다.
func TestDecodeUsesExactPropertyNames(t *testing.T) {
	decode := func(t *testing.T, raw string) map[string]json.RawMessage {
		t.Helper()
		members, ok := jsonObject([]byte(raw))
		if !ok {
			t.Fatalf("not an object: %s", raw)
		}
		return members
	}

	t.Run("ack accepted", func(t *testing.T) {
		got, status := decodeOperationAck(decode(t, `{"accepted":true,"ACCEPTED":false}`))
		if status != decodeOK || !got.Accepted {
			t.Fatalf("got %+v, %v, want accepted=true", got, status)
		}
		if _, status := decodeOperationAck(decode(t, `{"ACCEPTED":true}`)); status != decodeInvalid {
			t.Fatalf("status = %v, want invalid", status)
		}
	})
	t.Run("result outcome and resources", func(t *testing.T) {
		got, status := decodeOperationResult(decode(t, `{"outcome":"FAILED","OUTCOME":"SUCCEEDED","providerResources":[{"resourceType":"SERVER","providerId":"srv-1","PROVIDERID":"other","generation":1}]}`))
		if status != decodeOK || got.Outcome != "FAILED" || len(got.ProviderResources) != 1 || got.ProviderResources[0].ProviderID != "srv-1" {
			t.Fatalf("got %+v, %v", got, status)
		}
	})
	t.Run("progress stage", func(t *testing.T) {
		got, status := decodeOperationProgress(decode(t, `{"stage":"CREATE_VM","Stage":"OTHER"}`))
		if status != decodeOK || got.Stage != "CREATE_VM" {
			t.Fatalf("got %+v, %v", got, status)
		}
	})
	t.Run("reconcile observations", func(t *testing.T) {
		got, status := decodeReconcileResult(decode(t, `{"observations":[{"resourceType":"SERVER","providerId":"srv-1","exists":true,"EXISTS":false,"source":"KNOWN_RESOURCE"}]}`))
		if status != decodeOK || len(got.Observations) != 1 || !got.Observations[0].Exists {
			t.Fatalf("got %+v, %v", got, status)
		}
	})
	t.Run("unrepresentable nested generation is not invalid", func(t *testing.T) {
		_, status := decodeOperationResult(decode(t, `{"outcome":"SUCCEEDED","providerResources":[{"resourceType":"SERVER","providerId":"srv-1","generation":123456789012345678901234567890}]}`))
		if status != decodeUnrepresentable {
			t.Fatalf("status = %v, want unrepresentable", status)
		}
		// 다른 위반이 함께 있으면 invalid가 우선한다.
		_, status = decodeOperationResult(decode(t, `{"outcome":"SUCCEEDED","providerResources":[{"resourceType":"SERVER","providerId":"srv-1","generation":123456789012345678901234567890},{"resourceType":"SERVER"}]}`))
		if status != decodeInvalid {
			t.Fatalf("status = %v, want invalid", status)
		}
	})
}

func TestSafeErrorCodeOnlyAcceptsMachineReadableCodes(t *testing.T) {
	tests := map[string]string{
		`{"payload":{"code":"UNSUPPORTED_MESSAGE_TYPE"}}`: "UNSUPPORTED_MESSAGE_TYPE",
		`{"payload":{"code":"E1"}}`:                       "E1",
		`{"payload":{"code":"lower_case"}}`:               "INVALID",
		`{"payload":{"code":"HAS SPACE"}}`:                "INVALID",
		`{"payload":{"code":"line\nbreak"}}`:              "INVALID",
		`{"payload":{"code":""}}`:                         "INVALID",
		`{"payload":{"code":7}}`:                          "INVALID",
		`{"payload":{}}`:                                  "INVALID",
		`{"payload":"x"}`:                                 "INVALID",
		`{}`:                                              "INVALID",
	}
	for raw, want := range tests {
		members, ok := jsonObject([]byte(raw))
		if !ok {
			t.Fatalf("not an object: %s", raw)
		}
		if got := safeErrorCode(members); got != want {
			t.Fatalf("safeErrorCode(%s) = %q, want %q", raw, got, want)
		}
	}
	if got := safeErrorCode(map[string]json.RawMessage{"payload": json.RawMessage(`{"code":"` + strings.Repeat("A", 65) + `"}`)}); got != "INVALID" {
		t.Fatalf("긴 code = %q, want INVALID", got)
	}
}
