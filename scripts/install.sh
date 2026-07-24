#!/usr/bin/env bash
set -euo pipefail

REPO="${AGENT_REMOTE_NODE_REPO:-Agent-Remote/agent-remote-node}"
VERSION="${AGENT_REMOTE_NODE_VERSION:-latest}"
PREFIX="${PREFIX:-/usr/local}"
CONFIG_DIR="${CONFIG_DIR:-/etc/agent-remote-node}"
STATE_DIR="${STATE_DIR:-/var/lib/agent-remote-node}"
DATA_DIR="${DATA_DIR:-/var/lib/agent-remote}"
USER_NAME="${USER_NAME:-agent-remote}"
LIBC="${AGENT_REMOTE_NODE_LIBC:-glibc}"
OS_OVERRIDE="${AGENT_REMOTE_NODE_OS:-}"
ARCH_OVERRIDE="${AGENT_REMOTE_NODE_ARCH:-}"
TARGET_OVERRIDE="${AGENT_REMOTE_NODE_TARGET:-}"
TMP_DIR="${TMPDIR:-/tmp}"
KEEP_TEMP="${KEEP_TEMP:-0}"
INSTALL_SYSTEMD="${INSTALL_SYSTEMD:-1}"
CREATE_USER="${CREATE_USER:-1}"
USE_SUDO="${USE_SUDO:-auto}"
INSTALL_DEPENDENCIES="${INSTALL_DEPENDENCIES:-1}"
INSTALL_CLAUDE="${INSTALL_CLAUDE:-1}"
START_SERVICES="${START_SERVICES:-1}"
STRICT_PREREQUISITES="${STRICT_PREREQUISITES:-1}"
SERVER_URL="${AGENT_REMOTE_SERVER_URL:-}"
NODE_ID="${AGENT_REMOTE_NODE_ID:-}"
REGISTRATION_TOKEN="${AGENT_REMOTE_REGISTRATION_TOKEN:-}"
FORCE_REGISTER="${FORCE_REGISTER:-0}"
NODE_READY=0
RUNTIME_BACKENDS="${AGENT_REMOTE_RUNTIME_BACKENDS:-native}"
CLAUDE_CHANNEL="${CLAUDE_CHANNEL:-stable}"
CLAUDE_VERSION="${CLAUDE_VERSION:-}"
CLAUDE_SOURCE="${CLAUDE_SOURCE:-}"
CLAUDE_SHA256="${CLAUDE_SHA256:-}"
CLAUDE_RUNTIME_ROOT="${CLAUDE_RUNTIME_ROOT:-/opt/agent-remote/runtimes/claude}"
WIREGUARD_INTERFACE="${AGENT_REMOTE_WIREGUARD_INTERFACE:-agent-remote}"
WIREGUARD_ADDRESS="${AGENT_REMOTE_WIREGUARD_ADDRESS:-10.77.0.1/24}"
WIREGUARD_ENDPOINT="${AGENT_REMOTE_WIREGUARD_ENDPOINT:-}"
WIREGUARD_LISTEN_PORT="${AGENT_REMOTE_WIREGUARD_LISTEN_PORT:-51820}"
TEMP_PATHS=()

track_temp() {
  TEMP_PATHS+=("$1")
}

cleanup_temp() {
  local path
  for path in "${TEMP_PATHS[@]}"; do
    rm -rf -- "$path"
  done
}

trap cleanup_temp EXIT

resolve_script_dir() {
  if [ -n "${BASH_SOURCE+x}" ] && [ "${#BASH_SOURCE[@]}" -gt 0 ]; then
    local source="${BASH_SOURCE[0]:-}"
    if [ -n "$source" ] && [ -e "$source" ]; then
      cd "$(dirname "$source")" && pwd
      return
    fi
  fi
  printf ''
}

SCRIPT_DIR="$(resolve_script_dir)"

