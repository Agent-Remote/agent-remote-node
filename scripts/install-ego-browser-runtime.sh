#!/usr/bin/env bash
set -euo pipefail

RUNTIME_ROOT="${EGO_BROWSER_RUNTIME_ROOT:-/opt/agent-remote/ego-browser}"
VERSION=""
WRAPPER_SOURCE=""
EXPECTED_WRAPPER_SHA256=""
SKILL_SOURCE=""
SKILL_VERSION=""
EXPECTED_SKILL_TREE_SHA256=""
SOURCE_MANIFEST=""
EXPECTED_SOURCE_MANIFEST_SHA256=""
ALLOW_NON_ROOT="${ALLOW_NON_ROOT:-0}"
STAGING=""
NEXT_LINK=""

usage() {
  cat <<'EOF'
Usage: install-ego-browser-runtime.sh [options]

Installs one verified Linux ego-browser wrapper and official Skill tree into an
immutable, versioned managed runtime.

Required options:
  --version VERSION
  --wrapper-source PATH
  --wrapper-sha256 HEX
  --skill-source PATH
  --skill-version VERSION
  --skill-tree-sha256 HEX
  --source-manifest PATH
  --source-manifest-sha256 HEX

Optional:
  --runtime-root PATH   Default: /opt/agent-remote/ego-browser
  -h, --help            Show this help.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --runtime-root) RUNTIME_ROOT="${2:?--runtime-root requires a value}"; shift 2 ;;
    --version) VERSION="${2:?--version requires a value}"; shift 2 ;;
    --wrapper-source) WRAPPER_SOURCE="${2:?--wrapper-source requires a value}"; shift 2 ;;
    --wrapper-sha256) EXPECTED_WRAPPER_SHA256="${2:?--wrapper-sha256 requires a value}"; shift 2 ;;
    --skill-source) SKILL_SOURCE="${2:?--skill-source requires a value}"; shift 2 ;;
    --skill-version) SKILL_VERSION="${2:?--skill-version requires a value}"; shift 2 ;;
    --skill-tree-sha256) EXPECTED_SKILL_TREE_SHA256="${2:?--skill-tree-sha256 requires a value}"; shift 2 ;;
    --source-manifest) SOURCE_MANIFEST="${2:?--source-manifest requires a value}"; shift 2 ;;
    --source-manifest-sha256) EXPECTED_SOURCE_MANIFEST_SHA256="${2:?--source-manifest-sha256 requires a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ] && [ "$ALLOW_NON_ROOT" != "1" ]; then
  echo "install-ego-browser-runtime.sh must run as root" >&2
  exit 1
