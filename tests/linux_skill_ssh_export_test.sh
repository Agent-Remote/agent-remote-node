#!/usr/bin/env bash
set -euo pipefail

# Python runners may execute an immutable script snapshot with the original path as argv[0].
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
fixture="${AGENT_REMOTE_TEST_SKILL_SSH_EXPORT_FIXTURE:?requires a disposable Server fixture}"
control="${AGENT_REMOTE_TEST_SKILL_SSH_EXPORT_CONTROL:?requires a private coordination directory}"
mode="${AGENT_REMOTE_TEST_SKILL_SSH_EXPORT_MODE:-export}"
[[ "$mode" == export || "$mode" == cli-lifecycle || "$mode" == capacity ]]
if [[ "$mode" == capacity ]]; then
  [[ "${AGENT_REMOTE_RUN_SKILL_PIPELINE_CAPACITY:-}" == 1 ]]
fi
ssh_port="${AGENT_REMOTE_TEST_SKILL_SSH_EXPORT_PORT:?requires a reserved local SSH port}"
[[ "$ssh_port" =~ ^[0-9]+$ && "$ssh_port" -gt 1024 && "$ssh_port" -lt 65536 ]]
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-ssh-export.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-ssh-export:$test_tag"
cleanup() {
  if [[ "${AGENT_REMOTE_TEST_NATIVE_STOP_OBSERVATIONS:-}" == 1 ]]; then
    docker cp "$test_tag:/var/tmp/agent-remote-native-observations" "$control/native-observations" >/dev/null 2>&1 || true
    if [[ -f "$control/native-observations" ]]; then chmod 600 "$control/native-observations"; fi
  fi
  docker container rm --force "$test_tag" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -m 700 "$test_root/build" "$test_root/private"
cp -- "$fixture" "$test_root/private/fixture.json"
printf '{}\n' > "$test_root/private/credentials.json"
chmod 600 "$test_root/private/"*.json
cp -- "$repo_root/systemd/agent-remote-node.service" "$repo_root/systemd/agent-remote-runtime.service" "$repo_root/systemd/agent-remote-runtime.sudoers" "$test_root/build/"
public_ca="${AGENT_REMOTE_TEST_PUBLIC_CA:-/etc/ssl/cert.pem}"
if [[ ! -f "$public_ca" ]]; then public_ca=/etc/ssl/certs/ca-certificates.crt; fi
cp -- "$public_ca" "$test_root/build/public-ca.crt"
cat > "$test_root/build/tool" <<'TOOL'
#!/bin/sh
set -eu
case " $* " in
  *' --capacity-bytes '*)
    exec /usr/bin/python3 /opt/agent-remote/runtime/bin/pipeline_bytes.py "$@"
    ;;
  *' --capacity-write '*|*' --capacity-read '*)
    exec /usr/bin/python3 /opt/agent-remote/runtime/bin/capacity.py "$@"
    ;;
  *' --cli-write '*)
    while [ "$1" != --cli-write ]; do shift; done
    /home/runtime/.claude/skills/learning/scripts/learn.sh "$2"
    stty -icanon -isig -echo
    printf '%s' "$2" > /workspace/writer-ready
    # Keep fixed byte evidence if the synthetic terminal stop is classified unclean.
    received="$(dd bs=1 count=1 2>/dev/null | od -An -tu1 | tr -d ' ')"
    printf '%s' "$received" > /workspace/stop-byte
    test "$received" = 3
    ;;
  *' --cli-read '*)
    while [ "$1" != --cli-read ]; do shift; done
    /home/runtime/.claude/skills/learning/scripts/verify.sh "$2"
    cat /home/runtime/.claude/skills/learning/memory.txt > /workspace/inherited.txt
    ;;
  *' --export-proof '*)
    root=/home/runtime/.claude/skills
    mkdir -p "$root/learning/empty"
    printf 'retained SSH export learning\n' > "$root/learning/memory.txt"
    printf '\000\377\001binary state\000' > "$root/learning/state.db"
    dd if=/dev/zero of="$root/learning/large.db" bs=8192 count=1 2>/dev/null
    printf 'root auxiliary state\n' > "$root/root-state"
    ln -s memory.txt "$root/learning/link"
    chmod 750 "$root/learning/memory.txt"
    case " $* " in
      *' --export-long '*) dd if=/dev/zero of="$root/learning/long.db" bs=1048576 count=32 2>/dev/null ;;
      *' --export-bytes '*) exec /usr/bin/python3 /opt/agent-remote/runtime/bin/export_bytes.py "$@" ;;
      *' --export-capacity '*) exec /usr/bin/python3 /opt/agent-remote/runtime/bin/capacity.py "$@" ;;
    esac
    ;;
  *) printf 'synthetic export lifecycle fixture\n' ;;
