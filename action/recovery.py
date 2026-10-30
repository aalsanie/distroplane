#!/usr/bin/env python3
"""Verified, exact-path GitHub Actions recovery handoff for Distroplane.

The bundle is a same-workspace, same-OS/architecture transfer, not a portable
plan migration. Only explicit release inputs and private run state are captured.
"""
import argparse
import hashlib
import json
import os
import platform
import shutil
import stat
import sys
import tempfile
import zipfile
from pathlib import Path, PurePosixPath

MAX_FILES = 10000
MAX_FILE_BYTES = 512 * 1024 * 1024
MAX_TOTAL_BYTES = 2 * 1024 * 1024 * 1024
MAX_MANIFEST = 1024 * 1024


class RecoveryError(Exception):
    pass


def require(ok, explanation):
    if not ok:
        raise RecoveryError(explanation)


def identity():
    return (os.environ.get("RUNNER_OS", platform.system()),
            os.environ.get("RUNNER_ARCH", platform.machine()))


def inside(workspace, path):
    value = Path(path)
    if not value.is_absolute():
        value = workspace / value
    value = Path(os.path.abspath(value))
    try:
        relative = value.relative_to(workspace)
    except ValueError as exc:
        raise RecoveryError("recovery cannot capture or restore paths outside GITHUB_WORKSPACE: " + str(value)) from exc
    require(relative.parts and ".." not in relative.parts, "recovery path is not a workspace file")
    return value, relative.as_posix()


def regular(path, workspace):
    require(path.is_relative_to(workspace), "recovery file escapes workspace")
    cursor = workspace
    for part in path.relative_to(workspace).parts:
        cursor = cursor / part
        kind = cursor.lstat().st_mode
        require(not stat.S_ISLNK(kind), "recovery refuses symlinks: " + str(cursor))
    require(stat.S_ISREG(path.stat().st_mode), "recovery requires a regular file: " + str(path))
    require(path.stat().st_size <= MAX_FILE_BYTES, "recovery file exceeds size limit: " + str(path))


def walk_files(path, workspace):
    require(not path.is_symlink(), "recovery refuses a symlink directory: " + str(path))
    if path.is_file():
        regular(path, workspace)
        yield path
    elif path.is_dir():
        for root, directories, filenames in os.walk(path, followlinks=False):
            directories.sort()
            filenames.sort()
            for directory in directories:
                require(not (Path(root) / directory).is_symlink(), "recovery refuses symlink directories")
            for filename in filenames:
                item = Path(root) / filename
                regular(item, workspace)
                yield item
    else:
        raise RecoveryError("required recovery input is absent: " + str(path))


def safe_path(workspace, name):
    require(isinstance(name, str) and len(name) <= 1024 and name != "", "invalid recovery entry name")
    value = PurePosixPath(name)
    require(not value.is_absolute() and all(part not in (".", "..", "") for part in name.split("/")),
            "unsafe recovery entry path")
    require(not (":" in name or "\\" in name or "\x00" in name), "unsafe recovery entry path")
    result, canonical = inside(workspace, workspace / Path(*value.parts))
    require(canonical == name, "noncanonical recovery entry path")
    return result


def declared_inputs(workspace, config_path):
    try:
        data = json.loads(config_path.read_text("utf-8"))
    except (OSError, ValueError) as exc:
        raise RecoveryError("invalid recovery configuration") from exc
    require(isinstance(data, dict) and isinstance(data.get("release"), dict), "invalid release configuration")
    artifacts = data["release"].get("artifacts")
    if isinstance(artifacts, dict):
        sources = [item.get("source") for item in artifacts.values() if isinstance(item, dict)]
        require(len(sources) == len(artifacts), "invalid artifact map")
    elif isinstance(artifacts, list):
        sources = [item.get("source") for item in artifacts if isinstance(item, dict)]
        require(len(sources) == len(artifacts), "invalid artifact array")
    else:
        raise RecoveryError("release has no artifact sources")
    require(sources and all(isinstance(value, str) and value for value in sources), "invalid artifact source")
    selected = []
    for source in sources:
        path, _ = inside(workspace, config_path.parent / source)
        selected.append(path)
    targets = data.get("targets", [])
    if isinstance(targets, list):
        for target in targets:
            provider = target.get("provider", {}) if isinstance(target, dict) else {}
            if isinstance(provider, dict) and provider.get("executable"):
                executable, _ = inside(workspace, config_path.parent / provider["executable"])
                selected.append(executable)
    return selected


