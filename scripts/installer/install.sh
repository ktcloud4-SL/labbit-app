#!/bin/sh
# Labbit Connector Official HTTPS Installer
# Target OS: Ubuntu 24.04+ Linux/amd64
# Usage: curl -fsSL https://.../install.sh | sudo sh
set -eu

# Configuration defaults
DEFAULT_SAAS_BASE_URL="https://api.labbit.example.com"
LABBIT_SAAS_BASE_URL="${LABBIT_SAAS_BASE_URL:-$DEFAULT_SAAS_BASE_URL}"
CONFIG_DIR="${LABBIT_CONFIG_DIR:-/etc/labbit-connector}"
INSTALL_BIN="${LABBIT_INSTALL_BIN:-/usr/local/bin/labbit-connector}"
SYSTEMD_SERVICE_FILE="${LABBIT_SYSTEMD_SERVICE_FILE:-/etc/systemd/system/labbit-connector.service}"
SYSTEMD_RUN_DIR="${SYSTEMD_RUN_DIR:-/run/systemd/system}"
LOCK_DIR="${LABBIT_LOCK_DIR:-/run/lock/labbit-connector-installer.lock.d}"
CONNECTOR_USER="${LABBIT_CONNECTOR_USER:-labbit}"
CONNECTOR_GROUP="${LABBIT_CONNECTOR_GROUP:-labbit}"

# Release Configuration
DEFAULT_RELEASE_TAG="connector-v0.1.0"
RELEASE_TAG="${LABBIT_CONNECTOR_RELEASE_TAG:-$DEFAULT_RELEASE_TAG}"
EXPECTED_SHA256="${LABBIT_CONNECTOR_EXPECTED_SHA256:-}"
GITHUB_REPO="${LABBIT_GITHUB_REPO:-ktcloud4-SL/labbit-app}"
RELEASE_BASE_URL="https://github.com/${GITHUB_REPO}/releases/download/${RELEASE_TAG}"

LOCK_ACQUIRED=0
SAVED_STTY=""
TTY_DEV="${LABBIT_TTY_DEV:-/dev/tty}"
ID_TMP=""
CRED_TMP=""
ENV_TMP=""

cleanup() {
  if [ -n "${SAVED_STTY:-}" ] && [ -n "${TTY_DEV:-}" ]; then
    stty "$SAVED_STTY" < "$TTY_DEV" 2>/dev/null || stty echo < "$TTY_DEV" 2>/dev/null || true
  fi
  if [ "${LOCK_ACQUIRED:-0}" = "1" ] && [ -d "$LOCK_DIR" ]; then
    rmdir "$LOCK_DIR" 2>/dev/null || true
  fi
  if [ -n "${ENROLL_TMP:-}" ] && [ -f "$ENROLL_TMP" ]; then
    rm -f "$ENROLL_TMP" 2>/dev/null || true
  fi
  if [ -n "${HTTP_RESPONSE_FILE:-}" ] && [ -f "$HTTP_RESPONSE_FILE" ]; then
    rm -f "$HTTP_RESPONSE_FILE" 2>/dev/null || true
  fi
  if [ -n "${DOWNLOAD_TMP:-}" ] && [ -f "$DOWNLOAD_TMP" ]; then
    rm -f "$DOWNLOAD_TMP" 2>/dev/null || true
  fi
  if [ -n "${CHECKSUMS_TMP:-}" ] && [ -f "$CHECKSUMS_TMP" ]; then
    rm -f "$CHECKSUMS_TMP" 2>/dev/null || true
  fi
  if [ -n "${ID_TMP:-}" ] && [ -f "$ID_TMP" ]; then
    rm -f "$ID_TMP" 2>/dev/null || true
  fi
  if [ -n "${CRED_TMP:-}" ] && [ -f "$CRED_TMP" ]; then
    rm -f "$CRED_TMP" 2>/dev/null || true
  fi
  if [ -n "${ENV_TMP:-}" ] && [ -f "$ENV_TMP" ]; then
    rm -f "$ENV_TMP" 2>/dev/null || true
  fi
}

