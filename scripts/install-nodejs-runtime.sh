#!/usr/bin/env bash
set -euo pipefail

VERSION="${NODEJS_VERSION:-}"
SOURCE="${NODEJS_SOURCE:-}"
CHECKSUM="${NODEJS_SHA256:-}"
CHANNEL="${NODEJS_CHANNEL:-22}"
DIST_BASE="${NODEJS_DIST_BASE:-https://nodejs.org/dist}"
RUNTIME_ROOT="${RUNTIME_ROOT:-/opt/agent-remote/runtimes/claude}"
ALLOW_NON_ROOT="${ALLOW_NON_ROOT:-0}"
OS_NAME="${NODEJS_OS_OVERRIDE:-$(uname -s)}"
ARCH_NAME="${NODEJS_ARCH_OVERRIDE:-$(uname -m)}"

usage() {
  cat >&2 <<'EOF'
usage:
  install-nodejs-runtime.sh [--channel MAJOR] [--version VERSION]
  install-nodejs-runtime.sh --version VERSION --source PATH_OR_URL --sha256 CHECKSUM

Options:
  --channel MAJOR         Official Node.js release line. Default: 22.
  --version VERSION       Pin an official version, or use with --source.
  --source PATH_OR_URL    Pre-downloaded Node.js .tar.gz archive.
  --sha256 CHECKSUM       Required checksum for --source.
  --dist-base URL         Official distribution base URL override.
  --runtime-root PATH     Managed Claude runtime root receiving bin/node.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version requires a value}"; shift 2 ;;
    --source) SOURCE="${2:?--source requires a value}"; shift 2 ;;
    --sha256) CHECKSUM="${2:?--sha256 requires a value}"; shift 2 ;;
    --channel) CHANNEL="${2:?--channel requires a value}"; shift 2 ;;
    --dist-base) DIST_BASE="${2:?--dist-base requires a value}"; shift 2 ;;
    --runtime-root) RUNTIME_ROOT="${2:?--runtime-root requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

if { [ -n "$SOURCE" ] && { [ -z "$VERSION" ] || [ -z "$CHECKSUM" ]; }; } || \
   { [ -z "$SOURCE" ] && [ -n "$CHECKSUM" ]; }; then
  echo "--source, --version, and --sha256 must be provided together" >&2
  usage
  exit 2
fi
case "$CHANNEL" in
  ''|*[!0-9]*) echo "--channel must be a major version" >&2; exit 2 ;;
esac
VERSION="${VERSION#v}"

if [ "$(id -u)" -ne 0 ] && [ "$ALLOW_NON_ROOT" != "1" ]; then
  if ! command -v sudo >/dev/null 2>&1; then
    echo "run as root or install sudo" >&2
    exit 1
  fi
  args=(--channel "$CHANNEL" --dist-base "$DIST_BASE" --runtime-root "$RUNTIME_ROOT")
  [ -n "$VERSION" ] && args+=(--version "$VERSION")
  [ -n "$SOURCE" ] && args+=(--source "$SOURCE" --sha256 "$CHECKSUM")
  exec sudo "$0" "${args[@]}"
fi

if [ "$OS_NAME" != "Linux" ]; then
  echo "managed Node.js runtime currently supports Linux only" >&2
  exit 1
fi
case "$ARCH_NAME" in
  x86_64|amd64) node_arch=x64 ;;
  arm64|aarch64) node_arch=arm64 ;;
  *) echo "unsupported Node.js architecture: $ARCH_NAME" >&2; exit 1 ;;
esac

current="$RUNTIME_ROOT/current"
if [ ! -x "$current/bin/claude" ]; then
  echo "managed Claude runtime is required before installing Node.js" >&2
  exit 1
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-nodejs-runtime.XXXXXX")"
trap 'rm -rf "$work"' EXIT
archive="$work/node.tar.gz"

copy_source() {
  local source="$1" destination="$2"
  case "$source" in
    http://*|https://*) curl --fail --show-error --location --retry 5 --retry-all-errors "$source" -o "$destination" ;;
    *) cp "$source" "$destination" ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

