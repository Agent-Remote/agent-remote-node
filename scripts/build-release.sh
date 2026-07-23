#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.0.4-fix.3}"
OUT_DIR="${OUT_DIR:-dist}"
TARGETS="${TARGETS:-darwin/amd64 darwin/arm64 linux/amd64/glibc linux/arm64/glibc linux/amd64/musl linux/arm64/musl}"

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
  install -m 0755 scripts/install.sh "$work/install.sh"
  tar -C "$OUT_DIR" -czf "$OUT_DIR/$package.tar.gz" "$package"
done

echo "release artifacts written to $OUT_DIR"
