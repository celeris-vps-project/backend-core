#!/usr/bin/env bash
set -Eeuo pipefail

# ============================================================
# Celeris + PVE Bootstrap
# ============================================================

# bootstrap_token 默认留空，脚本只生成 agent.yaml 和 systemd，不启动 agent
# 如果想直接写入并启动：
# BOOTSTRAP_TOKEN='xxx' AUTO_START_AGENT=1 bash /root/celeris-pve-bootstrap.sh
BOOTSTRAP_TOKEN="${BOOTSTRAP_TOKEN:-}"
AUTO_START_AGENT="${AUTO_START_AGENT:-0}"

PVE_INSTALL_URL="${PVE_INSTALL_URL:-https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/install_pve.sh}"

VMID="${VMID:-9000}"
TEMPLATE_NAME="${TEMPLATE_NAME:-ubuntu-2404-cloudinit}"
STORAGE="${STORAGE:-local}"

WAN_BRIDGE="${WAN_BRIDGE:-vmbr0}"
NAT_BRIDGE="${NAT_BRIDGE:-vmbr2}"
NAT_BRIDGE_ADDR="${NAT_BRIDGE_ADDR:-10.0.0.1/24}"
NAT_GATEWAY="${NAT_GATEWAY:-10.0.0.1}"
NAT_NETWORK="${NAT_NETWORK:-10.0.0.0/24}"

CUSTOMIZE_IP="${CUSTOMIZE_IP:-10.0.0.2}"
CUSTOMIZE_IP_CIDR="${CUSTOMIZE_IP_CIDR:-10.0.0.2/24}"
NAMESERVER="${NAMESERVER:-1.1.1.1}"
CLOUD_USER="${CLOUD_USER:-ubuntu}"

IMAGE_URL="${IMAGE_URL:-https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img}"
IMAGE_PATH="${IMAGE_PATH:-/var/lib/vz/template/iso/noble-server-cloudimg-amd64.img}"
DISK_SIZE="${DISK_SIZE:-20G}"

SSH_KEY="${SSH_KEY:-/root/.ssh/celeris-cloudinit_ed25519}"

AGENT_VERSION="${AGENT_VERSION:-v0.0.64}"
AGENT_URL="${AGENT_URL:-https://github.com/celeris-vps-project/backend-core/releases/download/${AGENT_VERSION}/celeris-agent-linux-amd64}"
AGENT_DIR="${AGENT_DIR:-/opt/celeris-agent}"

GRPC_ADDRESS="${GRPC_ADDRESS:-1.0rtt.de:50051}"
CONTROLLER_URL="${CONTROLLER_URL:-http://127.0.0.1:8888}"
POLL_INTERVAL="${POLL_INTERVAL:-15}"

PVE_API_URL="${PVE_API_URL:-https://127.0.0.1:8006}"
PVE_API_TOKEN_ID="${PVE_API_TOKEN_ID:-root@pam!celeris}"

# 按你剪切板配置默认使用 pve
# 如果你的实际节点名不是 pve，可执行：
# PVE_NODE="$(hostname -s)" bash /root/celeris-pve-bootstrap.sh
PVE_NODE="${PVE_NODE:-pve}"

# 如果 VMID=9000 已存在：
# 0 = 尽量复用
# 1 = 删除后重建
RECREATE_TEMPLATE="${RECREATE_TEMPLATE:-0}"

RESET_TEMPLATE_IPCONFIG_TO_DHCP="${RESET_TEMPLATE_IPCONFIG_TO_DHCP:-1}"

MAX_PVE_INSTALL_ATTEMPTS="${MAX_PVE_INSTALL_ATTEMPTS:-8}"
BOOT_WAIT_SECONDS="${BOOT_WAIT_SECONDS:-25}"

STATE_DIR="/var/lib/celeris-pve-bootstrap"
SELF_PATH="/root/celeris-pve-bootstrap.sh"
ENV_FILE="/etc/celeris-pve-bootstrap.env"
LOG_FILE="/var/log/celeris-pve-bootstrap.log"
INSTALL_SCRIPT="/root/install_pve.sh"
BOOTSTRAP_SERVICE="/etc/systemd/system/celeris-pve-bootstrap.service"

log() {
    echo "[$(date '+%F %T')] $*"
}

die() {
    log "ERROR: $*"
    exit 1
}

if [[ "${EUID}" -ne 0 ]]; then
    echo "请用 root 执行本脚本" >&2
    exit 1
fi

mkdir -p "$STATE_DIR"
touch "$LOG_FILE"
exec > >(tee -a "$LOG_FILE") 2>&1

trap 'log "ERROR: line ${LINENO}: ${BASH_COMMAND}"' ERR

acquire_lock() {
    if command -v flock >/dev/null 2>&1; then
        exec 9>/run/celeris-pve-bootstrap.lock
        flock -n 9 || {
            log "已有一个 celeris-pve-bootstrap 实例正在运行，退出。"
            exit 0
        }
    fi
}

env_quote() {
    local s="${1:-}"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    s="${s//$'\n'/\\n}"
    printf '"%s"' "$s"
}

yaml_escape() {
    local s="${1:-}"
    s="${s//\\/\\\\}"
    s="${s//\"/\\\"}"
    printf '%s' "$s"
}