usage() {
  cat <<'EOF'
Usage:
  install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | \
    bash -s -- --server-url URL --node-id ID --registration-token TOKEN

Installs the latest agent-remote-node release by default. When executed from an
extracted release archive, installs the packaged files directly.

Options:
  --version VERSION       Release version, for example 0.0.4 or v0.0.4.
  --repo OWNER/REPO       GitHub repository to download from.
  --prefix PATH           Installation prefix for node binaries.
  --config-dir PATH       Node configuration directory.
  --state-dir PATH        Node service home/state directory.
  --data-dir PATH         agent-remote runtime data directory.
  --user NAME             System user for the node service.
  --target TARGET         Exact target, for example linux/amd64/glibc.
  --os OS                 Override detected OS: linux or darwin.
  --arch ARCH             Override detected arch: amd64, x86_64, arm64, aarch64.
  --libc glibc|musl       Linux package libc label. Default: glibc.
  --server-url URL        Control-plane base URL.
  --node-id ID            Node ID created in the admin console.
  --registration-token T  One-time node registration token.
  --force-register        Exchange the token even if this node is already configured.
  --runtime-backends LIST Comma-separated backends. Default: native.
  --wireguard-interface NAME  WireGuard interface. Default: agent-remote.
  --wireguard-address CIDR    Node tunnel address. Default: 10.77.0.1/24.
  --wireguard-endpoint HOST:PORT  Public UDP endpoint; inferred from server URL by default.
  --wireguard-listen-port PORT    UDP listen port. Default: 51820.
  --claude-channel VALUE  Official Claude channel. Default: stable.
  --claude-version VALUE  Pin an official Claude version, or use with --claude-source.
  --claude-source PATH    Pinned Claude executable path or URL.
  --claude-sha256 HASH    Required checksum for --claude-source.
  --no-dependencies       Do not install native runtime OS packages.
  --no-claude             Do not install the managed Claude runtime.
  --no-start              Install and register without starting services.
  --no-systemd            Do not install the systemd unit.
  --no-user               Do not create or modify a system user.
  --no-sudo               Do not re-exec through sudo when not root.
  --keep-temp             Keep downloaded archive and extraction directory.
  -h, --help              Show this help.

Environment:
  AGENT_REMOTE_NODE_VERSION  Same as --version.
  AGENT_REMOTE_NODE_REPO     Same as --repo.
  PREFIX                     Same as --prefix.
  CONFIG_DIR                 Same as --config-dir.
  STATE_DIR                  Same as --state-dir.
  DATA_DIR                   Same as --data-dir.
  USER_NAME                  Same as --user.
  AGENT_REMOTE_NODE_TARGET   Same as --target.
  AGENT_REMOTE_NODE_OS       Same as --os.
  AGENT_REMOTE_NODE_ARCH     Same as --arch.
  AGENT_REMOTE_NODE_LIBC     Same as --libc.
  AGENT_REMOTE_SERVER_URL    Same as --server-url.
  AGENT_REMOTE_NODE_ID       Same as --node-id.
  AGENT_REMOTE_REGISTRATION_TOKEN  Same as --registration-token.
  AGENT_REMOTE_RUNTIME_BACKENDS    Same as --runtime-backends.
  AGENT_REMOTE_WIREGUARD_INTERFACE Same as --wireguard-interface.
  AGENT_REMOTE_WIREGUARD_ADDRESS   Same as --wireguard-address.
  AGENT_REMOTE_WIREGUARD_ENDPOINT  Same as --wireguard-endpoint.
  AGENT_REMOTE_WIREGUARD_LISTEN_PORT Same as --wireguard-listen-port.
  CLAUDE_CHANNEL             Same as --claude-channel.
  CLAUDE_VERSION             Same as --claude-version.
  CLAUDE_SOURCE              Same as --claude-source.
  CLAUDE_SHA256              Same as --claude-sha256.
  INSTALL_DEPENDENCIES=0     Same as --no-dependencies.
  INSTALL_CLAUDE=0           Same as --no-claude.
  START_SERVICES=0           Same as --no-start.
  INSTALL_SYSTEMD=0          Same as --no-systemd.
  CREATE_USER=0              Same as --no-user.
  USE_SUDO=0                 Same as --no-sudo.
  KEEP_TEMP=1                Same as --keep-temp.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      VERSION="${2:?--version requires a value}"
      shift 2
      ;;
    --repo)
      REPO="${2:?--repo requires OWNER/REPO}"
      shift 2
      ;;
    --prefix)
      PREFIX="${2:?--prefix requires a path}"
      shift 2
      ;;
    --config-dir)
      CONFIG_DIR="${2:?--config-dir requires a path}"
      shift 2
      ;;
    --state-dir)
      STATE_DIR="${2:?--state-dir requires a path}"
      shift 2
      ;;
    --data-dir)
      DATA_DIR="${2:?--data-dir requires a path}"
      shift 2
      ;;
    --user)
      USER_NAME="${2:?--user requires a value}"
      shift 2
      ;;
    --target)
      TARGET_OVERRIDE="${2:?--target requires a value}"
      shift 2
      ;;
    --os)
      OS_OVERRIDE="${2:?--os requires a value}"
      shift 2
      ;;
    --arch)
      ARCH_OVERRIDE="${2:?--arch requires a value}"
      shift 2
      ;;
    --libc)
      LIBC="${2:?--libc requires glibc or musl}"
      shift 2
      ;;
    --server-url)
      SERVER_URL="${2:?--server-url requires a value}"
      shift 2
      ;;
    --node-id)
      NODE_ID="${2:?--node-id requires a value}"
      shift 2
      ;;
    --registration-token)
      REGISTRATION_TOKEN="${2:?--registration-token requires a value}"
      shift 2
      ;;
    --force-register)
      FORCE_REGISTER=1
      shift
      ;;
    --runtime-backends)
      RUNTIME_BACKENDS="${2:?--runtime-backends requires a value}"
      shift 2
      ;;
    --wireguard-interface)
      WIREGUARD_INTERFACE="${2:?--wireguard-interface requires a value}"
      shift 2
      ;;
    --wireguard-address)
      WIREGUARD_ADDRESS="${2:?--wireguard-address requires a value}"
      shift 2
      ;;
    --wireguard-endpoint)
      WIREGUARD_ENDPOINT="${2:?--wireguard-endpoint requires a value}"
      shift 2
      ;;
    --wireguard-listen-port)
      WIREGUARD_LISTEN_PORT="${2:?--wireguard-listen-port requires a value}"
      shift 2
      ;;
    --claude-channel)
      CLAUDE_CHANNEL="${2:?--claude-channel requires a value}"
      shift 2
      ;;
    --claude-version)
      CLAUDE_VERSION="${2:?--claude-version requires a value}"
      shift 2
      ;;
    --claude-source)
      CLAUDE_SOURCE="${2:?--claude-source requires a value}"
      shift 2
      ;;
    --claude-sha256)
      CLAUDE_SHA256="${2:?--claude-sha256 requires a value}"
      shift 2
      ;;
    --no-dependencies)
      INSTALL_DEPENDENCIES=0
      shift
      ;;
    --no-claude)
      INSTALL_CLAUDE=0
      shift
      ;;
    --no-start)
      START_SERVICES=0
      shift
      ;;
    --no-systemd)
      INSTALL_SYSTEMD=0
      shift
      ;;
    --no-user)
      CREATE_USER=0
      shift
      ;;
    --no-sudo)
      USE_SUDO=0
      shift
      ;;
    --keep-temp)
      KEEP_TEMP=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

