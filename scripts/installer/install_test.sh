#!/bin/bash
# Comprehensive Behavioral & Regression Test Suite for scripts/installer/install.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER="${SCRIPT_DIR}/install.sh"
TEST_ROOT="$(mktemp -d)"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

echo "==> Running install.sh behavioral test suite..."

# Helper to create base mock binaries for isolated shell execution
setup_base_mocks() {
  local bin_dir="$1"
  mkdir -p "$bin_dir"

  cat <<'EOF' > "${bin_dir}/id"
#!/bin/sh
echo "0"
EOF

  cat <<'EOF' > "${bin_dir}/uname"
#!/bin/sh
if [ "${1:-}" = "-s" ]; then echo "Linux"; else echo "x86_64"; fi
EOF

  cat <<'EOF' > "${bin_dir}/systemctl"
#!/bin/sh
echo "systemctl $*" >> "${MOCK_LOG_FILE:-/dev/null}"
if [ "${1:-}" = "is-active" ]; then
  exit "${MOCK_SERVICE_ACTIVE_EXIT:-1}"
fi
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/stty"
#!/bin/sh
echo "stty $*" >> "${MOCK_LOG_FILE:-/dev/null}"
if [ "${1:-}" = "-g" ]; then
  if [ "${MOCK_STTY_G_FAIL:-0}" = "1" ]; then exit 1; fi
  echo "saved-stty-state-001"
  exit 0
fi
if [ "${1:-}" = "-echo" ]; then
  if [ "${MOCK_STTY_ECHO_FAIL:-0}" = "1" ]; then exit 1; fi
  exit 0
fi
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/chown"
#!/bin/sh
echo "chown $*" >> "${MOCK_LOG_FILE:-/dev/null}"
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/getent"
#!/bin/sh
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/groupadd"
#!/bin/sh
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/useradd"
#!/bin/sh
exit 0
EOF

  cat <<'EOF' > "${bin_dir}/ssh-keygen"
#!/bin/sh
# Find -f argument and create key files
while [ $# -gt 0 ]; do
  if [ "$1" = "-f" ]; then
    printf 'PRIVATE-KEY\n' > "$2"
    printf 'PUBLIC-KEY\n' > "$2.pub"
    exit 0
  fi
  shift
done
exit 1
EOF

  if ! command -v jq >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    cat <<'EOF' > "${bin_dir}/jq"
#!/bin/sh
field=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--arg" ]; then
    field="$3"
    shift 3
  else
    shift
  fi
done
node -e '
  const fs = require("fs");
  const key = process.argv[1];
  try {
    const data = JSON.parse(fs.readFileSync(0, "utf8"));
    const val = data && typeof data[key] === "string" ? data[key] : "";
    if (val) process.stdout.write(val + "\n");
  } catch (_) {}
' "$field"
EOF
    chmod +x "${bin_dir}/jq"
  fi

  chmod +x "${bin_dir}/id" "${bin_dir}/uname" "${bin_dir}/systemctl" "${bin_dir}/stty" \
           "${bin_dir}/chown" "${bin_dir}/getent" "${bin_dir}/groupadd" "${bin_dir}/useradd" "${bin_dir}/ssh-keygen"
}

# Test 1: Non-root execution must fail
test_non_root() {
  echo "--- Test 1: Non-root execution check ---"
  local fake_bin="${TEST_ROOT}/bin_1"
  setup_base_mocks "$fake_bin"
  cat <<'EOF' > "${fake_bin}/id"
#!/bin/sh
echo "1000"
EOF
  chmod +x "${fake_bin}/id"

  local output
  output=$(PATH="${fake_bin}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if echo "$output" | grep -q "must be run as root"; then
    echo "PASS: Non-root execution correctly rejected."
  else
    echo "FAIL: Expected non-root rejection, got: $output"
    exit 1
  fi
}

# Test 2: Unsupported OS & Architecture must fail
test_unsupported_os_and_arch() {
  echo "--- Test 2: Unsupported OS & Architecture check ---"
  local fake_bin="${TEST_ROOT}/bin_2"
  setup_base_mocks "$fake_bin"
  cat <<'EOF' > "${fake_bin}/uname"
#!/bin/sh
if [ "${1:-}" = "-s" ]; then echo "Darwin"; else echo "x86_64"; fi
EOF
  chmod +x "${fake_bin}/uname"

  local output
  output=$(PATH="${fake_bin}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if ! echo "$output" | grep -q "Unsupported Operating System"; then
    echo "FAIL: Expected OS rejection, got: $output"
    exit 1
  fi

  cat <<'EOF' > "${fake_bin}/uname"
#!/bin/sh
if [ "${1:-}" = "-s" ]; then echo "Linux"; else echo "aarch64"; fi
EOF
  chmod +x "${fake_bin}/uname"

  output=$(PATH="${fake_bin}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if echo "$output" | grep -q "Unsupported Architecture"; then
    echo "PASS: Unsupported OS and Architecture correctly rejected."
  else
    echo "FAIL: Expected arch rejection, got: $output"
    exit 1
  fi
}

# Test 3: Concurrent Lock Ownership (Second instance must NOT delete First instance's lock)
test_concurrent_lock_ownership() {
  echo "--- Test 3: Concurrent lock ownership regression check ---"
  local fake_bin="${TEST_ROOT}/bin_3"
  setup_base_mocks "$fake_bin"
  local mock_run="${TEST_ROOT}/run_3/systemd/system"
  mkdir -p "$mock_run"
  local mock_lock="${TEST_ROOT}/lock_3.d"

  # Instance A holds the lock directory
  mkdir -p "$mock_lock"
  echo "instance-a-owner" > "${mock_lock}/owner"

  # Instance B attempts to run while Instance A holds the lock
  local status=0
  local output
  output=$(PATH="${fake_bin}:${PATH}" SYSTEMD_RUN_DIR="$mock_run" LABBIT_LOCK_DIR="$mock_lock" sh "$INSTALLER" 2>&1) || status=$?

  if [ "$status" -eq 0 ]; then
    echo "FAIL: Second instance should have failed when lock was held."
    exit 1
  fi
  if ! echo "$output" | grep -q "Another instance of the Labbit Connector installer is currently running"; then
    echo "FAIL: Expected lock contention error, got: $output"
    exit 1
  fi
  # Remove marker file and verify the lock directory itself was NOT rmdir'd by Instance B even when empty!
  rm -f "${mock_lock}/owner"
  output=$(PATH="${fake_bin}:${PATH}" SYSTEMD_RUN_DIR="$mock_run" LABBIT_LOCK_DIR="$mock_lock" sh "$INSTALLER" 2>&1 || true)
  if [ ! -d "$mock_lock" ]; then
    echo "FAIL: Second instance deleted the first instance's empty lock directory on EXIT!"
    exit 1
  fi
  echo "PASS: Lock directory preserved when second concurrent instance exits."
}

# Test 4: SHA256SUMS 404 / download failure must fail-closed without installing binary
test_checksum_404_fail_closed() {
  echo "--- Test 4: SHA256SUMS 404 fail-closed check ---"
  local fake_bin="${TEST_ROOT}/bin_4"
  setup_base_mocks "$fake_bin"

  cat <<'EOF' > "${fake_bin}/curl"
#!/bin/sh
url=""
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    http*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */SHA256SUMS)
    # Simulate 404 / network failure on SHA256SUMS
    exit 22
    ;;
  */labbit-connector)
    printf 'mock-connector-binary\n' > "$out"
    exit 0
    ;;
esac
exit 1
EOF
  chmod +x "${fake_bin}/curl"

  local mock_run="${TEST_ROOT}/run_4/systemd/system"
  mkdir -p "$mock_run"
  local mock_lock="${TEST_ROOT}/lock_4.d"
  local mock_config="${TEST_ROOT}/etc_4"
  local mock_bin="${TEST_ROOT}/usr_4/labbit-connector"
  local mock_tty="${TEST_ROOT}/tty_4"
  printf 'enroll-token-404\n' > "$mock_tty"

  local status=0
  local output
  output=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="$mock_lock" \
    LABBIT_CONFIG_DIR="$mock_config" \
    LABBIT_INSTALL_BIN="$mock_bin" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1) || status=$?

  if [ "$status" -eq 0 ]; then
    echo "FAIL: Installer succeeded when SHA256SUMS returned 404 (fail-open)!"
    exit 1
  fi
  if [ -f "$mock_bin" ]; then
    echo "FAIL: Binary was installed despite SHA256SUMS failure!"
    exit 1
  fi
  if echo "$output" | grep -q "Failed to download release SHA256SUMS"; then
    echo "PASS: SHA256SUMS 404 correctly aborted installation (fail-closed)."
  else
    echo "FAIL: Expected SHA256SUMS download failure error, got: $output"
    exit 1
  fi
}

# Test 5: TTY validation, stty -echo failure abort & restoration, and environment token prohibition
test_tty_and_token_security() {
  echo "--- Test 5: TTY/stty validation & environment token prohibition ---"
  local fake_bin="${TEST_ROOT}/bin_5"
  setup_base_mocks "$fake_bin"
  local mock_run="${TEST_ROOT}/run_5/systemd/system"
  mkdir -p "$mock_run"

  # 5A: Environment variable LABBIT_ENROLLMENT_TOKEN must be rejected
  local output
  output=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_5a.d" \
    LABBIT_ENROLLMENT_TOKEN="forbidden-env-token" \
    sh "$INSTALLER" 2>&1 || true)
  if ! echo "$output" | grep -q "LABBIT_ENROLLMENT_TOKEN environment variable is prohibited"; then
    echo "FAIL: Expected LABBIT_ENROLLMENT_TOKEN rejection, got: $output"
    exit 1
  fi

  # 5B: Inaccessible TTY must be rejected before binary download
  output=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_5b.d" \
    LABBIT_TTY_DEV="${TEST_ROOT}/nonexistent_tty" \
    sh "$INSTALLER" 2>&1 || true)
  if ! echo "$output" | grep -q "Interactive TTY .* is not accessible"; then
    echo "FAIL: Expected inaccessible TTY rejection, got: $output"
    exit 1
  fi

  # 5C: stty -echo failure during token prompt must abort before reading token and restore saved stty state
  local dummy_bin_content="connector-bin-v0.1.0"
  local expected_sha
  expected_sha="$(printf '%s' "$dummy_bin_content" | sha256sum | awk '{print $1}')"
  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
out=""
url=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  */SHA256SUMS)
    printf '%s  labbit-connector\n' "$expected_sha" > "\$out"
    exit 0
    ;;
  */labbit-connector)
    printf '%s' "$dummy_bin_content" > "\$out"
    exit 0
    ;;
