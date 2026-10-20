package installer_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerScript_IntegrityAndHardeningRequirements(t *testing.T) {
	// Root-relative path to scripts/installer/install.sh
	scriptPath := filepath.Join("..", "..", "..", "scripts", "installer", "install.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("failed to read installer script at %s: %v", scriptPath, err)
	}
	content := string(data)

	// 1. Must use set -eu (strict error handling)
	if !strings.Contains(content, "set -eu") {
		t.Error("install.sh must enforce strict mode with 'set -eu'")
	}

	// 2. Must verify root privileges
	if !strings.Contains(content, "$(id -u)") || !strings.Contains(content, "must be run as root") {
		t.Error("install.sh must check for root execution (id -u)")
	}

	// 3. Must verify supported OS (Linux) and Arch (amd64 / x86_64)
	if !strings.Contains(content, "uname -s") || !strings.Contains(content, "uname -m") {
		t.Error("install.sh must check uname -s and uname -m")
	}

	// 4. Must verify systemd
	if !strings.Contains(content, "systemctl") || !strings.Contains(content, "/run/systemd/system") {
		t.Error("install.sh must check for systemd availability")
	}

	// 5. Must verify SHA256 checksum
	if !strings.Contains(content, "sha256sum") {
		t.Error("install.sh must calculate and verify sha256sum")
	}

	// 6. Must not echo enrollment token on TTY
	if !strings.Contains(content, "stty -echo") || !strings.Contains(content, "/dev/tty") {
		t.Error("install.sh must use stty -echo on /dev/tty for interactive token input")
	}

	// 7. Must unset sensitive credentials after usage
	if !strings.Contains(content, "unset ENROLLMENT_TOKEN") || !strings.Contains(content, "unset CONNECTOR_CREDENTIAL") {
		t.Error("install.sh must unset ENROLLMENT_TOKEN and CONNECTOR_CREDENTIAL")
	}

	// 8. Must enforce restricted permissions (0600 credential, 0750 config dir)
	if !strings.Contains(content, "0600") || !strings.Contains(content, "0750") {
		t.Error("install.sh must enforce 0600 for credential and 0750 for config directory")
	}

	// 9. Must configure non-root systemd service
	if !strings.Contains(content, "User=${CONNECTOR_USER}") || !strings.Contains(content, "Group=${CONNECTOR_GROUP}") {
		t.Error("install.sh must configure systemd service with dedicated user/group")
	}

	// 10. Must initialize Ed25519 management key and known_hosts safely
	if !strings.Contains(content, "ssh-keygen -t ed25519") || !strings.Contains(content, "known_hosts") {
		t.Error("install.sh must initialize D-18 Ed25519 key pair and known_hosts")
	}

	// 11. Must protect existing credentials, connector.id, and service state without restarting
	if !strings.Contains(content, "Existing Connector credentials or identity found") {
		t.Error("install.sh must protect and preserve existing credentials and identity")
	}

	// 12. Must align with LBT-182 SaaS Enrollment API contract (POST /api/v1/connector-enrollments, enrollmentToken, connectorId, connectorCredential)
	if !strings.Contains(content, "/api/v1/connector-enrollments") {
		t.Error("install.sh must call POST /api/v1/connector-enrollments")
	}
	if !strings.Contains(content, `{"enrollmentToken":"%s"}`) {
		t.Error("install.sh must send enrollmentToken field in request JSON")
	}
	if !strings.Contains(content, `parse_json "connectorId"`) || !strings.Contains(content, `parse_json "connectorCredential"`) {
		t.Error("install.sh must parse connectorId and connectorCredential from enrollment response")
	}

	// 13. Must enforce lock ownership (LOCK_ACQUIRED) and fail-closed SHA256SUMS verification
	if !strings.Contains(content, "LOCK_ACQUIRED=1") {
		t.Error("install.sh must track LOCK_ACQUIRED so only the lock owner removes LOCK_DIR")
	}
	if strings.Contains(content, "::warning::Release SHA256SUMS not found") {
		t.Error("install.sh must not fail-open when SHA256SUMS is missing")
	}

	// 14. Must properly handle signals (SIGTERM, SIGINT) and exit cleanly to prevent execution after lock release
	if !strings.Contains(content, "on_signal()") || !strings.Contains(content, "trap 'on_signal TERM' TERM") {
		t.Error("install.sh must handle SIGTERM and SIGINT with on_signal to exit immediately upon signal")
	}

	// 15. Must provide recovery guidance when credential saving fails after successful token consumption
	if !strings.Contains(content, "Failed to securely save Connector credentials") || !strings.Contains(content, "revoke this Connector") {
		t.Error("install.sh must provide explicit recovery guidance (revoke -> reissue token) if credential saving fails after enrollment")
	}

	// 16. Must enforce HTTPS for LABBIT_SAAS_BASE_URL to prevent plaintext token/credential leakage
	if !strings.Contains(content, "Insecure HTTP protocol is not permitted") {
		t.Error("install.sh must enforce HTTPS for LABBIT_SAAS_BASE_URL and reject http:// protocol")
	}
}

