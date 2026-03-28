#!/usr/bin/env bash
# Celeris Installer
# Usage:
#   ./install.sh [api|api-embed|agent] [--version vX.Y.Z]
#   ./install.sh uninstall [api|agent]
set -euo pipefail

REPO="celeris-vps-project/backend-core"
DEFAULT_VERSION="latest"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/celeris"
DATA_DIR="/var/lib/celeris"
SYSTEMD_DIR="/etc/systemd/system"
SERVICE_USER="celeris"

# ── Helpers ─────────────────────────────────────────────────────────────

info()  { echo -e "\033[1;34m[INFO]\033[0m  $*"; }
warn()  { echo -e "\033[1;33m[WARN]\033[0m  $*"; }
error() { echo -e "\033[1;31m[ERROR]\033[0m $*"; exit 1; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) error "Unsupported architecture: $(uname -m)" ;;
  esac
}

require_root() {
  [[ $EUID -eq 0 ]] || error "This script must be run as root (use sudo)"
}

require_systemd() {
  command -v systemctl &>/dev/null || error "systemd is required but not found"
}

generate_secret() {
  head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 32
}

# Map component name to binary name for download
binary_name() {
  case "$1" in
    api)        echo "celeris-api" ;;
    api-embed)  echo "celeris-api-embed" ;;
    agent)      echo "celeris-agent" ;;
    *) error "Unknown component: $1 (choose: api, api-embed, agent)" ;;
  esac
}

# Map component name to the local installed binary name
local_binary_name() {
  case "$1" in
    api|api-embed) echo "celeris-api" ;;
    agent)         echo "celeris-agent" ;;
  esac
}

# Map component name to systemd service name
service_name() {
  case "$1" in
    api|api-embed) echo "celeris-api" ;;
    agent)         echo "celeris-agent" ;;
  esac
}

# ── Download ────────────────────────────────────────────────────────────

download_binary() {
  local component="$1" version="$2" arch="$3"
  local bin_name
  bin_name=$(binary_name "$component")
  local filename="${bin_name}-linux-${arch}"
  local url

  if [[ "$version" == "latest" ]]; then
    url="https://github.com/${REPO}/releases/latest/download/${filename}"
  else
    url="https://github.com/${REPO}/releases/download/${version}/${filename}"
  fi

  local local_name
  local_name=$(local_binary_name "$component")

  info "Downloading ${filename} → ${INSTALL_DIR}/${local_name}"
  curl -fSL --progress-bar -o "${INSTALL_DIR}/${local_name}" "$url"
  chmod +x "${INSTALL_DIR}/${local_name}"
}

# ── Install ─────────────────────────────────────────────────────────────

