#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-systemd.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-skill-systemd:$test_tag"
test_container="$test_tag"
cleanup() {
  if docker container inspect "$test_container" >/dev/null 2>&1; then
    docker stop --time 5 "$test_container" >/dev/null 2>&1 || true
    docker rm "$test_container" >/dev/null 2>&1 || true
  fi
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/runtimehelper.test" ./internal/runtimehelper
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build -o "$test_root/agent-remote-runtime" ./cmd/agent-remote-runtime
)
cat > "$test_root/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus tmux bubblewrap iproute2 nftables acl passwd && rm -rf /var/lib/apt/lists/*
COPY runtimehelper.test agent-remote-runtime /proof/
ENV container=docker
CMD ["/sbin/init"]
DOCKERFILE
docker build --quiet --tag "$test_image" "$test_root" >/dev/null
# No host filesystem or cgroup mounts: the writable cgroup namespace belongs only to this container.
docker run --detach --name "$test_container" --privileged --cgroupns=private \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
  --security-opt systempaths=unconfined --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  "$test_image" >/dev/null
for attempt in $(seq 1 50); do
  # Root systemctl can use systemd's private socket before systemd-run's system bus exists.
  if docker exec "$test_container" busctl --system --timeout=2s --no-pager list >/dev/null 2>&1; then break; fi
  if [[ "$attempt" = 50 ]]; then docker logs "$test_container"; exit 1; fi
  sleep 0.1
done
# Docker's /tmp tmpfs is noexec; command fixtures and ACL checks need the ordinary filesystem.
docker exec --env AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST=1 --env TMPDIR=/var/tmp "$test_container" \
  /proof/runtimehelper.test -test.v -test.run "${SKILL_SYSTEMD_TEST_FILTER:-^Test(Native(SkillSystemdLifecycle|TakeoverSystemdDescendantQuiescence)|AccountCopySystemdBackup|AccountMigrationSystemdLifecycle|MigrationWriterSystemdReplacementPreservesOriginalInvocation|MigrationCompletionSystemdCrashPrefixConvergesWithoutNewWriters|ManagedLaunchSystemd(RecoveryDoesNotExecuteTwice|CancellationRetainsWork|ExplicitCancellationRetainsWork|LostAdmissionRetainsWork|StartingExit|RebootMountGuards))$}"
