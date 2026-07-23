#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-install-test.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

fail() {
  echo "install script test failed: $*" >&2
  exit 1
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

bash -n "$ROOT/scripts/install.sh" "$ROOT/scripts/install-claude-runtime.sh" "$ROOT/scripts/build-release.sh"
"$ROOT/scripts/install.sh" --help | grep -q -- '--registration-token' || fail "one-command help is incomplete"
grep -q '^Match all$' "$ROOT/scripts/install.sh" || fail "SSH Match block is not reset"
grep -q 'apt-get install -y --no-upgrade' "$ROOT/scripts/install.sh" || \
  fail "dependency installation may upgrade existing packages"

cleanup_probe="$WORK/cleanup-probe"
if AGENT_REMOTE_INSTALL_LIB_ONLY=1 bash -c \
  'script=$1; probe=$2; set --; source "$script"; mkdir -p "$probe"; track_temp "$probe"; exit 17' \
  sh "$ROOT/scripts/install.sh" "$cleanup_probe"; then
  fail "cleanup probe unexpectedly succeeded"
fi
[ ! -e "$cleanup_probe" ] || fail "installer failure left temporary files behind"

rendered_unit="$WORK/agent-remote-runtime.service"
AGENT_REMOTE_INSTALL_LIB_ONLY=1 \
PREFIX=/opt/agent-remote-installer-e2e/prefix \
CONFIG_DIR=/etc/agent-remote-installer-e2e \
STATE_DIR=/var/lib/agent-remote-installer-e2e-node \
DATA_DIR=/var/lib/agent-remote-installer-e2e-data \
USER_NAME=agent-remote-e2e \
CLAUDE_RUNTIME_ROOT=/opt/agent-remote-installer-e2e/claude \
  bash -c 'script=$1; source_unit=$2; destination=$3; set --; source "$script"; render_system_file "$source_unit" "$destination"' sh \
  "$ROOT/scripts/install.sh" "$ROOT/systemd/agent-remote-runtime.service" "$rendered_unit"
grep -q -- '--state-root /var/lib/agent-remote-runtime' "$rendered_unit" || \
  fail "runtime state path was rewritten"
grep -q -- '--workspace-root /var/lib/agent-remote-installer-e2e-data/users' "$rendered_unit" || \
  fail "custom workspace path was rendered incorrectly"
grep -q -- '--account-root /var/lib/agent-remote-installer-e2e-data/users' "$rendered_unit" || \
  fail "custom account path was rendered incorrectly"
grep -q -- '--group agent-remote-e2e --user agent-remote-e2e' "$rendered_unit" || \
  fail "custom runtime user and group were rendered incorrectly"
if grep -q 'installer-e2e-data-installer-e2e-data' "$rendered_unit"; then
  fail "custom data path was substituted twice"
fi

rendered_sudoers="$WORK/agent-remote-runtime.sudoers"
AGENT_REMOTE_INSTALL_LIB_ONLY=1 \
PREFIX=/opt/agent-remote-installer-e2e/prefix \
DATA_DIR=/var/lib/agent-remote-installer-e2e-data \
USER_NAME=agent-remote-e2e \
  bash -c 'script=$1; source_file=$2; destination=$3; set --; source "$script"; render_system_file "$source_file" "$destination"' sh \
  "$ROOT/scripts/install.sh" "$ROOT/systemd/agent-remote-runtime.sudoers" "$rendered_sudoers"
grep -q -- '--workspace-root /var/lib/agent-remote-installer-e2e-data/users$' "$rendered_sudoers" || \
  fail "sudoers did not restrict sync to the configured workspace root"

if "$ROOT/scripts/install.sh" --server-url https://example.test --no-dependencies >/dev/null 2>&1; then
  fail "partial registration options were accepted"
fi

fake_claude="$WORK/claude"
cat > "$fake_claude" <<'EOF'
#!/bin/sh
echo "9.9.9 (Claude Code)"
EOF
chmod 0755 "$fake_claude"
checksum="$(sha256_file "$fake_claude")"
runtime_root="$WORK/runtime"

ALLOW_NON_ROOT=1 "$ROOT/scripts/install-claude-runtime.sh" \
  --version 9.9.9 --source "$fake_claude" --sha256 "$checksum" --runtime-root "$runtime_root" >/dev/null
[ -x "$runtime_root/current/bin/claude" ] || fail "pinned Claude executable was not installed"
[ "$(cat "$runtime_root/current/VERSION")" = "9.9.9" ] || fail "pinned Claude version metadata is wrong"
grep -q "$checksum" "$runtime_root/current/SHA256SUMS" || fail "pinned Claude checksum metadata is wrong"

tampered="$WORK/claude-tampered"
cat > "$tampered" <<'EOF'
#!/bin/sh
echo "9.9.9 (tampered Claude Code)"
EOF
chmod 0755 "$tampered"
if ALLOW_NON_ROOT=1 "$ROOT/scripts/install-claude-runtime.sh" \
  --version 9.9.9 --source "$tampered" --sha256 "$(sha256_file "$tampered")" \
  --runtime-root "$runtime_root" >/dev/null 2>&1; then
  fail "same Claude version with a different checksum was accepted"
fi

fake_official_installer="$WORK/official-install.sh"
cat > "$fake_official_installer" <<'EOF'
#!/bin/sh
set -eu
[ "$1" = "stable" ] || exit 9
mkdir -p "$HOME/.local/bin"
cat > "$HOME/.local/bin/claude" <<'INNER'
#!/bin/sh
echo "8.8.8 (Claude Code)"
INNER
chmod 0755 "$HOME/.local/bin/claude"
EOF
chmod 0755 "$fake_official_installer"
official_root="$WORK/official-runtime"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-claude-runtime.sh" \
  --channel stable --installer-source "$fake_official_installer" --runtime-root "$official_root" >/dev/null
[ "$(cat "$official_root/current/VERSION")" = "8.8.8" ] || fail "official Claude version was not detected"

package="$WORK/package"
mkdir -p "$package/scripts"
cp "$ROOT/scripts/install.sh" "$package/install.sh"
cp "$ROOT/scripts/install-claude-runtime.sh" "$package/scripts/install-claude-runtime.sh"
cp "$ROOT/config.example.json" "$package/config.example.json"
printf '7.7.7\n' > "$package/VERSION"
chmod 0755 "$package/install.sh" "$package/scripts/install-claude-runtime.sh"

cat > "$package/agent-remote-node" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FAKE_NODE_LOG"
if [ "$1" = register ]; then
  shift
  while [ "$#" -gt 0 ]; do
    if [ "$1" = --config ]; then
      config="$2"
      break
    fi
    shift
  done
  printf '{"server_url":"https://control.example","node_id":"node_1","node_token":"node_test"}\n' > "$config"
fi
EOF
cat > "$package/agent-remote-attach" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$package/agent-remote-runtime" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$package/agent-remote-node" "$package/agent-remote-attach" "$package/agent-remote-runtime"

prefix="$WORK/prefix"
config_dir="$WORK/etc"
state_dir="$WORK/state"
data_dir="$WORK/data"
managed_claude="$WORK/managed-claude"
export FAKE_NODE_LOG="$WORK/node.log"
ALLOW_NON_ROOT=1 STRICT_PREREQUISITES=0 INSTALL_DEPENDENCIES=0 \
CLAUDE_RUNTIME_ROOT="$managed_claude" \
  "$package/install.sh" \
  --prefix "$prefix" --config-dir "$config_dir" --state-dir "$state_dir" --data-dir "$data_dir" \
  --server-url https://control.example --node-id node_1 --registration-token registration_test \
  --claude-version 9.9.9 --claude-source "$fake_claude" --claude-sha256 "$checksum" \
  --no-user --no-sudo --no-systemd --no-start >/dev/null

[ -x "$prefix/bin/agent-remote-node" ] || fail "node binary was not installed"
[ -x "$managed_claude/current/bin/claude" ] || fail "one-command flow did not install Claude"
grep -q -- '--runtime-backends native' "$FAKE_NODE_LOG" || fail "native backend was not registered"
grep -q -- '--system-install' "$FAKE_NODE_LOG" || fail "system install layout was not registered"
grep -q -- "--claude-runtime-path $managed_claude/current/bin/claude" "$FAKE_NODE_LOG" || \
  fail "managed Claude path was not registered"

release_dir="$WORK/release"
GOCACHE="$WORK/go-cache" VERSION=9.9.9 OUT_DIR="$release_dir" TARGETS=linux/amd64/glibc \
  "$ROOT/scripts/build-release.sh" >/dev/null
release_package="$release_dir/agent-remote-node-9.9.9-linux-amd64-glibc"
for packaged_file in \
  VERSION \
  agent-remote-node \
  agent-remote-attach \
  agent-remote-runtime \
  install.sh \
  scripts/install-claude-runtime.sh \
  systemd/agent-remote-node.service \
  systemd/agent-remote-runtime.service \
  systemd/agent-remote-runtime.sudoers; do
  [ -f "$release_package/$packaged_file" ] || fail "release is missing $packaged_file"
done
[ "$(cat "$release_package/VERSION")" = "9.9.9" ] || fail "release version metadata is wrong"
[ -f "$release_package.tar.gz" ] || fail "release archive was not created"

echo "install script tests passed"
