#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-copy.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac

test_binary="$test_root/skillmanager.test"
helper_test_binary="$test_root/runtimehelper.test"
managed_test_binary="$test_root/managedskills.test"
worker_test_binary="$test_root/worker.test"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$test_binary" ./internal/skillmanager
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$helper_test_binary" ./internal/runtimehelper
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$managed_test_binary" ./internal/managedskills
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" \
    go test -c -o "$worker_test_binary" ./internal/worker
)

# This proves real Linux copies and UID permissions, not Native/Docker backend mount support.
docker run --rm --user 0:0 \
  --env AGENT_REMOTE_TEST_PYTHON=1 \
  --mount "type=bind,src=$test_binary,dst=/proof/skillmanager.test,readonly" \
  --mount "type=bind,src=$helper_test_binary,dst=/proof/runtimehelper.test,readonly" \
  --mount "type=bind,src=$managed_test_binary,dst=/proof/managedskills.test,readonly" \
  --mount "type=bind,src=$worker_test_binary,dst=/proof/worker.test,readonly" \
  --mount "type=bind,src=$repo_root/internal/skillmanager/testdata,dst=/testdata,readonly" \
  --workdir / \
  debian:bookworm-slim \
  /bin/sh -eu -c '
    apt-get update -qq
    apt-get install -y --no-install-recommends acl passwd python3-venv >/dev/null
    cp /proof/skillmanager.test /tmp/skillmanager.test
    chmod 0755 /tmp/skillmanager.test
    /tmp/skillmanager.test -test.v
    cp /proof/runtimehelper.test /tmp/runtimehelper.test
    chmod 0755 /tmp/runtimehelper.test
    /tmp/runtimehelper.test -test.v -test.run "^TestNative(Stop|Cgroup|Unit|Absent|Failed|Cancelled|Skill|Natural|Exited|Pane)|^TestSkill(Preparation|SystemPins)|^TestDeployment|^TestManaged(Spec|Launch|Observed)|^TestNativeTakeover|^TestHelper(Capture|Finalization|Takeover)"
    cp /proof/managedskills.test /tmp/managedskills.test
    chmod 0755 /tmp/managedskills.test
    /tmp/managedskills.test -test.v -test.run "^TestVerifySessionSkills"
    cp /proof/worker.test /tmp/worker.test
    chmod 0755 /tmp/worker.test
    /tmp/worker.test -test.v -test.run "^TestFinalization|^TestManagedStop|^TestStopSave|^TestSharedFinalization|^TestTakeover|^TestDeployment"
  '