func TestRuntimeContract_ConnectorConfigModes(t *testing.T) {
	contractPath := filepath.Join("..", "..", "..", "runtime", "contract.yaml")
	data, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("failed to read runtime/contract.yaml: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "<<<<<<<") || strings.Contains(content, "=======") || strings.Contains(content, ">>>>>>>") {
		t.Fatal("runtime/contract.yaml contains unresolved git conflict markers")
	}

	// 1. Control WSS ONLINE initial enrollment required keys (even when Provider is unconfigured)
	for _, key := range []string{
		"LABBIT_SAAS_BASE_URL:\n      required: true",
		"LABBIT_CONNECTOR_ID:\n      required: true",
		"LABBIT_CONNECTOR_CREDENTIAL_FILE:\n      required: true",
	} {
		normalized := strings.ReplaceAll(content, "\r\n", "\n")
		if !strings.Contains(normalized, key) {
			t.Errorf("runtime/contract.yaml missing unconditional required key definition: %s", key)
		}
	}

	// 2. OpenStack Provider keys conditional on Provider operations enabled
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, key := range []string{
		"LABBIT_PROVIDER_CONFIG_FILE:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"OS_CLOUD:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"LABBIT_PROVIDER_CONNECTION_ID:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"OS_PROJECT_ID:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"LABBIT_OPENSTACK_KEYPAIR_NAME:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"LABBIT_OPENSTACK_SSH_ALLOWED_CIDR:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"LABBIT_OPENSTACK_SSH_USERNAME:\n      required_when: \"OpenStack Provider operations are enabled\"",
		"LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE:\n      required_when: \"OpenStack Provider operations are enabled\"",
	} {
		if !strings.Contains(normalized, key) {
			t.Errorf("runtime/contract.yaml missing Provider conditional rule: %s", key)
		}
	}

	// 3. Standard OpenStack & KT D1 CIDR Pair configuration coexistence
	for _, rule := range []string{
		"LABBIT_OPENSTACK_MANAGEMENT_NETWORK_ID:\n      required_when: \"standard Neutron OpenStack profile\"",
		"LABBIT_OPENSTACK_MANAGEMENT_SECURITY_GROUP_ID:\n      required_when: \"standard Neutron OpenStack profile\"",
		"LABBIT_KTCLOUD_CONNECTOR_TIER_ID:\n      required_when: \"KT D1 auth_url profile\"",
		"LABBIT_KTCLOUD_MANAGEMENT_CIDR:\n      required_when: \"KT D1 auth_url profile without LABBIT_KTCLOUD_CIDR_PAIRS\"",
		"LABBIT_KTCLOUD_CIDR_PAIRS:\n      required: false",
		"LABBIT_KTCLOUD_CIDR_STATE_DIR:\n      required_when: \"LABBIT_KTCLOUD_CIDR_PAIRS is set\"",
		"LABBIT_OPENSTACK_LAB_SUBNET_CIDR:\n      required_when: \"OpenStack Provider operations are enabled on standard OpenStack or KT D1 without LABBIT_KTCLOUD_CIDR_PAIRS\"",
	} {
		if !strings.Contains(normalized, rule) {
			t.Errorf("runtime/contract.yaml missing standard OpenStack / KT D1 CIDR Pair rule: %s", rule)
		}
	}
}

func findBash(t *testing.T) string {
	t.Helper()
	gitBash := `C:\Program Files\Git\bin\bash.exe`
	if _, statErr := os.Stat(gitBash); statErr == nil {
		return gitBash
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	t.Skip("bash not found on system; skipping shell execution")
	return ""
}

func TestInstallerScript_ShellSyntaxAndBehavioralSuite(t *testing.T) {
	bashPath := findBash(t)
	tmpDir := t.TempDir()

	for _, name := range []string{"install.sh", "install_test.sh"} {
		src := filepath.Join("..", "..", "..", "scripts", "installer", name)
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("failed to read %s: %v", src, err)
		}
		lf := strings.ReplaceAll(string(raw), "\r\n", "\n")
		dst := filepath.Join(tmpDir, name)
		if err := os.WriteFile(dst, []byte(lf), 0755); err != nil {
			t.Fatalf("failed to write %s: %v", dst, err)
		}
	}

	// 1. Syntax check
	syntaxCmd := exec.Command(bashPath, "-n", filepath.Join(tmpDir, "install.sh"))
	if output, err := syntaxCmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "WSL") {
			t.Skipf("skipping syntax check: WSL bash stub returned %s", string(output))
		}
		t.Fatalf("bash -n syntax validation failed: %v\nOutput: %s", err, string(output))
	}

	// 2. Full behavioral & regression shell test suite execution
	suiteCmd := exec.Command(bashPath, filepath.Join(tmpDir, "install_test.sh"))
	output, err := suiteCmd.CombinedOutput()
	t.Logf("install_test.sh output:\n%s", string(output))
	if err != nil {
		if strings.Contains(string(output), "WSL") {
			t.Skipf("skipping behavioral suite: WSL bash stub returned %s", string(output))
		}
		t.Fatalf("install_test.sh behavioral suite failed: %v\nOutput:\n%s", err, string(output))
	}
}