esac
exit 0
EOF
  chmod +x "${fake_bin}/curl"

  local mock_tty="${TEST_ROOT}/tty_5c"
  printf 'secret-token-should-not-be-used\n' > "$mock_tty"
  local mock_log="${TEST_ROOT}/log_5c"
  : > "$mock_log"

  output=$(PATH="${fake_bin}:${PATH}" \
    MOCK_LOG_FILE="$mock_log" \
    MOCK_STTY_ECHO_FAIL="1" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_5c.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_5c" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_5c/labbit-connector" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$output" | grep -q "Failed to disable terminal echo"; then
    echo "FAIL: Expected stty -echo failure abort, got: $output"
    exit 1
  fi
  if ! grep -q "stty saved-stty-state-001" "$mock_log"; then
    echo "FAIL: Expected saved stty state to be restored on stty -echo failure; log: $(cat "$mock_log")"
    exit 1
  fi
  echo "PASS: Environment token rejected, inaccessible TTY rejected, and stty -echo failure safely aborted with TTY state restored."
}

# Test 6: Existing Credential / connector.id / Stopped Service Protection (No automatic restart)
test_existing_identity_and_service_protection() {
  echo "--- Test 6: Existing credential, connector.id & stopped service protection ---"
  local fake_bin="${TEST_ROOT}/bin_6"
  setup_base_mocks "$fake_bin"
  local mock_run="${TEST_ROOT}/run_6/systemd/system"
  mkdir -p "$mock_run"
  local mock_log="${TEST_ROOT}/log_6"
  : > "$mock_log"

  # 6A: Existing credential with stopped service (MOCK_SERVICE_ACTIVE_EXIT=1) must NOT call systemctl restart
  local mock_config_a="${TEST_ROOT}/etc_6a"
  mkdir -p "$mock_config_a"
  printf 'existing-secret-cred\n' > "${mock_config_a}/credential"

  local output
  output=$(PATH="${fake_bin}:${PATH}" \
    MOCK_LOG_FILE="$mock_log" \
    MOCK_SERVICE_ACTIVE_EXIT="1" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_6a.d" \
    LABBIT_CONFIG_DIR="$mock_config_a" \
    LABBIT_SYSTEMD_SERVICE_FILE="${TEST_ROOT}/etc_6a/labbit-connector.service" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$output" | grep -q "Existing Connector credentials or identity found"; then
    echo "FAIL: Expected existing credentials message, got: $output"
    exit 1
  fi
  if grep -q "systemctl restart" "$mock_log"; then
    echo "FAIL: Installer restarted a stopped existing service without operator consent!"
    exit 1
  fi
  if [ "$(cat "${mock_config_a}/credential")" != "existing-secret-cred" ]; then
    echo "FAIL: Existing credential was modified!"
    exit 1
  fi

  # 6B: Existing connector.id alone (without credential) must also be protected
  local mock_config_b="${TEST_ROOT}/etc_6b"
  mkdir -p "$mock_config_b"
  printf 'existing-conn-id-001\n' > "${mock_config_b}/connector.id"

  output=$(PATH="${fake_bin}:${PATH}" \
    MOCK_LOG_FILE="$mock_log" \
    MOCK_SERVICE_ACTIVE_EXIT="1" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_6b.d" \
    LABBIT_CONFIG_DIR="$mock_config_b" \
    LABBIT_SYSTEMD_SERVICE_FILE="${TEST_ROOT}/etc_6b/labbit-connector.service" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$output" | grep -q "Existing Connector credentials or identity found"; then
    echo "FAIL: Expected existing connector.id detection, got: $output"
    exit 1
  fi
  if [ "$(cat "${mock_config_b}/connector.id")" != "existing-conn-id-001" ]; then
    echo "FAIL: Existing connector.id was modified!"
    exit 1
  fi
  echo "PASS: Existing credential, connector.id, and stopped service state preserved without automatic restart."
}

# Test 7: Full End-to-End 201 Enrollment Happy Path & Uncertain Outcome Distinction
test_full_enrollment_201_and_uncertain_outcome() {
  echo "--- Test 7: Full 201 Enrollment execution, file layout & uncertain outcome handling ---"
  local fake_bin="${TEST_ROOT}/bin_7"
  setup_base_mocks "$fake_bin"

  local dummy_bin_content="verified-connector-binary-payload"
  local expected_sha
  expected_sha="$(printf '%s' "$dummy_bin_content" | sha256sum | awk '{print $1}')"
  local captured_req="${TEST_ROOT}/captured_enroll_request.json"

  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
url=""
out=""
data_file=""
write_code=0
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    -d)
      data_file="\${2#@}"
      shift 2
      ;;
    -w) write_code=1; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done

case "\$url" in
  */labbit-connector)
    printf '%s' "$dummy_bin_content" > "\$out"
    exit 0
    ;;
  */SHA256SUMS)
    printf '%s  labbit-connector\n0000000000000000000000000000000000000000000000000000000000000000  labbit-connector-v0.1.0-linux-amd64.tar.gz\n' "$expected_sha" > "\$out"
    exit 0
    ;;
  */api/v1/connector-enrollments)
    if [ -n "\$data_file" ] && [ -f "\$data_file" ]; then
      cp "\$data_file" "$captured_req"
    fi
    if [ "\${MOCK_ENROLL_HTTP_STATUS:-201}" = "201" ]; then
      printf '{"connectorId":"conn-e2e-201","connectorCredential":"cred-secret-xyz-201"}\n' > "\$out"
    elif [ "\${MOCK_ENROLL_HTTP_STATUS:-}" = "401" ]; then
      printf '{"code":"invalid_enrollment_token","message":"invalid token"}\n' > "\$out"
    else
      printf '' > "\$out"
    fi
    if [ "\$write_code" -eq 1 ]; then
      printf '%s' "\${MOCK_ENROLL_HTTP_STATUS:-201}"
    fi
    exit 0
    ;;
