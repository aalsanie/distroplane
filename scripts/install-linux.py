#!/usr/bin/env python3
"""Install a verified, immutable Distroplane Linux release without Go or Homebrew."""
import argparse
import hashlib
import fcntl
import io
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path

PROVIDERS = ("homebrew", "npm", "sdkman", "winget")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+", re.ASCII)
DIGEST = re.compile(r"[0-9a-f]{64}", re.ASCII)
ARCHES = {"x86_64": "amd64", "aarch64": "arm64"}
MAX_ARCHIVE = 512 << 20
MAX_BINARY = 128 << 20
MAX_METADATA = 1 << 20
SOURCE = "https://github.com/aalsanie/distroplane/releases/download"
REDIRECT_HOSTS = frozenset(("github.com", "release-assets.githubusercontent.com",
                            "objects.githubusercontent.com"))


class InstallError(Exception):
    pass


def fail(message):
    raise InstallError(message)


def read_regular(path, maximum):
    if path.is_symlink() or not path.is_file():
        fail("missing or unsafe release file: " + str(path))
    if path.stat().st_size > maximum:
        fail("oversized release file: " + str(path))
    return path.read_bytes()


def strict_json(blob):
    def distinct(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                fail("duplicate JSON key: " + key)
            result[key] = value
        return result
    try:
        return json.loads(blob, object_pairs_hook=distinct)
    except (ValueError, UnicodeDecodeError) as exc:
        raise InstallError("invalid release JSON") from exc


def sha256(blob):
    return hashlib.sha256(blob).hexdigest()


def release_inventory(directory, version, archive_name):
    manifest = strict_json(read_regular(directory / "RELEASE-METADATA.json", MAX_METADATA))
    if (not isinstance(manifest, dict) or manifest.get("schemaVersion") != "2"
            or manifest.get("version") != version or manifest.get("cgoEnabled") is not False
            or not re.fullmatch(r"[0-9a-f]{12}", str(manifest.get("commit", "")))):
        fail("release metadata identity invalid")
    items = manifest.get("assets")
    if not isinstance(items, list) or len(items) != 36:
        fail("release metadata inventory incomplete")
    expected = set()
    for os_name in ("darwin", "linux", "windows"):
        for cpu in ("amd64", "arm64"):
            suffix = ".zip" if os_name == "windows" else ".tar.gz"
            expected.add(f"distroplane_{version}_{os_name}_{cpu}{suffix}")
            for executable in ("distroplane",) + tuple(
                    "distroplane-provider-" + p for p in PROVIDERS):
                extension = ".exe" if os_name == "windows" else ""
                expected.add(f"{executable}_{version}_{os_name}_{cpu}{extension}")
    indexed = {}
    for asset in items:
        if (not isinstance(asset, dict) or set(asset) != {"name", "sha256", "size"}
                or not isinstance(asset["name"], str) or "/" in asset["name"]
                or "\\" in asset["name"] or not DIGEST.fullmatch(str(asset["sha256"]))
                or type(asset["size"]) is not int or asset["size"] <= 0):
            fail("invalid release inventory entry")
        if asset["name"] in indexed:
            fail("duplicate release asset")
        indexed[asset["name"]] = asset
    if set(indexed) != expected:
        fail("release inventory contains unexpected or missing asset names")
    lines = "".join(f'{indexed[name]["sha256"]}  {name}\n' for name in sorted(indexed))
    if read_regular(directory / "SHA256SUMS", MAX_METADATA) != lines.encode("ascii"):
        fail("release checksums do not match metadata")
    if archive_name not in indexed:
        fail("selected Linux archive missing from release inventory")
    blob = read_regular(directory / archive_name, MAX_ARCHIVE)
    record = indexed[archive_name]
    if len(blob) != record["size"] or sha256(blob) != record["sha256"]:
        fail("Linux archive digest mismatch")
    return manifest["commit"], blob


class ReleaseRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        url = urllib.parse.urlparse(newurl)
        if url.scheme != "https" or url.hostname not in REDIRECT_HOSTS:
            fail("release download redirected to an untrusted origin")
        return super().redirect_request(request, fp, code, msg, headers, newurl)


def download(directory, version, name, maximum):
    url = f"{SOURCE}/v{version}/{name}"
    opener = urllib.request.build_opener(ReleaseRedirects)
    with opener.open(url, timeout=30) as response:
        if response.url and (urllib.parse.urlparse(response.url).hostname not in REDIRECT_HOSTS
                             or urllib.parse.urlparse(response.url).scheme != "https"):
            fail("download returned an untrusted origin")
        data = response.read(maximum + 1)
    if len(data) > maximum:
        fail("download exceeds size limit")
    (directory / name).write_bytes(data)


def verify_and_stage(blob, version, commit, arch, destination):
    expected = {"bin/distroplane"} | {f"libexec/distroplane-provider-{n}" for n in PROVIDERS}
    paths = expected | {"PACKAGE-METADATA.json"}
    found = {}
    try:
        with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
            for member in archive:
                name = member.name
                limit = MAX_METADATA if name == "PACKAGE-METADATA.json" else MAX_BINARY
                mode = 0o644 if name == "PACKAGE-METADATA.json" else 0o755
                if (name not in paths or name in found or not member.isfile()
                        or member.mode != mode or member.size <= 0 or member.size > limit):
                    fail("unsafe or unexpected archive member: " + name)
                stream = archive.extractfile(member)
                if stream is None:
                    fail("archive member cannot be read")
                with stream:
                    data = stream.read(member.size + 1)
                if len(data) != member.size:
                    fail("archive member size mismatch")
                found[name] = data
    except (tarfile.TarError, OSError, EOFError) as exc:
        raise InstallError("invalid Linux archive") from exc
    if set(found) != paths:
        fail("Linux archive lacks required provider executables or package metadata")
    metadata = strict_json(found["PACKAGE-METADATA.json"])
    if (not isinstance(metadata, dict)
            or any(metadata.get(key) != value for key, value in (
                ("schemaVersion", "1"), ("version", version), ("commit", commit),
                ("os", "linux"), ("arch", arch), ("cgoEnabled", False)))):
        fail("embedded archive identity mismatch")
    records = metadata.get("files")
    if not isinstance(records, list) or len(records) != len(expected):
        fail("embedded executable manifest incomplete")
    seen = set()
    for item in records:
        if not isinstance(item, dict) or set(item) != {"path", "sha256", "size", "mode"}:
            fail("invalid embedded executable entry")
        name = item["path"]
        if (name not in expected or name in seen or item["mode"] != "0755"
                or item["size"] != len(found[name]) or item["sha256"] != sha256(found[name])):
            fail("embedded executable integrity failure")
        seen.add(name)
    if seen != expected:
        fail("missing executable digest")
    for name in sorted(expected):
        path = destination / name
        path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        with path.open("xb") as output:
            output.write(found[name])
        path.chmod(0o755)
    (destination / "PACKAGE-METADATA.json").write_bytes(found["PACKAGE-METADATA.json"])
    (destination / "PACKAGE-METADATA.json").chmod(0o644)


def smoke(location, version):
    cli = location / "bin" / "distroplane"
    try:
        output = subprocess.run([str(cli), "version", "--json"], capture_output=True,
                                check=True, text=True, timeout=20)
        if strict_json(output.stdout).get("version") != version:
            fail("installed CLI version does not match requested release")
        for name in PROVIDERS:
            request = {"protocolVersion": "1", "requestId": "install-check",
                       "operation": "describe", "payload": {}}
            result = subprocess.run(
                [str(location / "libexec" / ("distroplane-provider-" + name))],
                input=json.dumps(request), text=True, capture_output=True,
                check=True, timeout=20)
            payload = strict_json(result.stdout)
            if (payload.get("status") != "ok" or payload.get("requestId") != "install-check"
                    or payload.get("payload", {}).get("provider", {}).get("name") != name
                    or payload.get("payload", {}).get("provider", {}).get("version") != version):
                fail("installed provider identity failed: " + name)
    except (OSError, subprocess.SubprocessError, ValueError) as exc:
        raise InstallError("installed executable smoke check failed") from exc


def replace_owned_link(link, versions, location):
    if link.is_symlink():
        current = link.resolve(strict=False)
        if current != location / "bin" / "distroplane":
            try:
                current.relative_to(versions)
            except ValueError:
                fail("existing distroplane symlink is not owned by this installer")
    elif link.exists():
        fail("existing distroplane executable is not installer-managed")
    temp_link = link.parent / (".distroplane-link-" + str(os.getpid()))
    try:
        temp_link.symlink_to(location / "bin" / "distroplane")
        os.replace(temp_link, link)
    finally:
        temp_link.unlink(missing_ok=True)


def install(args):
    if platform.system() != "Linux":
        fail("direct installer supports Linux only")
    prefix = args.prefix.expanduser().resolve()
    if args.uninstall:
        link = prefix / "bin" / "distroplane"
        versions = prefix / "lib" / "distroplane" / "versions"
        if not link.is_symlink():
            fail("no installer-managed executable to uninstall")
        current = link.resolve(strict=False)
        try:
            current.relative_to(versions)
        except ValueError:
            fail("refusing to remove foreign executable")
        link.unlink()
        print("Removed Distroplane command; retained versioned binaries for run recovery")
        return
    if not VERSION.fullmatch(args.version or ""):
        fail("a stable, version-pinned --version is required")
    arch = ARCHES.get(platform.machine().lower())
    if arch is None:
        fail("unsupported Linux CPU architecture: " + platform.machine())
    versions = prefix / "lib" / "distroplane" / "versions"
    destination = versions / args.version
    link = prefix / "bin" / "distroplane"
    archive_name = f"distroplane_{args.version}_linux_{arch}.tar.gz"
    with tempfile.TemporaryDirectory(prefix="distroplane-download-") as temp:
        directory = args.offline_dir.expanduser().resolve() if args.offline_dir else Path(temp)
        if args.offline_dir is None:
            for name, maximum in (("RELEASE-METADATA.json", MAX_METADATA),
                                  ("SHA256SUMS", MAX_METADATA), (archive_name, MAX_ARCHIVE)):
                download(directory, args.version, name, maximum)
        commit, blob = release_inventory(directory, args.version, archive_name)
        versions.mkdir(parents=True, exist_ok=True, mode=0o755)
        link.parent.mkdir(parents=True, exist_ok=True, mode=0o755)
        with (versions.parent / "install.lock").open("a+b") as file_lock:
            fcntl.flock(file_lock.fileno(), fcntl.LOCK_EX)
            if destination.is_symlink():
                fail("installed version directory must not be a symlink")
            if destination.exists():
                # Existing immutable releases are checked byte-for-byte before reuse.
                if not destination.is_dir():
                    fail("installed version is not a directory")
                with tempfile.TemporaryDirectory(dir=versions, prefix=".check-") as tmp:
                    expected = Path(tmp)
                    verify_and_stage(blob, args.version, commit, arch, expected)
                    for file in expected.rglob("*"):
                        if file.is_file():
                            counterpart = destination / file.relative_to(expected)
                            if (counterpart.is_symlink() or not counterpart.is_file()
                                    or read_regular(counterpart, MAX_BINARY) != file.read_bytes()):
                                fail("installed release differs from verified published bytes")
                smoke(destination, args.version)
            else:
                staging = Path(tempfile.mkdtemp(dir=versions, prefix=".install-"))
                try:
                    staging.chmod(0o755)
                    verify_and_stage(blob, args.version, commit, arch, staging)
                    smoke(staging, args.version)
                    os.replace(staging, destination)
                finally:
                    if staging.exists():
                        shutil.rmtree(staging)
            replace_owned_link(link, versions, destination)
    print(f"Installed Distroplane {args.version} at {link}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", help="Exact stable release version, e.g. 0.9.1")
    parser.add_argument("--prefix", type=Path, default=Path.home() / ".local")
    parser.add_argument("--offline-dir", type=Path,
                        help="Directory containing downloaded metadata, SHA256SUMS and archive")
    parser.add_argument("--uninstall", action="store_true",
                        help="Remove the managed CLI link while retaining binaries and user state")
    args = parser.parse_args()
    try:
        install(args)
    except (InstallError, OSError, ValueError) as exc:
        print("Distroplane Linux installer: " + str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
