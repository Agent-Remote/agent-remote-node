"""
在真实受管会话中生成十份独立状态，支持默认容量和超过默认字节额度的恢复验收。
"""

import sys
from pathlib import Path

ROOT = Path("/home/runtime/.claude/skills")
GIB = 1 << 30
SYSTEM = {"ego-browser", "agent-remote-device"}
EXCESS = (1 << 20) if "--export-oversize" in sys.argv else 0


def ordinary_files(directory: Path) -> list[Path]:
    """
    枚举测试条目内的普通文件，不跟随链接计算展开内容。

    :param directory (Path): 独立测试目录
    :return list[Path]: 已存在的普通文件
    """
    return [path for path in directory.rglob("*") if not path.is_symlink() and path.is_file()]


def user_entries() -> list[Path]:
    """
    只统计普通第三方条目，排除只读系统挂载。

    :return list[Path]: 完整用户条目路径
    """
    return [path for path in ROOT.rglob("*") if path.relative_to(ROOT).parts[0] not in SYSTEM]


def fill(path: Path, size: int, value: int) -> None:
    """
    顺序写入实际字节，使用固定缓冲且不创建稀疏文件或共享硬链接。

    :param path (Path): 本会话独占的目标文件
    :param size (int): 必须写入的总字节数
    :param value (int): 区分各文件内容的填充值
    """
    assert size > 0
    block = bytes([value]) * (1 << 20)
    with path.open("wb") as output:
        remaining = size
        while remaining:
            written = output.write(block[: min(remaining, len(block))])
            assert written > 0
            remaining -= written


stage = "start"
try:
    assert "--export-bytes" in sys.argv
    stage = "instructions"
    directories = [ROOT / "learning"]
    assert (directories[0] / "SKILL.md").is_file()
    for index in range(1, 10):
        directory = ROOT / f"byte-capacity-{index}"
        directory.mkdir()
        (directory / "SKILL.md").write_text(
            f"---\nname: byte-capacity-{index}\ndescription: Capacity fixture.\n---\n"
        )
        directories.append(directory)
    for directory in directories:
        (directory / "payload.bin").touch(exist_ok=False)
    stage = "entries"
    if "--export-capacity" in sys.argv:
        for index in range(100_000 - len(user_entries())):
            with (ROOT / f"capacity-{index:06d}").open("xb") as output:
                output.write(f"pipeline-capacity-{index:06d}\n".encode())
        assert len(user_entries()) == 100_000
    else:
        assert len(user_entries()) == 36
    stage = "bytes"
    auxiliary = sum(
        path.stat().st_size for path in ROOT.iterdir() if not path.is_symlink() and path.is_file()
    )
    assert auxiliary < GIB
    for index, directory in enumerate(directories):
        existing = sum(path.stat().st_size for path in ordinary_files(directory))
        size = GIB - existing - (auxiliary if index == 0 else 0)
        size += EXCESS if index == 0 else 0
        fill(directory / "payload.bin", size, index)
        assert sum(path.stat().st_size for path in ordinary_files(directory)) <= GIB + (
            EXCESS if index == 0 else 0
        )
    assert (
        sum(
            path.stat().st_size
            for path in user_entries()
            if not path.is_symlink() and path.is_file()
        )
        == 10 * GIB + EXCESS
    )
    Path("/workspace/capacity-witness").write_text("written")
except Exception as error:
    Path("/workspace/capacity-status").write_text(f"{stage}:{type(error).__name__}")
    raise