esac
exit 1
EOF
  chmod +x "${fake_bin}/curl"

  local mock_run="${TEST_ROOT}/run_7/systemd/system"
  mkdir -p "$mock_run"
  local mock_config="${TEST_ROOT}/etc_7"
  local mock_bin="${TEST_ROOT}/usr_7/labbit-connector"
  local mock_service="${TEST_ROOT}/systemd_7/labbit-connector.service"
  mkdir -p "$(dirname "$mock_service")"
  local mock_tty="${TEST_ROOT}/tty_7"
  printf 'one-time-enroll-token-777\n' > "$mock_tty"
  local mock_log="${TEST_ROOT}/log_7"
  : > "$mock_log"

  # 7A: Network/timeout (HTTP 000) must report UNCERTAIN outcome and forbid blind retry
  local out_uncertain
  out_uncertain=$(PATH="${fake_bin}:${PATH}" \
    MOCK_ENROLL_HTTP_STATUS="000" \
    MOCK_SERVICE_ACTIVE_EXIT="1" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_7a.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_7a" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_7a/labbit-connector" \
    LABBIT_SYSTEMD_SERVICE_FILE="${TEST_ROOT}/systemd_7/svc_a.service" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)
  if ! echo "$out_uncertain" | grep -q "outcome is UNCERTAIN" || ! echo "$out_uncertain" | grep -q "DO NOT blindly retry"; then
    echo "FAIL: Expected UNCERTAIN outcome warning on transport failure, got: $out_uncertain"
    exit 1
  fi

  # 7B: Explicit 401 must report invalid_enrollment_token rejection
  printf 'one-time-enroll-token-777\n' > "$mock_tty"
  local out_401
  out_401=$(PATH="${fake_bin}:${PATH}" \
    MOCK_ENROLL_HTTP_STATUS="401" \
    MOCK_SERVICE_ACTIVE_EXIT="1" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_7b.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_7b" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_7b/labbit-connector" \
    LABBIT_SYSTEMD_SERVICE_FILE="${TEST_ROOT}/systemd_7/svc_b.service" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)
  if ! echo "$out_401" | grep -q "invalid_enrollment_token"; then
    echo "FAIL: Expected invalid_enrollment_token message on HTTP 401, got: $out_401"
    exit 1
  fi

  # 7C: Full 201 Created Happy Path
  # After systemctl restart, systemctl is-active should return 0; update systemctl mock for 7C
  cat <<'EOF' > "${fake_bin}/systemctl"
