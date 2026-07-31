#!/usr/bin/env bash
set -euo pipefail

profile=${1:?coverage profile path is required}
minimum=${2:-45}

if [[ ! -f "$profile" ]]; then
  echo "coverage profile does not exist: $profile" >&2
  exit 1
fi
if ! [[ "$minimum" =~ ^[0-9]+([.][0-9]+)?$ ]]; then
  echo "coverage minimum must be a nonnegative number" >&2
  exit 1
fi

coverage=$(go tool cover -func="$profile" | awk '$1 == "total:" {gsub(/%/, "", $3); print $3}')
if [[ -z "$coverage" ]]; then
  echo "coverage profile has no total" >&2
  exit 1
fi
if ! awk -v actual="$coverage" -v required="$minimum" \
  'BEGIN { exit !(actual + 0 >= required + 0) }'; then
  echo "Go statement coverage ${coverage}% is below ${minimum}%" >&2
  exit 1
fi
echo "Go statement coverage ${coverage}% meets ${minimum}%"
