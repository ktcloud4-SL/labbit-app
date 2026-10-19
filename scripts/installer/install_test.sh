#!/bin/bash
# Test suite for scripts/installer/install.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALLER="${SCRIPT_DIR}/install.sh"
TEST_ROOT="$(mktemp -d)"

cleanup() {
  rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

echo "==> Running install.sh test suite..."

# Test 1: Non-root execution must fail
test_non_root() {
  echo "--- Test 1: Non-root execution check ---"
  # Run in subshell overriding id command
  FAKE_BIN="${TEST_ROOT}/fake_bin_1"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "1000"
EOF
  chmod +x "${FAKE_BIN}/id"

  OUTPUT=$(PATH="${FAKE_BIN}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "must be run as root"; then
    echo "PASS: Non-root execution correctly rejected."
  else
    echo "FAIL: Expected non-root rejection, got: $OUTPUT"
    exit 1
  fi
}

# Test 2: Unsupported OS must fail
test_unsupported_os() {
  echo "--- Test 2: Unsupported OS check ---"
  FAKE_BIN="${TEST_ROOT}/fake_bin_2"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "0"
EOF
  cat <<'EOF' > "${FAKE_BIN}/uname"
#!/bin/sh
if [ "$1" = "-s" ]; then echo "Darwin"; else echo "x86_64"; fi
EOF
  chmod +x "${FAKE_BIN}/id" "${FAKE_BIN}/uname"

  OUTPUT=$(PATH="${FAKE_BIN}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "Unsupported Operating System"; then
    echo "PASS: Unsupported OS correctly rejected."
  else
    echo "FAIL: Expected OS rejection, got: $OUTPUT"
    exit 1
  fi
}

# Test 3: Unsupported Architecture must fail
test_unsupported_arch() {
  echo "--- Test 3: Unsupported Architecture check ---"
  FAKE_BIN="${TEST_ROOT}/fake_bin_3"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "0"
EOF
  cat <<'EOF' > "${FAKE_BIN}/uname"
#!/bin/sh
if [ "$1" = "-s" ]; then echo "Linux"; else echo "aarch64"; fi
EOF
  chmod +x "${FAKE_BIN}/id" "${FAKE_BIN}/uname"

  OUTPUT=$(PATH="${FAKE_BIN}:${PATH}" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "Unsupported Architecture"; then
    echo "PASS: Unsupported Architecture correctly rejected."
  else
    echo "FAIL: Expected arch rejection, got: $OUTPUT"
    exit 1
  fi
}

# Test 4: Missing systemd check
test_missing_systemd() {
  echo "--- Test 4: Missing systemd check ---"
  FAKE_BIN="${TEST_ROOT}/fake_bin_4"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "0"
EOF
  cat <<'EOF' > "${FAKE_BIN}/uname"
#!/bin/sh
if [ "$1" = "-s" ]; then echo "Linux"; else echo "x86_64"; fi
EOF
  chmod +x "${FAKE_BIN}/id" "${FAKE_BIN}/uname"

  # No systemctl in fake bin
  OUTPUT=$(PATH="${FAKE_BIN}:/usr/bin:/bin" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "systemd is not running"; then
    echo "PASS: Missing systemd correctly rejected."
  else
    echo "FAIL: Expected systemd rejection, got: $OUTPUT"
    exit 1
  fi
}

# Test 5: Checksum mismatch must abort before installation
test_checksum_mismatch() {
  echo "--- Test 5: SHA256 checksum mismatch aborts ---"
  FAKE_BIN="${TEST_ROOT}/fake_bin_5"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "0"
EOF
  cat <<'EOF' > "${FAKE_BIN}/uname"
#!/bin/sh
if [ "$1" = "-s" ]; then echo "Linux"; else echo "x86_64"; fi
EOF
  cat <<'EOF' > "${FAKE_BIN}/systemctl"
#!/bin/sh
exit 0
EOF
  cat <<'EOF' > "${FAKE_BIN}/curl"
#!/bin/sh
# Output dummy binary content to -o target
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    echo "dummy-binary-content" > "$2"
    exit 0
  fi
  shift
done
exit 0
EOF
  chmod +x "${FAKE_BIN}/id" "${FAKE_BIN}/uname" "${FAKE_BIN}/systemctl" "${FAKE_BIN}/curl"

  # Create mock /run/systemd/system
  MOCK_RUN="${TEST_ROOT}/mock_run_5"
  mkdir -p "${MOCK_RUN}/systemd/system"
  MOCK_LOCK="${TEST_ROOT}/mock_lock_5"

  OUTPUT=$(PATH="${FAKE_BIN}:${PATH}" SYSTEMD_RUN_DIR="${MOCK_RUN}/systemd/system" LABBIT_LOCK_DIR="$MOCK_LOCK" LABBIT_CONNECTOR_EXPECTED_SHA256="0000000000000000000000000000000000000000000000000000000000000000" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "SHA256 mismatch"; then
    echo "PASS: SHA256 mismatch correctly aborted."
  else
    echo "FAIL: Expected SHA256 mismatch error, got: $OUTPUT"
    exit 1
  fi
}

# Test 6: Preserves existing credentials when already present
test_existing_credentials_preserved() {
  echo "--- Test 6: Existing credential preservation ---"
  FAKE_BIN="${TEST_ROOT}/fake_bin_6"
  mkdir -p "$FAKE_BIN"
  cat <<'EOF' > "${FAKE_BIN}/id"
#!/bin/sh
echo "0"
EOF
  cat <<'EOF' > "${FAKE_BIN}/uname"
#!/bin/sh
if [ "$1" = "-s" ]; then echo "Linux"; else echo "x86_64"; fi
EOF
  cat <<'EOF' > "${FAKE_BIN}/systemctl"
#!/bin/sh
if [ "$1" = "is-active" ]; then exit 0; fi
exit 0
EOF
  chmod +x "${FAKE_BIN}/id" "${FAKE_BIN}/uname" "${FAKE_BIN}/systemctl"

  MOCK_RUN="${TEST_ROOT}/mock_run_6"
  mkdir -p "${MOCK_RUN}/systemd/system"
  MOCK_LOCK="${TEST_ROOT}/mock_lock_6"
  MOCK_CONFIG="${TEST_ROOT}/mock_etc_6"
  mkdir -p "$MOCK_CONFIG"
  echo "existing-secret" > "${MOCK_CONFIG}/credential"

  OUTPUT=$(PATH="${FAKE_BIN}:${PATH}" SYSTEMD_RUN_DIR="${MOCK_RUN}/systemd/system" LABBIT_LOCK_DIR="$MOCK_LOCK" LABBIT_CONFIG_DIR="$MOCK_CONFIG" sh "$INSTALLER" 2>&1 || true)
  if echo "$OUTPUT" | grep -q "Existing Connector credentials found"; then
    echo "PASS: Existing credentials detected and preserved."
  else
    echo "FAIL: Expected existing credentials message, got: $OUTPUT"
    exit 1
  fi
}

# Test 7: Enrollment API contract uses enrollmentToken request and connectorCredential response (LBT-182)
test_enrollment_api_contract() {
  echo "--- Test 7: LBT-182 Enrollment API contract check ---"
  if grep -q '{"enrollmentToken":"%s"}' "$INSTALLER" && grep -q 'parse_json "connectorId"' "$INSTALLER" && grep -q 'parse_json "connectorCredential"' "$INSTALLER"; then
    echo "PASS: Enrollment request/response fields match LBT-182 OpenAPI contract."
  else
    echo "FAIL: Installer enrollment fields do not match LBT-182 OpenAPI contract"
    exit 1
  fi
}

test_non_root
test_unsupported_os
test_unsupported_arch
test_missing_systemd
test_checksum_mismatch
test_existing_credentials_preserved
test_enrollment_api_contract

echo "==> All install.sh tests passed successfully!"
