//go:build integration

package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func applicationLogEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal("Application log line is not a JSON object")
		}
		for _, field := range []string{"timestamp", "level", "message", "service", "component", "version", "environment"} {
			if value, ok := event[field].(string); !ok || value == "" {
				t.Fatalf("Application log lacks base field %s", field)
			}
		}
		if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
			t.Fatal("Application log timestamp is not RFC3339")
		}
		for _, field := range []string{"trace_id", "request_id", "operation_id", "lab_instance_id", "connector_id", "terminal_session_id"} {
			if value, present := event[field]; present && value == "" {
				t.Fatalf("Application log has empty correlation field %s", field)
			}
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Fatal("Application log is empty")
	}
	return events
}

func (e *terminalEnv) logEvent(message, sessionID string) map[string]any {
	e.t.Helper()
	var found map[string]any
	eventually(e.t, "Application log: "+message, 5*time.Second, func() bool {
		for _, event := range applicationLogEvents(e.t, e.logs.String()) {
			if event["message"] == message && (sessionID == "" || event["terminal_session_id"] == sessionID) {
				found = event
				return true
			}
		}
		return false
	})
	return found
}

func requireLogFields(t *testing.T, event map[string]any, fields map[string]any) {
	t.Helper()
	for field, want := range fields {
		if got, present := event[field]; want == nil {
			if present {
				t.Errorf("log field %s should be absent", field)
			}
		} else if got != want {
			t.Errorf("log field %s = %v, want %v", field, got, want)
		}
	}
}