validate_options() {
  local registration_values=0 backend
  [ -n "$SERVER_URL" ] && registration_values=$((registration_values + 1))
  [ -n "$NODE_ID" ] && registration_values=$((registration_values + 1))
  [ -n "$REGISTRATION_TOKEN" ] && registration_values=$((registration_values + 1))
  if [ "$registration_values" -ne 0 ] && [ "$registration_values" -ne 3 ]; then
    echo "--server-url, --node-id, and --registration-token must be provided together" >&2
    exit 2
  fi
  if { [ -n "$CLAUDE_SOURCE" ] && { [ -z "$CLAUDE_VERSION" ] || [ -z "$CLAUDE_SHA256" ]; }; } || \
     { [ -z "$CLAUDE_SOURCE" ] && [ -n "$CLAUDE_SHA256" ]; }; then
    echo "--claude-source, --claude-version, and --claude-sha256 must be provided together" >&2
    exit 2
  fi
  IFS=, read -r -a backends <<< "$RUNTIME_BACKENDS"
  if [ "${#backends[@]}" -eq 0 ]; then
    echo "at least one runtime backend is required" >&2
    exit 2
  fi
  for backend in "${backends[@]}"; do
    case "$(printf '%s' "$backend" | tr -d '[:space:]')" in
      native|docker_sandbox) ;;
      *) echo "unsupported runtime backend: $backend" >&2; exit 2 ;;
    esac
  done
  case "$WIREGUARD_INTERFACE" in
    ''|*[!A-Za-z0-9_.-]*) echo "invalid WireGuard interface: $WIREGUARD_INTERFACE" >&2; exit 2 ;;
  esac
  if [ "${#WIREGUARD_INTERFACE}" -gt 15 ]; then
    echo "WireGuard interface must not exceed 15 characters" >&2
    exit 2
  fi
  case "$WIREGUARD_LISTEN_PORT" in
    ''|*[!0-9]*) echo "invalid WireGuard listen port" >&2; exit 2 ;;
  esac
  if [ "$WIREGUARD_LISTEN_PORT" -lt 1 ] || [ "$WIREGUARD_LISTEN_PORT" -gt 65535 ]; then
    echo "invalid WireGuard listen port" >&2
    exit 2
  fi
}

backend_enabled() {
  printf '%s' "$RUNTIME_BACKENDS" | tr -d '[:space:]' | tr ',' '\n' | grep -Fxq "$1"
}

validate_options

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
    return
  fi
  if [ "$USE_SUDO" = "0" ]; then
    "$@"
    return
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    echo "sudo is required; rerun as root or install sudo" >&2
    exit 1
  fi
  sudo "$@"
}

run_as_service_user() {
  if [ "$CREATE_USER" != "1" ] || ! id "$USER_NAME" >/dev/null 2>&1; then
    "$@"
    return
  fi
  if [ "$(id -u)" -eq 0 ]; then
    runuser -u "$USER_NAME" -- sh -c 'cd "$1" && shift && exec "$@"' sh "$STATE_DIR" "$@"
  else
    sudo -u "$USER_NAME" sh -c 'cd "$1" && shift && exec "$@"' sh "$STATE_DIR" "$@"
  fi
}

