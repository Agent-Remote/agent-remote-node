#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.0.2}"
OUT_DIR="${OUT_DIR:-dist}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64}"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

for target in $TARGETS; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  package="agent-remote-node-${VERSION}-${GOOS}-${GOARCH}"
  work="$OUT_DIR/$package"
  mkdir -p "$work"
  ldflags="-s -w -X github.com/Agent-Remote/agent-remote-node/internal/config.DefaultVersion=${VERSION}"
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$work/agent-remote-node" ./cmd/agent-remote-node
  GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -ldflags "$ldflags" -o "$work/agent-remote-attach" ./cmd/agent-remote-attach
  cp -R config.example.json systemd scripts/install-node.sh README.md LICENSE THIRD_PARTY_NOTICES.md "$work/"
  tar -C "$OUT_DIR" -czf "$OUT_DIR/$package.tar.gz" "$package"
done

echo "release artifacts written to $OUT_DIR"
