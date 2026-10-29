#!/usr/bin/env python3
"""Validate a complete, immutable Distroplane release before publishing it."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import tempfile

ARCHES = ("amd64", "arm64")
OSES = ("darwin", "linux", "windows")
PROVIDERS = ("npm", "sdkman", "homebrew", "winget")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?\Z")
COMMIT = re.compile(r"[0-9a-f]{12}\Z")
MAX_ASSET = 512 << 20
MAX_METADATA = 1 << 20


class StageError(ValueError):
    pass


def strict_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise StageError(f"duplicate JSON property: {key}")
            result[key] = value
        return result
    try:
        return json.loads(raw, object_pairs_hook=unique)
    except (UnicodeError, ValueError) as error:
        raise StageError("invalid JSON document") from error


def checked_file(path, limit=MAX_ASSET):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_size <= 0 or info.st_size > limit:
        raise StageError(f"unsafe, empty or oversized asset: {path.name}")
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        while block := handle.read(1 << 20):
            digest.update(block)
    if path.lstat().st_size != info.st_size:
        raise StageError(f"asset changed while reading: {path.name}")
    return {"name": path.name, "size": info.st_size, "sha256": digest.hexdigest()}


def expected_assets(version):
    expected = set()
    for os_name in OSES:
        for arch in ARCHES:
            ext = ".zip" if os_name == "windows" else ".tar.gz"
            expected.add(f"distroplane_{version}_{os_name}_{arch}{ext}")
            for tool in ("distroplane",) + tuple(f"distroplane-provider-{p}" for p in PROVIDERS):
                suffix = ".exe" if os_name == "windows" else ""
                expected.add(f"{tool}_{version}_{os_name}_{arch}{suffix}")
    return expected


def stage_records(directory, version, commit, signing_mode):
    if not VERSION.fullmatch(version) or not COMMIT.fullmatch(commit):
        raise StageError("invalid staged release identity")
    stable = "-rc." not in version
    if signing_mode not in (("unsigned", "pfx") if stable else ("not-applicable",)):
        raise StageError("release signing policy does not match release type")
    raw = (directory / "RELEASE-METADATA.json").read_bytes()
    if len(raw) > MAX_METADATA:
        raise StageError("oversized release metadata")
    meta = strict_json(raw)
    if (not isinstance(meta, dict) or meta.get("schemaVersion") != "2" or
            meta.get("version") != version or meta.get("commit") != commit or
            meta.get("cgoEnabled") is not False):
        raise StageError("release metadata does not match tag and source commit")
    names = expected_assets(version)
    items = meta.get("assets")
    if not isinstance(items, list) or len(items) != len(names):
        raise StageError("incomplete base asset inventory")
    records = []
    for item in items:
        if (not isinstance(item, dict) or set(item) != {"name", "size", "sha256"} or
                item["name"] not in names or type(item["size"]) is not int or
                item["size"] <= 0 or not isinstance(item["sha256"], str) or
                not DIGEST.fullmatch(item["sha256"])):
            raise StageError("invalid base asset inventory entry")
        records.append(item)
    if [r["name"] for r in records] != sorted(names):
        raise StageError("base asset inventory has duplicates or is unsorted")
    checksum = "".join(f'{item["sha256"]}  {item["name"]}\n' for item in records)
    if (directory / "SHA256SUMS").read_bytes() != checksum.encode("ascii"):
        raise StageError("base checksums do not match release metadata")
    results = []
    for item in records:
        actual = checked_file(directory / item["name"])
        if actual != item:
            raise StageError(f"base asset integrity mismatch: {item['name']}")
        results.append(actual)
    results.append(checked_file(directory / "SHA256SUMS", MAX_METADATA))
    results.append(checked_file(directory / "RELEASE-METADATA.json", MAX_METADATA))
    if stable:
        msis = []
        for arch in ARCHES:
            path = directory / f"distroplane_{version}_windows_{arch}.msi"
            with path.open("rb") as handle:
                if handle.read(8) != bytes.fromhex("d0cf11e0a1b11ae1"):
                    raise StageError(f"invalid MSI container: {path.name}")
            msis.append(checked_file(path))
        content = "".join(f'{item["sha256"]}  {item["name"]}\n' for item in msis)
        if (directory / "MSI-SHA256SUMS").read_bytes() != content.encode("ascii"):
            raise StageError("MSI checksum inventory mismatch")
        results.extend(msis)
        results.append(checked_file(directory / "MSI-SHA256SUMS", MAX_METADATA))
    expected = {x["name"] for x in results}
    present = {p.name for p in directory.iterdir()}
    if present not in (expected, expected | {"RELEASE-STAGING.json"}):
        raise StageError(f"unexpected or missing staging files: {sorted(present ^ expected)}")
    return sorted(results, key=lambda x: x["name"])


def staging_document(directory, version, commit, signing_mode):
    return {"schemaVersion": "1", "version": version, "commit": commit,
            "signingMode": signing_mode,
            "assets": stage_records(directory, version, commit, signing_mode)}


def run(directory, version, commit, signing_mode, verify=False):
    directory = directory.resolve(strict=True)
    doc = staging_document(directory, version, commit, signing_mode)
    target = directory / "RELEASE-STAGING.json"
    if verify:
        checked_file(target, MAX_METADATA)
        if strict_json(target.read_bytes()) != doc:
            raise StageError("staging manifest differs from verified release assets")
        return
    if target.exists() or target.is_symlink():
        raise StageError("refusing to overwrite an existing release staging manifest")
    payload = (json.dumps(doc, sort_keys=True, indent=2) + "\n").encode("ascii")
    with tempfile.NamedTemporaryFile(dir=directory, prefix=".release-staging-", delete=False) as handle:
        temporary = Path(handle.name)
        try:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        except BaseException:
            temporary.unlink(missing_ok=True)
            raise
    try:
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("create", "verify"))
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True, help="12-character source commit")
    parser.add_argument("--signing-mode", required=True, choices=("unsigned", "pfx", "not-applicable"))
    args = parser.parse_args()
    try:
        run(args.directory, args.version, args.commit, args.signing_mode, args.mode == "verify")
    except (StageError, OSError) as error:
        parser.exit(1, f"release staging: {error}\n")


if __name__ == "__main__":
    main()