fi
if [[ ! "$VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]; then
  echo "invalid ego-browser wrapper version" >&2
  exit 2
fi
if [[ ! "$SKILL_VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]; then
  echo "invalid ego-browser Skill version" >&2
  exit 2
fi
for digest in "$EXPECTED_WRAPPER_SHA256" "$EXPECTED_SKILL_TREE_SHA256" "$EXPECTED_SOURCE_MANIFEST_SHA256"; do
  if [[ ! "$digest" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "ego-browser SHA-256 values must contain 64 hexadecimal characters" >&2
    exit 2
  fi
done
if [ ! -f "$WRAPPER_SOURCE" ] || [ -L "$WRAPPER_SOURCE" ] || [ ! -x "$WRAPPER_SOURCE" ]; then
  echo "ego-browser wrapper source must be a non-symlink regular executable" >&2
  exit 2
fi
if [ ! -d "$SKILL_SOURCE" ] || [ -L "$SKILL_SOURCE" ] || [ ! -f "$SKILL_SOURCE/SKILL.md" ]; then
  echo "ego-browser Skill source must contain a non-symlink SKILL.md" >&2
  exit 2
fi
if [ ! -f "$SOURCE_MANIFEST" ] || [ -L "$SOURCE_MANIFEST" ]; then
  echo "ego-browser Skill source manifest must be a non-symlink regular file" >&2
  exit 2
fi

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

validate_skill_tree() {
  local root="$1" invalid
  invalid="$(find "$root" -mindepth 1 ! -type d ! -type f -print -quit)"
  if [ -n "$invalid" ]; then
    echo "ego-browser Skill tree contains a symlink or non-regular entry" >&2
    return 1
  fi
  if [ -n "$(find "$root" -mindepth 1 -type f -links +1 -print -quit 2>/dev/null || true)" ]; then
    echo "ego-browser Skill tree contains a multiply-linked file" >&2
    return 1
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

cleanup() {
  [ -z "$STAGING" ] || rm -rf -- "$STAGING"
  [ -z "$NEXT_LINK" ] || rm -f -- "$NEXT_LINK"
}
trap cleanup EXIT

validate_skill_tree "$SKILL_SOURCE"
actual_wrapper_sha256="$(sha256_file "$WRAPPER_SOURCE")"
actual_skill_tree_sha256="$(skill_tree_sha256 "$SKILL_SOURCE")"
actual_source_manifest_sha256="$(sha256_file "$SOURCE_MANIFEST")"
if [ "${actual_wrapper_sha256,,}" != "${EXPECTED_WRAPPER_SHA256,,}" ]; then
  echo "ego-browser wrapper SHA-256 verification failed" >&2
  exit 1
fi
if [ "${actual_skill_tree_sha256,,}" != "${EXPECTED_SKILL_TREE_SHA256,,}" ]; then
  echo "ego-browser Skill tree SHA-256 verification failed" >&2
  exit 1
fi
if [ "${actual_source_manifest_sha256,,}" != "${EXPECTED_SOURCE_MANIFEST_SHA256,,}" ]; then
  echo "ego-browser Skill source manifest SHA-256 verification failed" >&2
  exit 1
fi

releases="$RUNTIME_ROOT/releases"
release="$releases/$VERSION"
mkdir -p "$releases"
chmod 0755 "$RUNTIME_ROOT" "$releases"

if [ -e "$release" ]; then
  if [ -L "$release" ] || [ ! -f "$release/VERSION" ] || [ ! -f "$release/WRAPPER_SHA256" ] || \
     [ ! -f "$release/SKILL_VERSION" ] || [ ! -f "$release/SKILL_TREE_SHA256" ] || \
     [ ! -f "$release/SOURCE_MANIFEST_SHA256" ] || [ ! -f "$release/ego-browser-skill-source.json" ] || \
     [ ! -x "$release/bin/ego-browser" ] || [ ! -d "$release/skill/ego-browser" ]; then
    echo "existing ego-browser runtime release is invalid" >&2
    exit 1
  fi
  validate_skill_tree "$release/skill/ego-browser"
  installed_wrapper_sha256="$(sha256_file "$release/bin/ego-browser")"
  installed_skill_tree_sha256="$(skill_tree_sha256 "$release/skill/ego-browser")"
  installed_source_manifest_sha256="$(sha256_file "$release/ego-browser-skill-source.json")"
  if [ "$(tr -d '[:space:]' < "$release/VERSION")" != "$VERSION" ] || \
     [ "$(tr -d '[:space:]' < "$release/SKILL_VERSION")" != "$SKILL_VERSION" ] || \
     [ "$(tr -d '[:space:]' < "$release/WRAPPER_SHA256")" != "${actual_wrapper_sha256,,}" ] || \
     [ "$(tr -d '[:space:]' < "$release/SKILL_TREE_SHA256")" != "${actual_skill_tree_sha256,,}" ] || \
     [ "$(tr -d '[:space:]' < "$release/SOURCE_MANIFEST_SHA256")" != "${actual_source_manifest_sha256,,}" ] || \
     [ "${installed_wrapper_sha256,,}" != "${actual_wrapper_sha256,,}" ] || \
     [ "${installed_skill_tree_sha256,,}" != "${actual_skill_tree_sha256,,}" ] || \
     [ "${installed_source_manifest_sha256,,}" != "${actual_source_manifest_sha256,,}" ]; then
    echo "ego-browser runtime version already exists with different or invalid content" >&2
    exit 1
  fi
else
  STAGING="$releases/.${VERSION}.install-$$"
  rm -rf -- "$STAGING"
  mkdir -p "$STAGING/bin" "$STAGING/skill/ego-browser"
  install -m 0555 "$WRAPPER_SOURCE" "$STAGING/bin/ego-browser"
  cp -R "$SKILL_SOURCE/." "$STAGING/skill/ego-browser/"
  install -m 0444 "$SOURCE_MANIFEST" "$STAGING/ego-browser-skill-source.json"
  printf '%s\n' "$VERSION" > "$STAGING/VERSION"
  printf '%s\n' "${actual_wrapper_sha256,,}" > "$STAGING/WRAPPER_SHA256"
  printf '%s\n' "$SKILL_VERSION" > "$STAGING/SKILL_VERSION"
  printf '%s\n' "${actual_skill_tree_sha256,,}" > "$STAGING/SKILL_TREE_SHA256"
  printf '%s\n' "${actual_source_manifest_sha256,,}" > "$STAGING/SOURCE_MANIFEST_SHA256"
  find "$STAGING/skill/ego-browser" -type f -exec chmod 0444 {} +
  find "$STAGING/skill/ego-browser" -type d -exec chmod 0555 {} +
  chmod 0444 "$STAGING/VERSION" "$STAGING/WRAPPER_SHA256" "$STAGING/SKILL_VERSION" \
    "$STAGING/SKILL_TREE_SHA256" "$STAGING/SOURCE_MANIFEST_SHA256"
  chmod 0555 "$STAGING/bin" "$STAGING/skill" "$STAGING"
  mv "$STAGING" "$release"
  STAGING=""
fi

NEXT_LINK="$RUNTIME_ROOT/.current-$$"
ln -s "$release" "$NEXT_LINK"
if ! mv -Tf "$NEXT_LINK" "$RUNTIME_ROOT/current" 2>/dev/null; then
  mv -fh "$NEXT_LINK" "$RUNTIME_ROOT/current"
fi
NEXT_LINK=""

echo "installed ego-browser runtime $VERSION with official Skill $SKILL_VERSION"