#!/bin/sh
echo "systemctl $*" >> "${MOCK_LOG_FILE:-/dev/null}"
if [ "${1:-}" = "restart" ]; then
  touch "${MOCK_SERVICE_STARTED_FLAG}"
  exit 0
fi
if [ "${1:-}" = "is-active" ]; then
  if [ -f "${MOCK_SERVICE_STARTED_FLAG:-}" ]; then exit 0; fi
  exit 1
fi
exit 0
EOF
  chmod +x "${fake_bin}/systemctl"

  printf 'one-time-enroll-token-777\n' > "$mock_tty"
  local out_201
  out_201=$(PATH="${fake_bin}:${PATH}" \
    MOCK_LOG_FILE="$mock_log" \
    MOCK_ENROLL_HTTP_STATUS="201" \
    MOCK_SERVICE_STARTED_FLAG="${TEST_ROOT}/service_started_7c" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_SAAS_BASE_URL="https://saas.labbit.example.com" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_7c.d" \
    LABBIT_CONFIG_DIR="$mock_config" \
    LABBIT_INSTALL_BIN="$mock_bin" \
    LABBIT_SYSTEMD_SERVICE_FILE="$mock_service" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1)

  # Verify request JSON matched {"enrollmentToken":"one-time-enroll-token-777"}
  if [ "$(cat "$captured_req")" != '{"enrollmentToken":"one-time-enroll-token-777"}' ]; then
    echo "FAIL: Unexpected enrollment request JSON: $(cat "$captured_req")"
    exit 1
  fi

  # Verify saved connector.id and credential contents
  if [ "$(cat "${mock_config}/connector.id")" != "conn-e2e-201" ]; then
    echo "FAIL: Expected connector.id=conn-e2e-201, got: $(cat "${mock_config}/connector.id")"
    exit 1
  fi
  if [ "$(cat "${mock_config}/credential")" != "cred-secret-xyz-201" ]; then
    echo "FAIL: Expected credential=cred-secret-xyz-201, got: $(cat "${mock_config}/credential")"
    exit 1
  fi

  # Verify secret credential and enrollment token are never leaked into stdout/stderr or connector.env
  if echo "$out_201" | grep -q "cred-secret-xyz-201" || echo "$out_201" | grep -q "one-time-enroll-token-777"; then
    echo "FAIL: Secret token or credential leaked in installer output!"
    exit 1
  fi
  if grep -q "cred-secret-xyz-201" "${mock_config}/connector.env" || grep -q "one-time-enroll-token-777" "${mock_config}/connector.env"; then
    echo "FAIL: Secret token or credential leaked into connector.env!"
    exit 1
  fi

  # Verify SSH key pair, known_hosts, and systemd service file were created
  if [ ! -f "${mock_config}/ssh/id_ed25519" ] || [ ! -f "${mock_config}/ssh/id_ed25519.pub" ] || [ ! -f "${mock_config}/ssh/known_hosts" ]; then
    echo "FAIL: Expected SSH management key pair and known_hosts to be initialized."
    exit 1
  fi
  if ! grep -q "User=labbit" "$mock_service" || ! grep -q "EnvironmentFile=${mock_config}/connector.env" "$mock_service"; then
    echo "FAIL: systemd service unit missing expected directives."
    exit 1
  fi
  # Verify ownership commands were invoked for root:labbit (config dir & connector.id) and labbit:labbit (credential & ssh)
  if ! grep -q "chown root:labbit ${mock_config}" "$mock_log" || ! grep -q "chown labbit:labbit ${mock_config}/ssh" "$mock_log"; then
    echo "FAIL: Expected chown ownership commands not found in log: $(cat "$mock_log")"
    exit 1
  fi
  if [ -d "${TEST_ROOT}/lock_7c.d" ]; then
    echo "FAIL: Lock directory was not cleaned up by owning process on exit!"
    exit 1
  fi

  echo "PASS: Full 201 Enrollment happy path, SHA256SUMS verification, file layout, and uncertain outcome handling verified."
}