install_system_dependencies() {
  if [ "$INSTALL_DEPENDENCIES" != "1" ]; then
    return
  fi
  if [ "$(uname -s)" != "Linux" ]; then
    echo "native runtime dependency installation is only supported on Linux" >&2
    exit 1
  fi
  if [ ! -r /etc/os-release ]; then
    echo "cannot identify this Linux distribution" >&2
    exit 1
  fi
  local distro version_id
  distro="$(. /etc/os-release; printf '%s' "${ID:-unknown}")"
  version_id="$(. /etc/os-release; printf '%s' "${VERSION_ID:-0}")"
  case "$distro" in
    debian)
      if [ "${version_id%%.*}" -lt 12 ]; then
        echo "native runtime requires Debian 12+" >&2
        exit 1
      fi
      ;;
    ubuntu)
      if [ "${version_id%%.*}" -lt 22 ]; then
        echo "native runtime requires Ubuntu 22.04+" >&2
        exit 1
      fi
      ;;
    *)
      echo "automatic native dependency installation supports Debian 12+ and Ubuntu 22.04+; found $distro $version_id" >&2
      exit 1
      ;;
  esac
  echo "Installing native runtime dependencies"
  run_as_root apt-get update
  if backend_enabled native; then
    run_as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends \
      acl bubblewrap ca-certificates curl gh git iproute2 locales nftables openssh-client openssh-server procps tar tmux util-linux wireguard-tools
  else
    run_as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-upgrade --no-install-recommends wireguard-tools
  fi
  if ! backend_enabled native; then
    return
  fi
  if ! locale -a | tr '[:upper:]' '[:lower:]' | grep -Eq '^en_us\.(utf-?8|utf8)$'; then
    run_as_root locale-gen en_US.UTF-8
  fi
  if command -v systemctl >/dev/null 2>&1; then
    run_as_root systemctl enable --now ssh.service
  fi
}

configure_native_host() {
  if ! backend_enabled native || [ "$INSTALL_SYSTEMD" != "1" ]; then
    return
  fi
  local settings
  settings="$(mktemp "${TMP_DIR%/}/agent-remote-sysctl.XXXXXX")"
  track_temp "$settings"
  printf '%s\n' 'net.ipv4.ip_forward = 1' > "$settings"
  if [ -e /proc/sys/kernel/unprivileged_userns_clone ]; then
    printf '%s\n' 'kernel.unprivileged_userns_clone = 1' >> "$settings"
  fi
  if [ -e /proc/sys/user/max_user_namespaces ]; then
    printf '%s\n' 'user.max_user_namespaces = 28633' >> "$settings"
  fi
  run_as_root install -m 0644 "$settings" /etc/sysctl.d/99-agent-remote-native.conf
  rm -f "$settings"
  run_as_root sysctl --system >/dev/null
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing packaged file: $1" >&2
    exit 1
  fi
}

resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    VERSION="${VERSION#v}"
    return
  fi
  need_cmd curl
  local effective tag
  effective="$(curl --fail --show-error --silent --location --output /dev/null --write-out '%{url_effective}' "https://github.com/${REPO}/releases/latest" || true)"
  tag="${effective##*/}"
  if [ -z "$tag" ] || [ "$tag" = "latest" ] || [ "$tag" = "releases" ]; then
    tag="$(curl --fail --show-error --silent --location "https://api.github.com/repos/${REPO}/releases/latest" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1)"
  fi
  if [ -z "$tag" ]; then
    echo "failed to resolve latest release for $REPO; retry with --version 0.0.4" >&2
    exit 1
  fi
  VERSION="${tag#v}"
}

detect_target() {
  if [ -n "$TARGET_OVERRIDE" ]; then
    echo "$TARGET_OVERRIDE"
    return
  fi

  local os arch os_part arch_part
  os="${OS_OVERRIDE:-$(uname -s)}"
  arch="${ARCH_OVERRIDE:-$(uname -m)}"

  case "$(printf '%s' "$os" | tr '[:upper:]' '[:lower:]')" in
    linux) os_part="linux" ;;
    darwin) os_part="darwin" ;;
    *)
      echo "unsupported OS: $os" >&2
      exit 1
      ;;
  esac

  case "$(printf '%s' "$arch" | tr '[:upper:]' '[:lower:]')" in
    x86_64|amd64) arch_part="amd64" ;;
    arm64|aarch64) arch_part="arm64" ;;
    *)
      echo "unsupported architecture: $arch" >&2
      exit 1
      ;;
  esac

  if [ "$os_part" = "linux" ]; then
    if [ "$LIBC" != "glibc" ] && [ "$LIBC" != "musl" ]; then
      echo "invalid libc label: $LIBC" >&2
      exit 1
    fi
    echo "$os_part/$arch_part/$LIBC"
  else
    echo "$os_part/$arch_part"
  fi
}

package_name() {
  local target="$1"
  local os arch libc
  IFS=/ read -r os arch libc <<EOF
$target
EOF
  if [ "$os" = "linux" ]; then
    echo "agent-remote-node-${VERSION}-${os}-${arch}-${libc:-glibc}"
  else
    echo "agent-remote-node-${VERSION}-${os}-${arch}"
  fi
}

check_dependency() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "warn missing dependency: $name" >&2
  else
    echo "ok dependency: $name"
  fi
}

escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[&|]/\\&/g'
}

