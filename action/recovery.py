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
import re
import shutil
import stat
import struct
import sys
import tempfile
import zipfile
import zlib
from pathlib import Path, PurePosixPath

MAX_FILES = 10000
MAX_FILE_BYTES = 512 * 1024 * 1024
MAX_TOTAL_BYTES = 2 * 1024 * 1024 * 1024
MAX_MANIFEST = 1024 * 1024
MAX_PLAN = 16 * 1024 * 1024
MAX_ENTRY = 16 * 1024


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
        require(not unsafe_link(cursor), "recovery refuses symlinks and reparse points: " + str(cursor))
    require(stat.S_ISREG(path.stat().st_mode), "recovery requires a regular file: " + str(path))
    require(path.stat().st_size <= MAX_FILE_BYTES, "recovery file exceeds size limit: " + str(path))


def unsafe_link(path):
    info = path.lstat()
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, "st_file_attributes", 0) &
                                              getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0))


def safe_path(workspace, name):
    require(isinstance(name, str) and 0 < len(name) <= 1024, "invalid recovery entry name")
    parts = name.split("/")
    require(all(part not in (".", "..", "") and not part.endswith((".", " ")) for part in parts),
            "unsafe recovery entry path")
    require(not any(ord(c) < 32 or c in ':\\\\<>|?*"' for c in name), "unsafe recovery entry path")
    forbidden = {"credentials", "id_rsa", "id_ed25519"}
    require(not any(part.casefold() in forbidden or
                    (part.startswith(".") and part not in (".distroplane", ".catalog-required"))
                    or re.fullmatch(r"(?:con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?", part.casefold())
                    for part in parts), "private or unsafe recovery entry path")
    result, canonical = inside(workspace, workspace / Path(*PurePosixPath(name).parts))
    require(canonical == name, "noncanonical recovery entry path")
    return result


def unique_object(pairs):
    value = {}
    for key, item in pairs:
        require(key not in value, "duplicate recovery JSON key")
        value[key] = item
    return value