write_env_file() {
    umask 077

    cat > "$ENV_FILE" <<ENVEOF
BOOTSTRAP_TOKEN=$(env_quote "$BOOTSTRAP_TOKEN")
AUTO_START_AGENT=$(env_quote "$AUTO_START_AGENT")
PVE_INSTALL_URL=$(env_quote "$PVE_INSTALL_URL")
VMID=$(env_quote "$VMID")
TEMPLATE_NAME=$(env_quote "$TEMPLATE_NAME")
STORAGE=$(env_quote "$STORAGE")
WAN_BRIDGE=$(env_quote "$WAN_BRIDGE")
NAT_BRIDGE=$(env_quote "$NAT_BRIDGE")
NAT_BRIDGE_ADDR=$(env_quote "$NAT_BRIDGE_ADDR")
NAT_GATEWAY=$(env_quote "$NAT_GATEWAY")
NAT_NETWORK=$(env_quote "$NAT_NETWORK")
CUSTOMIZE_IP=$(env_quote "$CUSTOMIZE_IP")
CUSTOMIZE_IP_CIDR=$(env_quote "$CUSTOMIZE_IP_CIDR")
NAMESERVER=$(env_quote "$NAMESERVER")
CLOUD_USER=$(env_quote "$CLOUD_USER")
IMAGE_URL=$(env_quote "$IMAGE_URL")
IMAGE_PATH=$(env_quote "$IMAGE_PATH")
DISK_SIZE=$(env_quote "$DISK_SIZE")
SSH_KEY=$(env_quote "$SSH_KEY")
AGENT_VERSION=$(env_quote "$AGENT_VERSION")
AGENT_URL=$(env_quote "$AGENT_URL")
AGENT_DIR=$(env_quote "$AGENT_DIR")
GRPC_ADDRESS=$(env_quote "$GRPC_ADDRESS")
CONTROLLER_URL=$(env_quote "$CONTROLLER_URL")
POLL_INTERVAL=$(env_quote "$POLL_INTERVAL")
PVE_API_URL=$(env_quote "$PVE_API_URL")
PVE_API_TOKEN_ID=$(env_quote "$PVE_API_TOKEN_ID")
PVE_NODE=$(env_quote "$PVE_NODE")
RECREATE_TEMPLATE=$(env_quote "$RECREATE_TEMPLATE")
RESET_TEMPLATE_IPCONFIG_TO_DHCP=$(env_quote "$RESET_TEMPLATE_IPCONFIG_TO_DHCP")
MAX_PVE_INSTALL_ATTEMPTS=$(env_quote "$MAX_PVE_INSTALL_ATTEMPTS")
BOOT_WAIT_SECONDS=$(env_quote "$BOOT_WAIT_SECONDS")
ENVEOF

    chmod 600 "$ENV_FILE"
}

install_self_service() {
    local src
    src="$(readlink -f "$0" 2>/dev/null || true)"

    [[ -n "$src" && -f "$src" ]] || die "请先把脚本保存成文件再执行。"

    if [[ "$src" != "$SELF_PATH" ]]; then
        cp -f "$src" "$SELF_PATH"
    fi

    chmod 700 "$SELF_PATH"

    write_env_file

    cat > "$BOOTSTRAP_SERVICE" <<SERVICEEOF
[Unit]
Description=Celeris PVE Bootstrap
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
EnvironmentFile=-$ENV_FILE
ExecStart=/bin/bash $SELF_PATH --continue
TimeoutStartSec=0
Restart=no

[Install]
WantedBy=multi-user.target
SERVICEEOF

    systemctl unmask "$(basename "$BOOTSTRAP_SERVICE")" >/dev/null 2>&1 || true
    systemctl daemon-reload
    systemctl enable "$(basename "$BOOTSTRAP_SERVICE")" >/dev/null
}

cleanup_self_service() {
    systemctl disable "$(basename "$BOOTSTRAP_SERVICE")" >/dev/null 2>&1 || true
    rm -f "$BOOTSTRAP_SERVICE" "$ENV_FILE"
    systemctl daemon-reload || true
}

download() {
    local url="$1"
    local out="$2"
    local tmp="${out}.tmp.$$"

    mkdir -p "$(dirname "$out")"

    if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 5 --retry-delay 3 "$url" -o "$tmp"
    elif command -v wget >/dev/null 2>&1; then
        wget --tries=5 --timeout=30 "$url" -O "$tmp"
    else
        export DEBIAN_FRONTEND=noninteractive
        apt-get update
        apt-get install -y curl ca-certificates
        curl -fL --retry 5 --retry-delay 3 "$url" -o "$tmp"
    fi

    mv -f "$tmp" "$out"
}

request_reboot() {
    log "$*"
    log "系统将重启，开机后会自动继续执行。"
    sync || true
    systemctl reboot || reboot || true
    sleep 10
    exit 0
}

# ============================================================
# PVE 安装
# ============================================================

pve_ready() {
    command -v qm >/dev/null 2>&1 || return 1
    command -v pveum >/dev/null 2>&1 || return 1
    systemctl is-active --quiet pvedaemon || return 1
    systemctl is-active --quiet pveproxy || return 1
    return 0
}