# Test 8: Runbook Manual Upgrade SHA256SUMS verification command works in isolated shell
test_runbook_upgrade_checksum_command() {
  echo "--- Test 8: Runbook upgrade SHA256SUMS verification command check ---"
  local upgrade_dir="${TEST_ROOT}/upgrade_8"
  mkdir -p "$upgrade_dir"
  printf 'new-connector-binary-v0.2.0\n' > "${upgrade_dir}/labbit-connector"
  local bin_sha
  bin_sha="$(sha256sum "${upgrade_dir}/labbit-connector" | awk '{print $1}')"
  # SHA256SUMS contains both labbit-connector and the .tar.gz archive (which is NOT downloaded during binary upgrade)
  cat <<EOF > "${upgrade_dir}/SHA256SUMS"
${bin_sha}  labbit-connector
1111111111111111111111111111111111111111111111111111111111111111  labbit-connector-connector-v0.2.0-linux-amd64.tar.gz
EOF

  (
    cd "${upgrade_dir}"
    awk '$2 == "labbit-connector" || $2 == "*labbit-connector"' SHA256SUMS | sha256sum --check --strict >/dev/null
  )
  echo "PASS: Runbook upgrade SHA256SUMS verification command succeeded."
}

# Test 9: SIGTERM signal trap termination and lock cleanup regression check
test_sigterm_termination_and_lock_cleanup() {
  echo "--- Test 9: SIGTERM signal trap termination and lock cleanup ---"
  local fake_bin="${TEST_ROOT}/bin_9"
  setup_base_mocks "$fake_bin"

  local sync_file="${TEST_ROOT}/sigterm_wait_ready"
  local post_signal_marker="${TEST_ROOT}/sigterm_post_marker"
  rm -f "$sync_file" "$post_signal_marker"

  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
url=""
out=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  */labbit-connector)
    touch "${sync_file}"
    # Sleep to allow test runner to send SIGTERM while curl is active
    sleep 5
    printf 'mock-connector-binary\n' > "\$out"
    exit 0
    ;;
esac
exit 1
EOF
  chmod +x "${fake_bin}/curl"

  # Mock cp: if installer continues execution despite SIGTERM, this marker file would be created
  cat <<EOF > "${fake_bin}/cp"