def decode_json(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def digest(value):
    return isinstance(value, str) and re.fullmatch(r"sha256:[0-9a-f]{64}", value) is not None


def plan_id(value):
    return isinstance(value, str) and re.fullmatch(r"sha256-[0-9a-f]{64}", value) is not None


def run_id(value):
    return isinstance(value, str) and 0 < len(value.encode("utf-8")) <= 256 and value.strip() == value and all(ord(c) >= 32 for c in value)


def first_event(stream, expected_plan, expected_run):
    header = stream.read(12)
    require(len(header) == 12 and header[:4] == b"DPJ1", "recovery journal has no durable run identity")
    size, checksum = struct.unpack(">II", header[4:])
    require(0 < size <= 1 << 20, "invalid recovery journal frame size")
    raw = stream.read(size)
    require(len(raw) == size and zlib.crc32(raw) == checksum, "corrupt recovery journal identity frame")
    event = decode_json(raw)
    require(isinstance(event, dict) and event.get("schemaVersion") == 2 and event.get("sequence") == 1 and
            event.get("type") == "RUN_STARTED" and event.get("planId") == expected_plan and
            event.get("runId") == expected_run, "recovery journal belongs to a different plan or run")


def inventory(workspace, manifest, names, open_file, metadata):
    """Derive the allowlist from durable records, never recursively copy a state directory."""
    required = set()
    checks = {}

    def relative(path):
        require(isinstance(path, str) and Path(path).is_absolute(), "recovery requires an original absolute path")
        _, name = inside(workspace, path)
        safe_path(workspace, name)
        return name

    def add(name):
        safe_path(workspace, name)
        require(name in names, "required recovery file is missing: " + name)
        required.add(name)
        return name

    def document(name, limit):
        add(name)
        with open_file(name) as stream:
            raw = stream.read(limit + 1)
        require(len(raw) <= limit, "recovery metadata exceeds limit: " + name)
        value = decode_json(raw)
        require(isinstance(value, dict), "invalid recovery metadata: " + name)
        return value

    config_name = add(manifest["config"])
    config = document(config_name, 4 << 20)
    require(isinstance(config.get("release"), dict), "invalid recovery configuration")
    root = (PurePosixPath(config_name).parent / ".distroplane").as_posix()
    add(root + "/.catalog-required")
    prefix = root + "/catalog/plans/"
    claims = root + "/catalog/runs/"
    selected_plan = manifest["planId"]
    selected_run = manifest["runId"]
    require(plan_id(selected_plan) and (selected_run == "" or run_id(selected_run)), "invalid recovery plan or run identity")
    found = False
    for entry_name in sorted(name for name in names if name.startswith(prefix)):
        require(re.fullmatch(r"[0-9a-f]{64}\.json", entry_name[len(prefix):]) is not None,
                "unexpected recovery catalog plan entry")
        entry = document(entry_name, MAX_ENTRY)
        pid = entry.get("planId")
        require(plan_id(pid) and entry.get("schemaVersion") == "1" and
                set(entry) == {"schemaVersion", "planId", "planPath"}, "invalid recovery catalog plan")
        key = hashlib.sha256(pid.encode()).hexdigest() + ".json"
        require(entry_name == prefix + key, "recovery catalog plan key mismatch")
        plan_name = relative(entry["planPath"])
        plan = document(plan_name, MAX_PLAN)
        require(plan.get("planId") == pid and plan.get("schemaVersion") in ("1", "2"),
                "recovery catalog references a different plan")
        claim = None
        for claim_name in (claims + key, claims + key + ".backup"):
            if claim_name not in names:
                continue
            record = document(claim_name, MAX_ENTRY)
            require(set(record) == {"schemaVersion", "planId", "planPath", "runId", "journalPath"} and
                    record.get("schemaVersion") == "1" and record.get("planId") == pid and
                    record.get("planPath") == entry["planPath"] and run_id(record.get("runId")),
                    "invalid recovery run claim")
            require(claim is None or claim == record, "conflicting recovery run claims")
            claim = record
        if claim is not None:
            journal_name = relative(claim["journalPath"])
            if journal_name in names:
                add(journal_name)
                with open_file(journal_name) as stream:
                    first_event(stream, pid, claim["runId"])
            else:
                require(pid != selected_plan, "cannot upload recovery without the original journal")
        if pid != selected_plan:
            continue
        require(manifest["plan"] == plan_name, "recovery selection does not match the original plan")
        if selected_run:
            require(claim is not None and claim["runId"] == selected_run and manifest["journal"] == journal_name,
                    "recovery selection does not match the original plan and run")
        else:
            require(claim is None and not manifest["journal"], "reviewed plan already has a publication claim")
        found = True
        artifacts = plan.get("release", {}).get("artifacts")
        require(isinstance(artifacts, list) and artifacts, "recovery plan has no original artifacts")
        by_artifact = {}
        for artifact in artifacts:
            require(isinstance(artifact, dict) and digest(artifact.get("digest")) and
                    type(artifact.get("size")) is int and artifact["size"] >= 0,
                    "invalid recovery artifact identity")
            name = add(relative(artifact["source"]))
            checks[name] = (artifact["digest"][7:], artifact["size"])
            by_artifact[artifact["name"]] = checks[name]
        # npm reconciliation reads packagePath, which can be distinct from source.
        for operation in plan.get("operations", []):
            if operation.get("provider", {}).get("name") == "npm":
                payload = operation.get("providerPayload", {})
                expected = by_artifact.get(payload.get("artifact"))
                require(expected is not None and payload.get("sha256") == "sha256:" + expected[0] and
                        payload.get("size") == expected[1], "npm recovery archive does not match the planned artifact")
                name = add(relative(payload["packagePath"]))
                require(name not in checks or checks[name] == expected, "conflicting npm archive identity")
                checks[name] = expected
        for target in plan.get("targets", []):
            provider = target["provider"]
            name, version, value = provider["name"], provider["version"], provider["digest"]
            require(isinstance(name, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", name) and digest(value),
                    "invalid retained provider identity")
            directory = root + "/providers/" + name + "/" + value[7:]
            executable = add(directory + "/distroplane-provider-" + name +
                             (".exe" if manifest["os"] == "Windows" else ""))
            receipt = document(directory + "/binding.json", MAX_ENTRY)
            require(set(receipt) == {"schemaVersion", "name", "version", "digest", "trusted"} and
                    receipt["schemaVersion"] == "1" and receipt["name"] == name and
                    receipt["version"] == version and receipt["digest"] == value and
                    type(receipt["trusted"]) is bool, "retained provider receipt differs from the original plan")
            checks[executable] = (value[7:], None)
            if not selected_run:
                for source in explicit_provider_sources(config, name):
                    _, source_name = inside(workspace, safe_path(workspace, config_name).parent / source)
                    add(source_name)
                    checks[source_name] = (value[7:], None)
            if os.name != "nt" and metadata is not None:
                require(metadata[executable]["mode"] & 0o111 and
                        metadata[directory + "/binding.json"]["mode"] & 0o077 == 0,
                        "unsafe retained provider permissions")
    require(found, "original recovery plan and run were not found in the catalog")
    if manifest["binary"]:
        add(manifest["binary"])
    if metadata is not None:
        for name, (expected, size) in checks.items():
            item = metadata[name]
            require(item["sha256"] == expected and (size is None or item["size"] == size),
                    "original artifact or retained provider bytes differ from plan: " + name)
    return required


def explicit_provider_sources(config, name):
    targets = config.get("targets", [])
    if not isinstance(targets, list):
        return []
    return [target["provider"]["executable"] for target in targets
            if isinstance(target, dict) and isinstance(target.get("provider"), dict)
            and target["provider"].get("name") == name and target["provider"].get("executable")]


def source_names(workspace, manifest):
    """Only enumerate catalog records and their exact referenced paths."""
    config = safe_path(workspace, manifest["config"])
    root = config.parent / ".distroplane"
    names = set()

    def include(path, optional=False):
        path, name = inside(workspace, path)
        safe_path(workspace, name)
        if optional and not path.exists() and not path.is_symlink():
            return
        try:
            regular(path, workspace)
        except FileNotFoundError as exc:
            raise RecoveryError("required recovery input is absent: " + name) from exc
        names.add(name)
        require(len(names) <= MAX_FILES, "recovery file count exceeds limit")

    include(config)
    include(root / ".catalog-required")
    for folder in (root / "catalog/plans", root / "catalog/runs"):
        require(folder.is_dir() and not unsafe_link(folder), "required recovery catalog directory is absent or unsafe")
        for path in folder.iterdir():
            if path.name.startswith(".entry-"):
                continue
            include(path)
            require(re.fullmatch(r"[0-9a-f]{64}\.json(?:\.backup)?", path.name) is not None,
                    "unexpected recovery catalog file")
            with path.open("rb") as stream:
                raw = stream.read(MAX_ENTRY + 1)
            require(len(raw) <= MAX_ENTRY, "oversized recovery catalog entry")
            record = decode_json(raw)
            include(record["planPath"])
            if "journalPath" in record:
                include(record["journalPath"], optional=True)
    plan = safe_path(workspace, manifest["plan"])
    include(plan)
    if manifest["journal"]:
        include(safe_path(workspace, manifest["journal"]))
    with plan.open("rb") as stream:
        raw = stream.read(MAX_PLAN + 1)
    require(len(raw) <= MAX_PLAN, "recovery plan exceeds size limit")
    doc = decode_json(raw)
    for artifact in doc["release"]["artifacts"]:
        include(artifact["source"])
    for operation in doc.get("operations", []):
        if operation.get("provider", {}).get("name") == "npm":
            include(operation["providerPayload"]["packagePath"])
    with config.open("rb") as stream:
        configuration = decode_json(stream.read((4 << 20) + 1))
    for target in doc["targets"]:
        provider = target["provider"]
        name, value = provider["name"], provider["digest"]
        require(isinstance(name, str) and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}", name) and digest(value),
                "invalid retained provider identity")
        directory = root / "providers" / name / value[7:]
        include(directory / ("distroplane-provider-" + name + (".exe" if manifest["os"] == "Windows" else "")))
        include(directory / "binding.json")
        if not manifest["runId"]:
            for source in explicit_provider_sources(configuration, name):
                include(config.parent / source)
    if manifest["binary"]:
        include(safe_path(workspace, manifest["binary"]))
    return names


def credential_values(config):
    names = {"NPM_TOKEN", "HOMEBREW_GITHUB_TOKEN", "WINGET_GITHUB_TOKEN",
             "SDKMAN_VENDOR_EMAIL", "SDKMAN_VENDOR_PASSWORD", "SDKMAN_CONSUMER_KEY",
             "SDKMAN_CONSUMER_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"}
    for mapping in os.environ.get("RECOVERY_CREDENTIAL_MAPPINGS", "").splitlines():
        _, sep, name = mapping.strip().partition("=")
        if sep and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
            names.add(name)
    with config.open("rb") as stream:
        data = decode_json(stream.read((4 << 20) + 1))
    sources = data.get("credentialSources", {})
    if isinstance(sources, dict):
        for name in sources.values():
            if isinstance(name, str) and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
                names.add(name)
    # Values are never logged or included in the manifest.
    return {os.environ[name].encode() for name in names if os.environ.get(name)}


def create(args):
    workspace = Path(args.workspace).resolve(strict=True)
    require(args.plan and plan_id(args.plan_id) and
            ((args.journal and run_id(args.run_id)) or (not args.journal and args.run_id == "")),
            "recovery requires the original plan and matching journal/run identity, or an unclaimed reviewed plan")
    manifest = {"schemaVersion": "2", "os": identity()[0], "arch": identity()[1],
                "workspace": str(workspace), "config": inside(workspace, args.config)[1],
                "plan": inside(workspace, args.plan)[1], "journal": inside(workspace, args.journal)[1] if args.journal else "",
                "planId": args.plan_id, "runId": args.run_id,
                "binary": inside(workspace, args.binary)[1] if args.binary else "", "files": []}
    names = source_names(workspace, manifest)
    secrets = credential_values(safe_path(workspace, manifest["config"]))
    required = inventory(workspace, manifest, names, lambda name: safe_path(workspace, name).open("rb"), None)
    require(required == names, "unexpected recovery catalog content")
    require(len({name.casefold() for name in names}) == len(names), "colliding recovery file paths")
    output = Path(args.output).absolute()
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=output.parent, prefix=".recovery-", delete=False) as temporary:
        temp_name = temporary.name
    try:
        total = 0
        with zipfile.ZipFile(temp_name, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6,
                             allowZip64=True) as archive:
            for name in sorted(names):
                path = safe_path(workspace, name)
                regular(path, workspace)
                before = path.stat()
                mode = 0o600 if os.name == "nt" else stat.S_IMODE(before.st_mode)
                require(mode <= 0o777 and not mode & 0o022, "unsafe recovery permissions: " + name)
                h = hashlib.sha256()
                size = 0
                tail = b""
                overlap = max((len(value) - 1 for value in secrets), default=0)
                with path.open("rb") as source, archive.open("files/" + name, "w", force_zip64=True) as target:
                    while chunk := source.read(1 << 20):
                        size += len(chunk)
                        total += len(chunk)
                        require(size <= MAX_FILE_BYTES and total <= MAX_TOTAL_BYTES, "recovery exceeds size limit")
                        require(not any(value in tail + chunk for value in secrets), "recovery input contains a mapped credential value")
                        tail = (tail + chunk)[-overlap:] if overlap else b""
                        h.update(chunk)
                        target.write(chunk)
                after = path.stat()
                require(size == before.st_size == after.st_size and before.st_mtime_ns == after.st_mtime_ns,
                        "recovery input changed during capture: " + name)
                manifest["files"].append({"path": name, "sha256": h.hexdigest(), "size": size, "mode": mode})
            encoded = (json.dumps(manifest, separators=(",", ":"), sort_keys=True) + "\n").encode()
            require(not any(value in encoded for value in secrets), "recovery manifest contains a mapped credential value")
            require(len(encoded) <= MAX_MANIFEST, "recovery manifest exceeds limit")
            archive.writestr("manifest.json", encoded)
        with zipfile.ZipFile(temp_name) as archive:
            metadata = {item["path"]: item for item in manifest["files"]}
            required = inventory(workspace, manifest, names, lambda name: archive.open("files/" + name), metadata)
            require(required == names, "recovery inputs changed during capture")
        os.replace(temp_name, output)
    finally:
        if os.path.exists(temp_name):
            os.unlink(temp_name)
    print(json.dumps({"path": str(output), "files": len(names)}, separators=(",", ":")))


