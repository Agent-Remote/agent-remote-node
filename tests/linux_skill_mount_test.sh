#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-mount.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac

test_binary="$test_root/runtimehelper.test"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$test_binary" ./internal/runtimehelper
)

# Isolated mount namespace only: this does not launch systemd or Docker Sandbox sessions.
docker run --rm --user 0:0 --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  --env AGENT_REMOTE_RUN_SKILL_MOUNT_TEST=1 \
  --env AGENT_REMOTE_TEST_PYTHON=1 \
  --mount "type=bind,src=$test_binary,dst=/proof/runtimehelper.test,readonly" \
  debian:bookworm-slim \
  /bin/sh -eu -c '
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends acl passwd bubblewrap tmux python3-venv >/dev/null
    cp /proof/runtimehelper.test /tmp/runtimehelper.test
    chmod 0755 /tmp/runtimehelper.test
    /tmp/runtimehelper.test -test.v -test.run "^TestNative(Skill(Mount|Busy|Python)|ManagedPaneIntegration)|^TestHelperFinalizationReclamationMountAliases"
  '
