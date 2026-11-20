package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVerifierSQLSafety scans cmd/k3d-e2e-verifier source code to ensure that no
// unscoped or global SQL UPDATE/DELETE statements exist that could mutate external or
// non-test database records. All mutations must be strictly scoped to freshly generated test IDs.
func TestVerifierSQLSafety(t *testing.T) {
	content, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("failed to read main.go: %v", err)
	}
	src := string(content)

	// Explicitly prohibit global updates on operation_items or operations without ID scoping
	prohibitedPatterns := []string{
		`(?i)UPDATE\s+operation_items\s+SET\s+status\s*=\s*'FAILED'`,
		`(?i)UPDATE\s+operations\s+SET\s+status\s*=\s*'FAILED'`,
		`(?i)UPDATE\s+operation_items\s+SET[^;]+WHERE\s+status\s*=\s*'PENDING'\s*[` + "`" + `";]`,
		`(?i)UPDATE\s+operations\s+SET[^;]+WHERE\s+status\s*=\s*'PENDING'\s*[` + "`" + `";]`,
	}
	for _, pat := range prohibitedPatterns {
		re := regexp.MustCompile(pat)
		if re.MatchString(src) {
			t.Errorf("Prohibited unscoped SQL mutation found matching pattern %q", pat)
		}
	}

	// Any UPDATE statement in main.go must be strictly scoped to a specific ID ($1 or specific fixture)
	updateRegex := regexp.MustCompile("(?i)UPDATE\\s+([a-zA-Z0-9_]+)\\s+SET\\s+([^`]+)")
	matches := updateRegex.FindAllString(src, -1)
	for _, m := range matches {
		upper := strings.ToUpper(m)
		if strings.Contains(upper, "OPERATION_ITEMS") || strings.Contains(upper, "OPERATIONS") {
			if !strings.Contains(upper, "WHERE ID = $") {
				t.Errorf("Found UPDATE statement on operations/operation_items without strict id predicate: %s", m)
			}
		}
	}
}
