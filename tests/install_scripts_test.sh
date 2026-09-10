#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-install-test.XXXXXX")"
cleanup_work() {
  chmod -R u+w "$WORK" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup_work EXIT

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

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

skill_tree_sha256() {
  local root="$1"
  while IFS= read -r -d '' file; do
    relative="${file#"$root"/}"
    size="$(wc -c < "$file" | tr -d '[:space:]')"
    printf '%s\0%s\0' "$relative" "$size"
    command cat -- "$file"
    printf '\0'
  done < <(find "$root" -type f -print0 | LC_ALL=C sort -z) | sha256_stream
}

bash -n "$ROOT/scripts/install.sh" "$ROOT/scripts/install-claude-runtime.sh" \
  "$ROOT/scripts/install-nodejs-runtime.sh" "$ROOT/scripts/install-ego-browser-runtime.sh" \
  "$ROOT/scripts/build-release.sh"
"$ROOT/scripts/install.sh" --help | grep -q -- '--registration-token' || fail "one-command help is incomplete"
"$ROOT/scripts/install.sh" --help | grep -q -- '--nodejs-version' || fail "Node.js install help is incomplete"
"$ROOT/scripts/install.sh" --help | grep -q -- '--enable-ego-browser' || fail "ego-browser enable option is missing"
"$ROOT/scripts/install.sh" --help | grep -q -- '--disable-ego-browser' || fail "ego-browser disable option is missing"
grep -q '^Match all$' "$ROOT/scripts/install.sh" || fail "SSH Match block is not reset"
grep -q 'AllowAgentForwarding yes' "$ROOT/scripts/install.sh" || fail "SSH agent forwarding is not enabled for the forced-command gateway"
grep -q 'apt-get install -y --no-upgrade' "$ROOT/scripts/install.sh" || \
  fail "dependency installation may upgrade existing packages"
grep -q 'apt-get install -y --reinstall --no-upgrade.*gawk' "$ROOT/scripts/install.sh" || \
  fail "installer does not repair a broken awk package"
grep -q 'update-alternatives --set awk /usr/bin/gawk' "$ROOT/scripts/install.sh" || \
  fail "installer does not repair the awk alternatives link"
grep -Eq '^[[:space:]]+verify_ai_tooling$' "$ROOT/scripts/install.sh" || \
  fail "installed AI development commands are not verified"
for package in build-essential file git gh jq openssh-client python3 ripgrep rsync sqlite3 tree unzip wget zip; do
  grep -Eq "^[[:space:]].*${package}([[:space:]]|$)" "$ROOT/scripts/install.sh" || \
    fail "native developer dependency ${package} is not installed by default"
done
grep -q 'wireguard-tools' "$ROOT/scripts/install.sh" || fail "WireGuard tools are not installed"
grep -q 'acl ca-certificates git openssh-client openssh-server tmux util-linux wireguard-tools' "$ROOT/scripts/install.sh" || \
  fail "Docker-only runtime dependencies are incomplete"
if sed -n '/^install_managed_device_proxy()/,/^}/p' "$ROOT/scripts/install.sh" | grep -q 'backend_enabled native'; then
  fail "Docker-only installs still skip the managed device proxy"
fi
grep -q 'wg-quick@' "$ROOT/scripts/install.sh" || fail "WireGuard interface service is not enabled"
grep -q 'systemctl restart agent-remote-runtime.service' "$ROOT/scripts/install.sh" || \
  fail "runtime helper is not restarted during upgrades"
grep -q '^StateDirectoryMode=0711$' "$ROOT/systemd/agent-remote-runtime.service" || \
  fail "runtime state root must remain traversable after systemd restarts"
grep -q '^LimitCORE=0$' "$ROOT/systemd/agent-remote-node.service" || \
  fail "node service must disable core dumps"
grep -q '^ReadWritePaths=.* /var/lib/agent-remote-runtime/sessions ' "$ROOT/systemd/agent-remote-node.service" || \
  fail "node service must permit managed device bridge sockets under runtime sessions"
grep -q '^LimitCORE=0$' "$ROOT/systemd/agent-remote-runtime.service" || \
  fail "runtime helper service must disable core dumps"
grep -q '"--property=LimitCORE=0"' "$ROOT/internal/runtimehelper/engine.go" || \
  fail "managed runtime units must disable core dumps"
grep -q 'systemctl restart "wg-quick@\$WIREGUARD_INTERFACE.service"' "$ROOT/scripts/install.sh" || \
  fail "WireGuard is not restarted during upgrades"
grep -q 'systemctl restart agent-remote-node.service' "$ROOT/scripts/install.sh" || \
  fail "node worker is not restarted during upgrades"
grep -q -- '--version "$VERSION"' "$ROOT/scripts/install.sh" || \
  fail "existing node version is not refreshed"

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
grep -q -- '--wireguard-interface agent-remote' "$rendered_unit" || \
  fail "WireGuard runtime configuration is missing"
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
[ "$1" = "latest" ] || exit 9
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
  --installer-source "$fake_official_installer" --runtime-root "$official_root" >/dev/null
[ "$(cat "$official_root/current/VERSION")" = "8.8.8" ] || fail "official Claude version was not detected"

fake_node_tree="$WORK/node-v22.99.0-linux-x64"
mkdir -p "$fake_node_tree/bin" "$fake_node_tree/lib/node_modules/npm/bin"
cat > "$fake_node_tree/bin/node" <<'EOF'
#!/bin/sh
echo "v22.99.0"
EOF
chmod 0755 "$fake_node_tree/bin/node"
cat > "$fake_node_tree/lib/node_modules/npm/bin/npm-cli.js" <<'EOF'
#!/usr/bin/env node
console.log("10.99.0")
EOF
cp "$fake_node_tree/lib/node_modules/npm/bin/npm-cli.js" "$fake_node_tree/lib/node_modules/npm/bin/npx-cli.js"
chmod 0755 "$fake_node_tree/lib/node_modules/npm/bin/npm-cli.js" "$fake_node_tree/lib/node_modules/npm/bin/npx-cli.js"
fake_node_archive="$WORK/node-v22.99.0-linux-x64.tar.gz"
tar -C "$WORK" -czf "$fake_node_archive" "$(basename "$fake_node_tree")"
node_checksum="$(sha256_file "$fake_node_archive")"
ALLOW_NON_ROOT=1 NODEJS_OS_OVERRIDE=Linux NODEJS_ARCH_OVERRIDE=x86_64 \
  "$ROOT/scripts/install-nodejs-runtime.sh" \
  --version 22.99.0 --source "$fake_node_archive" --sha256 "$node_checksum" \
  --runtime-root "$runtime_root" >/dev/null
[ -x "$runtime_root/current/bin/node" ] || fail "pinned Node.js executable was not installed"
[ -x "$runtime_root/current/bin/npm" ] || fail "npm was not installed"
[ -x "$runtime_root/current/bin/npx" ] || fail "npx was not installed"
[ "$(cat "$runtime_root/current/NODE_VERSION")" = "22.99.0" ] || fail "pinned Node.js version metadata is wrong"
grep -q "$node_checksum" "$runtime_root/current/NODE_SHA256SUMS" || fail "pinned Node.js checksum metadata is wrong"

fake_device_proxy="$WORK/agent-remote-device-proxy"
cat > "$fake_device_proxy" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$fake_device_proxy"
device_proxy_checksum="$(sha256_file "$fake_device_proxy")"
device_runtime_root="$WORK/device-runtime"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-device-proxy.sh" \
  --version 1.2.3 --source "$fake_device_proxy" --sha256 "$device_proxy_checksum" \
  --runtime-root "$device_runtime_root" >/dev/null
[ -x "$device_runtime_root/current/bin/agent-remote-device-proxy" ] || \
  fail "managed device proxy was not installed"
[ "$(cat "$device_runtime_root/current/VERSION")" = "1.2.3" ] || \
  fail "managed device proxy version metadata is wrong"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-device-proxy.sh" \
  --version 1.2.3 --source "$fake_device_proxy" --sha256 "$device_proxy_checksum" \
  --runtime-root "$device_runtime_root" >/dev/null
fake_device_proxy_next="$WORK/agent-remote-device-proxy-next"
cp "$fake_device_proxy" "$fake_device_proxy_next"
printf '\n# next version\n' >> "$fake_device_proxy_next"
next_device_proxy_checksum="$(sha256_file "$fake_device_proxy_next")"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-device-proxy.sh" \
  --version 1.2.4 --source "$fake_device_proxy_next" --sha256 "$next_device_proxy_checksum" \
  --runtime-root "$device_runtime_root" >/dev/null
[ "$(cat "$device_runtime_root/current/VERSION")" = "1.2.4" ] || \
  fail "managed device proxy did not switch to the newer version"
[ "$(cat "$device_runtime_root/releases/1.2.3/VERSION")" = "1.2.3" ] || \
  fail "managed device proxy upgrade removed the previous version"
printf '\n# changed\n' >> "$fake_device_proxy"
changed_device_proxy_checksum="$(sha256_file "$fake_device_proxy")"
if ALLOW_NON_ROOT=1 "$ROOT/scripts/install-device-proxy.sh" \
  --version 1.2.3 --source "$fake_device_proxy" --sha256 "$changed_device_proxy_checksum" \
  --runtime-root "$device_runtime_root" >/dev/null 2>&1; then
  fail "same device proxy version with different content was accepted"
fi

fake_ego_wrapper="$WORK/ego-browser"
cat > "$fake_ego_wrapper" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod 0755 "$fake_ego_wrapper"
ego_wrapper_checksum="$(sha256_file "$fake_ego_wrapper")"
ego_skill_source="$ROOT/internal/managedskills/skills/ego-browser"
ego_skill_version="1.2.3"
ego_skill_digest="$(skill_tree_sha256 "$ego_skill_source")"
ego_source_manifest="$ROOT/ego-browser-skill-source.json"
ego_source_manifest_checksum="$(sha256_file "$ego_source_manifest")"
ego_runtime_root="$WORK/ego-browser-runtime"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-ego-browser-runtime.sh" \
  --runtime-root "$ego_runtime_root" \
  --version 0.1.0 \
  --wrapper-source "$fake_ego_wrapper" \
  --wrapper-sha256 "$ego_wrapper_checksum" \
  --skill-source "$ego_skill_source" \
  --skill-version "$ego_skill_version" \
  --skill-tree-sha256 "$ego_skill_digest" \
  --source-manifest "$ego_source_manifest" \
  --source-manifest-sha256 "$ego_source_manifest_checksum" >/dev/null
[ -x "$ego_runtime_root/current/bin/ego-browser" ] || fail "ego-browser wrapper was not installed"
[ "$(cat "$ego_runtime_root/current/SKILL_VERSION")" = "$ego_skill_version" ] || \
  fail "ego-browser Skill version metadata is wrong"
[ "$(cat "$ego_runtime_root/current/SKILL_TREE_SHA256")" = "$ego_skill_digest" ] || \
  fail "ego-browser Skill digest metadata is wrong"
[ "$(skill_tree_sha256 "$ego_runtime_root/current/skill/ego-browser")" = "$ego_skill_digest" ] || \
  fail "installed ego-browser Skill bytes drifted"
ALLOW_NON_ROOT=1 "$ROOT/scripts/install-ego-browser-runtime.sh" \
  --runtime-root "$ego_runtime_root" \
  --version 0.1.0 \
  --wrapper-source "$fake_ego_wrapper" \
  --wrapper-sha256 "$ego_wrapper_checksum" \
  --skill-source "$ego_skill_source" \
  --skill-version "$ego_skill_version" \
  --skill-tree-sha256 "$ego_skill_digest" \
  --source-manifest "$ego_source_manifest" \
  --source-manifest-sha256 "$ego_source_manifest_checksum" >/dev/null
fake_ego_wrapper_changed="$WORK/ego-browser-changed"
cp "$fake_ego_wrapper" "$fake_ego_wrapper_changed"
printf '\n# changed\n' >> "$fake_ego_wrapper_changed"
chmod 0755 "$fake_ego_wrapper_changed"
if ALLOW_NON_ROOT=1 "$ROOT/scripts/install-ego-browser-runtime.sh" \
  --runtime-root "$ego_runtime_root" \
  --version 0.1.0 \
  --wrapper-source "$fake_ego_wrapper_changed" \
  --wrapper-sha256 "$(sha256_file "$fake_ego_wrapper_changed")" \
  --skill-source "$ego_skill_source" \
  --skill-version "$ego_skill_version" \
  --skill-tree-sha256 "$ego_skill_digest" \
  --source-manifest "$ego_source_manifest" \
  --source-manifest-sha256 "$ego_source_manifest_checksum" >/dev/null 2>&1; then
  fail "same ego-browser version with different wrapper bytes was accepted"
fi
unsafe_skill="$WORK/unsafe-ego-skill"
cp -R "$ego_skill_source" "$unsafe_skill"
ln -s SKILL.md "$unsafe_skill/alias.md"
if ALLOW_NON_ROOT=1 "$ROOT/scripts/install-ego-browser-runtime.sh" \
  --runtime-root "$WORK/unsafe-ego-runtime" \
  --version 0.1.0 \
  --wrapper-source "$fake_ego_wrapper" \
  --wrapper-sha256 "$ego_wrapper_checksum" \
  --skill-source "$unsafe_skill" \
  --skill-version "$ego_skill_version" \
  --skill-tree-sha256 "$ego_skill_digest" \
  --source-manifest "$ego_source_manifest" \
  --source-manifest-sha256 "$ego_source_manifest_checksum" >/dev/null 2>&1; then
  fail "ego-browser Skill symlink was accepted"
fi

package="$WORK/package"
mkdir -p "$package/scripts" "$package/device" "$package/ego-browser/skill"
cp "$ROOT/scripts/install.sh" "$package/install.sh"
cp "$ROOT/scripts/install-claude-runtime.sh" "$package/scripts/install-claude-runtime.sh"
cp "$ROOT/scripts/install-nodejs-runtime.sh" "$package/scripts/install-nodejs-runtime.sh"
cp "$ROOT/scripts/install-device-proxy.sh" "$package/scripts/install-device-proxy.sh"
cp "$ROOT/scripts/install-ego-browser-runtime.sh" "$package/scripts/install-ego-browser-runtime.sh"
cp "$fake_device_proxy" "$package/device/agent-remote-device-proxy"
cp "$fake_ego_wrapper" "$package/ego-browser/ego-browser"
cp -R "$ego_skill_source" "$package/ego-browser/skill/ego-browser"
cp "$ego_source_manifest" "$package/ego-browser/ego-browser-skill-source.json"
cp "$ROOT/config.example.json" "$package/config.example.json"
printf '7.7.7\n' > "$package/VERSION"
printf '1.2.3\n' > "$package/device/VERSION"
printf '0.1.0\n' > "$package/ego-browser/VERSION"
printf '%s\n' "$ego_wrapper_checksum" > "$package/ego-browser/WRAPPER_SHA256"
printf '%s\n' "$ego_skill_version" > "$package/ego-browser/SKILL_VERSION"
printf '%s\n' "$ego_skill_digest" > "$package/ego-browser/SKILL_TREE_SHA256"
printf '%s\n' "$ego_source_manifest_checksum" > "$package/ego-browser/SOURCE_MANIFEST_SHA256"
chmod 0755 "$package/install.sh" "$package/scripts/install-claude-runtime.sh" "$package/scripts/install-nodejs-runtime.sh" \
  "$package/scripts/install-device-proxy.sh" "$package/scripts/install-ego-browser-runtime.sh" \
  "$package/device/agent-remote-device-proxy" "$package/ego-browser/ego-browser"

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
NODEJS_OS_OVERRIDE=Linux NODEJS_ARCH_OVERRIDE=x86_64 \
CLAUDE_RUNTIME_ROOT="$managed_claude" \
EGO_BROWSER_RUNTIME_ROOT="$WORK/managed-ego-browser" \
  "$package/install.sh" \
  --prefix "$prefix" --config-dir "$config_dir" --state-dir "$state_dir" --data-dir "$data_dir" \
  --server-url https://control.example --node-id node_1 --registration-token registration_test \
  --claude-version 9.9.9 --claude-source "$fake_claude" --claude-sha256 "$checksum" \
  --nodejs-version 22.99.0 --nodejs-source "$fake_node_archive" --nodejs-sha256 "$node_checksum" \
  --no-user --no-sudo --no-systemd --no-start >/dev/null

[ -x "$prefix/bin/agent-remote-node" ] || fail "node binary was not installed"
[ -x "$managed_claude/current/bin/claude" ] || fail "one-command flow did not install Claude"
[ -x "$managed_claude/current/bin/node" ] || fail "one-command flow did not install Node.js"
grep -q -- '--runtime-backends native' "$FAKE_NODE_LOG" || fail "native backend was not registered"
grep -q -- '--system-install' "$FAKE_NODE_LOG" || fail "system install layout was not registered"
grep -q -- "--claude-runtime-path $managed_claude/current/bin/claude" "$FAKE_NODE_LOG" || \
  fail "managed Claude path was not registered"
# The managed ego-browser runtime is Linux-only. The end-to-end installer
# flow above still exercises the common registration path on macOS, while the
# synchronization invocation is asserted where the installer actually runs it.
if [ "$(uname -s)" = "Linux" ]; then
  grep -q -- "configure-ego-browser" "$FAKE_NODE_LOG" || \
    fail "ego-browser configuration was not synchronized"
fi

release_dir="$WORK/release"
proxy_dir="$WORK/device-proxies/linux-amd64-glibc"
ego_wrapper_dir="$WORK/ego-wrappers/linux-amd64-glibc"
mkdir -p "$proxy_dir" "$ego_wrapper_dir"
cp "$fake_device_proxy" "$proxy_dir/agent-remote-device-proxy"
chmod 0755 "$proxy_dir/agent-remote-device-proxy"
printf '1.2.3\n' > "$proxy_dir/VERSION"
cp "$fake_ego_wrapper" "$ego_wrapper_dir/ego-browser"
chmod 0755 "$ego_wrapper_dir/ego-browser"
printf '0.1.11\n' > "$ego_wrapper_dir/VERSION"
GOCACHE="$WORK/go-cache" VERSION=9.9.9 OUT_DIR="$release_dir" TARGETS=linux/amd64/glibc \
  DEVICE_PROXY_DIR="$WORK/device-proxies" \
  EGO_BROWSER_WRAPPER_DIR="$WORK/ego-wrappers" \
  "$ROOT/scripts/build-release.sh" >/dev/null
release_package="$release_dir/agent-remote-node-9.9.9-linux-amd64-glibc"
for packaged_file in \
  VERSION \
  agent-remote-node \
  agent-remote-attach \
  agent-remote-runtime \
  install.sh \
  scripts/install-claude-runtime.sh \
  scripts/install-nodejs-runtime.sh \
	  scripts/install-device-proxy.sh \
	  scripts/install-ego-browser-runtime.sh \
	  device/agent-remote-device-proxy \
	  device/VERSION \
	  ego-browser/ego-browser \
	  ego-browser/VERSION \
	  ego-browser/WRAPPER_SHA256 \
	  ego-browser/SKILL_VERSION \
	  ego-browser/SKILL_TREE_SHA256 \
	  ego-browser/SOURCE_MANIFEST_SHA256 \
	  ego-browser/ego-browser-skill-source.json \
	  ego-browser/skill/ego-browser/SKILL.md \
  systemd/agent-remote-node.service \
  systemd/agent-remote-runtime.service \
  systemd/agent-remote-runtime.sudoers; do
  [ -f "$release_package/$packaged_file" ] || fail "release is missing $packaged_file"
done
[ "$(cat "$release_package/VERSION")" = "9.9.9" ] || fail "release version metadata is wrong"
[ -f "$release_package.tar.gz" ] || fail "release archive was not created"

echo "install script tests passed"