#!/bin/sh
touch "${post_signal_marker}"
exit 0
EOF
  chmod +x "${fake_bin}/cp"

  local mock_run="${TEST_ROOT}/run_9/systemd/system"
  mkdir -p "$mock_run"
  local mock_lock="${TEST_ROOT}/lock_9.d"
  local mock_config="${TEST_ROOT}/etc_9"
  local mock_bin="${TEST_ROOT}/usr_9/labbit-connector"
  local mock_tty="${TEST_ROOT}/tty_9"
  printf 'dummy-token\n' > "$mock_tty"

  # Run installer in background
  PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="$mock_lock" \
    LABBIT_CONFIG_DIR="$mock_config" \
    LABBIT_INSTALL_BIN="$mock_bin" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" > "${TEST_ROOT}/sigterm.log" 2>&1 &
  local installer_pid=$!

  # Wait until curl starts and lock is acquired
  local elapsed=0
  while [ ! -f "$sync_file" ] || [ ! -d "$mock_lock" ]; do
    sleep 0.05
    elapsed=$((elapsed + 1))
    if [ "$elapsed" -gt 80 ]; then
      echo "FAIL: Timed out waiting for installer to acquire lock and start download."
      kill -9 "$installer_pid" 2>/dev/null || true
      exit 1
    fi
  done

  # Lock must be acquired by installer
  if [ ! -d "$mock_lock" ]; then
    echo "FAIL: Expected lock directory $mock_lock to exist while running."
    exit 1
  fi

  # Send SIGTERM to the installer process
  kill -TERM "$installer_pid"

  # Wait for installer process to terminate and capture exit status
  local term_exit=0
  wait "$installer_pid" || term_exit=$?

  # Check that exit status reflects SIGTERM (143)
  if [ "$term_exit" -ne 143 ] && [ "$term_exit" -eq 0 ]; then
    echo "FAIL: Expected non-zero/143 exit code on SIGTERM, got $term_exit"
    exit 1
  fi

  # Verify lock directory was cleaned up
  if [ -d "$mock_lock" ]; then
    echo "FAIL: Lock directory $mock_lock was NOT cleaned up after SIGTERM!"
    exit 1
  fi

  # Verify execution stopped and did not proceed to subsequent commands
  if [ -f "$post_signal_marker" ]; then
    echo "FAIL: Installer continued execution after SIGTERM (post_signal_marker was created)!"
    exit 1
  fi

  # Verify log mentions signal interruption
  if ! grep -q "interrupted by signal TERM" "${TEST_ROOT}/sigterm.log"; then
    echo "FAIL: Expected 'interrupted by signal TERM' in log: $(cat "${TEST_ROOT}/sigterm.log")"
    exit 1
  fi

  echo "PASS: SIGTERM termination and lock cleanup verified."
}

# Test 10: Credential save failure after enrollment recovery guidance check
test_credential_save_failure_recovery_guidance() {
  echo "--- Test 10: Credential save failure recovery guidance check ---"
  local fake_bin="${TEST_ROOT}/bin_10"
  setup_base_mocks "$fake_bin"

  local dummy_bin_content="binary-payload-10-save-failure"
  local expected_sha
  expected_sha="$(printf '%s' "$dummy_bin_content" | sha256sum | awk '{print $1}')"

  local captured_req="${TEST_ROOT}/req_10.json"
  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
url=""
out=""
data_file=""
write_code=0
while [ \$# -gt 0 ]; do
  case "\$1" in
    -d)
      if [ "\${2#@}" != "\$2" ]; then
        data_file="\${2#@}"
      fi
      shift 2
      ;;
    -o) out="\$2"; shift 2 ;;
    -w) write_code=1; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  */SHA256SUMS)
    printf '%s  labbit-connector\n' "${expected_sha}" > "\$out"
    exit 0
    ;;
  */labbit-connector)
    printf '%s' "${dummy_bin_content}" > "\$out"
    exit 0
    ;;
  */api/v1/connector-enrollments)
    # SaaS consumed token and responded 201 Created successfully
    printf '{"connectorId":"conn-save-fail-789","connectorCredential":"cred-secret-consumed-999"}\n' > "\$out"
    if [ "\$write_code" -eq 1 ]; then
      printf '201'
    fi
    exit 0
    ;;
esac
exit 1
EOF
  chmod +x "${fake_bin}/curl"

  # Override mktemp to fail when creating temp file in CONFIG_DIR to simulate filesystem write failure
  cat <<'EOF' > "${fake_bin}/mktemp"
#!/bin/sh
for arg in "$@"; do
  if echo "$arg" | grep -q "etc_10"; then
    # Simulate write failure in config dir after enrollment
    exit 1
  fi