if [ -n "$SOURCE" ]; then
  copy_source "$SOURCE" "$archive"
else
  if [ -n "$VERSION" ]; then
    release_base="${DIST_BASE%/}/v$VERSION"
  else
    release_base="${DIST_BASE%/}/latest-v${CHANNEL}.x"
  fi
  checksums="$work/SHASUMS256.txt"
  copy_source "$release_base/SHASUMS256.txt" "$checksums"
  archive_name="$(awk -v arch="$node_arch" '$2 ~ ("^node-v[0-9.]+-linux-" arch "\\.tar\\.gz$") {print $2; exit}' "$checksums")"
  if [ -z "$archive_name" ]; then
    echo "Node.js release does not contain a linux-$node_arch archive" >&2
    exit 1
  fi
  detected_version="$(printf '%s' "$archive_name" | sed -n 's/^node-v\([0-9][0-9.]*\)-linux-.*/\1/p')"
  if [ -z "$detected_version" ] || { [ -n "$VERSION" ] && [ "$VERSION" != "$detected_version" ]; }; then
    echo "Node.js release version mismatch" >&2
    exit 1
  fi
  VERSION="$detected_version"
  CHECKSUM="$(awk -v name="$archive_name" '$2 == name {print $1; exit}' "$checksums")"
  copy_source "$release_base/$archive_name" "$archive"
fi

actual="$(sha256_file "$archive")"
if [ "$actual" != "$CHECKSUM" ]; then
  echo "checksum mismatch: expected $CHECKSUM, got $actual" >&2
  exit 1
fi

extract="$work/extract"
mkdir -p "$extract"
tar -xzf "$archive" -C "$extract"
node_binary="$(find "$extract" -mindepth 3 -maxdepth 3 -type f -path '*/bin/node' -print -quit)"
if [ -z "$node_binary" ] || [ ! -x "$node_binary" ]; then
  echo "Node.js archive does not contain an executable bin/node" >&2
  exit 1
fi
node_root="$(cd "$(dirname "$node_binary")/.." && pwd -P)"
if [ ! -f "$node_root/lib/node_modules/npm/bin/npm-cli.js" ] || \
   [ ! -f "$node_root/lib/node_modules/npm/bin/npx-cli.js" ]; then
  echo "Node.js archive does not contain npm and npx" >&2
  exit 1
fi
detected_version="$($node_binary --version | sed -n 's/^v\([0-9][0-9.]*\)$/\1/p')"
if [ "$detected_version" != "$VERSION" ]; then
  echo "Node.js version mismatch: requested $VERSION, archive contains ${detected_version:-unknown}" >&2
  exit 1
fi

target="$(cd "$current" && pwd -P)"
if [ -f "$target/NODE_SHA256SUMS" ]; then
  installed_version="$(cat "$target/NODE_VERSION" 2>/dev/null || true)"
  installed_checksum="$(awk 'NR == 1 {print $1}' "$target/NODE_SHA256SUMS")"
  if [ "$installed_version" = "$VERSION" ] && [ "$installed_checksum" != "$CHECKSUM" ]; then
    echo "refusing to replace Node.js $VERSION with a different checksum" >&2
    exit 1
  fi
fi
install -m 0755 "$node_binary" "$target/bin/node"
rm -rf "$target/lib/node_modules/npm"
install -d -m 0755 "$target/lib/node_modules"
cp -R "$node_root/lib/node_modules/npm" "$target/lib/node_modules/npm"
ln -sfn ../lib/node_modules/npm/bin/npm-cli.js "$target/bin/npm"
ln -sfn ../lib/node_modules/npm/bin/npx-cli.js "$target/bin/npx"
printf '%s  %s\n' "$CHECKSUM" "bin/node" > "$target/NODE_SHA256SUMS"
printf '%s\n' "$VERSION" > "$target/NODE_VERSION"
chmod 0644 "$target/NODE_SHA256SUMS" "$target/NODE_VERSION"
"$target/bin/node" --version
PATH="$target/bin:${PATH:-/usr/bin:/bin}" "$target/bin/npm" --version
echo "managed Node.js runtime installed: version=$VERSION sha256=$CHECKSUM"