start_pve_services() {
    systemctl start pvedaemon pveproxy pvestatd qmeventd 2>/dev/null || true
}

wait_for_pve_ready() {
    command -v qm >/dev/null 2>&1 || return 1
    command -v pveum >/dev/null 2>&1 || return 1

    for _ in $(seq 1 60); do
        start_pve_services
        if pve_ready; then
            pveversion || true
            return 0
        fi
        sleep 5
    done

    return 1
}

sleep_until_boot_stable() {
    local up wait
    up="$(awk '{print int($1)}' /proc/uptime 2>/dev/null || echo 999)"

    if [[ "$up" =~ ^[0-9]+$ ]] && (( up < BOOT_WAIT_SECONDS )); then
        wait=$((BOOT_WAIT_SECONDS - up))
        log "开机未满 ${BOOT_WAIT_SECONDS} 秒，等待 ${wait}s 后再执行 install_pve.sh。"
        sleep "$wait"
    fi
}

repair_proxmox_apt_key() {
    local codename key_url tmp bin p

    codename="$(
        . /etc/os-release 2>/dev/null || true
        echo "${VERSION_CODENAME:-bookworm}"
    )"

    case "$codename" in
        bookworm|bullseye|trixie)
            ;;
        *)
            codename="bookworm"
            ;;
    esac

    key_url="https://enterprise.proxmox.com/debian/proxmox-release-${codename}.gpg"

    tmp="$(mktemp)"
    bin="$(mktemp)"

    log "尝试修复 Proxmox APT key：${key_url}"

    if command -v curl >/dev/null 2>&1; then
        if ! curl -fsSL --retry 5 --retry-delay 2 "$key_url" -o "$tmp"; then
            rm -f "$tmp" "$bin"
            return 1
        fi
    elif command -v wget >/dev/null 2>&1; then
        if ! wget --tries=5 --timeout=30 "$key_url" -O "$tmp"; then
            rm -f "$tmp" "$bin"
            return 1
        fi
    else
        log "WARN: curl/wget 不存在，无法下载 Proxmox key。"
        rm -f "$tmp" "$bin"
        return 1
    fi

    if grep -qa 'BEGIN PGP PUBLIC KEY BLOCK' "$tmp"; then
        if ! command -v gpg >/dev/null 2>&1; then
            log "WARN: key 是 ASCII armor 格式，但系统没有 gpg。"
            rm -f "$tmp" "$bin"
            return 1
        fi

        if ! gpg --batch --yes --dearmor -o "$bin" "$tmp"; then
            rm -f "$tmp" "$bin"
            return 1
        fi
    else
        cp -f "$tmp" "$bin"
    fi

    for p in \
        "/etc/apt/trusted.gpg.d/proxmox-release-${codename}.gpg" \
        "/etc/apt/keyrings/proxmox-release-${codename}.gpg" \
        "/usr/share/keyrings/proxmox-release-${codename}.gpg" \
        "/etc/apt/keyrings/proxmox-archive-keyring.gpg" \
        "/usr/share/keyrings/proxmox-archive-keyring.gpg"
    do
        chattr -i "$p" 2>/dev/null || true
        install -D -m 0644 "$bin" "$p"
    done

    rm -f "$tmp" "$bin"

    log "Proxmox APT key 修复完成。"
}

LAST_INSTALL_LOG=""

run_pve_installer_once() {
    LAST_INSTALL_LOG="$STATE_DIR/install_pve.$(date +%Y%m%d-%H%M%S).log"
    LAST_INSTALL_RC=0
    sleep_until_boot_stable
    log "执行 install_pve.sh，日志：$LAST_INSTALL_LOG"
    local rc err_trap
    err_trap="$(trap -p ERR || true)"
    # install_pve.sh 返回 1 可能只是要求 reboot，不应该被 set -e/ERR trap 提前中断
    trap - ERR
    set +e
    DEBIAN_FRONTEND=noninteractive bash "$INSTALL_SCRIPT" > >(tee "$LAST_INSTALL_LOG") 2>&1
    rc=$?
    set -e
    if [[ -n "$err_trap" ]]; then
        eval "$err_trap"
    else
        trap - ERR
    fi
    LAST_INSTALL_RC="$rc"
    return 0
}

install_log_has_reboot_request() {
    [[ -n "${LAST_INSTALL_LOG:-}" && -f "$LAST_INSTALL_LOG" ]] || return 1

    grep -Eqi \
        'Please execute reboot|请执行[[:space:]]*reboot|重启系统后再次执行|execute this script again' \
        "$LAST_INSTALL_LOG"
}

install_log_has_apt_key_error() {
    [[ -n "${LAST_INSTALL_LOG:-}" && -f "$LAST_INSTALL_LOG" ]] || return 1

    grep -Eqi \
        'NO_PUBKEY|GPG error|not signed|public key is not available|keyring .*ignored|unsupported filetype|EXPKEYSIG|BADSIG' \
        "$LAST_INSTALL_LOG"
}

