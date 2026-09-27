#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-reclamation.XXXXXX")"
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
    go test -c -o "$test_root/skillmanager.test" ./internal/skillmanager
)

# Only disposable container content is writable; the host binary is mounted read-only.
docker run --rm --user 0:0 --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --env AGENT_REMOTE_RUN_RECLAMATION_MOUNT_TEST=1 \
  --mount "type=bind,src=$test_root/skillmanager.test,dst=/proof/test,readonly" \
  debian:bookworm-slim \
  /proof/test -test.v -test.run '^TestReclamation|^TestOpenSessionWorkRejects'
