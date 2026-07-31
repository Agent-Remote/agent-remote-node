#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 <version>" >&2
  echo "Example: $0 0.1.0" >&2
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

python3 - "$VERSION" <<'PY'
from __future__ import annotations

import re
import stat
import sys
import tempfile
from pathlib import Path

version = sys.argv[1]

replacements = {
    Path("scripts/prepare-release.sh"): (
        r"Example: \$0 [0-9A-Za-z.+-]+",
        f"Example: $0 {version}",
    ),
    Path("internal/config/config.go"): (
        r'var DefaultVersion = "[^"]+"',
        f'var DefaultVersion = "{version}"',
    ),
    Path("scripts/build-release.sh"): (
        r'VERSION="\$\{VERSION:-[^}]+\}"',
        f'VERSION="${{VERSION:-{version}}}"',
    ),
    Path("config.example.json"): (
        r'"version": "[0-9A-Za-z.+-]+"',
        f'"version": "{version}"',
    ),
}

for path, (pattern, replacement) in replacements.items():
    text = path.read_text()
    text = re.sub(pattern, replacement, text, count=1)
    if path == Path("scripts/prepare-release.sh"):
        mode = stat.S_IMODE(path.stat().st_mode)
        with tempfile.NamedTemporaryFile(
            mode="w", encoding="utf-8", dir=path.parent, delete=False
        ) as temporary:
            temporary.write(text)
        staged = Path(temporary.name)
        staged.chmod(mode)
        staged.replace(path)
    else:
        path.write_text(text)

readme = Path("README.md")
if readme.exists():
    text = readme.read_text()
    text = re.sub(r'"version": "[0-9A-Za-z.+-]+"', f'"version": "{version}"', text, count=1)
    text = re.sub(
        r"VERSION=[0-9A-Za-z.+-]+(?= DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release\.sh)",
        f"VERSION={version}",
        text,
    )
    if f"VERSION={version} DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh" not in text:
        raise SystemExit("README release example was not updated")
    text = re.sub(r"--version [0-9A-Za-z.+-]+", f"--version {version}", text)
    readme.write_text(text)

readme_cn = Path("README.zh-CN.md")
if readme_cn.exists():
    text = readme_cn.read_text()
    text = re.sub(r'"version": "[0-9A-Za-z.+-]+"', f'"version": "{version}"', text, count=1)
    text = re.sub(
        r"VERSION=[0-9A-Za-z.+-]+(?= DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release\.sh)",
        f"VERSION={version}",
        text,
    )
    if f"VERSION={version} DEVICE_PROXY_DIR=/path/to/device-proxies scripts/build-release.sh" not in text:
        raise SystemExit("README.zh-CN release example was not updated")
    text = re.sub(r"--version [0-9A-Za-z.+-]+", f"--version {version}", text)
    readme_cn.write_text(text)
PY

go test ./...
tests/install_scripts_test.sh

scripts/update-changelog.sh "$VERSION"

echo "Prepared agent-remote-node v${VERSION}"