ensure_pve_installed() {
    if wait_for_pve_ready; then
        log "PVE 已就绪。"
        return 0
    fi

    local attempts_file="$STATE_DIR/pve_install_attempts"
    local attempts=0

    [[ -s "$attempts_file" ]] && attempts="$(cat "$attempts_file" 2>/dev/null || echo 0)"
    attempts=$((attempts + 1))
    echo "$attempts" > "$attempts_file"

    if (( attempts > MAX_PVE_INSTALL_ATTEMPTS )); then
        die "PVE 安装尝试次数超过 ${MAX_PVE_INSTALL_ATTEMPTS}，停止自动处理，避免无限重启。"
    fi

    log "PVE 未就绪，开始第 ${attempts} 次执行 install_pve.sh。"

    download "$PVE_INSTALL_URL" "$INSTALL_SCRIPT"
    chmod +x "$INSTALL_SCRIPT"

    repair_proxmox_apt_key || log "WARN: Proxmox APT key 预修复失败，继续执行 install_pve.sh。"

    local rc=0

    run_pve_installer_once
    rc="$LAST_INSTALL_RC"

    log "install_pve.sh 退出码：$rc"

    if wait_for_pve_ready; then
        log "PVE 已就绪。"
        return 0
    fi

    if (( rc != 0 )) && install_log_has_apt_key_error; then
        log "检测到 Proxmox APT key/GPG 错误，修复后重试一次，不直接重启。"

        repair_proxmox_apt_key || die "Proxmox APT key 修复失败，请手动检查 apt 源和 key。"

        set +e
        run_pve_installer_once
        rc=$?
        set -e

        log "修复 key 后 install_pve.sh 退出码：$rc"

        if wait_for_pve_ready; then
            log "PVE 已就绪。"
            return 0
        fi
    fi

    if (( rc != 0 )); then
        if install_log_has_apt_key_error; then
            die "Proxmox APT key/GPG 错误修复后仍失败，停止自动重启。请查看：$LAST_INSTALL_LOG"
        fi

        if install_log_has_reboot_request; then
            request_reboot "install_pve.sh 明确要求 reboot，执行重启。"
        fi

        die "install_pve.sh 失败，且没有明确要求 reboot。为避免无限重启，已停止。请查看：$LAST_INSTALL_LOG"
    fi

    if install_log_has_reboot_request; then
        request_reboot "install_pve.sh 正常退出但明确要求 reboot，执行重启。"
    fi

    local success_reboot_marker="$STATE_DIR/pve_installer_success_rebooted"

    if [[ ! -e "$success_reboot_marker" ]]; then
        touch "$success_reboot_marker"
        request_reboot "install_pve.sh 正常退出，但 PVE 尚未就绪，重启一次后继续检查。"
    fi

    die "install_pve.sh 正常退出且已重启过一次，但 PVE 仍未就绪。请查看：$LAST_INSTALL_LOG"
}

