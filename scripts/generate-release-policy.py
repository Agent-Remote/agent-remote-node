#!/usr/bin/env python3

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent.parent
OUTPUT = ROOT / "internal/egobrowserartifact/release_policy_generated.go"
SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$"
)
REPOSITORY = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$")
WORKFLOW = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*\.ya?ml$")
PROTOCOL = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
COMMIT = re.compile(r"^[0-9a-f]{40}$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")


def reject_duplicates(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def load_object(name: str) -> dict[str, Any]:
    path = ROOT / name
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 32 * 1024:
        raise ValueError(f"{name} must be a bounded regular file")
    value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=reject_duplicates)
    if not isinstance(value, dict):
        raise ValueError(f"{name} must contain a JSON object")
    return value


def require_string(value: dict[str, Any], field: str, pattern: re.Pattern[str]) -> str:
    result = value.get(field)
    if not isinstance(result, str) or pattern.fullmatch(result) is None:
        raise ValueError(f"{field} is invalid")
    return result


def load_policy() -> dict[str, str]:
    dependencies = load_object("release-dependencies.json")
    if set(dependencies) != {"schema_version", "device_proxy", "ego_browser_wrapper"}:
        raise ValueError("release dependency fields are invalid")
    if dependencies["schema_version"] != 4:
        raise ValueError("release dependency schema is unsupported")
    wrapper = dependencies.get("ego_browser_wrapper")
    if not isinstance(wrapper, dict) or set(wrapper) != {
        "repository",
        "release_workflow",
        "version",
        "protocol_version",
    }:
        raise ValueError("ego-browser wrapper dependency is invalid")
    require_string(wrapper, "repository", REPOSITORY)
    require_string(wrapper, "release_workflow", WORKFLOW)
    wrapper_version = require_string(wrapper, "version", SEMVER)
    protocol_version = require_string(wrapper, "protocol_version", PROTOCOL)

    skill = load_object("ego-browser-skill-source.json")
    required_skill_fields = {
        "schema_version",
        "name",
        "version",
        "upstream_repository",
        "upstream_tag",
        "upstream_commit",
        "source_path",
        "skill_document_sha256",
        "tree_digest_algorithm",
        "tree_sha256",
    }
    if set(skill) != required_skill_fields or skill["schema_version"] != 1:
        raise ValueError("ego-browser Skill source manifest is invalid")
    name = skill.get("name")
    upstream_repository = skill.get("upstream_repository")
    if name != "ego-browser" or upstream_repository != "https://github.com/citrolabs/ego-lite":
        raise ValueError("ego-browser Skill identity is invalid")
    skill_version = require_string(skill, "version", SEMVER)
    if skill.get("upstream_tag") != f"v{skill_version}":
        raise ValueError("ego-browser Skill tag is inconsistent")
    skill_commit = require_string(skill, "upstream_commit", COMMIT)
    document_sha256 = require_string(skill, "skill_document_sha256", SHA256)
    tree_sha256 = require_string(skill, "tree_sha256", SHA256)
    return {
        "wrapper_version": wrapper_version,
        "protocol_version": protocol_version,
        "skill_name": name,
        "skill_version": skill_version,
        "skill_repository": upstream_repository,
        "skill_commit": skill_commit,
        "skill_document_sha256": document_sha256,
        "skill_tree_sha256": tree_sha256,
    }


def go_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=True)


def render(policy: dict[str, str]) -> str:
    values = (
        ("PinnedWrapperVersion", "reviewed Linux ego-browser wrapper release", "wrapper_version"),
        ("PinnedProtocolVersion", "reviewed ego-browser protocol", "protocol_version"),
        ("OfficialSkillName", "reviewed upstream Skill name", "skill_name"),
        ("OfficialSkillVersion", "reviewed upstream ego-browser Skill release", "skill_version"),
        ("OfficialSkillUpstreamRepository", "reviewed upstream Skill repository", "skill_repository"),
        ("OfficialSkillSourceCommit", "reviewed upstream ego-lite tree", "skill_commit"),
        ("OfficialSkillDocumentSHA256", "reviewed upstream SKILL.md bytes", "skill_document_sha256"),
        ("OfficialSkillTreeSHA256", "reviewed upstream Skill tree", "skill_tree_sha256"),
    )
    lines = [
        "// Code generated by scripts/generate-release-policy.py; DO NOT EDIT.",
        "",
        "package egobrowserartifact",
        "",
        "const (",
    ]
    for name, description, key in values:
        lines.append(f"\t// {name} identifies the {description}.")
        lines.append(f"\t{name} = {go_string(policy[key])}")
    lines.extend((")", ""))
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    expected = render(load_policy())
    if args.check:
        actual = OUTPUT.read_text(encoding="utf-8") if OUTPUT.is_file() else ""
        if actual != expected:
            print(f"generated release policy is stale: {OUTPUT.relative_to(ROOT)}", file=sys.stderr)
            return 1
        return 0
    OUTPUT.write_text(expected, encoding="utf-8")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, json.JSONDecodeError) as error:
        raise SystemExit(f"release policy generation failed: {error}") from error
