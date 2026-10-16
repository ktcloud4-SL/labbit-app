package openstackprovider

import "testing"

func TestLinuxRegressionRequiresEveryRequestedTest(t *testing.T) {
	required := []string{"Expected/symlink", "Expected/replacement"}
	complete := "    --- PASS: Expected/symlink (0.00s)\n    --- PASS: Expected/replacement (0.00s)\nPASS\n"
	if !linuxRegressionPassed([]byte(complete), required) {
		t.Fatal("complete requested regressions rejected")
	}
	for _, output := range []string{
		"testing: warning: no tests to run\nPASS\n",
		"--- PASS: WrongBinary (0.00s)\nPASS\n",
		"--- PASS: Expected/symlink (0.00s)\nPASS\n",
		complete + "--- SKIP: Expected (0.00s)\nPASS\n",
		"--- PASS: Expected/symlink-other (0.00s)\n--- PASS: Expected/replacement (0.00s)\nPASS\n",
	} {
		if linuxRegressionPassed([]byte(output), required) {
			t.Fatal("missing, skipped or unrelated regression accepted")
		}
	}
}