on_signal() {
  sig="$1"
  trap - EXIT INT TERM
  cleanup
  echo "::error::Installer interrupted by signal ${sig}." >&2
  case "$sig" in
    INT)  exit 130 ;;
    TERM) exit 143 ;;
    *)    exit 1 ;;
  esac
}

trap cleanup EXIT
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM

# -------------------------------------------------------------
# 1. Pre-flight Checks (System, Privileges, Dependencies, TTY)
# -------------------------------------------------------------
echo "==> [1/6] Running pre-flight system checks..."

# Check Root Privileges
if [ "$(id -u)" -ne 0 ]; then
  echo "::error::This installer must be run as root. Please run with sudo or as root user." >&2
  exit 1
fi

# Check OS (Linux only)
OS_TYPE="$(uname -s)"
if [ "$OS_TYPE" != "Linux" ]; then
  echo "::error::Unsupported Operating System: $OS_TYPE. Labbit Connector requires Linux (e.g. Ubuntu 24.04)." >&2
  exit 1
fi

# Check Architecture (amd64 / x86_64 only)
ARCH_TYPE="$(uname -m)"
case "$ARCH_TYPE" in
  x86_64|amd64) ;;
  *)
    echo "::error::Unsupported Architecture: $ARCH_TYPE. Labbit Connector currently supports linux/amd64 only." >&2
    exit 1
    ;;
esac

# Check systemd availability
if ! command -v systemctl >/dev/null 2>&1 || [ ! -d "$SYSTEMD_RUN_DIR" ]; then
  echo "::error::systemd is not running. Labbit Connector requires systemd as the init system." >&2
  exit 1
fi

# Check required utilities
for cmd in curl sha256sum ssh-keygen stty; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "::error::Required utility '$cmd' is not installed. Please install it and retry." >&2
    exit 1
  fi
done

# Check secure JSON parser availability
if ! command -v python3 >/dev/null 2>&1 && ! command -v jq >/dev/null 2>&1; then
  echo "::error::Neither python3 nor jq is available. A secure JSON parser is required." >&2
  exit 1
fi

# Reject environment variable token injection (strictly forbidden by security contract)
if [ -n "${LABBIT_ENROLLMENT_TOKEN:-}" ]; then
  echo "::error::Passing enrollment token via LABBIT_ENROLLMENT_TOKEN environment variable is prohibited." >&2
  echo "        Unset LABBIT_ENROLLMENT_TOKEN and enter the token interactively via /dev/tty." >&2
  exit 1
fi

# Single execution lock (only the acquiring process may remove LOCK_DIR on exit)
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  echo "::error::Another instance of the Labbit Connector installer is currently running ($LOCK_DIR)." >&2
  exit 1
fi
LOCK_ACQUIRED=1

# Protect existing credentials, connector.id, and service state without restarting or overwriting
EXISTING_CRED=0
EXISTING_ID=0
EXISTING_UNIT=0
SERVICE_ACTIVE=0
if [ -f "$CONFIG_DIR/credential" ] && [ -s "$CONFIG_DIR/credential" ]; then
  EXISTING_CRED=1
fi
if [ -f "$CONFIG_DIR/connector.id" ] && [ -s "$CONFIG_DIR/connector.id" ]; then
  EXISTING_ID=1
fi
if [ -f "$SYSTEMD_SERVICE_FILE" ]; then
  EXISTING_UNIT=1
fi
if systemctl is-active --quiet labbit-connector.service 2>/dev/null; then
  SERVICE_ACTIVE=1
fi

if [ "$EXISTING_CRED" -eq 1 ] || [ "$EXISTING_ID" -eq 1 ] || [ "$EXISTING_UNIT" -eq 1 ] || [ "$SERVICE_ACTIVE" -eq 1 ]; then
  echo "::error::Existing Connector credentials or identity found (credential=${EXISTING_CRED}, connector.id=${EXISTING_ID}, service_unit=${EXISTING_UNIT}, active=${SERVICE_ACTIVE})." >&2
  echo "        Existing credentials, connector.id, and service state are preserved without modification or automatic restart." >&2
  echo "        To upgrade the binary or manage the service, consult the manual upgrade runbook." >&2
  exit 1