done
command -p mktemp "$@"
EOF
  chmod +x "${fake_bin}/mktemp"

  local mock_run="${TEST_ROOT}/run_10/systemd/system"
  mkdir -p "$mock_run"
  local mock_lock="${TEST_ROOT}/lock_10.d"
  local mock_config="${TEST_ROOT}/etc_10"
  local mock_bin="${TEST_ROOT}/usr_10/labbit-connector"
  local mock_service="${TEST_ROOT}/systemd_10/svc.service"
  local mock_tty="${TEST_ROOT}/tty_10"

  printf 'token-will-be-consumed\n' > "$mock_tty"
  local out_fail
  out_fail=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_SAAS_BASE_URL="https://saas.labbit.example.com" \
    LABBIT_LOCK_DIR="$mock_lock" \
    LABBIT_CONFIG_DIR="$mock_config" \
    LABBIT_INSTALL_BIN="$mock_bin" \
    LABBIT_SYSTEMD_SERVICE_FILE="$mock_service" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)

  # 1. Output must report failure to save credentials with notice that token was already consumed
  if ! echo "$out_fail" | grep -q "Failed to securely save Connector credentials.*Token was already consumed"; then
    echo "FAIL: Expected 'Failed to securely save Connector credentials (Token was already consumed)', got: $out_fail"
    exit 1
  fi

  # 2. Output must provide explicit recovery guidance: check list -> revoke -> issue new token
  if ! echo "$out_fail" | grep -q "Check the Connector list in the Admin Web Console" || \
     ! echo "$out_fail" | grep -q "revoke this Connector" || \
     ! echo "$out_fail" | grep -q "conn-save-fail-789"; then
    echo "FAIL: Expected recovery guidance with Connector ID in output, got: $out_fail"
    exit 1
  fi

  # 3. Credential secret must NEVER be leaked in output
  if echo "$out_fail" | grep -q "cred-secret-consumed-999"; then
    echo "FAIL: Secret credential leaked in error output!"
    exit 1
  fi

  # 4. Also test failure when connector.id succeeds but credential atomic move fails
  rm -f "${fake_bin}/mktemp"
  cat <<'EOF' > "${fake_bin}/mv"
#!/bin/sh
for arg in "$@"; do
  if echo "$arg" | grep -q "/credential$"; then
    # Simulate atomic move failure specifically for credential file
    exit 1
  fi
done
command -p mv "$@"
EOF
  chmod +x "${fake_bin}/mv"

  printf 'token-will-be-consumed-2\n' > "$mock_tty"
  local out_fail_cred
  out_fail_cred=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_SAAS_BASE_URL="https://saas.labbit.example.com" \
    LABBIT_LOCK_DIR="$mock_lock" \
    LABBIT_CONFIG_DIR="$mock_config" \
    LABBIT_INSTALL_BIN="$mock_bin" \
    LABBIT_SYSTEMD_SERVICE_FILE="$mock_service" \
    LABBIT_CONNECTOR_EXPECTED_SHA256="$expected_sha" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$out_fail_cred" | grep -q "Failed to securely save Connector credentials.*Token was already consumed" || \
     ! echo "$out_fail_cred" | grep -q "Revoke(폐기) → 신규 Token 발급"; then
    echo "FAIL: Expected recovery guidance on credential move failure, got: $out_fail_cred"
    exit 1
  fi

  if [ -f "${mock_config}/connector.id" ] || [ -f "${mock_config}/credential" ]; then
    echo "FAIL: Partial connector.id or credential file was left behind after credential save failure!"
    exit 1
  fi

  echo "PASS: Credential save failure recovery guidance and secret scrubbing verified."
}


# --- Test 11: Insecure HTTP SaaS URL rejection (HTTPS enforcement) ---
test_insecure_http_url_rejection() {
  echo "--- Test 11: Insecure HTTP SaaS URL rejection (HTTPS enforcement) ---"
  local fake_bin="${TEST_ROOT}/bin_11"
  setup_base_mocks "$fake_bin"

  local mock_run="${TEST_ROOT}/run_11/systemd/system"
  mkdir -p "$mock_run"

  # 1. Plain HTTP URL must be rejected immediately
  local out_http
  out_http=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_SAAS_BASE_URL="http://insecure-saas.labbit.example.com" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$out_http" | grep -q "Insecure HTTP protocol is not permitted"; then
    echo "FAIL: Expected error for http:// SaaS URL, got: $out_http"
    exit 1
  fi

  # 2. Invalid non-https URL scheme (e.g. ftp:// or arbitrary string) must also be rejected
  local out_invalid
  out_invalid=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_SAAS_BASE_URL="ftp://insecure-saas.labbit.example.com" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$out_invalid" | grep -q "Invalid LABBIT_SAAS_BASE_URL scheme"; then
    echo "FAIL: Expected scheme error for ftp:// SaaS URL, got: $out_invalid"
    exit 1
  fi

  echo "PASS: Insecure HTTP protocol and non-https schemes correctly rejected."
}

# --- Test 12: Approved Release SHA256 Pinned Verification & Attack Defense ---
test_default_approved_sha256_verification() {
  echo "--- Test 12: Approved Release SHA256 Pinned Verification & Attack Defense ---"
  local fake_bin="${TEST_ROOT}/bin_12"
  setup_base_mocks "$fake_bin"

  local approved_sha="742ac899d880b538ea56161394a857dc52bd4aa954858db0425073c039ad7346"
  local mock_run="${TEST_ROOT}/run_12/systemd/system"
  mkdir -p "$mock_run"
  local mock_tty="${TEST_ROOT}/tty_12"
  # Provide empty token so installer safely stops at Step 3 after installing binary
  printf '\n' > "$mock_tty"

  # Case 12A: Approved binary and matching release SHA256SUMS succeeds in default execution
  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
out=""
url=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  */SHA256SUMS)
    printf '%s  labbit-connector\n' "$approved_sha" > "\$out"
    exit 0
    ;;
  */labbit-connector)
    printf 'approved-binary-payload' > "\$out"
    exit 0
    ;;
