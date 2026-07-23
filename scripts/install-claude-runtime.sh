#!/usr/bin/env bash
set -euo pipefail

VERSION="${CLAUDE_VERSION:-}"
SOURCE="${CLAUDE_SOURCE:-}"
CHECKSUM="${CLAUDE_SHA256:-}"
CHANNEL="${CLAUDE_CHANNEL:-stable}"
INSTALLER_SOURCE="${CLAUDE_INSTALLER_SOURCE:-https://claude.ai/install.sh}"
RUNTIME_ROOT="${RUNTIME_ROOT:-/opt/agent-remote/runtimes/claude}"
ALLOW_NON_ROOT="${ALLOW_NON_ROOT:-0}"

usage() {
  cat >&2 <<'EOF'
usage:
  install-claude-runtime.sh [--channel stable|latest|VERSION]
  install-claude-runtime.sh --version VERSION --source PATH_OR_URL --sha256 CHECKSUM

Options:
  --channel VALUE          Official Claude installer channel. Default: stable.
  --installer-source URL   Official installer URL override.
  --version VERSION        Required with --source; optional official version pin.
  --source PATH_OR_URL     Pre-downloaded Claude executable.
  --sha256 CHECKSUM        Required checksum for --source.
  --runtime-root PATH      Managed runtime root.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version requires a value}"; shift 2 ;;
    --source) SOURCE="${2:?--source requires a value}"; shift 2 ;;
    --sha256) CHECKSUM="${2:?--sha256 requires a value}"; shift 2 ;;
    --channel) CHANNEL="${2:?--channel requires a value}"; shift 2 ;;
    --installer-source) INSTALLER_SOURCE="${2:?--installer-source requires a value}"; shift 2 ;;
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
if [ "$(id -u)" -ne 0 ] && [ "$ALLOW_NON_ROOT" != "1" ]; then
  if ! command -v sudo >/dev/null 2>&1; then
    echo "run as root or install sudo" >&2
    exit 1
  fi
  args=(--channel "$CHANNEL" --installer-source "$INSTALLER_SOURCE" --runtime-root "$RUNTIME_ROOT")
  [ -n "$VERSION" ] && args+=(--version "$VERSION")
  [ -n "$SOURCE" ] && args+=(--source "$SOURCE" --sha256 "$CHECKSUM")
  exec sudo "$0" "${args[@]}"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-claude-runtime.XXXXXX")"
trap 'rm -rf "$work"' EXIT
artifact="$work/claude"

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
  copy_source "$SOURCE" "$artifact"
else
  installer="$work/install.sh"
  install_home="$work/home"
  install -d -m 0700 "$install_home"
  copy_source "$INSTALLER_SOURCE" "$installer"
  selector="${VERSION:-$CHANNEL}"
  HOME="$install_home" CLAUDE_CODE_DISABLE_AUTOUPDATER=1 bash "$installer" "$selector"
  installed="$install_home/.local/bin/claude"
  if [ ! -e "$installed" ]; then
    echo "official Claude installer did not create $installed" >&2
    exit 1
  fi
  cp -L "$installed" "$artifact"
  detected_version="$("$artifact" --version | sed -n 's/^\([0-9][0-9.]*\).*/\1/p' | head -n 1)"
  if [ -z "$detected_version" ]; then
    echo "failed to detect the installed Claude version" >&2
    exit 1
  fi
  if [ -n "$VERSION" ] && [ "$VERSION" != "$detected_version" ]; then
    echo "Claude version mismatch: requested $VERSION, installed $detected_version" >&2
    exit 1
  fi
  VERSION="$detected_version"
  CHECKSUM="$(sha256_file "$artifact")"
fi

actual="$(sha256_file "$artifact")"
if [ "$actual" != "$CHECKSUM" ]; then
  echo "checksum mismatch: expected $CHECKSUM, got $actual" >&2
  exit 1
fi

target="$RUNTIME_ROOT/$VERSION"
if [ -f "$target/SHA256SUMS" ]; then
  installed_checksum="$(awk 'NR == 1 {print $1}' "$target/SHA256SUMS")"
  if [ "$installed_checksum" != "$CHECKSUM" ]; then
    echo "refusing to replace Claude $VERSION with a different checksum" >&2
    exit 1
  fi
fi
install -d -m 0755 "$target/bin"
install -m 0755 "$artifact" "$target/bin/claude"
printf '%s  %s\n' "$CHECKSUM" "bin/claude" > "$target/SHA256SUMS"
printf '%s\n' "$VERSION" > "$target/VERSION"
chmod 0644 "$target/SHA256SUMS" "$target/VERSION"
ln -sfn "$VERSION" "$RUNTIME_ROOT/current"
"$target/bin/claude" --version
echo "managed Claude runtime installed: version=$VERSION sha256=$CHECKSUM"