render_system_file() {
  local source="$1" destination="$2" prefix config_dir state_dir data_dir user_name claude_runtime_root wireguard_interface
  prefix="$(escape_sed_replacement "$PREFIX")"
  config_dir="$(escape_sed_replacement "$CONFIG_DIR")"
  state_dir="$(escape_sed_replacement "$STATE_DIR")"
  data_dir="$(escape_sed_replacement "$DATA_DIR")"
  user_name="$(escape_sed_replacement "$USER_NAME")"
  claude_runtime_root="$(escape_sed_replacement "$CLAUDE_RUNTIME_ROOT")"
  wireguard_interface="$(escape_sed_replacement "$WIREGUARD_INTERFACE")"
  sed \
    -e "s|/var/lib/agent-remote-runtime|@AGENT_REMOTE_RUNTIME_STATE@|g" \
    -e "s|/var/lib/agent-remote-node|@AGENT_REMOTE_NODE_STATE@|g" \
    -e "s|/var/lib/agent-remote/users|@AGENT_REMOTE_USERS@|g" \
    -e "s|/var/lib/agent-remote|$data_dir|g" \
    -e "s|/etc/agent-remote-node|$config_dir|g" \
    -e "s|/usr/local|$prefix|g" \
    -e "s|/opt/agent-remote/runtimes/claude|$claude_runtime_root|g" \
    -e "s|User=agent-remote|User=$user_name|g" \
    -e "s|Group=agent-remote|Group=$user_name|g" \
    -e "s|--group agent-remote|--group $user_name|g" \
    -e "s|--user agent-remote|--user $user_name|g" \
    -e "s|--wireguard-interface agent-remote|--wireguard-interface $wireguard_interface|g" \
    -e "s|wg-quick@agent-remote|wg-quick@$wireguard_interface|g" \
    -e "s|--wireguard-listen-port 51820|--wireguard-listen-port $WIREGUARD_LISTEN_PORT|g" \
    -e "s|^agent-remote ALL=|$user_name ALL=|" \
    -e "s|@AGENT_REMOTE_NODE_STATE@|$state_dir|g" \
    -e "s|@AGENT_REMOTE_USERS@|$data_dir/users|g" \
    -e "s|@AGENT_REMOTE_RUNTIME_STATE@|/var/lib/agent-remote-runtime|g" \
    "$source" > "$destination"
}

check_native_prerequisites() {
  if ! backend_enabled native; then
    return
  fi
  local kernel_version systemd_version distro version_id failed dependency
  failed=0
  if [ "$(uname -s)" != "Linux" ]; then
    echo "error native runtime requires Linux" >&2
    [ "$STRICT_PREREQUISITES" = "1" ] && return 1
    return 0
  fi
  for dependency in bwrap systemd-run systemctl nft ip setfacl mount umount mountpoint tmux locale; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
      echo "error missing native runtime dependency: $dependency" >&2
      failed=1
    fi
  done
  kernel_version="$(uname -r | cut -d- -f1)"
  if [ "$(printf '%s\n' 5.15 "$kernel_version" | sort -V | head -n1)" != "5.15" ]; then
    echo "error native runtime requires Linux kernel 5.15+, found $kernel_version" >&2
    failed=1
  fi
  systemd_version="$(systemd-run --version 2>/dev/null | awk 'NR==1 {print $2}')"
  if [ -z "$systemd_version" ] || [ "$systemd_version" -lt 249 ]; then
    echo "error native runtime requires systemd 249+, found ${systemd_version:-unknown}" >&2
    failed=1
  fi
  if [ ! -f /sys/fs/cgroup/cgroup.controllers ]; then
    echo "error native runtime requires cgroup v2" >&2
    failed=1
  fi
  if [ "$INSTALL_SYSTEMD" = "1" ] && [ ! -d /run/systemd/system ]; then
    echo "error native runtime requires systemd as PID 1" >&2
    failed=1
  fi
  if [ -r /etc/os-release ]; then
    distro="$(. /etc/os-release; printf '%s' "${ID:-unknown}")"
    version_id="$(. /etc/os-release; printf '%s' "${VERSION_ID:-0}")"
    case "$distro" in
      debian) [ "${version_id%%.*}" -ge 12 ] || { echo "error native runtime requires Debian 12+" >&2; failed=1; } ;;
      ubuntu) [ "${version_id%%.*}" -ge 22 ] || { echo "error native runtime requires Ubuntu 22.04+" >&2; failed=1; } ;;
      *) echo "error native runtime is not qualified on $distro $version_id" >&2; failed=1 ;;
    esac
  fi
  if command -v bwrap >/dev/null 2>&1 && ! run_as_service_user bwrap --ro-bind / / --proc /proc --dev /dev --unshare-user true >/dev/null 2>&1; then
    echo "error Bubblewrap user namespace self-test failed" >&2
    failed=1
  fi
  if command -v locale >/dev/null 2>&1 && ! locale -a | tr '[:upper:]' '[:lower:]' | grep -Eq '^en_us\.(utf-?8|utf8)$'; then
    echo "error en_US.UTF-8 locale is not generated" >&2
    failed=1
  fi
  if [ "$failed" -ne 0 ]; then
    if [ "$STRICT_PREREQUISITES" = "1" ]; then
      return 1
    fi
    echo "warning native prerequisite failures were ignored because STRICT_PREREQUISITES=0" >&2
  fi
}

