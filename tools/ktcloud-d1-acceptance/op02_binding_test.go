package main

import (
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
	"testing"
)

func TestOP02ControlEvidenceRejectsForeignIdentityWithSameReply(t *testing.T) {
	for _, file := range []bool{false, true} {
		expected := protocol.BaseEnvelope{LabInstanceID: "owned-lab", Generation: 2, ReplyToMessageID: "same-reply"}
		if file {
			expected.FileRequestID = "owned-file"
		} else {
			expected.TerminalSessionID = "owned-terminal"
		}
		for _, field := range []string{"lab", "generation", "terminal", "file"} {
			actual := expected
			switch field {
			case "lab":
				actual.LabInstanceID = "foreign"
			case "generation":
				actual.Generation = 1
			case "terminal":
				actual.TerminalSessionID = "foreign"
			case "file":
				actual.FileRequestID = "foreign"
			}
			if controlBinding(actual, expected) {
				t.Fatalf("foreign %s accepted with same reply", field)
			}
		}
		if !controlBinding(expected, expected) {
			t.Fatal("expected identity rejected")
		}
	}
}
