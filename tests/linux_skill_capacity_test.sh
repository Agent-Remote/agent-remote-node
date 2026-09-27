#!/usr/bin/env bash
set -euo pipefail

if [[ "${AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST:-}" != 1 ]]; then
  printf 'Set AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST=1 to run real default-limit storage tests.\n' >&2
  exit 2
fi
mode="${AGENT_REMOTE_SKILL_CAPACITY_MODE:-}"
if [[ "$mode" != bytes && "$mode" != entries && "$mode" != helper ]]; then
  printf 'Select AGENT_REMOTE_SKILL_CAPACITY_MODE=bytes, entries or helper.\n' >&2
  exit 2
fi
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-capacity.XXXXXX")"
container="$(basename "$test_root")"
image="debian:bookworm-slim"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [[ "$image" != debian:bookworm-slim ]]; then docker image rm "$image" >/dev/null 2>&1 || true; fi
  rm -rf -- "$test_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

case "$(docker version --format '{{.Server.Arch}}')" in
  amd64 | x86_64) go_arch=amd64 ;;
  arm64 | aarch64) go_arch=arm64 ;;
  *) printf 'Unsupported Docker architecture\n' >&2; exit 1 ;;
esac
package=./internal/skillmanager
test_name=TestSkillDefaultCapacity
if [[ "$mode" == helper ]]; then
  package=./internal/runtimehelper
  test_name=TestHelperDefaultCapacityReconciliation
fi
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/capacity.test" "$package"
)
if [[ "$mode" != entries ]]; then
  mkdir "$test_root/build"
  public_ca="${AGENT_REMOTE_TEST_PUBLIC_CA:-/etc/ssl/cert.pem}"
  if [[ ! -f "$public_ca" ]]; then public_ca=/etc/ssl/certs/ca-certificates.crt; fi
  cp "$public_ca" "$test_root/build/public-ca.crt"
  cat > "$test_root/build/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
COPY public-ca.crt /proof/public-ca.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends acl passwd e2fsprogs util-linux && rm -rf /var/lib/apt/lists/*
DOCKERFILE
  image="agent-remote-skill-capacity:${container##*.}"
  docker build -q -t "$image" "$test_root/build"
fi
mounts=()
if [[ "$mode" == bytes ]]; then
  # A private ext4 image preserves Linux ownership without resizing the user's Docker disk.
  python3 - "$test_root/capacity.img" <<'PYTHON'
import sys
with open(sys.argv[1], "xb") as image:
    image.truncate(40 << 30)
PYTHON
  mounts+=(--privileged --mount "type=bind,src=$test_root/capacity.img,dst=/capacity.img")
fi
docker run --rm --name "$container" --network none --user 0:0 --memory 2g --cpus 2 \
  --env AGENT_REMOTE_RUN_SKILL_CAPACITY_TEST=1 \
  --env "AGENT_REMOTE_SKILL_CAPACITY_MODE=$mode" --env TMPDIR=/capacity \
  --env "CAPACITY_TEST_NAME=$test_name" \
  --mount "type=bind,src=$test_root/capacity.test,dst=/proof/capacity.test,readonly" \
  "${mounts[@]}" "$image" /bin/sh -eu -c '
    mkdir -p /capacity
    if test -f /capacity.img; then
      mkfs.ext4 -q -F /capacity.img
      mount -o loop /capacity.img /capacity
      trap "umount /capacity" EXIT
    fi
    chmod 700 /capacity
    df -h /capacity
    stat -f -c "filesystem=%T block_bytes=%S" /capacity
    /proof/capacity.test -test.v -test.timeout=30m -test.run "^${CAPACITY_TEST_NAME}$"
  '
