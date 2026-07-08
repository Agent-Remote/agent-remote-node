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
  curl -fsSL https://raw.githubusercontent.com/Agent-Remote/agent-remote-node/main/scripts/install.sh | sudo bash

Installs the latest agent-remote-node release by default. When executed from an
extracted release archive, installs the packaged files directly.

Options:
  --version VERSION       Release version, for example 0.0.3 or v0.0.3.
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
    echo "failed to resolve latest release for $REPO; retry with --version 0.0.3" >&2
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

install_user() {
  if [ "$CREATE_USER" != "1" ]; then
    return
  fi
  if ! id "$USER_NAME" >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
      run_as_root useradd --system --create-home --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$USER_NAME"
    else
      echo "warn useradd not found; skipping system user creation" >&2
      return
    fi
  fi
  if getent group docker >/dev/null 2>&1; then
    run_as_root usermod -aG docker "$USER_NAME"
  fi
}

install_packaged() {
  local package_dir="$1"
  require_file "$package_dir/agent-remote-node"
  require_file "$package_dir/agent-remote-attach"
  require_file "$package_dir/config.example.json"

  check_dependency docker
  check_dependency tmux
  check_dependency sshd
  check_dependency mutagen
  if [ "$(uname -s)" = "Linux" ] && [ ! -c /dev/net/tun ]; then
    echo "warn missing /dev/net/tun; WireGuard tunnel support may be unavailable" >&2
  fi

  install_user

  run_as_root install -d -m 0755 "$PREFIX/bin"
  run_as_root install -m 0755 "$package_dir/agent-remote-node" "$PREFIX/bin/agent-remote-node"
  run_as_root install -m 0755 "$package_dir/agent-remote-attach" "$PREFIX/bin/agent-remote-attach"

  if [ "$CREATE_USER" = "1" ] && id "$USER_NAME" >/dev/null 2>&1; then
    run_as_root install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$CONFIG_DIR" "$STATE_DIR" "$DATA_DIR"
    if [ ! -f "$CONFIG_DIR/config.json" ]; then
      run_as_root install -m 0600 -o "$USER_NAME" -g "$USER_NAME" "$package_dir/config.example.json" "$CONFIG_DIR/config.json"
    fi
  else
    run_as_root install -d -m 0750 "$CONFIG_DIR" "$STATE_DIR" "$DATA_DIR"
    if [ ! -f "$CONFIG_DIR/config.json" ]; then
      run_as_root install -m 0600 "$package_dir/config.example.json" "$CONFIG_DIR/config.json"
    fi
  fi

  if [ "$INSTALL_SYSTEMD" = "1" ]; then
    if [ ! -d /run/systemd/system ] && ! command -v systemctl >/dev/null 2>&1; then
      echo "warn systemd not detected; skipping service install" >&2
    else
      require_file "$package_dir/systemd/agent-remote-node.service"
      require_file "$package_dir/systemd/agent-remote-node.env.example"
      run_as_root install -m 0644 "$package_dir/systemd/agent-remote-node.service" /etc/systemd/system/agent-remote-node.service
      if [ ! -f "$CONFIG_DIR/agent-remote-node.env" ]; then
        run_as_root install -m 0644 "$package_dir/systemd/agent-remote-node.env.example" "$CONFIG_DIR/agent-remote-node.env"
      fi
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

if [ -n "$SCRIPT_DIR" ] && [ -x "$SCRIPT_DIR/agent-remote-node" ] && [ -x "$SCRIPT_DIR/agent-remote-attach" ]; then
  install_packaged "$SCRIPT_DIR"
elif [ -n "$SCRIPT_DIR" ] && [ -x "$SCRIPT_DIR/../agent-remote-node" ] && [ -x "$SCRIPT_DIR/../agent-remote-attach" ]; then
  install_packaged "$(cd "$SCRIPT_DIR/.." && pwd)"
else
  download_and_install
fi

cat <<EOF
agent-remote-node installed.

Edit:
  $CONFIG_DIR/config.json

Register:
  agent-remote-node register --config $CONFIG_DIR/config.json --server-url <url> --node-id <id> --registration-token <token>
EOF

if [ "$INSTALL_SYSTEMD" = "1" ]; then
  cat <<'EOF'

Start:
  systemctl enable --now agent-remote-node
EOF
else
  cat <<EOF

Run:
  $PREFIX/bin/agent-remote-node run --config $CONFIG_DIR/config.json
EOF
fi
