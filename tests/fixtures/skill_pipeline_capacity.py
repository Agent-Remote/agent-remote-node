"""
在真实受管会话内写入并逐字节核验默认十万条目的确定性容量样本。
"""

import sys
from pathlib import Path

root = Path("/home/runtime/.claude/skills")
write = "--capacity-write" in sys.argv
export = "--export-capacity" in sys.argv
count = 99_992 if export else 99_998
over_entries = "--export-over-entries" in sys.argv
if over_entries:
    assert export
    count += 1
stage = "start"
try:
    assert write or export or "--capacity-read" in sys.argv
    stage = "instructions"
    assert (root / "learning/SKILL.md").is_file()
    stage = "files"
    for index in range(count):
        path = root / f"capacity-{index:06d}"
        content = f"pipeline-capacity-{index:06d}\n".encode()
        if write or export:
            with path.open("xb") as output:
                output.write(content)
        else:
            assert path.read_bytes() == content
    stage = "cardinality"
    assert sum(
        1
        for path in root.rglob("*")
        if path.relative_to(root).parts[0] not in {"ego-browser", "agent-remote-device"}
    ) == 100_000 + int(over_entries)
    Path("/workspace/capacity-witness").write_text("written" if write or export else "inherited")
except Exception as error:
    Path("/workspace/capacity-status").write_text(f"{stage}:{type(error).__name__}")
    raise