esac
exit 0
EOF
  chmod +x "${fake_bin}/curl"

  cat <<EOF > "${fake_bin}/sha256sum"
#!/bin/sh
echo "${approved_sha}  \$1"
exit 0
EOF
  chmod +x "${fake_bin}/sha256sum"

  # Run installer with default parameters (no LABBIT_CONNECTOR_EXPECTED_SHA256 override)
  local out_success
  out_success=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_12a.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_12a" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_12a/labbit-connector" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$out_success" | grep -q "SHA256 verified successfully against approved pinned checksum"; then
    echo "FAIL: Expected approved checksum verification success in default execution, got: $out_success"
    exit 1
  fi
  if [ ! -f "${TEST_ROOT}/usr_12a/labbit-connector" ]; then
    echo "FAIL: Approved binary was NOT installed to INSTALL_BIN!"
    exit 1
  fi
  echo "PASS: Case 12A - Approved pinned checksum successfully verified and binary installed in default execution."

  # Case 12B: Tampered binary with legitimate release SHA256SUMS fails on release SHA256SUMS check
  cat <<EOF > "${fake_bin}/sha256sum"
#!/bin/sh
echo "0000000000000000000000000000000000000000000000000000000000000000  \$1"
exit 0
EOF
  chmod +x "${fake_bin}/sha256sum"

  local out_mismatch_single
  out_mismatch_single=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_12b.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_12b" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_12b/labbit-connector" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1 || true)

  if ! echo "$out_mismatch_single" | grep -q "SHA256 mismatch against release SHA256SUMS"; then
    echo "FAIL: Expected release SHA256SUMS mismatch failure, got: $out_mismatch_single"
    exit 1
  fi
  if [ -f "${TEST_ROOT}/usr_12b/labbit-connector" ]; then
    echo "FAIL: Tampered binary was installed!"
    exit 1
  fi
  echo "PASS: Case 12B - Tampered binary aborted on release SHA256SUMS check."

  # Case 12C: ATTACK SCENARIO - Attacker replaced BOTH binary AND SHA256SUMS on GitHub release!
  # Real sha256sum calculates actual hash of tampered binary, release SHA256SUMS matches it,
  # but installer aborts because it does NOT match approved pinned checksum.
  rm -f "${fake_bin}/sha256sum"
  local tampered_content="tampered-payload-attack-vector"
  local tampered_real_sha
  tampered_real_sha=$(printf '%s' "$tampered_content" | sha256sum | awk '{print $1}')

  cat <<EOF > "${fake_bin}/curl"
#!/bin/sh
out=""
url=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out="\$2"; shift 2 ;;
    http*) url="\$1"; shift ;;
    *) shift ;;
  esac
done
case "\$url" in
  */SHA256SUMS)
    # Attacker modified SHA256SUMS to match the real sha256 of tampered binary
    printf '%s  labbit-connector\n' "$tampered_real_sha" > "\$out"
    exit 0
    ;;
  */labbit-connector)
    printf '%s' "$tampered_content" > "\$out"
    exit 0
    ;;
esac
exit 0
EOF
  chmod +x "${fake_bin}/curl"

  local out_attack
  local attack_status=0
  out_attack=$(PATH="${fake_bin}:${PATH}" \
    SYSTEMD_RUN_DIR="$mock_run" \
    LABBIT_LOCK_DIR="${TEST_ROOT}/lock_12c.d" \
    LABBIT_CONFIG_DIR="${TEST_ROOT}/etc_12c" \
    LABBIT_INSTALL_BIN="${TEST_ROOT}/usr_12c/labbit-connector" \
    LABBIT_TTY_DEV="$mock_tty" \
    sh "$INSTALLER" 2>&1) || attack_status=$?

  if [ "$attack_status" -eq 0 ]; then
    echo "FAIL: Installer exited successfully despite approved SHA256 mismatch. Output: $out_attack"
    exit 1
  fi
  if ! echo "$out_attack" | grep -q "SHA256 mismatch against approved release checksum"; then
    echo "FAIL: Expected approved checksum mismatch abort when both binary and SHA256SUMS are modified, got: $out_attack"
    exit 1
  fi
  if [ -f "${TEST_ROOT}/usr_12c/labbit-connector" ]; then
    echo "FAIL: Tampered binary was installed despite approved pinned checksum mismatch!"
    exit 1
  fi
  echo "PASS: Case 12C - Dual-tampering attack defense verified (aborted on approved pinned checksum mismatch)."
}
test_non_root
test_unsupported_os_and_arch
test_concurrent_lock_ownership
test_checksum_404_fail_closed
test_tty_and_token_security
test_existing_identity_and_service_protection
test_full_enrollment_201_and_uncertain_outcome
test_runbook_upgrade_checksum_command
test_sigterm_termination_and_lock_cleanup
test_credential_save_failure_recovery_guidance
test_insecure_http_url_rejection
test_default_approved_sha256_verification

echo "==> All install.sh behavioral & regression tests passed successfully!"
