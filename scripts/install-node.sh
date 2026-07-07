#!/usr/bin/env bash
set -euo pipefail

PREFIX="${PREFIX:-/usr/local}"
CONFIG_DIR="${CONFIG_DIR:-/etc/agent-remote-node}"
STATE_DIR="${STATE_DIR:-/var/lib/agent-remote-node}"
DATA_DIR="${DATA_DIR:-/var/lib/agent-remote}"
USER_NAME="${USER_NAME:-agent-remote}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "install-node.sh must run as root" >&2
    exit 1
  fi
}

check_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "warn missing dependency: $name" >&2
  else
    echo "ok dependency: $name"
  fi
}

install_user() {
  if ! id "$USER_NAME" >/dev/null 2>&1; then
    useradd --system --create-home --home-dir "$STATE_DIR" --shell /usr/sbin/nologin "$USER_NAME"
  fi
  if getent group docker >/dev/null 2>&1; then
    usermod -aG docker "$USER_NAME"
  fi
}

install_binaries() {
  install -d -m 0755 "$PREFIX/bin"
  if [ -x "${REPO_DIR}/agent-remote-node" ] && [ -x "${REPO_DIR}/agent-remote-attach" ]; then
    install -m 0755 "${REPO_DIR}/agent-remote-node" "$PREFIX/bin/agent-remote-node"
    install -m 0755 "${REPO_DIR}/agent-remote-attach" "$PREFIX/bin/agent-remote-attach"
  else
    echo "building node binaries from source"
    (cd "$REPO_DIR" && go build -o "$PREFIX/bin/agent-remote-node" ./cmd/agent-remote-node)
    (cd "$REPO_DIR" && go build -o "$PREFIX/bin/agent-remote-attach" ./cmd/agent-remote-attach)
  fi
}

install_config() {
  install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$CONFIG_DIR" "$STATE_DIR" "$DATA_DIR"
  if [ ! -f "$CONFIG_DIR/config.json" ]; then
    install -m 0600 -o "$USER_NAME" -g "$USER_NAME" "$REPO_DIR/config.example.json" "$CONFIG_DIR/config.json"
  fi
}

install_service() {
  install -m 0644 "$REPO_DIR/systemd/agent-remote-node.service" /etc/systemd/system/agent-remote-node.service
  if [ ! -f "$CONFIG_DIR/agent-remote-node.env" ]; then
    install -m 0644 "$REPO_DIR/systemd/agent-remote-node.env.example" "$CONFIG_DIR/agent-remote-node.env"
  fi
  systemctl daemon-reload
}

require_root
check_command docker
check_command tmux
check_command sshd
check_command mutagen
if [ ! -c /dev/net/tun ]; then
  echo "warn missing /dev/net/tun; WireGuard tunnel support may be unavailable" >&2
fi
install_user
install_binaries
install_config
install_service

cat <<EOF
agent-remote-node installed.

Edit:
  $CONFIG_DIR/config.json

Register:
  agent-remote-node register --config $CONFIG_DIR/config.json --server-url <url> --node-id <id> --registration-token <token>

Start:
  systemctl enable --now agent-remote-node
EOF