def create(args):
    workspace = Path(args.workspace).resolve(strict=True)
    config, config_relative = inside(workspace, args.config)
    selected = [config, config.parent / ".distroplane"] + declared_inputs(workspace, config)
    for optional in (args.plan, args.journal, args.binary):
        if optional:
            candidate, _ = inside(workspace, optional)
            if optional == args.journal and not candidate.exists() and not candidate.is_symlink():
                raise RecoveryError("cannot upload recovery without the journal: " + str(candidate))
            selected.append(candidate)
    all_files = {}
    for item in selected:
        for actual in walk_files(item, workspace):
            _, relative = inside(workspace, actual)
            all_files[relative] = actual
    require(0 < len(all_files) <= MAX_FILES, "recovery file count exceeds limit")
    total = sum(item.stat().st_size for item in all_files.values())
    require(total <= MAX_TOTAL_BYTES, "recovery exceeds total size limit")
    manifest = {"schemaVersion": "1", "os": identity()[0], "arch": identity()[1],
                "workspace": str(workspace), "config": config_relative, "files": []}
    output = Path(args.output).absolute()
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=output.parent, prefix=".recovery-", delete=False) as temporary:
        temp_name = temporary.name
    try:
        with zipfile.ZipFile(temp_name, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6,
                             allowZip64=True) as archive:
            for name, path in sorted(all_files.items()):
                raw = path.read_bytes()
                entry = {"path": name, "sha256": hashlib.sha256(raw).hexdigest(),
                         "size": len(raw), "mode": 0o600 if os.name == "nt" else stat.S_IMODE(path.stat().st_mode)}
                manifest["files"].append(entry)
                archive.writestr("files/" + name, raw)
            encoded = (json.dumps(manifest, separators=(",", ":"), sort_keys=True) + "\n").encode()
            require(len(encoded) <= MAX_MANIFEST, "recovery manifest exceeds limit")
            archive.writestr("manifest.json", encoded)
        os.replace(temp_name, output)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)
    print(json.dumps({"path": str(output), "files": len(all_files)}, separators=(",", ":")))


