#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-ego-browser-acl.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64)
    go_arch="amd64"
    ;;
  arm64 | aarch64)
    go_arch="arm64"
    ;;
  *)
    printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2
    exit 1
    ;;
esac

test_binary="$test_root/egobrowser.test"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$test_binary" ./internal/egobrowser
)

docker run --rm --user 0:0 \
  --env AGENT_REMOTE_RUN_LINUX_UID_ACL_TEST=1 \
  --mount "type=bind,src=$test_binary,dst=/proof/egobrowser.test,readonly" \
  debian:bookworm-slim \
  /bin/sh -eu -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update >/dev/null
    apt-get install --no-install-recommends --yes acl >/dev/null
    cp /proof/egobrowser.test /tmp/egobrowser.test
    chmod 0755 /tmp/egobrowser.test
    /tmp/egobrowser.test -test.run="^TestLinuxNativeRuntimePeerUIDACLIntegration$" -test.v
  '
