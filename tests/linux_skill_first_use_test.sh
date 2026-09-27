#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="${AGENT_REMOTE_TEST_SKILL_FIRST_USE_FIXTURE:?requires a disposable Server fixture}"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-first-use.XXXXXX")"
test_tag="$(basename "$test_root")"
test_image="agent-remote-first-use:$test_tag"
test_container="$test_tag"
cleanup() {
  docker container rm --force "$test_container" >/dev/null 2>&1 || true
  docker image rm "$test_image" >/dev/null 2>&1 || true
  rm -rf -- "$test_root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cp -- "$fixture" "$test_root/fixture.json"
chmod 600 "$test_root/fixture.json"
docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_root/worker.test" ./internal/worker
)
cat > "$test_root/Dockerfile" <<'DOCKERFILE'
FROM debian:bookworm-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends systemd systemd-sysv dbus && rm -rf /var/lib/apt/lists/*
COPY worker.test /proof/worker.test
ENV container=docker
CMD ["/sbin/init"]
DOCKERFILE
# Credentials are mounted only for execution and must never enter an image layer or build context.
printf 'fixture.json\n' > "$test_root/.dockerignore"
docker build --quiet --tag "$test_image" "$test_root" >/dev/null
docker run --detach --name "$test_container" --privileged --cgroupns=private \
  --add-host host.docker.internal:host-gateway \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp \
  --security-opt systempaths=unconfined --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
  --mount "type=bind,src=$test_root/fixture.json,dst=/proof/fixture.json,readonly" \
  "$test_image" >/dev/null
for attempt in $(seq 1 50); do
  if docker exec "$test_container" busctl --system --timeout=2s --no-pager list >/dev/null 2>&1; then break; fi
  if [[ "$attempt" = 50 ]]; then docker logs "$test_container"; exit 1; fi
  sleep 0.1
done
# Keep the default 2 GiB storage reserve on the container disk, independently of systemd's tmpfs.
docker exec --env AGENT_REMOTE_RUN_SKILL_SYSTEMD_TEST=1 \
  --env TMPDIR=/var/tmp \
  --env AGENT_REMOTE_TEST_SKILL_FIRST_USE_FIXTURE=/proof/fixture.json "$test_container" \
  /proof/worker.test -test.v -test.run '^TestFirstUseWorkerLiveServer$'
