#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-export-enospc.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac

(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$test_root/helper.test" ./internal/runtimehelper
)

# Only this disposable, size-checked tmpfs is filled; host filesystems are never fill targets.
docker run --rm --user 0:0 \
  --mount "type=bind,src=$test_root/helper.test,dst=/proof/helper.test,readonly" \
  --tmpfs /skill-export-full:rw,nosuid,nodev,size=16m,mode=1777 \
  --env AGENT_REMOTE_TEST_EXPORT_ENOSPC=1 \
  debian:bookworm-slim \
  /bin/sh -eu -c '
    apt-get update -qq
    apt-get install -y --no-install-recommends acl passwd >/dev/null
    cp /proof/helper.test /tmp/helper.test
    chmod 0755 /tmp/helper.test
    /tmp/helper.test -test.v -test.run "^TestHelperFinalizationStoppedExportPhysicalENOSPC$"
  '