install_user() {
  if [ "$CREATE_USER" != "1" ]; then
    return
  fi
  if ! id "$USER_NAME" >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
      run_as_root useradd --system --create-home --home-dir "$STATE_DIR" --shell /bin/sh "$USER_NAME"
    else
      echo "warn useradd not found; skipping system user creation" >&2
      return
    fi
  fi
  if command -v usermod >/dev/null 2>&1; then
    run_as_root usermod --shell /bin/sh "$USER_NAME"
  fi
}

configure_ssh_gateway() {
  if [ "$CREATE_USER" != "1" ] || [ "$INSTALL_SYSTEMD" != "1" ]; then
    return
  fi
  local config
  config="$(mktemp "${TMP_DIR%/}/agent-remote-sshd.XXXXXX")"
  track_temp "$config"
  cat > "$config" <<EOF
Match User $USER_NAME
    AuthorizedKeysFile $STATE_DIR/authorized_keys
    AuthenticationMethods publickey
    PasswordAuthentication no
    KbdInteractiveAuthentication no
    AllowAgentForwarding yes
    AllowTcpForwarding no
    X11Forwarding no
    PermitTunnel no
    PermitTTY yes
Match all
EOF
  run_as_root install -d -m 0755 /etc/ssh/sshd_config.d /run/sshd
  run_as_root install -m 0644 "$config" /etc/ssh/sshd_config.d/90-agent-remote.conf
  rm -f "$config"
  if ! run_as_root sshd -t; then
    run_as_root rm -f /etc/ssh/sshd_config.d/90-agent-remote.conf
    echo "generated agent-remote SSH configuration is invalid" >&2
    exit 1
  fi
  run_as_root systemctl reload ssh.service
}

install_packaged() {
  local package_dir="$1" rendered
  require_file "$package_dir/agent-remote-node"
  require_file "$package_dir/agent-remote-attach"
  require_file "$package_dir/agent-remote-runtime"
  require_file "$package_dir/config.example.json"
  require_file "$package_dir/scripts/install-claude-runtime.sh"
  if [ "$VERSION" = "latest" ] && [ -f "$package_dir/VERSION" ]; then
    VERSION="$(tr -d '[:space:]' < "$package_dir/VERSION")"
  fi

  install_user

  if backend_enabled docker_sandbox; then
    if ! command -v docker >/dev/null 2>&1 || ! docker sandbox --help >/dev/null 2>&1; then
      echo "error docker_sandbox requires a Docker CLI with the docker sandbox command" >&2
      exit 1
    fi
  fi
  check_dependency tmux
  check_dependency sshd
  check_dependency bwrap
  check_dependency systemd-run
  check_dependency nft
  check_dependency ip
  check_dependency setfacl
  check_dependency mount
  check_dependency umount
  check_dependency cp
  check_dependency wg
  check_dependency wg-quick
  check_native_prerequisites
  if [ "$(uname -s)" = "Linux" ] && [ ! -c /dev/net/tun ]; then
    echo "warn missing /dev/net/tun; WireGuard tunnel support may be unavailable" >&2
  fi

  run_as_root install -d -m 0755 "$PREFIX/bin"
  run_as_root install -m 0755 "$package_dir/agent-remote-node" "$PREFIX/bin/agent-remote-node"
  run_as_root install -m 0755 "$package_dir/agent-remote-attach" "$PREFIX/bin/agent-remote-attach"
  run_as_root install -m 0755 "$package_dir/agent-remote-runtime" "$PREFIX/bin/agent-remote-runtime"
  run_as_root install -d -m 0755 "$PREFIX/lib/agent-remote-node"
  run_as_root install -m 0755 "$package_dir/scripts/install-claude-runtime.sh" "$PREFIX/lib/agent-remote-node/install-claude-runtime.sh"

  if [ "$CREATE_USER" = "1" ] && id "$USER_NAME" >/dev/null 2>&1; then
    run_as_root install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$CONFIG_DIR" "$STATE_DIR" "$DATA_DIR"
    run_as_root install -d -m 0710 -o root -g "$USER_NAME" "$DATA_DIR/users"
    run_as_root setfacl -m "u:$USER_NAME:--x" "$DATA_DIR/users"
    if [ ! -f "$CONFIG_DIR/config.json" ]; then
      run_as_root install -m 0600 -o "$USER_NAME" -g "$USER_NAME" "$package_dir/config.example.json" "$CONFIG_DIR/config.json"
    fi
  else
    run_as_root install -d -m 0750 "$CONFIG_DIR" "$STATE_DIR" "$DATA_DIR"
    if [ ! -f "$CONFIG_DIR/config.json" ]; then
      run_as_root install -m 0600 "$package_dir/config.example.json" "$CONFIG_DIR/config.json"
    fi
  fi

  configure_ssh_gateway

  if [ "$INSTALL_SYSTEMD" = "1" ]; then
    if [ ! -d /run/systemd/system ]; then
      echo "warn systemd not detected; skipping service install" >&2
    else
      require_file "$package_dir/systemd/agent-remote-node.service"
      require_file "$package_dir/systemd/agent-remote-node.env.example"
      require_file "$package_dir/systemd/agent-remote-runtime.service"
      require_file "$package_dir/systemd/agent-remote-runtime.sudoers"
      rendered="$(mktemp -d "${TMP_DIR%/}/agent-remote-systemd.XXXXXX")"
      track_temp "$rendered"
      render_system_file "$package_dir/systemd/agent-remote-node.service" "$rendered/agent-remote-node.service"
      render_system_file "$package_dir/systemd/agent-remote-runtime.service" "$rendered/agent-remote-runtime.service"
      render_system_file "$package_dir/systemd/agent-remote-runtime.sudoers" "$rendered/agent-remote-runtime.sudoers"
      render_system_file "$package_dir/systemd/agent-remote-node.env.example" "$rendered/agent-remote-node.env"
      run_as_root install -m 0644 "$rendered/agent-remote-node.service" /etc/systemd/system/agent-remote-node.service
      run_as_root install -m 0644 "$rendered/agent-remote-runtime.service" /etc/systemd/system/agent-remote-runtime.service
      run_as_root install -m 0440 "$rendered/agent-remote-runtime.sudoers" /etc/sudoers.d/agent-remote-runtime
      if [ ! -f "$CONFIG_DIR/agent-remote-node.env" ]; then
        run_as_root install -m 0644 "$rendered/agent-remote-node.env" "$CONFIG_DIR/agent-remote-node.env"
      fi
      rm -rf "$rendered"
      run_as_root systemctl daemon-reload
    fi
  fi
}

