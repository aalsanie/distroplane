#!/usr/bin/env python3
"""Validate the current changelog entry and extract its GitHub release notes."""
import argparse
import re
import sys
from pathlib import Path

VERSION_PATTERN = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?")
HEADING_PATTERN = re.compile(
    r"^## \[([0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?)\]"
    r"(?: - ([0-9]{4}-[0-9]{2}-[0-9]{2}))?$"
)


def release_notes(version: str, changelog: str) -> str:
    if not VERSION_PATTERN.fullmatch(version):
        raise ValueError("invalid VERSION")
    lines = changelog.splitlines()
    if not lines or lines[0] != "# Changelog":
        raise ValueError("missing changelog title")
    headings = []
    for index, line in enumerate(lines):
        if line.startswith("## "):
            match = HEADING_PATTERN.fullmatch(line)
            if not match:
                raise ValueError("invalid changelog release heading")
            headings.append((index, match.group(1)))
    if not headings or headings[0][1] != version:
        raise ValueError(f"latest changelog entry must be [{version}]")
    if len({v for _, v in headings}) != len(headings):
        raise ValueError("duplicate changelog release version")
    for i, (start, entry_version) in enumerate(headings):
        stop = headings[i + 1][0] if i + 1 < len(headings) else len(lines)
        section = lines[start + 1:stop]
        if not any(re.fullmatch(r"- \S.*", line) for line in section):
            raise ValueError(f"empty changelog entry: {entry_version}")
    end = headings[1][0] if len(headings) > 1 else len(lines)
    body = "\n".join(lines[headings[0][0] + 1:end]).strip()
    return f"# Distroplane v{version}\n\n{body}\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--extract", action="store_true", help="Write current release notes to stdout")
    parser.add_argument("--root", type=Path, default=Path("."))
    args = parser.parse_args()
    try:
        version = (args.root / "VERSION").read_text(encoding="utf-8").strip()
        changelog = (args.root / "CHANGELOG.md").read_text(encoding="utf-8")
        notes = release_notes(version, changelog)
    except (OSError, UnicodeError, ValueError) as exc:
        print(f"changelog check: {exc}", file=sys.stderr)
        return 1
    if args.extract:
        sys.stdout.write(notes)
    else:
        print(f"changelog check passed: {version}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
