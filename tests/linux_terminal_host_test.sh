#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-terminal.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT
docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch=amd64 ;;
  arm64 | aarch64) go_arch=arm64 ;;
  *) exit 1 ;;
esac
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -o "$test_root/runtime" ./cmd/agent-remote-runtime
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/runtimehelper.test" ./internal/runtimehelper
)
docker run --rm --user 0:0 \
  --mount "type=bind,src=$test_root/runtime,dst=/proof/runtime,readonly" \
  --mount "type=bind,src=$test_root/runtimehelper.test,dst=/proof/runtimehelper.test,readonly" \
  --mount "type=bind,src=$repo_root/tests/fixtures/terminal_host.py,dst=/proof/test.py,readonly" \
  debian:bookworm-slim /bin/sh -eu -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null
    apt-get install --no-install-recommends --yes tmux python3 >/dev/null
    cp /proof/runtime /tmp/runtime
    cp /proof/runtimehelper.test /tmp/runtimehelper.test
    chmod 0755 /tmp/runtime
    chmod 0755 /tmp/runtimehelper.test
    /tmp/runtimehelper.test -test.run="^Test(DockerTerminal|DockerTmux|WaitForSessionReady)" -test.v
    python3 /proof/test.py /tmp/runtime
  '