download_and_install() {
  need_cmd curl
  need_cmd tar
  resolve_version
  local target package url work archive
  target="$(detect_target)"
  package="$(package_name "$target")"
  url="https://github.com/${REPO}/releases/download/v${VERSION}/${package}.tar.gz"
  work="$(mktemp -d "${TMP_DIR%/}/agent-remote-node-install.XXXXXX")"
  if [ "$KEEP_TEMP" != "1" ]; then
    track_temp "$work"
  fi
  archive="$work/${package}.tar.gz"

  echo "Downloading $url"
  curl --fail --show-error --location --retry 5 --retry-all-errors --retry-delay 3 "$url" -o "$archive"
  tar -xzf "$archive" -C "$work"
  install_packaged "$work/$package"

  if [ "$KEEP_TEMP" = "1" ]; then
    echo "kept temporary directory: $work"
  else
    rm -rf "$work"
  fi
}

install_managed_claude() {
  if ! backend_enabled native; then
    return
  fi
  if [ "$INSTALL_CLAUDE" = "1" ]; then
    local installer args
    installer="$PREFIX/lib/agent-remote-node/install-claude-runtime.sh"
    args=(--runtime-root "$CLAUDE_RUNTIME_ROOT" --channel "$CLAUDE_CHANNEL")
    if [ -n "$CLAUDE_VERSION" ]; then
      args+=(--version "$CLAUDE_VERSION")
    fi
    if [ -n "$CLAUDE_SOURCE" ]; then
      args+=(--source "$CLAUDE_SOURCE" --sha256 "$CLAUDE_SHA256")
    fi
    run_as_root "$installer" "${args[@]}"
  fi
  if [ ! -x "$CLAUDE_RUNTIME_ROOT/current/bin/claude" ]; then
    echo "managed Claude runtime is required for the native backend" >&2
    exit 1
  fi
}

register_node() {
  if [ -z "$REGISTRATION_TOKEN" ]; then
    return
  fi
  local args
  args=(
    register
    --config "$CONFIG_DIR/config.json"
    --server-url "$SERVER_URL"
    --node-id "$NODE_ID"
    --registration-token "$REGISTRATION_TOKEN"
    --version "$VERSION"
    --runtime-backends "$RUNTIME_BACKENDS"
    --system-install
    --prefix "$PREFIX"
    --state-dir "$STATE_DIR"
    --data-dir "$DATA_DIR"
    --claude-runtime-path "$CLAUDE_RUNTIME_ROOT/current/bin/claude"
  )
  if [ "$FORCE_REGISTER" = "1" ]; then
    args+=(--force)
  fi
  run_as_root "$PREFIX/bin/agent-remote-node" "${args[@]}"
  if [ "$CREATE_USER" = "1" ] && id "$USER_NAME" >/dev/null 2>&1; then
    run_as_root chown "$USER_NAME:$USER_NAME" "$CONFIG_DIR/config.json"
  fi
  run_as_root chmod 0600 "$CONFIG_DIR/config.json"
}