fi

# Verify interactive TTY accessibility and stty capability BEFORE downloading or altering anything
if [ ! -r "$TTY_DEV" ] || [ ! -w "$TTY_DEV" ]; then
  echo "::error::Interactive TTY ($TTY_DEV) is not accessible. Enrollment requires interactive token input via /dev/tty." >&2
  exit 1
fi
if ! stty -g < "$TTY_DEV" >/dev/null 2>&1; then
  echo "::error::Interactive TTY ($TTY_DEV) does not support stty terminal control. Cannot safely mask token input." >&2
  exit 1
fi

# -------------------------------------------------------------
# 2. Download Binary & Checksum Verification (Fail-Closed)
# -------------------------------------------------------------
echo "==> [2/6] Downloading Labbit Connector binary (${RELEASE_TAG})..."
DOWNLOAD_TMP="$(mktemp)"
chmod 0600 "$DOWNLOAD_TMP"

BINARY_URL="${RELEASE_BASE_URL}/labbit-connector"
if ! curl -fsSL "$BINARY_URL" -o "$DOWNLOAD_TMP"; then
  echo "::error::Failed to download binary from $BINARY_URL." >&2
  exit 1
fi

echo "==> [2/6] Verifying binary integrity (SHA256)..."
ACTUAL_SHA256="$(sha256sum "$DOWNLOAD_TMP" | awk '{print $1}')"

if [ -n "$EXPECTED_SHA256" ]; then
  if [ "$ACTUAL_SHA256" != "$EXPECTED_SHA256" ]; then
    echo "::error::SHA256 mismatch! Expected: $EXPECTED_SHA256, Got: $ACTUAL_SHA256." >&2
    exit 1
  fi
  echo "    SHA256 verified successfully against pinned checksum: $ACTUAL_SHA256"
else
  # Fetch official SHA256SUMS from the release (fail-closed if unavailable or mismatched)
  CHECKSUMS_TMP="$(mktemp)"
  chmod 0600 "$CHECKSUMS_TMP"
  if ! curl -fsSL "${RELEASE_BASE_URL}/SHA256SUMS" -o "$CHECKSUMS_TMP" 2>/dev/null; then
    rm -f "$CHECKSUMS_TMP"
    CHECKSUMS_TMP=""
    echo "::error::Failed to download release SHA256SUMS from ${RELEASE_BASE_URL}/SHA256SUMS. Aborting installation (fail-closed)." >&2
    exit 1
  fi
  EXPECTED_FROM_FILE="$(awk '$2 == "labbit-connector" || $2 == "*labbit-connector" {print $1}' "$CHECKSUMS_TMP" | head -n1)"
  rm -f "$CHECKSUMS_TMP"
  CHECKSUMS_TMP=""
  if [ -z "$EXPECTED_FROM_FILE" ]; then
    echo "::error::Release SHA256SUMS did not contain a valid entry for labbit-connector. Aborting installation." >&2
    exit 1
  fi
  if [ "$ACTUAL_SHA256" != "$EXPECTED_FROM_FILE" ]; then
    echo "::error::SHA256 mismatch against release SHA256SUMS! Expected: $EXPECTED_FROM_FILE, Got: $ACTUAL_SHA256." >&2
    exit 1
  fi
  echo "    SHA256 verified successfully against release SHA256SUMS: $ACTUAL_SHA256"
fi

# Install binary to /usr/local/bin
mkdir -p "$(dirname "$INSTALL_BIN")"
cp "$DOWNLOAD_TMP" "$INSTALL_BIN"
chmod 0755 "$INSTALL_BIN"
chown root:root "$INSTALL_BIN"
rm -f "$DOWNLOAD_TMP"
DOWNLOAD_TMP=""
echo "    Installed binary to $INSTALL_BIN"