def restore(args):
    workspace = Path(args.workspace).resolve(strict=True)
    source = Path(args.bundle).absolute()
    require(source.is_file() and not source.is_symlink(), "recovery bundle is missing or unsafe")
    with zipfile.ZipFile(source, "r") as archive:
        entries = archive.infolist()
        require(1 < len(entries) <= MAX_FILES + 1, "invalid recovery archive member count")
        names = [entry.filename for entry in entries]
        require(len(set(names)) == len(names) and "manifest.json" in names, "duplicate or missing manifest")
        info = archive.getinfo("manifest.json")
        require(info.file_size <= MAX_MANIFEST, "recovery manifest is oversized")
        manifest = json.loads(archive.read("manifest.json"))
        require(isinstance(manifest, dict) and set(manifest) == {"schemaVersion", "os", "arch", "workspace", "config", "files"},
                "unsupported recovery manifest")
        require(manifest["schemaVersion"] == "1" and (manifest["os"], manifest["arch"]) == identity(),
                "recovery runner OS or architecture differs from the original")
        require(manifest["workspace"] == str(workspace),
                "recovery workspace changed; original absolute plan paths cannot be relocated")
        files = manifest["files"]
        require(isinstance(files, list) and files and len(files) <= MAX_FILES, "invalid recovery inventory")
        declared = set()
        validated = []
        total = 0
        for item in files:
            require(isinstance(item, dict) and set(item) == {"path", "sha256", "size", "mode"},
                    "invalid recovery file record")
            path = safe_path(workspace, item["path"])
            relative = item["path"]
            require(relative not in declared, "duplicate recovery path")
            declared.add(relative)
            name = "files/" + relative
            require(name in names, "required recovery file is missing")
            zinfo = archive.getinfo(name)
            require(not zinfo.is_dir() and 0 <= item["size"] <= MAX_FILE_BYTES and zinfo.file_size == item["size"],
                    "recovery file size mismatch")
            require(isinstance(item["sha256"], str) and len(item["sha256"]) == 64 and
                    all(c in "0123456789abcdef" for c in item["sha256"]), "invalid recovery digest")
            require(type(item["mode"]) is int and 0 <= item["mode"] <= 0o777 and
                    not item["mode"] & 0o022, "unsafe recovery permissions")
            total += item["size"]
            require(total <= MAX_TOTAL_BYTES, "recovery exceeds total size limit")
            validated.append((relative, path, item, name))
        require(set(names) == {"manifest.json"} | {"files/" + name for name in declared},
                "unexpected recovery archive content")
        require(manifest["config"] in declared, "recovery configuration is absent")
        with tempfile.TemporaryDirectory(prefix="distroplane-recovery-") as stage:
            staged = []
            for i, (relative, destination, metadata, name) in enumerate(validated):
                target = Path(stage) / str(i)
                h = hashlib.sha256()
                written = 0
                with archive.open(name) as stream, target.open("wb") as output:
                    while True:
                        chunk = stream.read(1 << 20)
                        if not chunk:
                            break
                        written += len(chunk)
                        require(written <= metadata["size"], "expanded recovery file exceeds declared size")
                        h.update(chunk)
                        output.write(chunk)
                require(written == metadata["size"] and h.hexdigest() == metadata["sha256"],
                        "recovery digest mismatch: " + relative)
                staged.append((destination, target, metadata["mode"]))
            # Check existing files and path components before changing any path.
            for destination, source_file, _ in staged:
                cursor = workspace
                for part in destination.relative_to(workspace).parts[:-1]:
                    cursor = cursor / part
                    if cursor.exists() or cursor.is_symlink():
                        require(cursor.is_dir() and not cursor.is_symlink(), "unsafe recovery directory: " + str(cursor))
                if destination.exists() or destination.is_symlink():
                    regular(destination, workspace)
                    require(destination.read_bytes() == source_file.read_bytes(),
                            "existing workspace file conflicts with recovery: " + str(destination))
            for destination, source_file, mode in staged:
                if destination.exists():
                    continue
                parent = destination.parent
                parent.mkdir(parents=True, exist_ok=True)
                if ".distroplane" in destination.relative_to(workspace).parts:
                    cursor = parent
                    while cursor != workspace and cursor != workspace.parent:
                        if cursor.name == ".distroplane" or ".distroplane" in cursor.relative_to(workspace).parts:
                            cursor.chmod(0o700)
                        cursor = cursor.parent
                with tempfile.NamedTemporaryFile(dir=parent, prefix=".recovery-", delete=False) as temporary:
                    temp_path = Path(temporary.name)
                try:
                    shutil.copyfile(source_file, temp_path)
                    temp_path.chmod(mode)
                    os.replace(temp_path, destination)
                finally:
                    temp_path.unlink(missing_ok=True)
    print(json.dumps({"restored": len(validated), "config": str(workspace / manifest["config"])}, separators=(",", ":")))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("create", "restore"):
        p = commands.add_parser(command)
        p.add_argument("--workspace", required=True)
        if command == "create":
            p.add_argument("--config", required=True)
            p.add_argument("--plan", default="")
            p.add_argument("--journal", default="")
            p.add_argument("--binary", default="")
            p.add_argument("--output", required=True)
        else:
            p.add_argument("--bundle", required=True)
    args = parser.parse_args()
    try:
        (create if args.command == "create" else restore)(args)
    except (RecoveryError, OSError, ValueError, KeyError, zipfile.BadZipFile) as exc:
        print("Distroplane recovery: " + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())