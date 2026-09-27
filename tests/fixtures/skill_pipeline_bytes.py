"""
在独立受管会话中写入并逐字节验证默认十 GiB 状态，可同时覆盖十万条目。
"""

import sys
from pathlib import Path

ROOT = Path("/home/runtime/.claude/skills")
GIB = 1 << 30
SYSTEM = {"ego-browser", "agent-remote-device"}


def payload(path: Path, size: int, value: int, write: bool) -> None:
    """
    使用固定缓冲写入实际普通文件，或由后继会话独立逐字节核验。

    :param path (Path): 独占会话中的文件
    :param size (int): 完整预期字节数
    :param value (int): 区分十个独立内容对象的填充值
    :param write (bool): 是否生成首个会话的内容
    """
    block = bytes([value]) * (1 << 20)
    with path.open("xb" if write else "rb") as stream:
        remaining = size
        while remaining:
            expected = block[: min(remaining, len(block))]
            if write:
                assert stream.write(expected) == len(expected)
            else:
                assert stream.read(len(expected)) == expected
            remaining -= len(expected)
        if not write:
            assert stream.read(1) == b""
    assert path.stat().st_size == size


def run() -> None:
    """
    生成或检查十个有效技能及完整辅助状态，仅输出固定阶段失败类型。
    """
    stage = "start"
    try:
        write = "--capacity-write" in sys.argv
        assert write != ("--capacity-read" in sys.argv)
        combined = "--capacity-entries" in sys.argv
        stage = "files"
        auxiliary = 0
        for index in range(99_970 if combined else 0):
            path = ROOT / f"capacity-{index:06d}"
            content = f"pipeline-capacity-{index:06d}\n".encode()
            if write:
                with path.open("xb") as stream:
                    assert stream.write(content) == len(content)
            else:
                assert path.read_bytes() == content
            auxiliary += len(content)
        stage = "bytes"
        for index in range(10):
            name = "learning" if index == 0 else f"byte-capacity-{index}"
            directory = ROOT / name
            document = f"---\nname: {name}\ndescription: Capacity test.\n---\n".encode()
            if write and index:
                directory.mkdir()
                with (directory / "SKILL.md").open("xb") as stream:
                    assert stream.write(document) == len(document)
            assert (directory / "SKILL.md").read_bytes() == document
            size = GIB - len(document) - (auxiliary if index == 0 else 0)
            payload(directory / "payload.bin", size, index, write)
        stage = "cardinality"
        entries = [
            path for path in ROOT.rglob("*") if path.relative_to(ROOT).parts[0] not in SYSTEM
        ]
        assert len(entries) == (100_000 if combined else 30)
        assert not any(path.is_symlink() for path in entries)
        assert sum(path.stat().st_size for path in entries if path.is_file()) == 10 * GIB
        Path("/workspace/capacity-witness").write_text("written" if write else "inherited")
    except Exception as error:
        Path("/workspace/capacity-status").write_text(f"{stage}:{type(error).__name__}")
        raise


if __name__ == "__main__":
    run()
