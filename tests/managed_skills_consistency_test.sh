#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/agent-remote-managed-skill-test.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT

device_skill="$work/device/skills/agent-remote-device"
mkdir -p "$(dirname "$device_skill")"
cp -R "$repo_root/internal/managedskills/skills/agent-remote-device" "$device_skill"
mkdir -p "$device_skill/agents"
printf '%s\n' 'interface: codex-only' > "$device_skill/agents/openai.yaml"

"$repo_root/scripts/check-managed-skills.sh" "$work/device" >/dev/null

printf '%s\n' 'drift' >> "$device_skill/SKILL.md"
if "$repo_root/scripts/check-managed-skills.sh" "$work/device" >/dev/null 2>&1; then
  echo "managed skill consistency check accepted content drift" >&2
  exit 1
fi
cp "$repo_root/internal/managedskills/skills/agent-remote-device/SKILL.md" "$device_skill/SKILL.md"

printf '%s\n' 'new reference' > "$device_skill/references/new-reference.md"
if "$repo_root/scripts/check-managed-skills.sh" "$work/device" >/dev/null 2>&1; then
  echo "managed skill consistency check accepted file-list drift" >&2
  exit 1
fi

echo "managed skill consistency tests passed"
