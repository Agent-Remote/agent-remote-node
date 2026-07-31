#!/usr/bin/env bash
set -euo pipefail

RUNTIME_ROOT="${DEVICE_RUNTIME_ROOT:-/opt/agent-remote/device}"
VERSION=""
SOURCE=""
EXPECTED_SHA256=""
ALLOW_NON_ROOT="${ALLOW_NON_ROOT:-0}"
STAGING=""
NEXT_LINK=""

usage() {
  cat <<'EOF'
Usage: install-device-proxy.sh --version VERSION --source PATH --sha256 HEX [--runtime-root PATH]

Installs one verified agent-remote-device proxy into a versioned managed runtime.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --runtime-root) RUNTIME_ROOT="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --source) SOURCE="$2"; shift 2 ;;
    --sha256) EXPECTED_SHA256="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ] && [ "$ALLOW_NON_ROOT" != "1" ]; then
  echo "install-device-proxy.sh must run as root" >&2
  exit 1
fi
if [[ ! "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]; then
  echo "invalid device proxy version" >&2
  exit 2
fi
if [[ ! "$EXPECTED_SHA256" =~ ^[0-9a-fA-F]{64}$ ]]; then
  echo "device proxy SHA-256 must contain 64 hexadecimal characters" >&2
  exit 2
fi
if [ ! -f "$SOURCE" ] || [ -L "$SOURCE" ] || [ ! -x "$SOURCE" ]; then
  echo "device proxy source must be a regular executable file" >&2
  exit 2
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

cleanup() {
  [ -z "$STAGING" ] || rm -rf -- "$STAGING"
  [ -z "$NEXT_LINK" ] || rm -f -- "$NEXT_LINK"
}
trap cleanup EXIT

actual_sha256="$(sha256_file "$SOURCE")"
if [ "${actual_sha256,,}" != "${EXPECTED_SHA256,,}" ]; then
  echo "device proxy SHA-256 verification failed" >&2
  exit 1
fi

releases="$RUNTIME_ROOT/releases"
release="$releases/$VERSION"
mkdir -p "$releases"
chmod 0755 "$RUNTIME_ROOT" "$releases"

if [ -e "$release" ]; then
  if [ -L "$release" ] || [ ! -f "$release/SHA256" ] || [ ! -x "$release/bin/agent-remote-device-proxy" ]; then
    echo "existing device proxy release is invalid" >&2
    exit 1
  fi
  installed_sha256="$(tr -d '[:space:]' < "$release/SHA256")"
  if [ "$installed_sha256" != "${actual_sha256,,}" ]; then
    echo "device proxy version already exists with different content" >&2
    exit 1
  fi
else
  STAGING="$releases/.${VERSION}.install-$$"
  rm -rf -- "$STAGING"
  mkdir -p "$STAGING/bin"
  install -m 0755 "$SOURCE" "$STAGING/bin/agent-remote-device-proxy"
  printf '%s\n' "$VERSION" > "$STAGING/VERSION"
  printf '%s\n' "${actual_sha256,,}" > "$STAGING/SHA256"
  chmod 0644 "$STAGING/VERSION" "$STAGING/SHA256"
  mv "$STAGING" "$release"
  STAGING=""
fi

NEXT_LINK="$RUNTIME_ROOT/.current-$$"
ln -s "$release" "$NEXT_LINK"
mv -f "$NEXT_LINK" "$RUNTIME_ROOT/current"
NEXT_LINK=""

echo "installed agent-remote-device proxy $VERSION"
