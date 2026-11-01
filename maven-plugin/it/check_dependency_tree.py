#!/usr/bin/env python3
"""Verify resolved Maven dependencies and reject known vulnerable runtime versions."""
import argparse
import json
import time
import urllib.error
import urllib.request
from pathlib import Path

REQUIRED = {
    ("com.fasterxml.jackson.core", "jackson-annotations"): "2.18.12",
    ("com.fasterxml.jackson.core", "jackson-core"): "2.18.12",
    ("com.fasterxml.jackson.core", "jackson-databind"): "2.18.12",
    ("org.apache.commons", "commons-compress"): "1.28.0",
    ("org.apache.commons", "commons-lang3"): "3.18.0",
}
OSV_URL = "https://api.osv.dev/v1/querybatch"


def walk(root):
    if not isinstance(root, dict):
        raise ValueError("malformed Maven dependency tree")
    yield root
    children = root.get("children", [])
    if not isinstance(children, list):
        raise ValueError("malformed Maven dependency children")
    for child in children:
        yield from walk(child)


def validate(root):
    versions = {key: set() for key in REQUIRED}
    for node in walk(root):
        key = (node.get("groupId"), node.get("artifactId"))
        if key in versions:
            version = node.get("version")
            if not isinstance(version, str):
                raise ValueError(f"invalid resolved dependency {key}")
            versions[key].add(version)
    for key, expected in REQUIRED.items():
        if versions[key] != {expected}:
            raise ValueError(f"{key[0]}:{key[1]} resolved to {sorted(versions[key])}, require {expected}")


def runtime_dependencies(root):
    packages = set()
    for node in walk(root):
        key = (node.get("groupId"), node.get("artifactId"))
        if key[0] is None or key[1] is None or key == (root.get("groupId"), root.get("artifactId")):
            continue
        if node.get("scope", "compile") not in ("compile", "runtime"):
            continue
        version = node.get("version")
        if not isinstance(version, str) or not version:
            raise ValueError(f"unversioned runtime dependency {key}")
        packages.add((f"{key[0]}:{key[1]}", version))
    if not packages or not {(":".join(k), v) for k, v in REQUIRED.items()}.issubset(packages):
        raise ValueError("incomplete Maven runtime dependency inventory")
    return sorted(packages)


def query_osv(packages):
    body = json.dumps({"queries": [
        {"package": {"ecosystem": "Maven", "name": name}, "version": version}
        for name, version in packages
    ]}).encode("utf-8")
    request = urllib.request.Request(
        OSV_URL, data=body, method="POST",
        headers={"Content-Type": "application/json", "User-Agent": "distroplane-maven-security-check"},
    )
    for attempt in range(3):
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                data = response.read(10 * 1024 * 1024 + 1)
            if len(data) > 10 * 1024 * 1024:
                raise ValueError("oversized OSV response")
            return json.loads(data)
        except (OSError, urllib.error.URLError, ValueError) as exc:
            if attempt == 2:
                raise ValueError(f"OSV vulnerability lookup unavailable: {exc}") from exc
            time.sleep(2 ** attempt)
    raise AssertionError("unreachable")


def check_vulnerabilities(packages, response):
    results = response.get("results") if isinstance(response, dict) else None
    if not isinstance(results, list) or len(results) != len(packages):
        raise ValueError("incomplete OSV vulnerability response")
    findings = []
    for (name, version), result in zip(packages, results):
        if not isinstance(result, dict) or "next_page_token" in result:
            raise ValueError("incomplete OSV vulnerability query")
        vulns = result.get("vulns", [])
        if not isinstance(vulns, list):
            raise ValueError("invalid OSV vulnerability record")
        for item in vulns:
            if not isinstance(item, dict) or not isinstance(item.get("id"), str):
                raise ValueError("invalid OSV vulnerability ID")
            findings.append(f"{name}@{version}: {item['id']}")
    if findings:
        raise ValueError("known runtime vulnerabilities: " + "; ".join(sorted(set(findings))))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tree", type=Path)
    parser.add_argument("--osv", action="store_true", help="Require a clean OSV runtime vulnerability scan")
    args = parser.parse_args()
    try:
        graph = json.loads(args.tree.read_text(encoding="utf-8"))
        validate(graph)
        if args.osv:
            packages = runtime_dependencies(graph)
            check_vulnerabilities(packages, query_osv(packages))
    except (ValueError, OSError) as exc:
        raise SystemExit(f"Maven runtime dependency verification failed: {exc}") from exc
    print("Maven runtime dependency policy and vulnerability checks passed")


if __name__ == "__main__":
    main()
