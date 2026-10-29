#!/usr/bin/env python3
"""Run actual standalone and unpacked release executables on a native runner."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import zipfile

PROVIDERS = ("npm", "sdkman", "homebrew", "winget")
OS_NAMES = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}
ARCH_NAMES = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}


class SmokeError(RuntimeError):
    pass


def digest(data):
    return hashlib.sha256(data).hexdigest()


def strict_json(raw):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise SmokeError("duplicate JSON field")
            result[key] = value
        return result
    return json.loads(raw, object_pairs_hook=unique)


def read_archive(path, expected, os_name):
    result = {}
    if os_name == "windows":
        with zipfile.ZipFile(path) as archive:
            for entry in archive.infolist():
                name = entry.filename
                if (name not in expected or name in result or entry.is_dir() or
                        entry.file_size <= 0 or entry.file_size > (128 << 20)):
                    raise SmokeError(f"unsafe ZIP entry: {name}")
                result[name] = archive.read(entry)
    else:
        with tarfile.open(path, "r:gz") as archive:
            for entry in archive:
                name = entry.name
                if (name not in expected or name in result or not entry.isfile() or
                        entry.size <= 0 or entry.size > (128 << 20)):
                    raise SmokeError(f"unsafe tar entry: {name}")
                handle = archive.extractfile(entry)
                if handle is None:
                    raise SmokeError(f"missing tar entry bytes: {name}")
                result[name] = handle.read((128 << 20) + 1)
    if set(result) != expected:
        raise SmokeError("native archive missing required executables")
    return result


def execute(install, version, commit, suffix):
    cli = install / "bin" / ("distroplane" + suffix)
    result = subprocess.run([str(cli), "version", "--json"],
                            capture_output=True, text=True, check=True, timeout=20)
    data = strict_json(result.stdout)
    if data.get("version") != version or data.get("commit") != commit:
        raise SmokeError("native CLI reports wrong version or source commit")
    for provider in PROVIDERS:
        name = "distroplane-provider-" + provider
        request = {"protocolVersion": "1", "requestId": "native-release-smoke",
                   "operation": "describe", "payload": {}}
        result = subprocess.run([str(install / "libexec" / (name + suffix))],
                                input=json.dumps(request), capture_output=True,
                                text=True, check=True, timeout=20)
        data = strict_json(result.stdout)
        identity = data.get("payload", {}).get("provider", {})
        if (data.get("status") != "ok" or data.get("requestId") != "native-release-smoke" or
                identity.get("name") != provider or identity.get("version") != version):
            raise SmokeError("native provider identity mismatch: " + provider)


def run(directory, version, commit, expected_os=None, expected_arch=None):
    os_name = OS_NAMES.get(platform.system())
    arch = ARCH_NAMES.get(platform.machine().lower())
    if os_name is None or arch is None:
        raise SmokeError("unsupported native host: " + platform.platform())
    if (expected_os and os_name != expected_os) or (expected_arch and arch != expected_arch):
        raise SmokeError("runner architecture does not match native test target")
    suffix = ".exe" if os_name == "windows" else ""
    archive_name = f"distroplane_{version}_{os_name}_{arch}" + (
        ".zip" if os_name == "windows" else ".tar.gz")
    names = {"bin/distroplane" + suffix} | {
        "libexec/distroplane-provider-" + p + suffix for p in PROVIDERS}
    expected = names | {"PACKAGE-METADATA.json"}
    archive = read_archive(directory / archive_name, expected, os_name)
    meta = strict_json(archive["PACKAGE-METADATA.json"])
    if (meta.get("version") != version or meta.get("commit") != commit or
            meta.get("arch") != arch or meta.get("os") != os_name or
            meta.get("cgoEnabled") is not False):
        raise SmokeError("native archive metadata identity mismatch")
    files = meta.get("files")
    if (not isinstance(files, list) or len(files) != len(names) or
            {x.get("path") for x in files if isinstance(x, dict)} != names):
        raise SmokeError("archive file inventory invalid")
    for record in files:
        name = record["path"]
        if (record.get("sha256") != digest(archive[name]) or
                record.get("size") != len(archive[name])):
            raise SmokeError("archive executable hash mismatch: " + name)
        asset = Path(name).name.replace(suffix, "") + f"_{version}_{os_name}_{arch}{suffix}"
        if (directory / asset).read_bytes() != archive[name]:
            raise SmokeError("standalone and archive executable differ: " + name)
    with tempfile.TemporaryDirectory(prefix="distroplane native é space ") as temp:
        install = Path(temp)
        for name in names:
            path = install / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(archive[name])
            if os_name != "windows":
                path.chmod(0o755)
        execute(install, version, commit, suffix)
        print(f"Native release execution passed: {os_name}/{arch} ({version})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--expected-os", choices=("linux", "darwin", "windows"))
    parser.add_argument("--expected-arch", choices=("amd64", "arm64"))
    args = parser.parse_args()
    try:
        run(args.directory, args.version, args.commit, args.expected_os, args.expected_arch)
    except (SmokeError, OSError, ValueError, subprocess.SubprocessError,
            tarfile.TarError, zipfile.BadZipFile) as error:
        parser.exit(1, f"native release smoke: {error}\n")


if __name__ == "__main__":
    main()