ensure_packages() {
    local missing=()

    command -v curl >/dev/null 2>&1 || missing+=(curl)
    command -v wget >/dev/null 2>&1 || missing+=(wget)
    command -v ssh >/dev/null 2>&1 || missing+=(openssh-client)
    command -v ssh-keygen >/dev/null 2>&1 || missing+=(openssh-client)
    command -v iptables >/dev/null 2>&1 || missing+=(iptables)
    command -v gpg >/dev/null 2>&1 || missing+=(gnupg)
    dpkg -s ca-certificates >/dev/null 2>&1 || missing+=(ca-certificates)

    if ((${#missing[@]})); then
        log "安装依赖：${missing[*]}"
        repair_proxmox_apt_key || true
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -o Acquire::Retries=3
        apt-get install -y "${missing[@]}"
    fi
}

# ============================================================
# vmbr2 / NAT
# ============================================================

detect_wan_bridge() {
    if ip link show "$WAN_BRIDGE" >/dev/null 2>&1; then
        echo "$WAN_BRIDGE"
        return 0
    fi

    ip route show default | awk '{
        for (i=1; i<=NF; i++) {
            if ($i == "dev") {
                print $(i+1)
                exit
            }
        }
    }'
}

configure_vmbr2() {
    local wan
    wan="$(detect_wan_bridge)"

    [[ -n "$wan" ]] || die "无法检测外网出口网卡/网桥，请手动设置 WAN_BRIDGE。"

    log "配置 ${NAT_BRIDGE}: ${NAT_BRIDGE_ADDR}，NAT 出口：${wan}"

    local iptables_bin sysctl_bin
    iptables_bin="$(command -v iptables || true)"
    sysctl_bin="$(command -v sysctl || true)"

    [[ -n "$iptables_bin" ]] || die "找不到 iptables"
    [[ -n "$sysctl_bin" ]] || die "找不到 sysctl"

    cat > /usr/local/sbin/celeris-vmbr2-nat-up <<NATEOF
#!/usr/bin/env bash
set -e
"$sysctl_bin" -w net.ipv4.ip_forward=1 >/dev/null
"$iptables_bin" -t nat -C POSTROUTING -s "$NAT_NETWORK" -o "$wan" -j MASQUERADE 2>/dev/null || "$iptables_bin" -t nat -A POSTROUTING -s "$NAT_NETWORK" -o "$wan" -j MASQUERADE
NATEOF

    cat > /usr/local/sbin/celeris-vmbr2-nat-down <<NATEOF
#!/usr/bin/env bash
"$iptables_bin" -t nat -D POSTROUTING -s "$NAT_NETWORK" -o "$wan" -j MASQUERADE 2>/dev/null || true
NATEOF

    chmod +x /usr/local/sbin/celeris-vmbr2-nat-up /usr/local/sbin/celeris-vmbr2-nat-down

    cat > /etc/network/if-up.d/celeris-vmbr2-nat <<HOOKEOF
#!/bin/sh
[ "\$IFACE" = "$NAT_BRIDGE" ] && /usr/local/sbin/celeris-vmbr2-nat-up || true
exit 0
HOOKEOF

    cat > /etc/network/if-down.d/celeris-vmbr2-nat <<HOOKEOF
#!/bin/sh
[ "\$IFACE" = "$NAT_BRIDGE" ] && /usr/local/sbin/celeris-vmbr2-nat-down || true
exit 0
HOOKEOF

    chmod +x /etc/network/if-up.d/celeris-vmbr2-nat /etc/network/if-down.d/celeris-vmbr2-nat

    chattr -i /etc/network/interfaces 2>/dev/null || true
    mkdir -p /etc/network/interfaces.d
    touch /etc/network/interfaces

    cp -a /etc/network/interfaces "$STATE_DIR/interfaces.bak.$(date +%F-%H%M%S)" || true

    if ! grep -Eq '^[[:space:]]*source[[:space:]]+/etc/network/interfaces\.d/\*' /etc/network/interfaces; then
        echo >> /etc/network/interfaces
        echo "source /etc/network/interfaces.d/*" >> /etc/network/interfaces
    fi

    if grep -RqsE "^[[:space:]]*iface[[:space:]]+$NAT_BRIDGE[[:space:]]+inet" /etc/network/interfaces /etc/network/interfaces.d 2>/dev/null; then
        log "${NAT_BRIDGE} 已存在于 network interfaces，保留现有配置。"
    else
        chattr -i "/etc/network/interfaces.d/celeris-${NAT_BRIDGE}" 2>/dev/null || true

        cat > "/etc/network/interfaces.d/celeris-${NAT_BRIDGE}" <<IFEOF
auto $NAT_BRIDGE
iface $NAT_BRIDGE inet static
    address $NAT_BRIDGE_ADDR
    bridge-ports none
    bridge-stp off
    bridge-fd 0
    post-up /usr/local/sbin/celeris-vmbr2-nat-up
    post-down /usr/local/sbin/celeris-vmbr2-nat-down
IFEOF
    fi

    cat > /etc/sysctl.d/99-celeris-ipforward.conf <<SYSCTLEOF
net.ipv4.ip_forward=1
SYSCTLEOF

    sysctl -w net.ipv4.ip_forward=1 >/dev/null || true

    if command -v ifreload >/dev/null 2>&1; then
        ifreload -a || log "WARN: ifreload -a 失败，尝试运行时创建 ${NAT_BRIDGE}。"
    fi

    if ! ip link show "$NAT_BRIDGE" >/dev/null 2>&1; then
        ip link add name "$NAT_BRIDGE" type bridge || true
    fi

    ip addr replace "$NAT_BRIDGE_ADDR" dev "$NAT_BRIDGE" || true
    ip link set "$NAT_BRIDGE" up || true
    /usr/local/sbin/celeris-vmbr2-nat-up || true

    ip -br addr show "$NAT_BRIDGE" || true
}

# ============================================================
# cloud-init 模板
# ============================================================

ensure_storage() {
    if ! pvesm status | awk -v s="$STORAGE" 'NR > 1 && $1 == s { found=1 } END { exit !found }'; then
        die "PVE storage 不存在：$STORAGE"
    fi

    if [[ "$STORAGE" == "local" ]]; then
        log "确保 local storage 支持 images/cloudinit。"
        pvesm set local --content images,iso,vztmpl,backup,snippets,rootdir || true
    fi
}

vm_exists() {
    qm status "$VMID" >/dev/null 2>&1
}

is_template() {
    qm config "$VMID" 2>/dev/null | grep -q '^template: 1'
}

destroy_vm() {
    if vm_exists; then
        local st
        st="$(qm status "$VMID" 2>/dev/null | awk '{print $2}' || true)"

        qm unlock "$VMID" 2>/dev/null || true

        if [[ "$st" == "running" ]]; then
            qm stop "$VMID" || true
            sleep 3
        fi

        qm destroy "$VMID" --purge 1 --destroy-unreferenced-disks 1 || \
        qm destroy "$VMID" --purge 1 || \
        qm destroy "$VMID"
    fi
}

ensure_ssh_key() {
    mkdir -p "$(dirname "$SSH_KEY")"
    chmod 700 "$(dirname "$SSH_KEY")"

    if [[ ! -f "$SSH_KEY" ]]; then
        ssh-keygen -t ed25519 -N "" -C "celeris-cloudinit-$(hostname -s)" -f "$SSH_KEY"
    fi

    if [[ ! -f "${SSH_KEY}.pub" ]]; then
        ssh-keygen -y -f "$SSH_KEY" > "${SSH_KEY}.pub"
    fi

    chmod 600 "$SSH_KEY"
}

set_vm_sshkeys() {
    local pub
    pub="$(cat "${SSH_KEY}.pub")"

    if qm set "$VMID" --sshkeys "$pub"; then
        return 0
    fi

    if qm set "$VMID" --sshkey "${SSH_KEY}.pub"; then
        return 0
    fi

    die "写入 cloud-init SSH key 失败。"
}

ensure_cloud_image() {
    mkdir -p "$(dirname "$IMAGE_PATH")"

    if [[ ! -s "$IMAGE_PATH" ]]; then
        log "下载 Ubuntu 24.04 cloud image：$IMAGE_URL"
        download "$IMAGE_URL" "$IMAGE_PATH"
    else
        log "cloud image 已存在：$IMAGE_PATH"
    fi
}


ensure_cloudinit_drive() {
    local cfg ide2_line vol f ts

    cfg="$(qm config "$VMID" 2>/dev/null || true)"
    ide2_line="$(awk '/^ide2:/ {print; exit}' <<<"$cfg" || true)"

    # 已经挂了 cloud-init drive，直接跳过
    if grep -qE '^ide2: .*cloudinit' <<<"$cfg"; then
        log "cloud-init drive 已存在，跳过创建：$ide2_line"
        return 0
    fi

    # ide2 被别的东西占用，当前 VM 是模板候选机，直接删除旧 ide2
    if [[ -n "$ide2_line" ]]; then
        log "ide2 已存在但不是 cloud-init，删除旧 ide2：$ide2_line"
        qm set "$VMID" --delete ide2 || true
        cfg="$(qm config "$VMID" 2>/dev/null || true)"
    fi

    # 删除 PVE storage 里未挂载的 cloud-init 残留卷
    while IFS= read -r vol; do
        [[ -n "$vol" ]] || continue

        if grep -Fq "$vol" <<<"$cfg"; then
            log "cloud-init 卷已被 VM 配置引用，保留：$vol"
            continue
        fi

        log "删除未挂载的 cloud-init 残留卷：$vol"
        pvesm free "$vol" || true
    done < <(
        pvesm list "$STORAGE" --vmid "$VMID" 2>/dev/null | \
        awk -v vmid="$VMID" 'NR > 1 && ($1 ~ ("vm-" vmid "-cloudinit") || $1 ~ "cloudinit") {print $1}' || true
    )

    # local 目录型 storage 的兜底处理
    for f in \
        "/var/lib/vz/images/$VMID/vm-${VMID}-cloudinit.qcow2" \
        "/var/lib/vz/images/$VMID/vm-${VMID}-cloudinit.raw"
    do
        if [[ -e "$f" ]]; then
            ts="$(date +%Y%m%d-%H%M%S)"
            log "发现未登记的 cloud-init 残留文件，移动备份：$f -> ${f}.bak.$ts"
            mv -f "$f" "${f}.bak.$ts"
        fi
    done

    log "创建 cloud-init drive：${STORAGE}:cloudinit"
    qm set "$VMID" --ide2 "${STORAGE}:cloudinit"
}

create_or_update_template_vm() {
    ensure_storage
    ensure_ssh_key

    if vm_exists && [[ "$RECREATE_TEMPLATE" == "1" ]]; then
        log "RECREATE_TEMPLATE=1，删除现有 VMID=${VMID} 后重建。"
        destroy_vm
    fi

    if vm_exists && is_template; then
        log "VMID=${VMID} 已经是模板，跳过模板创建。"
        return 0
    fi

    if ! vm_exists; then
        ensure_cloud_image

        log "创建模板候选 VM：VMID=${VMID}, STORAGE=${STORAGE}, BRIDGE=${NAT_BRIDGE}"

        qm create "$VMID" \
            --name "$TEMPLATE_NAME" \
            --memory 2048 \
            --cores 2 \
            --cpu host \
            --net0 "virtio,bridge=${NAT_BRIDGE}" \
            --ostype l26

        qm importdisk "$VMID" "$IMAGE_PATH" "$STORAGE" --format raw

        local unused_disk
        unused_disk="$(qm config "$VMID" | awk -F': ' '/^unused[0-9]+:/ { print $2; exit }')"

        [[ -n "$unused_disk" ]] || die "未找到 importdisk 生成的 unused disk。"

        qm set "$VMID" --scsihw virtio-scsi-pci --scsi0 "${unused_disk},discard=on"
        ensure_cloudinit_drive
        qm set "$VMID" --boot order=scsi0
        qm set "$VMID" --serial0 socket --vga serial0
        qm set "$VMID" --agent enabled=1
        qm resize "$VMID" scsi0 "$DISK_SIZE" || true
    else
        log "VMID=${VMID} 已存在且不是模板，继续尝试配置。"
        qm unlock "$VMID" 2>/dev/null || true
    fi

    qm set "$VMID" --name "$TEMPLATE_NAME" || true
    qm set "$VMID" --net0 "virtio,bridge=${NAT_BRIDGE}"
    ensure_cloudinit_drive
    qm set "$VMID" --boot order=scsi0
    qm set "$VMID" --serial0 socket --vga serial0
    qm set "$VMID" --agent enabled=1
    qm set "$VMID" --ciuser "$CLOUD_USER"

    set_vm_sshkeys

    qm set "$VMID" --ipconfig0 "ip=${CUSTOMIZE_IP_CIDR},gw=${NAT_GATEWAY}" --nameserver "$NAMESERVER"
    qm cloudinit update "$VMID" || true
}

wait_for_ssh() {
    local user="$1"
    local ip="$2"
    local timeout="${3:-600}"
    local end=$((SECONDS + timeout))

    local ssh_opts=(
        -i "$SSH_KEY"
        -o StrictHostKeyChecking=no
        -o UserKnownHostsFile=/dev/null
        -o LogLevel=ERROR
        -o ConnectTimeout=5
    )

    log "等待 SSH 可用：${user}@${ip}"

    while (( SECONDS < end )); do
        if ssh "${ssh_opts[@]}" "${user}@${ip}" "echo ok" >/dev/null 2>&1; then
            log "SSH 已可用。"
            return 0
        fi

        sleep 5
    done

    return 1
}

shutdown_vm() {
    local st
    st="$(qm status "$VMID" 2>/dev/null | awk '{print $2}' || true)"

    if [[ "$st" != "running" ]]; then
        return 0
    fi

    set +e
    qm shutdown "$VMID" --timeout 180
    local rc=$?
    set -e

    if (( rc != 0 )); then
        log "WARN: qm shutdown 超时，执行 qm stop。"
        qm stop "$VMID" || true
    fi

    for _ in $(seq 1 60); do
        st="$(qm status "$VMID" 2>/dev/null | awk '{print $2}' || true)"
        [[ "$st" == "stopped" ]] && return 0
        sleep 3
    done

    die "VMID=${VMID} 未能正常关闭。"
}

customize_vm_and_convert_template() {
    if is_template; then
        log "VMID=${VMID} 已是模板，跳过 SSH 自定义。"
        return 0
    fi

    local st
    st="$(qm status "$VMID" 2>/dev/null | awk '{print $2}' || true)"

    if [[ "$st" != "running" ]]; then
        log "启动 VMID=${VMID}，用于 SSH 修改 cloud image。"
        qm start "$VMID"
    fi

    wait_for_ssh "$CLOUD_USER" "$CUSTOMIZE_IP" 600 || die "无法通过 SSH 连接 ${CLOUD_USER}@${CUSTOMIZE_IP}"

    local ssh_opts=(
        -i "$SSH_KEY"
        -o StrictHostKeyChecking=no
        -o UserKnownHostsFile=/dev/null
        -o LogLevel=ERROR
        -o ConnectTimeout=10
    )

    log "进入 cloud image，删除 /etc/ssh/sshd_config.d/* 并允许 root 登录。"

    ssh "${ssh_opts[@]}" "${CLOUD_USER}@${CUSTOMIZE_IP}" "sudo bash -s" <<'SSHEOF'
set -euo pipefail

timeout 300 cloud-init status --wait >/dev/null 2>&1 || true

rm -f /etc/ssh/sshd_config.d/* || true
touch /etc/ssh/sshd_config

sed -i -E '/^[[:space:]]*#?[[:space:]]*PermitRootLogin([[:space:]=]|$)/d' /etc/ssh/sshd_config || true
printf '\nPermitRootLogin=yes\n' >> /etc/ssh/sshd_config

if [[ -f /etc/cloud/cloud.cfg ]]; then
    if grep -qE '^[[:space:]]*disable_root:' /etc/cloud/cloud.cfg; then
        sed -i -E 's/^[[:space:]]*disable_root:.*/disable_root: false/' /etc/cloud/cloud.cfg
    else
        printf '\ndisable_root: false\n' >> /etc/cloud/cloud.cfg
    fi
fi

cloud-init clean --logs --machine-id 2>/dev/null || cloud-init clean --logs || true
truncate -s 0 /etc/machine-id || true
rm -f /var/lib/dbus/machine-id || true
rm -f /etc/ssh/ssh_host_* || true

sync
SSHEOF

    log "关闭 VMID=${VMID}。"
    shutdown_vm

    if [[ "$RESET_TEMPLATE_IPCONFIG_TO_DHCP" == "1" ]]; then
        qm set "$VMID" --ipconfig0 ip=dhcp
    fi

    qm set "$VMID" --net0 "virtio,bridge=${NAT_BRIDGE}"
    qm cloudinit update "$VMID" || true

    log "转换 VMID=${VMID} 为模板。"
    qm template "$VMID"
}

# ============================================================
# Celeris Agent
# ============================================================

parse_pve_token_secret() {
    local data="$1"
    local secret=""

    if command -v python3 >/dev/null 2>&1; then
        secret="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("value",""))' <<<"$data" 2>/dev/null || true)"
    fi

    if [[ -z "$secret" ]]; then
        secret="$(sed -n 's/.*"value"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$data" | head -n1)"
    fi

    if [[ -z "$secret" ]]; then
        secret="$(grep -Eo '[0-9a-fA-F-]{30,}' <<<"$data" | tail -n1 || true)"
    fi

    printf '%s' "$secret"
}

PVE_TOKEN_SECRET=""

ensure_pve_token() {
    mkdir -p "$AGENT_DIR"

    local token_secret_file="$AGENT_DIR/pve-root-pam-celeris.token"

    if [[ -s "$token_secret_file" ]]; then
        PVE_TOKEN_SECRET="$(cat "$token_secret_file")"
        log "使用已保存的 PVE API token secret：$token_secret_file"
        return 0
    fi

    local token_user token_name
    token_user="${PVE_API_TOKEN_ID%%!*}"
    token_name="${PVE_API_TOKEN_ID#*!}"

    [[ "$token_user" != "$PVE_API_TOKEN_ID" ]] || die "PVE_API_TOKEN_ID 格式错误，应类似 root@pam!celeris"

    log "创建 PVE API Token：${PVE_API_TOKEN_ID}"

    pveum user token remove "$token_user" "$token_name" 2>/dev/null || true

    local token_json
    token_json="$(pveum user token add "$token_user" "$token_name" --privsep 0 --comment "Celeris Agent" --output-format json)"

    PVE_TOKEN_SECRET="$(parse_pve_token_secret "$token_json")"

    [[ -n "$PVE_TOKEN_SECRET" ]] || die "未能解析 PVE API token secret。原始输出：$token_json"

    umask 077
    printf '%s\n' "$PVE_TOKEN_SECRET" > "$token_secret_file"
    chmod 600 "$token_secret_file"
}

install_celeris_agent() {
    mkdir -p "$AGENT_DIR"

    log "下载 celeris-agent：$AGENT_URL"
    download "$AGENT_URL" "$AGENT_DIR/celeris-agent"
    chmod 755 "$AGENT_DIR/celeris-agent"

    ensure_pve_token

    local bootstrap_value
    bootstrap_value="$BOOTSTRAP_TOKEN"

    if [[ -z "$bootstrap_value" ]]; then
        bootstrap_value="PLEASE_FILL_BOOTSTRAP_TOKEN"
    fi

    cat > "$AGENT_DIR/agent.yaml" <<YAMLEOF
# Celeris Agent Configuration
# Copy this file to agent.yaml and adjust as needed.

# The following fields are managed in the admin panel and must NOT be set here:
#   node_id, location, total_slots
# The server determines node identity from the bootstrap token binding.

bootstrap_token: "$(yaml_escape "$bootstrap_value")"
credential_file: "node-credential.yaml"
grpc_address: "$(yaml_escape "$GRPC_ADDRESS")"
controller_url: "$(yaml_escape "$CONTROLLER_URL")"
poll_interval: $POLL_INTERVAL

virt_backend: "pve"

nat:
  ssh_target_port: 22
  internal_network: "$(yaml_escape "$NAT_NETWORK")"

virt_opts:
  api_url: "$(yaml_escape "$PVE_API_URL")"
  api_token_id: "$(yaml_escape "$PVE_API_TOKEN_ID")"
  api_token_secret: "$(yaml_escape "$PVE_TOKEN_SECRET")"
  node: "$(yaml_escape "$PVE_NODE")"
  insecure: "true"
  template_vmid: "$VMID"
  storage: "$(yaml_escape "$STORAGE")"
YAMLEOF

    chmod 600 "$AGENT_DIR/agent.yaml"

    cat > /etc/systemd/system/celeris-agent.service <<SERVICEEOF
[Unit]
Description=Celeris Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$AGENT_DIR
ExecStart=$AGENT_DIR/celeris-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SERVICEEOF

    systemctl daemon-reload

    if [[ "$AUTO_START_AGENT" == "1" && -n "$BOOTSTRAP_TOKEN" ]]; then
        systemctl enable --now celeris-agent
        log "celeris-agent 已启动。"
    else
        log "celeris-agent systemd 文件已生成，但未启动。"
        log "请填写 $AGENT_DIR/agent.yaml 里的 bootstrap_token 后执行："
        log "  systemctl enable --now celeris-agent"
    fi
}

# ============================================================
# 主流程
# ============================================================

run_pipeline() {
    log "========== Celeris PVE Bootstrap 开始 =========="

    ensure_pve_installed
    ensure_packages

    configure_vmbr2

    create_or_update_template_vm
    customize_vm_and_convert_template

    install_celeris_agent

    cleanup_self_service

    log "========== 全部完成 =========="
    log "日志文件：$LOG_FILE"
    log "Agent 配置：$AGENT_DIR/agent.yaml"
}

main() {
    if [[ "${1:-}" != "--continue" ]]; then
        mkdir -p "$STATE_DIR"

        # 用户主动重新执行时，重置安装尝试计数，避免继承旧循环状态。
        rm -f "$STATE_DIR/pve_install_attempts" "$STATE_DIR/pve_installer_success_rebooted"

        install_self_service

        log "已注册开机自启动服务：$(basename "$BOOTSTRAP_SERVICE")"
        log "现在交给 systemd 后台执行。查看进度："
        log "  tail -f $LOG_FILE"

        systemctl start --no-block "$(basename "$BOOTSTRAP_SERVICE")"
        exit 0
    fi

    acquire_lock
    install_self_service
    run_pipeline
}

main "$@"