# -------------------------------------------------------------
# 3. Secure Interactive Token Input & SaaS Enrollment
# -------------------------------------------------------------
echo "==> [3/6] Requesting Enrollment Token..."
ENROLLMENT_TOKEN=""
SAVED_STTY="$(stty -g < "$TTY_DEV" 2>/dev/null)" || {
  echo "::error::Failed to save terminal state from $TTY_DEV." >&2
  exit 1
}
printf "Enter Labbit Connector Enrollment Token: " >> "$TTY_DEV"
if ! stty -echo < "$TTY_DEV" 2>/dev/null; then
  stty "$SAVED_STTY" < "$TTY_DEV" 2>/dev/null || true
  SAVED_STTY=""
  printf "\n" >> "$TTY_DEV" 2>/dev/null || true
  echo "::error::Failed to disable terminal echo (stty -echo) on $TTY_DEV. Aborting token input." >&2
  exit 1
fi
if ! IFS= read -r ENROLLMENT_TOKEN < "$TTY_DEV"; then
  stty "$SAVED_STTY" < "$TTY_DEV" 2>/dev/null || stty echo < "$TTY_DEV" 2>/dev/null || true
  SAVED_STTY=""
  printf "\n" >> "$TTY_DEV" 2>/dev/null || true
  echo "::error::Failed to read enrollment token from $TTY_DEV." >&2
  exit 1
fi
stty "$SAVED_STTY" < "$TTY_DEV" 2>/dev/null || stty echo < "$TTY_DEV" 2>/dev/null || true
SAVED_STTY=""
printf "\n" >> "$TTY_DEV"

if [ -z "$ENROLLMENT_TOKEN" ]; then
  echo "::error::Enrollment token cannot be empty." >&2
  exit 1
fi

echo "==> [3/6] Enrolling connector with SaaS (${LABBIT_SAAS_BASE_URL})..."
ENROLL_TMP="$(mktemp)"
chmod 0600 "$ENROLL_TMP"
printf '{"enrollmentToken":"%s"}' "$ENROLLMENT_TOKEN" > "$ENROLL_TMP"
unset ENROLLMENT_TOKEN

HTTP_RESPONSE_FILE="$(mktemp)"
chmod 0600 "$HTTP_RESPONSE_FILE"

HTTP_STATUS="$(curl -s -w "%{http_code}" \
  -X POST "${LABBIT_SAAS_BASE_URL}/api/v1/connector-enrollments" \
  -H "Content-Type: application/json" \
  -d @"$ENROLL_TMP" \
  -o "$HTTP_RESPONSE_FILE" 2>/dev/null || echo "000")"

rm -f "$ENROLL_TMP"
ENROLL_TMP=""

if [ "$HTTP_STATUS" = "401" ] || [ "$HTTP_STATUS" = "400" ] || [ "$HTTP_STATUS" = "403" ] || [ "$HTTP_STATUS" = "422" ]; then
  echo "::error::Connector enrollment was rejected by SaaS (HTTP $HTTP_STATUS: invalid_enrollment_token)." >&2
  echo "        Please verify that your enrollment token is valid, unconsumed, and not expired, or issue a new token in the Web Console." >&2
  exit 1
