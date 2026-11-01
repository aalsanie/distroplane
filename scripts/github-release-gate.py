#!/usr/bin/env python3
"""Verify a GitHub release draft by immutable release ID before publication."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

REPO = re.compile(r"^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")
TAG = re.compile(r"^v[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?$")


def gh(*args):
    result = subprocess.run(["gh", *args], text=True, capture_output=True,
                            check=False, timeout=90)
    if result.returncode:
        raise RuntimeError("GitHub API operation failed: " + result.stderr.strip()[-1000:])
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise ValueError("GitHub API returned invalid JSON") from exc


def api(repo, path, *, publish=False):
    command = ["api", f"repos/{repo}/{path}"]
    if publish:
        command.extend(["--method", "PATCH", "-F", "draft=false"])
    return gh(*command)


def find_release(repo, tag):
    matches = []
    for page in range(1, 51):
        releases = api(repo, f"releases?per_page=100&page={page}")
        if not isinstance(releases, list):
            raise ValueError("GitHub release listing was not an array")
        for release in releases:
            if not isinstance(release, dict):
                raise ValueError("malformed GitHub release listing")
            if release.get("tag_name") == tag:
                matches.append(release)
        if len(releases) < 100:
            break
    else:
        raise ValueError("release listing exceeded safe pagination bound")
    if len(matches) > 1:
        raise ValueError("duplicate GitHub releases for tag")
    return matches[0] if matches else None


def release_id(value):
    identifier = value.get("id")
    if type(identifier) is not int or identifier <= 0:
        raise ValueError("release has no valid immutable ID")
    return identifier


def check(repo, tag):
    if find_release(repo, tag) is not None:
        raise ValueError("release already exists (including drafts); inspect existing release ID")
    print("GitHub release tag is not already published or drafted")


def verify_upload(release, tag, directory):
    if release.get("tag_name") != tag or release.get("draft") is not True:
        raise ValueError("release identity changed or draft is already public")
    paths = list(directory.iterdir())
    if not paths or any(not path.is_file() or path.is_symlink() for path in paths):
        raise ValueError("staged release directory contains no assets or unsafe entries")
    expected = {path.name: path for path in paths}
    if len(expected) != len(paths):
        raise ValueError("duplicate staged asset names")
    assets = release.get("assets")
    if not isinstance(assets, list) or any(not isinstance(asset, dict) for asset in assets):
        raise ValueError("invalid uploaded release asset listing")
    actual = {asset.get("name"): asset for asset in assets}
    if len(actual) != len(assets) or set(actual) != set(expected):
        raise ValueError("draft release asset inventory differs from verified stage")
    for name, path in expected.items():
        asset = actual[name]
        with path.open("rb") as source:
            digest = "sha256:" + hashlib.file_digest(source, "sha256").hexdigest()
        if (asset.get("state") != "uploaded" or
                asset.get("size") != path.stat().st_size or
                asset.get("digest") != digest):
            raise ValueError("uploaded release asset integrity mismatch: " + name)


def publish(repo, tag, directory):
    listed = find_release(repo, tag)
    if listed is None:
        raise ValueError("draft release not found; publishing refused")
    identifier = release_id(listed)
    release = api(repo, f"releases/{identifier}")
    if release_id(release) != identifier:
        raise ValueError("release identity changed between listing and detail lookup")
    verify_upload(release, tag, directory)
    updated = api(repo, f"releases/{identifier}", publish=True)
    if (release_id(updated) != identifier or updated.get("tag_name") != tag or
            updated.get("draft") is not False or not updated.get("published_at")):
        raise ValueError("GitHub release publication was not confirmed")
    print(f"Published verified release {tag} (ID {identifier})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("check", "publish"))
    parser.add_argument("--repo", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--directory", type=Path)
    args = parser.parse_args()
    if not REPO.fullmatch(args.repo) or not TAG.fullmatch(args.tag):
        parser.error("invalid repository or stable/prerelease tag")
    try:
        if args.command == "check":
            if args.directory is not None:
                parser.error("check does not accept --directory")
            check(args.repo, args.tag)
        else:
            if args.directory is None or not args.directory.is_dir():
                parser.error("publish requires an existing --directory")
            publish(args.repo, args.tag, args.directory)
    except (ValueError, OSError, RuntimeError) as exc:
        parser.exit(1, f"release publication rejected: {exc}\n")


if __name__ == "__main__":
    main()
