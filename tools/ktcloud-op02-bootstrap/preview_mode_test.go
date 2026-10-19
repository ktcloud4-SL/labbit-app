package main

import (
	"strings"
	"testing"
)

func TestPreviewDeployModeAndRecoveryIsolation(t *testing.T) {
	if op02, preview := fixtureModes(false); op02 != "1" || preview != "0" {
		t.Fatal("default OP02 mode changed")
	}
	if op02, preview := fixtureModes(true); op02 != "0" || preview != "1" {
		t.Fatal("Preview mode is not exclusive")
	}
	for _, readOnly := range []bool{false, true} {
		command := controlCommand(true, readOnly)
		for _, gate := range []string{"LABBIT_KTCLOUD_OP02_TEST=0", "LABBIT_KTCLOUD_PREVIEW_SMOKE=0", "LABBIT_KTCLOUD_PREVIEW_TRANSPORT_TEST=0", "LBT145_CONTROL_RECOVERY=1"} {
			if !strings.Contains(command, gate) {
				t.Fatalf("recovery missing %s", gate)
			}
		}
	}
}