def file_digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        while chunk := stream.read(1 << 20):
            h.update(chunk)
    return h.digest()


def restore(args):
    workspace = Path(args.workspace).resolve(strict=True)
    source = Path(args.bundle).absolute()
    require(source.is_file() and not unsafe_link(source), "recovery bundle is missing or unsafe")
    with zipfile.ZipFile(source, "r") as archive:
        entries = archive.infolist()
        require(1 < len(entries) <= MAX_FILES + 1, "invalid recovery archive member count")
        names = [entry.filename for entry in entries]
        require(len(set(names)) == len(names) and "manifest.json" in names, "duplicate or missing manifest")
        require(all(not entry.is_dir() and not entry.flag_bits & 1 and
                    stat.S_IFMT(entry.external_attr >> 16) in (0, stat.S_IFREG) for entry in entries),
                "unsafe recovery archive member type")
        name_set = set(names)
        info = archive.getinfo("manifest.json")
        require(info.file_size <= MAX_MANIFEST, "recovery manifest is oversized")
        manifest = decode_json(archive.read("manifest.json"))
        require(isinstance(manifest, dict) and set(manifest) == {"schemaVersion", "os", "arch", "workspace", "config", "plan", "journal", "planId", "runId", "binary", "files"},
                "unsupported recovery manifest")
        require(manifest["schemaVersion"] == "2", "unsupported recovery bundle version; preserve original files and export with compatible tools")
        require((manifest["os"], manifest["arch"]) == identity(),
                "recovery runner OS or architecture differs from the original")
        require(manifest["workspace"] == str(workspace),
                "recovery workspace changed; original absolute plan paths cannot be relocated")
        require(plan_id(args.plan_id) and (args.run_id == "" or run_id(args.run_id)) and manifest["planId"] == args.plan_id and
                manifest["runId"] == args.run_id and manifest["config"] == inside(workspace, args.config)[1],
                "recovery bundle does not match the expected configuration, plan and run")
        files = manifest["files"]
        require(isinstance(files, list) and files and len(files) <= MAX_FILES, "invalid recovery inventory")
        declared = set()
        folded = set()
        validated = []
        total = 0
        for item in files:
            require(isinstance(item, dict) and set(item) == {"path", "sha256", "size", "mode"},
                    "invalid recovery file record")
            path = safe_path(workspace, item["path"])
            relative = item["path"]
            require(relative.casefold() not in folded, "duplicate or colliding recovery path")
            folded.add(relative.casefold())
            declared.add(relative)
            name = "files/" + relative
            require(name in name_set, "required recovery file is missing")
            zinfo = archive.getinfo(name)
            require(not zinfo.is_dir() and type(item["size"]) is int and 0 <= item["size"] <= MAX_FILE_BYTES and zinfo.file_size == item["size"],
                    "recovery file size mismatch")
            require(isinstance(item["sha256"], str) and len(item["sha256"]) == 64 and
                    all(c in "0123456789abcdef" for c in item["sha256"]), "invalid recovery digest")
            require(type(item["mode"]) is int and 0 <= item["mode"] <= 0o777 and
                    not item["mode"] & 0o022, "unsafe recovery permissions")
            total += item["size"]
            require(total <= MAX_TOTAL_BYTES, "recovery exceeds total size limit")
            validated.append((relative, path, item, name))
        for name in folded:
            parts = name.split("/")
            require(not any("/".join(parts[:i]) in folded for i in range(1, len(parts))),
                    "recovery file conflicts with a parent path")
        require(name_set == {"manifest.json"} | {"files/" + name for name in declared},
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
            files_by_name = {relative: Path(stage) / str(i) for i, (relative, _, _, _) in enumerate(validated)}
            metadata_by_name = {item["path"]: item for item in files}
            require(inventory(workspace, manifest, declared, lambda name: files_by_name[name].open("rb"),
                              metadata_by_name) == declared, "unexpected recovery file outside the original run inventory")
            # Refuse a merge into another catalog, including a newer recovery snapshot.
            root = safe_path(workspace, manifest["config"]).parent / ".distroplane"
            for folder in (root / "catalog/plans", root / "catalog/runs"):
                if folder.exists() or folder.is_symlink():
                    require(folder.is_dir() and not unsafe_link(folder), "unsafe existing recovery catalog")
                    for child in folder.iterdir():
                        _, name = inside(workspace, child)
                        require(name in declared, "existing catalog differs from the recovery bundle: " + name)
            # Check existing files and path components before changing any path.
            for destination, source_file, mode in staged:
                cursor = workspace
                for part in destination.relative_to(workspace).parts[:-1]:
                    cursor = cursor / part
                    if cursor.exists() or cursor.is_symlink():
                        require(cursor.is_dir() and not unsafe_link(cursor), "unsafe recovery directory: " + str(cursor))
                        if (os.name != "nt" and cursor.is_relative_to(root)
                                and cursor.relative_to(root).parts[:1] in (("catalog",), ("providers",))):
                            require(stat.S_IMODE(cursor.stat().st_mode) & 0o077 == 0,
                                    "existing recovery state directory is not private: " + str(cursor))
                if destination.exists() or destination.is_symlink():
                    regular(destination, workspace)
                    require(destination.stat().st_size == source_file.stat().st_size and file_digest(destination) == file_digest(source_file),
                            "existing workspace file conflicts with recovery: " + str(destination))
                    if os.name != "nt":
                        require(stat.S_IMODE(destination.stat().st_mode) == mode, "existing recovery file permissions differ: " + str(destination))
            for destination, source_file, mode in staged:
                if destination.exists():
                    continue
                parent = destination.parent
                cursor = workspace
                private = False
                for part in parent.relative_to(workspace).parts:
                    cursor = cursor / part
                    private = private or part == ".distroplane"
                    if not cursor.exists():
                        cursor.mkdir(mode=0o700 if private else 0o755)
                    require(cursor.is_dir() and not unsafe_link(cursor), "unsafe recovery directory: " + str(cursor))
                with tempfile.NamedTemporaryFile(dir=parent, prefix=".recovery-", delete=False) as temporary:
                    temp_path = Path(temporary.name)
                try:
                    shutil.copyfile(source_file, temp_path)
                    temp_path.chmod(mode)
                    os.link(temp_path, destination)
                finally:
                    temp_path.unlink(missing_ok=True)
            # An unclaimed plan has an empty runs directory, not a missing catalog.
            (root / "catalog/runs").mkdir(mode=0o700, exist_ok=True)
    print(json.dumps({"restored": len(validated), "config": str(workspace / manifest["config"])}, separators=(",", ":")))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("create", "restore"):
        p = commands.add_parser(command)
        p.add_argument("--workspace", required=True)
        p.add_argument("--config", required=True)
        p.add_argument("--plan-id", required=True)
        p.add_argument("--run-id", required=True)
        if command == "create":
            p.add_argument("--plan", default="")
            p.add_argument("--journal", default="")
            p.add_argument("--binary", default="")
            p.add_argument("--output", required=True)
        else:
            p.add_argument("--bundle", required=True)
    args = parser.parse_args()
    try:
        (create if args.command == "create" else restore)(args)
    except (RecoveryError, OSError, ValueError, KeyError, TypeError, AttributeError, UnicodeError, RuntimeError, zipfile.BadZipFile) as exc:
        print("Distroplane recovery: " + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())