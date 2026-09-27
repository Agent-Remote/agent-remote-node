#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${1:?Pass the private authorized-download directory}"
fixture="$(cd "$fixture" && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-restore.XXXXXX")"
container_name="skill-restore-node-$(basename "$test_root" | tr '[:upper:]' '[:lower:]')"
cleanup() {
  docker rm --force "$container_name" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

case "$(docker version --format '{{.Server.Arch}}')" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture\n' >&2; exit 1 ;;
esac
(
 cd "$repo_root"
 CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/skillmanager.test" ./internal/skillmanager
)
# Only authorized downloaded objects enter this empty Node; no source volume or old Node state.
docker run --rm --name "$container_name" --user 0:0 --network none \
 --env TMPDIR=/var/tmp --env AGENT_REMOTE_RESTORE_FIXTURE=/fixture \
 --mount "type=bind,src=$fixture,dst=/fixture,readonly" \
 --mount "type=bind,src=$test_root/skillmanager.test,dst=/proof/skillmanager.test,readonly" \
 debian:bookworm-slim /bin/sh -eu -c '
  cp /proof/skillmanager.test /var/tmp/skillmanager.test
  chmod 0755 /var/tmp/skillmanager.test
  /var/tmp/skillmanager.test -test.v -test.run "^TestRestoredServerSnapshotMaterializesOnFreshNode$"
 '