install_component() {
  local component="$1" version="$2"

  require_root
  require_systemd

  local arch
  arch=$(detect_arch)
  info "Detected architecture: ${arch}"

  # 1. Download binary
  download_binary "$component" "$version" "$arch"

  # 2. Create system user (only for api)
  if [[ "$component" == "api" || "$component" == "api-embed" ]]; then
    if ! id "$SERVICE_USER" &>/dev/null; then
      info "Creating system user: ${SERVICE_USER}"
      useradd --system --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
    fi
  fi

  # 3. Create directories
  info "Creating directories: ${CONFIG_DIR}, ${DATA_DIR}"
  mkdir -p "$CONFIG_DIR" "$DATA_DIR"

  if [[ "$component" == "api" || "$component" == "api-embed" ]]; then
    chown "$SERVICE_USER":"$SERVICE_USER" "$DATA_DIR"
  fi

  # 4. Install example config (if not already present)
  local svc
  svc=$(service_name "$component")

  if [[ "$component" == "api" || "$component" == "api-embed" ]]; then
    if [[ ! -f "${CONFIG_DIR}/api.yaml" ]]; then
      info "Installing example config: ${CONFIG_DIR}/api.yaml"
      cat > "${CONFIG_DIR}/api.yaml" <<'YAML'
server:
  port: 8080
  domain: localhost

database:
  dsn: /var/lib/celeris/data.db

jwt:
  secret: ""
  issuer: celeris

grpc:
  listen: ":50051"
YAML
    else
      info "Config ${CONFIG_DIR}/api.yaml already exists, skipping"
    fi

    # Auto-generate JWT secret if empty
    if grep -q 'secret: ""' "${CONFIG_DIR}/api.yaml" 2>/dev/null; then
      local secret
      secret=$(generate_secret)
      sed -i "s|secret: \"\"|secret: \"${secret}\"|" "${CONFIG_DIR}/api.yaml"
      info "Generated random JWT secret"
    fi

    # Environment file
    if [[ ! -f "${CONFIG_DIR}/api.env" ]]; then
      cat > "${CONFIG_DIR}/api.env" <<ENV
# Celeris API environment overrides
# API_DATABASE_DSN=/var/lib/celeris/data.db
# API_JWT_SECRET=
# API_GRPC_LISTEN=:50051
ENV
      info "Installed ${CONFIG_DIR}/api.env"
    fi
  fi

  if [[ "$component" == "agent" ]]; then
    if [[ ! -f "${CONFIG_DIR}/agent.yaml" ]]; then
      info "Installing example config: ${CONFIG_DIR}/agent.yaml"
      cat > "${CONFIG_DIR}/agent.yaml" <<'YAML'
grpc_address: "localhost:50051"
bootstrap_token: ""
virt_backend: "mock"
poll_interval: 10
credential_file: /var/lib/celeris/node-credential.yaml
YAML
    else
      info "Config ${CONFIG_DIR}/agent.yaml already exists, skipping"
    fi

    if [[ ! -f "${CONFIG_DIR}/agent.env" ]]; then
      cat > "${CONFIG_DIR}/agent.env" <<ENV
# Celeris Agent environment overrides
# AGENT_BOOTSTRAP_TOKEN=
# AGENT_GRPC_ADDRESS=localhost:50051
# AGENT_VIRT_BACKEND=mock
ENV
      info "Installed ${CONFIG_DIR}/agent.env"
    fi
  fi

  # 5. Install systemd service
  local service_file="${SYSTEMD_DIR}/${svc}.service"
  info "Installing systemd service: ${service_file}"

  if [[ "$component" == "api" || "$component" == "api-embed" ]]; then
    cat > "$service_file" <<'SERVICE'
[Unit]
Description=Celeris API Server
Documentation=https://github.com/celeris-vps-project/backend-core
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=celeris
Group=celeris
ExecStart=/usr/local/bin/celeris-api -config /etc/celeris/api.yaml
Restart=on-failure
RestartSec=5
EnvironmentFile=-/etc/celeris/api.env
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/celeris
PrivateTmp=true
WorkingDirectory=/var/lib/celeris

[Install]
WantedBy=multi-user.target
SERVICE
  fi

  if [[ "$component" == "agent" ]]; then
    cat > "$service_file" <<'SERVICE'
[Unit]
Description=Celeris Agent
Documentation=https://github.com/celeris-vps-project/backend-core
After=network-online.target libvirtd.service
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/celeris-agent -config /etc/celeris/agent.yaml
Restart=on-failure
RestartSec=5
EnvironmentFile=-/etc/celeris/agent.env
WorkingDirectory=/var/lib/celeris

[Install]
WantedBy=multi-user.target
SERVICE
  fi

  systemctl daemon-reload
  systemctl enable "${svc}.service"
  info "Service ${svc} enabled (not started)"

  # 6. Print next steps
  echo ""
  echo "════════════════════════════════════════════════════════════════"
  info "Installation complete!"
  echo ""
  echo "  Config:  ${CONFIG_DIR}/"
  echo "  Data:    ${DATA_DIR}/"
  echo "  Binary:  ${INSTALL_DIR}/$(local_binary_name "$component")"
  echo "  Service: ${svc}.service"
  echo ""
  echo "  Next steps:"
  echo "    1. Edit ${CONFIG_DIR}/$([ "$component" = "agent" ] && echo "agent" || echo "api").yaml"
  echo "    2. sudo systemctl start ${svc}"
  echo "    3. sudo journalctl -u ${svc} -f"
  echo "════════════════════════════════════════════════════════════════"
}

# ── Uninstall ───────────────────────────────────────────────────────────

uninstall_component() {
  local component="$1"
  require_root

  local svc
  svc=$(service_name "$component")
  local local_name
  local_name=$(local_binary_name "$component")

  info "Uninstalling ${component}..."

  # Stop and disable service
  if systemctl is-active --quiet "${svc}.service" 2>/dev/null; then
    systemctl stop "${svc}.service"
    info "Stopped ${svc}.service"
  fi
  if systemctl is-enabled --quiet "${svc}.service" 2>/dev/null; then
    systemctl disable "${svc}.service"
    info "Disabled ${svc}.service"
  fi

  # Remove service file
  rm -f "${SYSTEMD_DIR}/${svc}.service"
  systemctl daemon-reload
  info "Removed ${SYSTEMD_DIR}/${svc}.service"

  # Remove binary
  rm -f "${INSTALL_DIR}/${local_name}"
  info "Removed ${INSTALL_DIR}/${local_name}"

  echo ""
  warn "Config (${CONFIG_DIR}) and data (${DATA_DIR}) were NOT removed."
  warn "Delete them manually if no longer needed:"
  warn "  sudo rm -rf ${CONFIG_DIR} ${DATA_DIR}"
}

# ── Main ────────────────────────────────────────────────────────────────

usage() {
  cat <<EOF
Celeris Installer

Usage:
  $0 <component> [--version <version>]
  $0 uninstall <component>

Components:
  api          Pure backend API server
  api-embed    All-in-one (frontend embedded in backend)
  agent        Node agent (VM management)

Options:
  --version    Release version tag (default: latest), e.g. v1.0.0

Examples:
  $0 api --version v1.0.0
  $0 api-embed
  $0 agent --version v1.2.3
  $0 uninstall api
  $0 uninstall agent
EOF
  exit 1
}

main() {
  local component="" version="$DEFAULT_VERSION" action="install"

  # Parse arguments
  while [[ $# -gt 0 ]]; do
    case "$1" in
      uninstall)
        action="uninstall"
        shift
        ;;
      api|api-embed|agent)
        component="$1"
        shift
        ;;
      --version)
        version="${2:-}"
        [[ -n "$version" ]] || error "--version requires a value"
        shift 2
        ;;
      -h|--help)
        usage
        ;;
      *)
        error "Unknown argument: $1"
        ;;
    esac
  done

  [[ -n "$component" ]] || usage

  case "$action" in
    install)   install_component "$component" "$version" ;;
    uninstall) uninstall_component "$component" ;;
  esac
}

main "$@"
