#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
upstream_root="${1:-$repo_root/.external/ego-lite}"
manifest="$repo_root/ego-browser-skill-source.json"
embedded_root="$repo_root/internal/managedskills/skills/ego-browser"

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

tree_sha256() {
  local root="$1"
  while IFS= read -r -d '' file; do
    relative="${file#"$root"/}"
    size="$(wc -c < "$file" | tr -d '[:space:]')"
    printf '%s\0%s\0' "$relative" "$size"
    command cat -- "$file"
    printf '\0'
  done < <(find "$root" -type f -print0 | LC_ALL=C sort -z) | sha256_stream
}

test "$(jq -er '.schema_version' "$manifest")" = "1"
test "$(jq -er '.name' "$manifest")" = "ego-browser"
test "$(jq -er '.upstream_repository' "$manifest")" = "https://github.com/citrolabs/ego-lite"
commit="$(jq -er '.upstream_commit' "$manifest")"
source_path="$(jq -er '.source_path' "$manifest")"
expected_document="$(jq -er '.skill_document_sha256' "$manifest")"
expected_tree="$(jq -er '.tree_sha256' "$manifest")"
if ! [[ "$commit" =~ ^[0-9a-f]{40}$ ]] || [[ "$source_path" = /* ]] || [[ "$source_path" == *..* ]]; then
  echo "ego-browser Skill provenance manifest is invalid" >&2
  exit 1
fi
upstream_skill="$upstream_root/$source_path"
if [ ! -d "$upstream_skill" ] || [ ! -f "$upstream_skill/SKILL.md" ]; then
  echo "pinned upstream ego-browser Skill is missing" >&2
  exit 1
fi
if [ -d "$upstream_root/.git" ] && [ "$(git -C "$upstream_root" rev-parse HEAD)" != "$commit" ]; then
  echo "upstream ego-lite checkout is not at the pinned commit" >&2
  exit 1
fi
for root in "$embedded_root" "$upstream_skill"; do
  if [ -n "$(find "$root" -mindepth 1 ! -type d ! -type f -print -quit)" ]; then
    echo "ego-browser Skill tree contains a symlink or non-regular entry" >&2
    exit 1
  fi
done

embedded_files="$(cd "$embedded_root" && find . -type f | LC_ALL=C sort)"
upstream_files="$(cd "$upstream_skill" && find . -type f | LC_ALL=C sort)"
if [ "$embedded_files" != "$upstream_files" ]; then
  echo "official ego-browser Skill file list differs from pinned upstream" >&2
  exit 1
fi
while IFS= read -r relative; do
  [ -z "$relative" ] || cmp -s "$embedded_root/$relative" "$upstream_skill/$relative" || {
    echo "official ego-browser Skill content differs: $relative" >&2
    exit 1
  }
done <<< "$embedded_files"
if [ "$(sha256_file "$embedded_root/SKILL.md")" != "$expected_document" ] || \
   [ "$(tree_sha256 "$embedded_root")" != "$expected_tree" ]; then
  echo "embedded ego-browser Skill digest differs from provenance manifest" >&2
  exit 1
fi

echo "official ego-browser Skill matches pinned ego-lite source $commit"
