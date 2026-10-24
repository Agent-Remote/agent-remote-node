#!/usr/bin/env bash
# Opt-in, disposable real CLI/Server/Node/SSH/Mutagen integration proof.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
server_repo="${SYNC_SERVER_REPO:-$repo_root/../agent-remote-server}"
cli_binary="${SYNC_CLI_BINARY:?Set SYNC_CLI_BINARY to the built agent-remote executable}"
mutagen_source="${SYNC_MUTAGEN_SOURCE:?Set SYNC_MUTAGEN_SOURCE to the reviewed Mutagen v0.18.1 checkout}"
# Darwin's default TMPDIR can exceed the Unix socket path limit for Mutagen.
test_root="$(mktemp -d /tmp/agent-remote-sync.XXXXXX)"
cleanup() {
  if [[ -f "$test_root/container-name" ]]; then
    local container_name
    container_name="$(cat "$test_root/container-name")"
    if [[ "$container_name" == agent-remote-sync-proof-* ]]; then
      docker rm --force "$container_name" >/dev/null 2>&1 || true
    fi
  fi
  rm -rf -- "$test_root"
}
trap cleanup EXIT
mkdir -p "$test_root/bin"

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch=amd64 ;;
  arm64 | aarch64) go_arch=arm64 ;;
  *) printf 'Unsupported Docker architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -o "$test_root/bin/" \
    ./cmd/agent-remote-node ./cmd/agent-remote-attach ./cmd/agent-remote-runtime
)
(
  cd "$mutagen_source"
  go build -tags mutagencli -o "$test_root/bin/mutagen" ./cmd/mutagen
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -tags mutagenagent \
    -o "$test_root/bin/linux_$go_arch" ./cmd/mutagen-agent
)
"$server_repo/.venv/bin/python" "$repo_root/tests/fixtures/sync_chain.py" \
  --root "$test_root" --node-repo "$repo_root" --server-repo "$server_repo" \
  --cli "$cli_binary" --architecture "$go_arch"