configure_wireguard() {
  if [ "$INSTALL_SYSTEMD" != "1" ] || [ "$(uname -s)" != "Linux" ]; then
    return
  fi
  if ! command -v wg >/dev/null 2>&1 || ! command -v wg-quick >/dev/null 2>&1; then
    echo "WireGuard tools are required; install wireguard-tools or remove --no-dependencies" >&2
    exit 1
  fi
  local key_path config_path public_key wg_path endpoint_args
  key_path="$CONFIG_DIR/wireguard.key"
  config_path="/etc/wireguard/$WIREGUARD_INTERFACE.conf"
  wg_path="$(command -v wg)"
  run_as_root install -d -m 0700 "$CONFIG_DIR"
  run_as_root install -d -m 0700 /etc/wireguard
  if ! run_as_root test -s "$key_path"; then
    run_as_root sh -c 'umask 077; "$1" genkey > "$2"' sh "$wg_path" "$key_path"
  fi
  run_as_root chmod 0600 "$key_path"
  public_key="$(run_as_root sh -c '"$1" pubkey < "$2"' sh "$wg_path" "$key_path")"
  run_as_root sh -c 'umask 077; { printf "[Interface]\nPrivateKey = "; cat "$1"; printf "Address = %s\nListenPort = %s\n" "$3" "$4"; } > "$2"' \
    sh "$key_path" "$config_path" "$WIREGUARD_ADDRESS" "$WIREGUARD_LISTEN_PORT"
  endpoint_args=()
  if [ -n "$WIREGUARD_ENDPOINT" ]; then
    endpoint_args=(--endpoint "$WIREGUARD_ENDPOINT")
  fi
  run_as_root "$PREFIX/bin/agent-remote-node" configure-wireguard \
    --config "$CONFIG_DIR/config.json" \
    --public-key "$public_key" \
    --address "$WIREGUARD_ADDRESS" \
    --interface "$WIREGUARD_INTERFACE" \
    --private-key-path "$key_path" \
    --listen-port "$WIREGUARD_LISTEN_PORT" \
    --version "$VERSION" \
    "${endpoint_args[@]}"
  if [ "$CREATE_USER" = "1" ] && id "$USER_NAME" >/dev/null 2>&1; then
    run_as_root chown "$USER_NAME:$USER_NAME" "$CONFIG_DIR/config.json"
  fi
  run_as_root chmod 0600 "$CONFIG_DIR/config.json"
}

start_and_verify() {
  if [ "$START_SERVICES" != "1" ]; then
    return
  fi
  if [ "$INSTALL_SYSTEMD" != "1" ]; then
    echo "--no-systemd requires --no-start" >&2
    exit 2
  fi
  run_as_root systemctl enable agent-remote-runtime.service
  run_as_root systemctl restart agent-remote-runtime.service
  run_as_root systemctl enable "wg-quick@$WIREGUARD_INTERFACE.service"
  run_as_root systemctl restart "wg-quick@$WIREGUARD_INTERFACE.service"
  local attempt
  for attempt in $(seq 1 20); do
    if [ -S /run/agent-remote/runtime.sock ]; then
      break
    fi
    sleep 1
  done
  run_as_root "$PREFIX/bin/agent-remote-runtime" probe --socket /run/agent-remote/runtime.sock
  if ! run_as_service_user "$PREFIX/bin/agent-remote-node" heartbeat --config "$CONFIG_DIR/config.json"; then
    if [ -z "$REGISTRATION_TOKEN" ]; then
      echo "node files and Claude are installed; pass registration options to start the node" >&2
      return
    fi
    exit 1
  fi
  NODE_READY=1
  run_as_service_user "$PREFIX/bin/agent-remote-node" install-ssh --config "$CONFIG_DIR/config.json"
  run_as_root systemctl enable agent-remote-node.service
  run_as_root systemctl restart agent-remote-node.service
  if ! run_as_root systemctl is-active --quiet agent-remote-runtime.service || \
     ! run_as_root systemctl is-active --quiet agent-remote-node.service; then
    echo "agent-remote services did not become active" >&2
    exit 1
  fi
}

if [ "${AGENT_REMOTE_INSTALL_LIB_ONLY:-0}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

install_system_dependencies
configure_native_host
if [ -n "$SCRIPT_DIR" ] && [ -x "$SCRIPT_DIR/agent-remote-node" ] && [ -x "$SCRIPT_DIR/agent-remote-attach" ]; then
  install_packaged "$SCRIPT_DIR"
elif [ -n "$SCRIPT_DIR" ] && [ -x "$SCRIPT_DIR/../agent-remote-node" ] && [ -x "$SCRIPT_DIR/../agent-remote-attach" ]; then
  install_packaged "$(cd "$SCRIPT_DIR/.." && pwd)"
else
  download_and_install
fi
install_managed_claude
register_node
configure_wireguard
start_and_verify

echo "agent-remote-node installation completed"
echo "  config: $CONFIG_DIR/config.json"
echo "  runtime backends: $RUNTIME_BACKENDS"
if [ "$INSTALL_SYSTEMD" = "1" ] && [ "$(uname -s)" = "Linux" ]; then
  echo "  WireGuard: $WIREGUARD_INTERFACE ($WIREGUARD_ADDRESS, UDP $WIREGUARD_LISTEN_PORT)"
fi
if backend_enabled native; then
  echo "  Claude: $CLAUDE_RUNTIME_ROOT/current/bin/claude"
fi
if [ "$NODE_READY" = "1" ] && [ "$START_SERVICES" = "1" ]; then
  echo "  services: active"
elif [ -z "$REGISTRATION_TOKEN" ]; then
  echo "  registration: pending (--server-url, --node-id, --registration-token)"
fi
