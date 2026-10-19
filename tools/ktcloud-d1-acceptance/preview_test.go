package main

import (
	"bufio"
	"fmt"
	"strings"
	"testing"
)

func TestPreviewScopeRequiresExclusiveFreshTwoGenerations(t *testing.T) {
	for _, tc := range []struct {
		name                string
		enabled, op02       bool
		generations         int
		recovery, wantError bool
	}{
		{"preview", true, false, 2, false, false},
		{"mixed", true, true, 2, false, true},
		{"single", true, false, 1, false, true},
		{"recovery", true, false, 2, true, true},
		{"default", false, true, 2, false, false},
		{"recoveryDefault", false, false, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePreviewScope(tc.enabled, tc.op02, tc.generations, tc.recovery); (got != nil) != tc.wantError {
				t.Fatalf("scope error=%v", got)
			}
		})
	}
}

func TestPreviewCapabilityRequiresProductionAdvertisement(t *testing.T) {
	for _, raw := range []string{`{}`, `{"payload":{"capabilities":["file-v1"]}}`, `malformed`} {
		if requirePreviewCapability([]byte(raw)) == nil {
			t.Fatal("missing capability accepted")
		}
	}
	if err := requirePreviewCapability([]byte(`{"payload":{"capabilities":["preview-v1"]}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewHTTPRequiresExactResponse(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		good      bool
	}{
		{"exact", "HTTP/1.1 200 OK\r\nContent-Length: 14\r\n\r\nLBT173-HTTP-OK", true},
		{"truncated", "HTTP/1.1 200 OK\r\nContent-Length: 15\r\n\r\nLBT173-HTTP-OK", false},
		{"wrongBody", "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nwrong", false},
		{"status", "HTTP/1.1 503 Unavailable\r\nContent-Length: 14\r\n\r\nLBT173-HTTP-OK", false},
		{"oversized", fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: 1024\r\n\r\n%s", strings.Repeat("x", 1024)), false},
		{"malformed", "malformed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePreviewHTTP(bufio.NewReader(strings.NewReader(tc.raw)))
			if (err == nil) != tc.good {
				t.Fatalf("validation error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "wrong") {
				t.Fatal("response leaked into evidence")
			}
		})
	}
}
