#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

bash -n scripts/*.sh tests/*.sh

unformatted="$(gofmt -l cmd internal)"
if [[ -n "$unformatted" ]]; then
  printf 'The following Go files need gofmt:\n%s\n' "$unformatted" >&2
  exit 1
fi

go vet ./...
coverage_profile=$(mktemp)
trap 'rm -f "$coverage_profile"' EXIT
go test -covermode=atomic -coverprofile="$coverage_profile" ./...
scripts/check-coverage.sh "$coverage_profile" 45
if scripts/check-coverage.sh "$coverage_profile" 100 >/dev/null 2>&1; then
  echo "coverage gate accepted an unmet threshold" >&2
  exit 1
fi
tests/install_scripts_test.sh
ruby tests/release_workflow_contract_test.rb
git diff --check
