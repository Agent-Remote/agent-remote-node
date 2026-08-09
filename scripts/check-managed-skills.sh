#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
device_repo="${1:-}"
if [[ -z "$device_repo" ]]; then
  echo "usage: $0 PATH_TO_AGENT_REMOTE_DEVICE" >&2
  exit 2
fi
device_repo="$(cd "$device_repo" && pwd)"

source_root="$device_repo/skills/agent-remote-device"
embedded_root="$repo_root/internal/managedskills/skills/agent-remote-device"
if [[ ! -f "$source_root/SKILL.md" ]]; then
  echo "device skill source is missing: $source_root/SKILL.md" >&2
  exit 1
fi
if [[ ! -f "$embedded_root/SKILL.md" ]]; then
  echo "node managed skill is missing: $embedded_root/SKILL.md" >&2
  exit 1
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-skill-check.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT

# agents/ contains Codex discovery metadata and is not installed into Claude accounts.
(
  cd "$source_root"
  find . -type f ! -path './agents/*' -print | LC_ALL=C sort
) > "$work/device-files"
(
  cd "$embedded_root"
  find . -type f -print | LC_ALL=C sort
) > "$work/node-files"

if ! cmp -s "$work/device-files" "$work/node-files"; then
  echo "managed skill file lists differ between device and node repositories" >&2
  diff -u "$work/device-files" "$work/node-files" || true
  exit 1
fi

while IFS= read -r relative_path; do
  relative_path="${relative_path#./}"
  if ! cmp -s "$source_root/$relative_path" "$embedded_root/$relative_path"; then
    echo "managed skill content differs: $relative_path" >&2
    diff -u "$source_root/$relative_path" "$embedded_root/$relative_path" || true
    exit 1
  fi
done < "$work/device-files"

echo "managed skill matches agent-remote-device source"
