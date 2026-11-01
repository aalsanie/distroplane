#!/usr/bin/env python3
"""Verify exact resolved runtime dependency versions in the Maven plugin graph."""
import json
import sys
from pathlib import Path

REQUIRED = {
    ("com.fasterxml.jackson.core", "jackson-annotations"): "2.18.12",
    ("com.fasterxml.jackson.core", "jackson-core"): "2.18.12",
    ("com.fasterxml.jackson.core", "jackson-databind"): "2.18.12",
    ("org.apache.commons", "commons-compress"): "1.28.0",
}


def validate(root):
    versions = {key: set() for key in REQUIRED}

    def visit(node):
        if not isinstance(node, dict):
            raise ValueError("malformed Maven dependency tree")
        key = (node.get("groupId"), node.get("artifactId"))
        if key in versions:
            version = node.get("version")
            if not isinstance(version, str):
                raise ValueError(f"invalid resolved dependency {key}")
            versions[key].add(version)
        children = node.get("children", [])
        if not isinstance(children, list):
            raise ValueError("malformed Maven dependency children")
        for child in children:
            visit(child)

    visit(root)
    for key, expected in REQUIRED.items():
        if versions[key] != {expected}:
            raise ValueError(f"{key[0]}:{key[1]} resolved to {sorted(versions[key])}, require {expected}")


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: check_dependency_tree.py TREE.json")
    try:
        validate(json.loads(Path(sys.argv[1]).read_text(encoding="utf-8")))
    except (ValueError, OSError) as exc:
        raise SystemExit(f"Maven runtime dependency verification failed: {exc}") from exc
    print("Maven Jackson and Commons Compress dependency versions verified")


if __name__ == "__main__":
    main()
