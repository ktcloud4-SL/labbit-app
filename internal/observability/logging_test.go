package observability

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewJSONLoggerToMatchesRuntimeContractBaseFields(t *testing.T) {
	var output bytes.Buffer

	logger := NewJSONLoggerTo(&output, "labbit-server", "test", "development", "info")
	logger.Info("테스트 로그")

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("JSON 로그를 파싱할 수 없습니다: %v", err)
	}

	required := []string{
		"timestamp",
		"level",
		"message",
		"service",
		"component",
		"version",
		"environment",
	}
	for _, key := range required {
		if _, ok := event[key]; !ok {
			t.Errorf("Runtime Contract 필수 필드가 없습니다: %s", key)
		}
	}

	// slog의 기본 필드명이 그대로 노출되면 Runtime Contract와 불일치한다.
	for _, key := range []string{"time", "msg"} {
		if _, ok := event[key]; ok {
			t.Errorf("slog 기본 필드가 Runtime Contract 이름으로 치환되지 않았습니다: %s", key)
		}
	}

	if got := event["message"]; got != "테스트 로그" {
		t.Errorf("message = %v, want %q", got, "테스트 로그")
	}
}