esac
TOOL
cp -- "$repo_root/tests/fixtures/skill_pipeline_capacity.py" "$test_root/build/capacity.py"
cp -- "$repo_root/tests/fixtures/skill_export_bytes.py" "$test_root/build/export_bytes.py"
cp -- "$repo_root/tests/fixtures/skill_pipeline_bytes.py" "$test_root/build/pipeline_bytes.py"
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
  go build -o "$test_root/build/agent-remote-attach" ./cmd/agent-remote-attach
  go test -c -o "$test_root/build/export.test" ./tests/skilllifecycle
)
cat > "$test_root/build/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
RUN --mount=type=secret,id=proof_ca,required=true,mode=0444 sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources && apt-get -o APT::Update::Error-Mode=any -o Acquire::https::CaInfo=/run/secrets/proof_ca -o Acquire::https::Timeout=30 update -qq && apt-get -o Acquire::https::CaInfo=/run/secrets/proof_ca -o Acquire::https::Timeout=30 install -y -qq --no-install-recommends systemd systemd-sysv dbus tmux bubblewrap iproute2 nftables acl passwd ca-certificates python3 git gh openssh-client openssh-server locales nodejs sudo && rm -rf /var/lib/apt/lists/*
RUN localedef -i en_US -f UTF-8 en_US.UTF-8
RUN groupadd --gid 22000 ar-proof-worker && useradd --uid 22000 --gid 22000 --no-create-home --shell /usr/sbin/nologin ar-proof-worker
COPY agent-remote-node agent-remote-runtime agent-remote-attach export.test agent-remote-node.service agent-remote-runtime.service agent-remote-runtime.sudoers /proof/
COPY --chmod=755 tool /opt/agent-remote/runtimes/claude/proof/bin/claude
COPY capacity.py /opt/agent-remote/runtimes/claude/proof/bin/capacity.py
COPY export_bytes.py /opt/agent-remote/runtimes/claude/proof/bin/export_bytes.py
COPY pipeline_bytes.py /opt/agent-remote/runtimes/claude/proof/bin/pipeline_bytes.py
RUN install -m 755 /usr/bin/node /opt/agent-remote/runtimes/claude/proof/bin/node
ENV container=docker
CMD ["/sbin/init"]
DOCKERFILE
if [[ "${AGENT_REMOTE_TEST_NATIVE_STOP_OBSERVATIONS:-}" == 1 ]]; then
  cp -- "$repo_root/tests/native_stop_observation.sh" "$test_root/build/"
  cat >> "$test_root/build/Dockerfile" <<'OBSERVATION'
COPY native_stop_observation.sh /proof/native_stop_observation.sh
RUN /bin/sh /proof/native_stop_observation.sh
OBSERVATION
fi
# Rotating public CA input must not duplicate the entire installed-package cache for every run.
docker build --quiet --secret "id=proof_ca,src=$test_root/build/public-ca.crt" --tag "$test_image" "$test_root/build" >/dev/null
docker run --detach --name "$test_tag" --privileged --cgroupns=private \
  --add-host host.docker.internal:host-gateway --publish "127.0.0.1:$ssh_port:2222" \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
  --security-opt systempaths=unconfined --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --mount "type=bind,src=$test_root/private,dst=/proof/private,readonly" \
  --mount "type=bind,src=$control,dst=/proof/control" \
  "$test_image" >/dev/null
for attempt in $(seq 1 50); do
  if docker exec "$test_tag" busctl --system --timeout=2s --no-pager list >/dev/null 2>&1; then break; fi
  if [[ "$attempt" = 50 ]]; then printf 'Disposable systemd did not become ready\n' >&2; exit 1; fi
  sleep 0.1
done
test_case='^TestNativeSSHExport$'
test_timeout=8m
if [[ "${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY:-}" == 1 || "${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES:-}" == 1 ]]; then test_timeout=30m; fi
if [[ "${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_LONG:-}" == 1 ]]; then test_timeout=30m; fi
if [[ "$mode" == cli-lifecycle ]]; then test_case='^TestNativeCLILifecycle$'; fi
if [[ "$mode" == capacity ]]; then
  test_case='^TestNativePipelineCapacity$'
  test_timeout=6h
fi
docker exec --env AGENT_REMOTE_RUN_SKILL_LIFECYCLE_TEST=1 --env AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_TEST=1 --env TMPDIR=/var/tmp \
  --env "AGENT_REMOTE_RUN_SKILL_PIPELINE_CAPACITY=${AGENT_REMOTE_RUN_SKILL_PIPELINE_CAPACITY:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_PIPELINE_BYTES=${AGENT_REMOTE_RUN_SKILL_PIPELINE_BYTES:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_PIPELINE_COMBINED=${AGENT_REMOTE_RUN_SKILL_PIPELINE_COMBINED:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_CAPACITY:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_BYTES:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_LONG=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_LONG:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_DESTINATION_FULL=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_DESTINATION_FULL:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVERSIZE=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVERSIZE:-}" \
  --env "AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVER_ENTRIES=${AGENT_REMOTE_RUN_SKILL_SSH_EXPORT_OVER_ENTRIES:-}" \
  "$test_tag" /proof/export.test -test.v -test.timeout="$test_timeout" -test.run "$test_case"