elif [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "::error::Connector enrollment outcome is UNCERTAIN due to transport/server failure (HTTP $HTTP_STATUS)." >&2
  echo "        DO NOT blindly retry with the same token. Check the Connector list in the Admin Web Console, revoke any partially enrolled Connector, and issue a new token." >&2
  exit 1
fi

# Secure JSON extraction using python3 or jq via stdin (no grep/sed for credentials)
parse_json() {
  field="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -r --arg f "$field" '.[$f] // empty' < "$HTTP_RESPONSE_FILE"
  else
    python3 -c "import sys, json; data=json.load(sys.stdin); val=data.get(sys.argv[1], ''); print(val if isinstance(val, str) else '')" "$field" < "$HTTP_RESPONSE_FILE"
  fi
}

CONNECTOR_ID="$(parse_json "connectorId" 2>/dev/null || true)"
CONNECTOR_CREDENTIAL="$(parse_json "connectorCredential" 2>/dev/null || true)"
rm -f "$HTTP_RESPONSE_FILE"
HTTP_RESPONSE_FILE=""

if [ -z "$CONNECTOR_ID" ] || [ -z "$CONNECTOR_CREDENTIAL" ]; then
  echo "::error::Enrollment response did not contain required connectorId and connectorCredential (outcome UNCERTAIN)." >&2
  echo "        Check the Connector list in the Admin Web Console, revoke any partially enrolled Connector, and issue a new token." >&2
  exit 1
fi

echo "    Enrolled successfully! Assigned Connector ID: $CONNECTOR_ID"

# -------------------------------------------------------------
# 4. Local User & Protected Storage Setup
# -------------------------------------------------------------
echo "==> [4/6] Setting up system user and protected directories..."

save_enrolled_credentials() {
  # Create system user and group if not present
  if ! getent group "$CONNECTOR_GROUP" >/dev/null 2>&1; then
    if ! groupadd --system "$CONNECTOR_GROUP"; then
      return 1
    fi
  fi
  if ! id -u "$CONNECTOR_USER" >/dev/null 2>&1; then
    if ! useradd --system --no-create-home --gid "$CONNECTOR_GROUP" --shell /usr/sbin/nologin "$CONNECTOR_USER"; then
      return 1
    fi
  fi

  # Base configuration directory (/etc/labbit-connector: 0750, root:labbit)
  if ! mkdir -p "$CONFIG_DIR" || ! chown "root:${CONNECTOR_GROUP}" "$CONFIG_DIR" || ! chmod 0750 "$CONFIG_DIR"; then
    return 1
  fi

  # Write connector.id atomically (0640, root:labbit)
  if ! ID_TMP="$(mktemp -p "$CONFIG_DIR")" || \
     ! printf '%s\n' "$CONNECTOR_ID" > "$ID_TMP" || \
     ! chmod 0640 "$ID_TMP" || \
     ! chown "root:${CONNECTOR_GROUP}" "$ID_TMP" || \
     ! mv -f "$ID_TMP" "$CONFIG_DIR/connector.id"; then
    rm -f "${ID_TMP:-}" 2>/dev/null || true
    ID_TMP=""
    return 1
  fi
  ID_TMP=""

  # Write credential atomically (0600, labbit:labbit)
  if ! CRED_TMP="$(mktemp -p "$CONFIG_DIR")" || \
     ! printf '%s\n' "$CONNECTOR_CREDENTIAL" > "$CRED_TMP" || \
     ! chmod 0600 "$CRED_TMP" || \
     ! chown "${CONNECTOR_USER}:${CONNECTOR_GROUP}" "$CRED_TMP" || \
     ! mv -f "$CRED_TMP" "$CONFIG_DIR/credential"; then
    rm -f "${CRED_TMP:-}" "$CONFIG_DIR/connector.id" 2>/dev/null || true
    CRED_TMP=""
    return 1
  fi
  CRED_TMP=""
  unset CONNECTOR_CREDENTIAL
  return 0
}

if ! save_enrolled_credentials; then
  unset CONNECTOR_CREDENTIAL
  echo "::error::Failed to securely save Connector credentials to $CONFIG_DIR (Token was already consumed)." >&2
  echo "        Recovery steps: Check the Connector list in the Admin Web Console, revoke this Connector (ID: ${CONNECTOR_ID}), and issue a new Enrollment Token." >&2
  echo "        복구 안내: 관리자 웹 콘솔의 커넥터 목록 확인 → 등록된 커넥터(ID: ${CONNECTOR_ID}) Revoke(폐기) → 신규 Token 발급 후 재설치" >&2
  exit 1
fi

# -------------------------------------------------------------
# 5. D-18 Local Management SSH Key & known_hosts Setup
# -------------------------------------------------------------
echo "==> [5/6] Initializing local management SSH key pair and known_hosts..."
SSH_DIR="$CONFIG_DIR/ssh"
mkdir -p "$SSH_DIR"
chmod 0700 "$SSH_DIR"
chown "${CONNECTOR_USER}:${CONNECTOR_GROUP}" "$SSH_DIR"

if [ ! -f "$SSH_DIR/id_ed25519" ]; then
  ssh-keygen -t ed25519 -N "" -f "$SSH_DIR/id_ed25519" -C "labbit-connector-management" >/dev/null 2>&1
  chmod 0600 "$SSH_DIR/id_ed25519"
  chmod 0644 "$SSH_DIR/id_ed25519.pub"
  chown "${CONNECTOR_USER}:${CONNECTOR_GROUP}" "$SSH_DIR/id_ed25519" "$SSH_DIR/id_ed25519.pub"
  echo "    Generated new Ed25519 management key pair."
else
  echo "    Existing management key pair preserved."
fi

if [ ! -f "$SSH_DIR/known_hosts" ]; then
  touch "$SSH_DIR/known_hosts"
  chmod 0644 "$SSH_DIR/known_hosts"
  chown "${CONNECTOR_USER}:${CONNECTOR_GROUP}" "$SSH_DIR/known_hosts"
  echo "    Initialized empty known_hosts file."
fi

# -------------------------------------------------------------
# 6. systemd Service Configuration & Startup
# -------------------------------------------------------------
echo "==> [6/6] Configuring systemd service and starting connector..."

ENV_FILE="$CONFIG_DIR/connector.env"
ENV_TMP="$(mktemp -p "$CONFIG_DIR")"
cat <<EOF > "$ENV_TMP"
LABBIT_ENVIRONMENT=production
LABBIT_SAAS_BASE_URL=${LABBIT_SAAS_BASE_URL}
LABBIT_CONNECTOR_ID=${CONNECTOR_ID}
LABBIT_CONNECTOR_CREDENTIAL_FILE=${CONFIG_DIR}/credential
LABBIT_OPENSTACK_SSH_KNOWN_HOSTS_FILE=${CONFIG_DIR}/ssh/known_hosts
LABBIT_OPENSTACK_SSH_PRIVATE_KEY_FILE=${CONFIG_DIR}/ssh/id_ed25519
LABBIT_LOG_LEVEL=info
EOF
chmod 0640 "$ENV_TMP"
chown "root:${CONNECTOR_GROUP}" "$ENV_TMP"
mv -f "$ENV_TMP" "$ENV_FILE"

cat <<EOF > "$SYSTEMD_SERVICE_FILE"
[Unit]
Description=Labbit Connector Service
After=network-online.target
Wants=network-online.target
Documentation=https://github.com/${GITHUB_REPO}

[Service]
Type=simple
User=${CONNECTOR_USER}
Group=${CONNECTOR_GROUP}
EnvironmentFile=${CONFIG_DIR}/connector.env
ExecStart=${INSTALL_BIN}
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${CONFIG_DIR}/ssh

[Install]
WantedBy=multi-user.target
EOF

chmod 0644 "$SYSTEMD_SERVICE_FILE"
systemctl daemon-reload
systemctl enable labbit-connector.service
systemctl restart labbit-connector.service

# Verify service liveness
sleep 2
if systemctl is-active --quiet labbit-connector.service; then
  echo ""
  echo "================================================================="
  echo "  Labbit Connector installed and started successfully!"
  echo "  Connector ID: ${CONNECTOR_ID}"
  echo "  Service:      active (running)"
  echo "  Control WSS:  connecting to ${LABBIT_SAAS_BASE_URL}"
  echo ""
  echo "  Please check the Labbit Web Console to verify ONLINE status."
  echo "================================================================="
else
  echo "::error::Labbit Connector service failed to start or exited immediately." >&2
  echo "        Run: journalctl -u labbit-connector -n 20 --no-pager" >&2
  exit 1
fi
