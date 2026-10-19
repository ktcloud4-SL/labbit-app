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

	// 11. Must protect existing credentials and identity
	if !strings.Contains(content, "Existing Connector credentials found") {
		t.Error("install.sh must protect and preserve existing credentials")
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

func TestInstallerScript_ShellSyntaxCheck(t *testing.T) {
	var bashPath string
	// On Windows, prefer Git bash over Windows WSL stub
	gitBash := `C:\Program Files\Git\bin\bash.exe`
	if _, statErr := os.Stat(gitBash); statErr == nil {
		bashPath = gitBash
	} else if p, err := exec.LookPath("bash"); err == nil {
		bashPath = p
	} else {
		t.Skip("bash not found on system; skipping syntax check")
	}

	scriptPath := filepath.Join("..", "..", "..", "scripts", "installer", "install.sh")
	cmd := exec.Command(bashPath, "-n", scriptPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "WSL") {
			t.Skipf("skipping syntax check: WSL bash stub returned %s", string(output))
		}
		t.Fatalf("bash -n syntax validation failed for %s: %v\nOutput: %s", scriptPath, err, string(output))
	}
}
