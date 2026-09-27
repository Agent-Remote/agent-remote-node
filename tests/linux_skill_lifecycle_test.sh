#!/usr/bin/env bash
set -euo pipefail

# Python runners may execute an immutable script snapshot with the original path as argv[0].
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
fixture="${AGENT_REMOTE_TEST_SKILL_LIFECYCLE_FIXTURE:?requires a disposable Server fixture}"
test_mode="${AGENT_REMOTE_TEST_SKILL_LIFECYCLE_MODE:-learning}"
case "$test_mode" in
  learning) credentials="${AGENT_REMOTE_TEST_CLAUDE_CREDENTIALS:?requires a private Linux Claude credentials file}"; test_name=TestNativeClaudeLifecycle ;;
  runtime) credentials=""; test_name=TestNativeDaemonLifecycle ;;
  *) printf 'Unknown lifecycle acceptance mode\n' >&2; exit 1 ;;
esac
claude_source="${AGENT_REMOTE_TEST_CLAUDE_BINARY:?requires a real Linux Claude executable}"
claude_sha256="${AGENT_REMOTE_TEST_CLAUDE_SHA256:?requires the verified artifact checksum}"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-lifecycle.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-lifecycle:$test_tag"
cleanup() {
  docker container rm --force "$test_tag" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# No login state or control-plane credential can enter the Docker build context.
mkdir -m 700 "$test_root/build" "$test_root/private"
cp -- "$fixture" "$test_root/private/fixture.json"
if [[ "$test_mode" = learning ]]; then
  cp -- "$credentials" "$test_root/private/credentials.json"
else
  printf '{}\n' > "$test_root/private/credentials.json"
fi
chmod 600 "$test_root/private/"*.json
cp -- "$claude_source" "$test_root/build/claude"
cp -- "$repo_root/systemd/agent-remote-node.service" "$repo_root/systemd/agent-remote-runtime.service" "$test_root/build/"
actual_sha256="$(shasum -a 256 "$test_root/build/claude" | awk '{print $1}')"
if [[ ! "$claude_sha256" =~ ^[a-f0-9]{64}$ || "$actual_sha256" != "$claude_sha256" ]]; then
  printf 'Claude artifact checksum mismatch\n' >&2
  exit 1
fi
docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker architecture\n' >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  export CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch"
  go build -o "$test_root/build/agent-remote-node" ./cmd/agent-remote-node
  go build -o "$test_root/build/agent-remote-runtime" ./cmd/agent-remote-runtime
  go test -c -o "$test_root/build/lifecycle.test" ./tests/skilllifecycle
)
public_ca="${AGENT_REMOTE_TEST_PUBLIC_CA:-/etc/ssl/cert.pem}"
if [[ ! -f "$public_ca" ]]; then public_ca=/etc/ssl/certs/ca-certificates.crt; fi
cp -- "$public_ca" "$test_root/build/public-ca.crt"
cat > "$test_root/build/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
RUN --mount=type=secret,id=proof_ca,required=true,mode=0444 sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/run/secrets/proof_ca -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/run/secrets/proof_ca -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends systemd systemd-sysv dbus tmux bubblewrap iproute2 nftables acl passwd ca-certificates python3 git gh openssh-client locales nodejs && rm -rf /var/lib/apt/lists/*
RUN localedef -i en_US -f UTF-8 en_US.UTF-8
RUN groupadd --gid 22000 ar-proof-worker && useradd --uid 22000 --gid 22000 --no-create-home --shell /usr/sbin/nologin ar-proof-worker
COPY agent-remote-node agent-remote-runtime lifecycle.test /proof/
COPY agent-remote-node.service agent-remote-runtime.service /proof/
COPY --chmod=755 claude /opt/agent-remote/runtimes/claude/proof/bin/claude
RUN install -m 755 /usr/bin/node /opt/agent-remote/runtimes/claude/proof/bin/node
ENV container=docker
CMD ["/sbin/init"]
DOCKERFILE
docker build --quiet --secret "id=proof_ca,src=$test_root/build/public-ca.crt" --tag "$test_image" "$test_root/build" >/dev/null
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
docker exec --env AGENT_REMOTE_RUN_SKILL_LIFECYCLE_TEST=1 --env TMPDIR=/var/tmp \
  "$test_tag" /proof/lifecycle.test -test.v -test.timeout=12m -test.run "^${test_name}$"
