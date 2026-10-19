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

cleanup() {
  if [ -d "$LOCK_DIR" ]; then
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
}
trap cleanup EXIT INT TERM

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
for cmd in curl sha256sum ssh-keygen; do
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

# Single execution lock
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  echo "::error::Another instance of the Labbit Connector installer is currently running ($LOCK_DIR)." >&2
  exit 1
fi

# Check TTY for interactive token input BEFORE downloading or altering anything
TTY_DEV=""
if [ -c /dev/tty ]; then
  TTY_DEV="/dev/tty"
elif [ -t 0 ]; then
  TTY_DEV="/dev/stdin"
fi

# If already installed and active, protect existing identity and prevent accidental overwrite
if [ -f "$CONFIG_DIR/credential" ] && [ -s "$CONFIG_DIR/credential" ]; then
  echo "==> Existing Connector credentials found in $CONFIG_DIR/credential."
  if systemctl is-active --quiet labbit-connector.service; then
    echo "==> Labbit Connector service is already running. Existing identity preserved."
    echo "    To upgrade the binary or service, consult the manual upgrade runbook."
    exit 0
  else
    echo "==> Connector is enrolled but service is inactive. Re-starting service..."
    systemctl restart labbit-connector.service
    exit 0
  fi
fi

# If non-interactive and no credential exists, fail early before secrets are requested
if [ -z "$TTY_DEV" ] && [ -z "${LABBIT_ENROLLMENT_TOKEN:-}" ]; then
  echo "::error::Interactive TTY not detected and LABBIT_ENROLLMENT_TOKEN is not provided." >&2
  echo "        Enrollment requires interactive token input via /dev/tty." >&2
  exit 1
fi

# -------------------------------------------------------------
# 2. Download Binary & Checksum Verification
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
  # Fetch official SHA256SUMS from the release
  CHECKSUMS_TMP="$(mktemp)"
  chmod 0600 "$CHECKSUMS_TMP"
  if curl -fsSL "${RELEASE_BASE_URL}/SHA256SUMS" -o "$CHECKSUMS_TMP" 2>/dev/null; then
    EXPECTED_FROM_FILE="$(grep -w "labbit-connector" "$CHECKSUMS_TMP" | awk '{print $1}' | head -n1)"
    rm -f "$CHECKSUMS_TMP"
    if [ -n "$EXPECTED_FROM_FILE" ] && [ "$ACTUAL_SHA256" = "$EXPECTED_FROM_FILE" ]; then
      echo "    SHA256 verified successfully against release SHA256SUMS: $ACTUAL_SHA256"
    else
      echo "::error::SHA256 mismatch against release SHA256SUMS! Got: $ACTUAL_SHA256." >&2
      exit 1
    fi
  else
    rm -f "$CHECKSUMS_TMP"
    echo "::warning::Release SHA256SUMS not found; proceeding with downloaded binary hash: $ACTUAL_SHA256"
  fi
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
if [ -n "${LABBIT_ENROLLMENT_TOKEN:-}" ]; then
  ENROLLMENT_TOKEN="$LABBIT_ENROLLMENT_TOKEN"
elif [ -n "$TTY_DEV" ]; then
  printf "Enter Labbit Connector Enrollment Token: " > "$TTY_DEV"
  stty -echo < "$TTY_DEV" 2>/dev/null || true
  read -r ENROLLMENT_TOKEN < "$TTY_DEV"
  stty echo < "$TTY_DEV" 2>/dev/null || true
  printf "\n" > "$TTY_DEV"
fi

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

if [ "$HTTP_STATUS" != "200" ] && [ "$HTTP_STATUS" != "201" ]; then
  echo "::error::Connector enrollment failed with HTTP status $HTTP_STATUS." >&2
  echo "        Please check that your enrollment token is valid, unconsumed, and not expired." >&2
  exit 1
fi

# Secure JSON extraction using python3 or jq (no grep/sed for credentials)
parse_json() {
  field="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 -c "import sys, json; data=json.load(open('$HTTP_RESPONSE_FILE')); print(data.get('$field', ''))"
  else
    jq -r --arg f "$field" '.[$f] // empty' "$HTTP_RESPONSE_FILE"
  fi
}

CONNECTOR_ID="$(parse_json "connectorId")"
CONNECTOR_CREDENTIAL="$(parse_json "connectorCredential")"
rm -f "$HTTP_RESPONSE_FILE"
HTTP_RESPONSE_FILE=""

if [ -z "$CONNECTOR_ID" ] || [ -z "$CONNECTOR_CREDENTIAL" ]; then
  echo "::error::Enrollment response did not contain required connectorId and connectorCredential." >&2
  exit 1
fi

echo "    Enrolled successfully! Assigned Connector ID: $CONNECTOR_ID"

# -------------------------------------------------------------
# 4. Local User & Protected Storage Setup
# -------------------------------------------------------------
echo "==> [4/6] Setting up system user and protected directories..."

# Create system user and group if not present
if ! getent group "$CONNECTOR_GROUP" >/dev/null 2>&1; then
  groupadd --system "$CONNECTOR_GROUP"
fi
if ! id -u "$CONNECTOR_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --gid "$CONNECTOR_GROUP" --shell /usr/sbin/nologin "$CONNECTOR_USER"
fi

# Base configuration directory (/etc/labbit-connector: 0750, root:labbit)
mkdir -p "$CONFIG_DIR"
chown "root:${CONNECTOR_GROUP}" "$CONFIG_DIR"
chmod 0750 "$CONFIG_DIR"

# Write connector.id atomically (0640, root:labbit)
ID_TMP="$(mktemp -p "$CONFIG_DIR")"
printf '%s\n' "$CONNECTOR_ID" > "$ID_TMP"
chmod 0640 "$ID_TMP"
chown "root:${CONNECTOR_GROUP}" "$ID_TMP"
mv -f "$ID_TMP" "$CONFIG_DIR/connector.id"

# Write credential atomically (0600, labbit:labbit)
CRED_TMP="$(mktemp -p "$CONFIG_DIR")"
printf '%s\n' "$CONNECTOR_CREDENTIAL" > "$CRED_TMP"
chmod 0600 "$CRED_TMP"
chown "${CONNECTOR_USER}:${CONNECTOR_GROUP}" "$CRED_TMP"
mv -f "$CRED_TMP" "$CONFIG_DIR/credential"
unset CONNECTOR_CREDENTIAL

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
