#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.2.7}"
OUT_DIR="${OUT_DIR:-dist}"
TARGETS="${TARGETS:-darwin/amd64 darwin/arm64 linux/amd64/glibc linux/arm64/glibc linux/amd64/musl linux/arm64/musl}"
DEVICE_PROXY_DIR="${DEVICE_PROXY_DIR:-}"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

for target in $TARGETS; do
  IFS=/ read -r GOOS GOARCH LIBC <<EOF
$target
EOF
  if [ -z "$GOOS" ] || [ -z "$GOARCH" ]; then
    echo "invalid target: $target" >&2
    exit 2
  fi
  if [ "$GOOS" = "linux" ]; then
    LIBC="${LIBC:-glibc}"
    if [ "$LIBC" != "glibc" ] && [ "$LIBC" != "musl" ]; then
      echo "invalid linux libc for target $target: $LIBC" >&2
      exit 2
    fi
    package="agent-remote-node-${VERSION}-${GOOS}-${GOARCH}-${LIBC}"
  else
    if [ -n "${LIBC:-}" ]; then
      echo "non-linux targets must not specify libc: $target" >&2
      exit 2
    fi
    package="agent-remote-node-${VERSION}-${GOOS}-${GOARCH}"
  fi
  work="$OUT_DIR/$package"
  mkdir -p "$work"
  ldflags="-s -w -X github.com/Agent-Remote/agent-remote-node/internal/config.DefaultVersion=${VERSION}"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$work/agent-remote-node" ./cmd/agent-remote-node
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$work/agent-remote-attach" ./cmd/agent-remote-attach
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$work/agent-remote-runtime" ./cmd/agent-remote-runtime
  cp -R config.example.json systemd README.md README.zh-CN.md CHANGELOG.md LICENSE THIRD_PARTY_NOTICES.md "$work/"
  printf '%s\n' "$VERSION" > "$work/VERSION"
  mkdir -p "$work/scripts"
  install -m 0755 scripts/install.sh "$work/scripts/install.sh"
  install -m 0755 scripts/install-claude-runtime.sh "$work/scripts/install-claude-runtime.sh"
  install -m 0755 scripts/install-nodejs-runtime.sh "$work/scripts/install-nodejs-runtime.sh"
  install -m 0755 scripts/install-device-proxy.sh "$work/scripts/install-device-proxy.sh"
  install -m 0755 scripts/install.sh "$work/install.sh"
  if [ "$GOOS" = "linux" ]; then
    proxy_root="$DEVICE_PROXY_DIR/${GOOS}-${GOARCH}-${LIBC}"
    proxy_source="$proxy_root/agent-remote-device-proxy"
    if [ -z "$DEVICE_PROXY_DIR" ] || [ ! -f "$proxy_source" ] || [ -L "$proxy_source" ] || [ ! -x "$proxy_source" ] || [ ! -f "$proxy_root/VERSION" ]; then
      echo "missing executable device proxy for $target at $proxy_source" >&2
      exit 1
    fi
    proxy_version="$(tr -d '[:space:]' < "$proxy_root/VERSION")"
    if ! [[ "$proxy_version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]; then
      echo "invalid device proxy version for $target" >&2
      exit 1
    fi
    mkdir -p "$work/device"
    install -m 0755 "$proxy_source" "$work/device/agent-remote-device-proxy"
    printf '%s\n' "$proxy_version" > "$work/device/VERSION"
  fi
  tar -C "$OUT_DIR" -czf "$OUT_DIR/$package.tar.gz" "$package"
done

echo "release artifacts written to $OUT_DIR"
