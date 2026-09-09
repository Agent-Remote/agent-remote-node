#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.2.18}"
OUT_DIR="${OUT_DIR:-dist}"
TARGETS="${TARGETS:-darwin/amd64 darwin/arm64 linux/amd64/glibc linux/arm64/glibc linux/amd64/musl linux/arm64/musl}"
DEVICE_PROXY_DIR="${DEVICE_PROXY_DIR:-}"
EGO_BROWSER_WRAPPER_DIR="${EGO_BROWSER_WRAPPER_DIR:-}"
EGO_BROWSER_SKILL_ROOT="${EGO_BROWSER_SKILL_ROOT:-internal/managedskills/skills/ego-browser}"
EGO_BROWSER_SKILL_SOURCE_MANIFEST="${EGO_BROWSER_SKILL_SOURCE_MANIFEST:-ego-browser-skill-source.json}"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

skill_tree_sha256() {
  local root="$1"
  while IFS= read -r -d '' file; do
    relative="${file#"$root"/}"
    size="$(wc -c < "$file" | tr -d '[:space:]')"
    printf '%s\0%s\0' "$relative" "$size"
    command cat -- "$file"
    printf '\0'
  done < <(find "$root" -type f -print0 | LC_ALL=C sort -z) | sha256_stream
}

if [ ! -d "$EGO_BROWSER_SKILL_ROOT" ] || [ -L "$EGO_BROWSER_SKILL_ROOT" ] || \
   [ ! -f "$EGO_BROWSER_SKILL_ROOT/SKILL.md" ] || [ ! -f "$EGO_BROWSER_SKILL_SOURCE_MANIFEST" ]; then
  echo "missing official ego-browser Skill source or provenance manifest" >&2
  exit 1
fi
if [ -n "$(find "$EGO_BROWSER_SKILL_ROOT" -mindepth 1 ! -type d ! -type f -print -quit)" ]; then
  echo "official ego-browser Skill source contains a symlink or non-regular entry" >&2
  exit 1
fi
skill_version="$(jq -er '.version' "$EGO_BROWSER_SKILL_SOURCE_MANIFEST")"
skill_tree_sha256="$(jq -er '.tree_sha256' "$EGO_BROWSER_SKILL_SOURCE_MANIFEST")"
if [ "$(skill_tree_sha256 "$EGO_BROWSER_SKILL_ROOT")" != "$skill_tree_sha256" ]; then
  echo "official ego-browser Skill tree does not match its provenance manifest" >&2
  exit 1
fi

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
  install -m 0755 scripts/install-ego-browser-runtime.sh "$work/scripts/install-ego-browser-runtime.sh"
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

    ego_root="$EGO_BROWSER_WRAPPER_DIR/${GOOS}-${GOARCH}-${LIBC}"
    ego_wrapper="$ego_root/ego-browser"
    if [ -z "$EGO_BROWSER_WRAPPER_DIR" ] || [ ! -f "$ego_wrapper" ] || [ -L "$ego_wrapper" ] || \
       [ ! -x "$ego_wrapper" ] || [ ! -f "$ego_root/VERSION" ]; then
      echo "missing executable ego-browser wrapper for $target at $ego_wrapper" >&2
      exit 1
    fi
    ego_version="$(tr -d '[:space:]' < "$ego_root/VERSION")"
    if ! [[ "$ego_version" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]; then
      echo "invalid ego-browser wrapper version for $target" >&2
      exit 1
    fi
    mkdir -p "$work/ego-browser/skill"
    install -m 0755 "$ego_wrapper" "$work/ego-browser/ego-browser"
    cp -R "$EGO_BROWSER_SKILL_ROOT" "$work/ego-browser/skill/ego-browser"
    install -m 0644 "$EGO_BROWSER_SKILL_SOURCE_MANIFEST" "$work/ego-browser/ego-browser-skill-source.json"
    printf '%s\n' "$ego_version" > "$work/ego-browser/VERSION"
    printf '%s\n' "$(sha256_file "$ego_wrapper")" > "$work/ego-browser/WRAPPER_SHA256"
    printf '%s\n' "$skill_version" > "$work/ego-browser/SKILL_VERSION"
    printf '%s\n' "$skill_tree_sha256" > "$work/ego-browser/SKILL_TREE_SHA256"
    printf '%s\n' "$(sha256_file "$EGO_BROWSER_SKILL_SOURCE_MANIFEST")" > "$work/ego-browser/SOURCE_MANIFEST_SHA256"
    jq --arg wrapper_version "$ego_version" \
      '.ego_browser_wrapper_version = $wrapper_version' config.example.json > "$work/config.example.json"
  fi
  tar -C "$OUT_DIR" -czf "$OUT_DIR/$package.tar.gz" "$package"
done

echo "release artifacts written to $OUT_DIR"
