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
