#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version>" >&2
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

VERSION="${1#v}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.+][0-9A-Za-z.-]+)?$ ]]; then
  echo "Invalid semantic version: $1" >&2
  exit 2
fi

python3 scripts/generate-release-policy.py --check
temporary="$(mktemp .VERSION.XXXXXX)"
trap 'rm -f "$temporary"' EXIT
printf '%s\n' "$VERSION" > "$temporary"
chmod 0644 "$temporary"
mv -f "$temporary" VERSION
trap - EXIT

go test ./...
tests/install_scripts_test.sh

scripts/update-changelog.sh "$VERSION"

echo "Prepared agent-remote-node v${VERSION}"
