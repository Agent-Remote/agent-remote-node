#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-config-import.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac

cd "$repo_root"
for package in skillmanager runtimehelper toolaccounts; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$test_root/$package.test" "./internal/$package"
done

# Real Linux ownership, private receipts, and socket transfer; not runtime backend acceptance.
docker run --rm --user 0:0 \
  --mount "type=bind,src=$test_root,dst=/proof,readonly" \
  --workdir / \
  debian:bookworm-slim \
  /bin/sh -eu -c '
    cp /proof/*.test /tmp/
    chmod 0755 /tmp/*.test
    /tmp/skillmanager.test -test.v -test.run "^Test(AccountMigration|AccountCopy|AccountFence|ImportReceipt|AccountCapture|AccountImportDrain|AccountInventory)"
    /tmp/runtimehelper.test -test.v -test.run "^Test(Helper(ConfigImport|Import|Fence|CaptureDescriptor)|AccountMigration|AccountCopy|AccountFence|DockerWriter|DockerSandboxProbe|RemovedDocker|ProbeDeclares|NativeTakeover)"
    /tmp/toolaccounts.test -test.v -test.run "^TestImport"
  '
