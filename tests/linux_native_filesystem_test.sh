#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-native-files.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

docker_arch="$(docker version --format '{{.Server.Arch}}')"
case "$docker_arch" in
  amd64 | x86_64) go_arch="amd64" ;;
  arm64 | aarch64) go_arch="arm64" ;;
  *) printf 'Unsupported Docker server architecture: %s\n' "$docker_arch" >&2; exit 1 ;;
esac

test_binary="$test_root/runtimehelper.test"
(
  cd "$repo_root"
  CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c -o "$test_binary" ./internal/runtimehelper
)

if [[ $# -eq 0 ]]; then
  set -- debian:bookworm-slim ubuntu:22.04 ubuntu:24.04
fi
for distro in "$@"; do
  docker run --rm --user 0:0 --cap-add SYS_ADMIN \
    --security-opt seccomp=unconfined --security-opt apparmor=unconfined \
    --security-opt systempaths=unconfined \
    --env AGENT_REMOTE_RUN_NATIVE_FILES_TEST=1 \
    --env AGENT_REMOTE_TEST_APT_MIRROR \
    --mount "type=bind,src=$test_binary,dst=/proof/runtimehelper.test,readonly" \
    "$distro" /bin/sh -eu -c '
      export DEBIAN_FRONTEND=noninteractive
      if [ -n "${AGENT_REMOTE_TEST_APT_MIRROR:-}" ]; then
        find /etc/apt -type f \( -name "*.sources" -o -name "*.list" \) -exec \
          sed -i "s|http://deb.debian.org|$AGENT_REMOTE_TEST_APT_MIRROR|g; s|http://ports.ubuntu.com|$AGENT_REMOTE_TEST_APT_MIRROR|g; s|http://archive.ubuntu.com|$AGENT_REMOTE_TEST_APT_MIRROR|g; s|http://security.ubuntu.com|$AGENT_REMOTE_TEST_APT_MIRROR|g" {} +
      fi
      apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 update -qq
      apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 install -y -qq --no-install-recommends ca-certificates >/dev/null
      find /etc/apt -type f \( -name "*.sources" -o -name "*.list" \) -exec sed -i "s|http://|https://|g" {} +
      apt-get -o Acquire::https::Timeout=30 -o Acquire::Retries=2 install -y -qq --no-install-recommends bubblewrap gcc libc6-dev gawk python3 ca-certificates openssl netbase media-types tzdata default-jdk-headless maven >/dev/null
      printf "192.0.2.42 native-hosts-proof.invalid\n" >> /etc/hosts
      printf "must remain hidden\n" > /etc/agent-remote-native-test-secret
      for java_config in /etc/java-*-openjdk; do
        mkdir -p "$java_config/management"
        printf "must remain hidden\n" > "$java_config/management/jmxremote.password"
      done
      cp /proof/runtimehelper.test /tmp/runtimehelper.test
      chmod 0755 /tmp/runtimehelper.test
      /tmp/runtimehelper.test -test.v -test.run "^TestNativeSystemFiles$"
    '
done
