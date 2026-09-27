#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${AGENT_REMOTE_TEST_RUNTIME_RECOVERY_FIXTURE:?requires a disposable Server fixture}"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-recovery.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-recovery:$test_tag"
cleanup() {
  docker container rm --force "$test_tag" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -m 700 "$test_root/build" "$test_root/private"
cp -- "$fixture" "$test_root/private/fixture.json"
chmod 600 "$test_root/private/fixture.json"
cp -- "$repo_root/systemd/agent-remote-node.service" "$repo_root/systemd/agent-remote-runtime.service" "$test_root/build/"
# Only public CA certificates enter the build context; all disposable tokens stay outside it.
public_ca="${AGENT_REMOTE_TEST_PUBLIC_CA:-/etc/ssl/cert.pem}"
if [[ ! -f "$public_ca" ]]; then public_ca=/etc/ssl/certs/ca-certificates.crt; fi
cp -- "$public_ca" "$test_root/build/public-ca.crt"
docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch=amd64 ;;
  arm64 | aarch64) go_arch=arm64 ;;
  *) printf 'Unsupported Docker architecture\n' >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  export CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch"
  go build -o "$test_root/build/agent-remote-node" ./cmd/agent-remote-node
  go build -o "$test_root/build/agent-remote-runtime" ./cmd/agent-remote-runtime
  go test -c -o "$test_root/build/recovery.test" ./tests/runtimerecovery
)
cat > "$test_root/build/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
COPY public-ca.crt /proof/public-ca.crt
RUN sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/proof/public-ca.crt -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends systemd systemd-sysv dbus acl passwd tmux bubblewrap iproute2 nftables git gh openssh-client locales nodejs && rm -rf /var/lib/apt/lists/*
RUN localedef -i en_US -f UTF-8 en_US.UTF-8
RUN groupadd --gid 22000 ar-proof-worker && useradd --uid 22000 --gid 22000 --no-create-home --shell /usr/sbin/nologin ar-proof-worker
COPY agent-remote-node agent-remote-runtime recovery.test agent-remote-node.service agent-remote-runtime.service /proof/
ENV container=docker
CMD ["/sbin/init"]
DOCKERFILE
docker build --quiet --tag "$test_image" "$test_root/build" >/dev/null
docker run --detach --name "$test_tag" --privileged --cgroupns=private \
  --add-host host.docker.internal:host-gateway \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
  --security-opt systempaths=unconfined --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --mount "type=bind,src=$test_root/private,dst=/proof/private,readonly" \
  "$test_image" >/dev/null
for attempt in $(seq 1 50); do
  if docker exec "$test_tag" busctl --system --timeout=2s --no-pager list >/dev/null 2>&1; then break; fi
  if [[ "$attempt" = 50 ]]; then printf 'Disposable systemd did not become ready\n' >&2; exit 1; fi
  sleep 0.1
done
docker exec --env AGENT_REMOTE_RUN_RUNTIME_RECOVERY_TEST=1 --env TMPDIR=/var/tmp \
  "$test_tag" /proof/recovery.test -test.v -test.timeout=4m -test.run '^TestRuntimeRecoveryThroughProductionDaemons$'
