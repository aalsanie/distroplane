"""Offline and adversarial tests for the standard-library Linux installer."""
import hashlib
import importlib.util
import io
import json
import os
import platform
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("install-linux.py")
spec = importlib.util.spec_from_file_location("linux_install", SCRIPT)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
COMMIT = "0123456789ab"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def executable_script(name, version):
    if name == "distroplane":
        return ("#!/usr/bin/env python3\nimport json\nprint(json.dumps("
                + repr({"version": version}) + "))\n").encode()
    provider = name.removeprefix("distroplane-provider-")
    return ("#!/usr/bin/env python3\nimport json, sys\n"
            "request=json.load(sys.stdin)\n"
            "print(json.dumps({'protocolVersion':'1',"
            "'requestId':request['requestId'],'operation':request['operation'],"
            "'status':'ok','payload':{'provider':{'name':"
            + repr(provider) + ",'version':" + repr(version)
            + "},'protocolVersions':['1']}}))\n").encode()


def fixture(directory, version, tamper=None):
    directory.mkdir(parents=True, exist_ok=True)
    arch = module.ARCHES[platform.machine().lower()]
    tar_name = f"distroplane_{version}_linux_{arch}.tar.gz"
    source = {}
    paths = ["bin/distroplane"] + [
        f"libexec/distroplane-provider-{p}" for p in module.PROVIDERS]
    files = []
    for path in paths:
        name = Path(path).name
        payload = executable_script(name, version)
        source[path] = payload
        files.append({"path": path, "sha256": digest(payload), "size": len(payload),
                      "mode": "0755"})
    package = {"schemaVersion": "1", "version": version, "commit": COMMIT,
               "os": "linux", "arch": arch, "cgoEnabled": False, "files": files}
    source["PACKAGE-METADATA.json"] = json.dumps(package).encode()
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as output:
        for path, payload in source.items():
            member = tarfile.TarInfo(path if not tamper or path != tamper else "../escape")
            member.mode = 0o644 if path == "PACKAGE-METADATA.json" else 0o755
            member.size = len(payload)
            output.addfile(member, io.BytesIO(payload))
    data = buffer.getvalue()
    (directory / tar_name).write_bytes(data)
    assets = []
    for os_name in ("darwin", "linux", "windows"):
        for cpu in ("amd64", "arm64"):
            ext = ".zip" if os_name == "windows" else ".tar.gz"
            archive_name = f"distroplane_{version}_{os_name}_{cpu}{ext}"
            archive_bytes = data if archive_name == tar_name else archive_name.encode()
            assets.append({"name": archive_name, "sha256": digest(archive_bytes),
                           "size": len(archive_bytes)})
            for exe in ("distroplane",) + tuple("distroplane-provider-" + p for p in module.PROVIDERS):
                name = f"{exe}_{version}_{os_name}_{cpu}" + (".exe" if os_name == "windows" else "")
                blob = name.encode()
                assets.append({"name": name, "sha256": digest(blob), "size": len(blob)})
    assets.sort(key=lambda a: a["name"])
    metadata = {"schemaVersion": "2", "version": version, "commit": COMMIT,
                "cgoEnabled": False, "assets": assets}
    (directory / "RELEASE-METADATA.json").write_text(json.dumps(metadata))
    (directory / "SHA256SUMS").write_text(
        "".join(f'{asset["sha256"]}  {asset["name"]}\n' for asset in assets))


@unittest.skipUnless(platform.system() == "Linux" and
                     platform.machine().lower() in module.ARCHES,
                     "Linux x64/arm64 native installer only")
class InstallTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.prefix = self.root / "prefix"
        self.release = self.root / "release"
        fixture(self.release, "0.9.1")

    def run_installer(self, version="0.9.1", release=None, *options):
        cmd = [sys.executable, str(SCRIPT), "--prefix", str(self.prefix)]
        if version:
            cmd += ["--version", version]
        if release:
            cmd += ["--offline-dir", str(release)]
        cmd += list(options)
        return subprocess.run(cmd, capture_output=True, text=True, timeout=25)

    def test_offline_install_repeat_and_uninstall_preserve_state(self):
        first = self.run_installer("0.9.1", self.release)
        self.assertEqual(first.returncode, 0, first.stderr)
        link = self.prefix / "bin/distroplane"
        self.assertTrue(link.is_symlink())
        self.assertEqual(json.loads(subprocess.check_output(
            [str(link), "version", "--json"], text=True))["version"], "0.9.1")
        journal = self.root / "project/.distroplane/journal"
        journal.parent.mkdir(parents=True)
        journal.write_text("retained")
        second = self.run_installer("0.9.1", self.release)
        self.assertEqual(second.returncode, 0, second.stderr)
        removed = self.run_installer(None, None, "--uninstall")
        self.assertEqual(removed.returncode, 0, removed.stderr)
        self.assertFalse(link.exists())
        self.assertTrue((self.prefix / "lib/distroplane/versions/0.9.1/bin/distroplane").exists())
        self.assertEqual(journal.read_text(), "retained")

    def test_upgrades_rollback_and_keep_original_provider_bytes(self):
        first = self.run_installer("0.9.1", self.release)
        self.assertEqual(first.returncode, 0, first.stderr)
        old = self.prefix / "lib/distroplane/versions/0.9.1"
        provider = old / "libexec/distroplane-provider-npm"
        old_digest = digest(provider.read_bytes())
        newer = self.root / "newer"
        fixture(newer, "0.9.2")
        result = self.run_installer("0.9.2", newer)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("0.9.2", os.readlink(self.prefix / "bin/distroplane"))
        self.assertEqual(old_digest, digest(provider.read_bytes()))
        rollback = self.run_installer("0.9.1", self.release)
        self.assertEqual(rollback.returncode, 0, rollback.stderr)
        self.assertIn("0.9.1", os.readlink(self.prefix / "bin/distroplane"))

    def test_reject_tampered_archive_preserve_healthy_install(self):
        self.assertEqual(self.run_installer("0.9.1", self.release).returncode, 0)
        current = os.readlink(self.prefix / "bin/distroplane")
        newer = self.root / "newer"
        fixture(newer, "0.9.2")
        archive = next(newer.glob("*.tar.gz"))
        archive.write_bytes(archive.read_bytes() + b"corrupt")
        failed = self.run_installer("0.9.2", newer)
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(current, os.readlink(self.prefix / "bin/distroplane"))
        self.assertFalse((self.prefix / "lib/distroplane/versions/0.9.2").exists())

    def test_reject_traversal_even_when_checksum_is_updated(self):
        unsafe = self.root / "unsafe"
        fixture(unsafe, "0.9.2", tamper="bin/distroplane")
        failed = self.run_installer("0.9.2", unsafe)
        self.assertNotEqual(failed.returncode, 0)
        self.assertFalse((self.prefix / "bin/distroplane").exists())

    def test_reject_metadata_and_checksum_corruption(self):
        (self.release / "SHA256SUMS").write_text("invalid\n")
        self.assertNotEqual(self.run_installer("0.9.1", self.release).returncode, 0)
        fixture(self.release, "0.9.1")
        metadata_file = self.release / "RELEASE-METADATA.json"
        data = json.loads(metadata_file.read_text())
        data["version"] = "0.9.2"
        metadata_file.write_text(json.dumps(data))
        self.assertNotEqual(self.run_installer("0.9.1", self.release).returncode, 0)

    def test_refuse_foreign_executable_link(self):
        foreign = self.root / "foreign"
        foreign.write_text("owner")
        link = self.prefix / "bin/distroplane"
        link.parent.mkdir(parents=True)
        link.symlink_to(foreign)
        result = self.run_installer("0.9.1", self.release)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(link.resolve(), foreign)
        self.assertEqual(foreign.read_text(), "owner")

    def test_fail_closed_if_existing_version_is_modified(self):
        self.assertEqual(self.run_installer("0.9.1", self.release).returncode, 0)
        installed = self.prefix / "lib/distroplane/versions/0.9.1/bin/distroplane"
        installed.write_bytes(installed.read_bytes() + b"modified")
        result = self.run_installer("0.9.1", self.release)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("differs from verified", result.stderr)


if __name__ == "__main__":
    unittest.main()
