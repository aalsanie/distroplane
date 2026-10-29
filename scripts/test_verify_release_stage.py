"""Negative and round-trip release staging contract tests; no external downloads."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("verify-release-stage.py")
spec = importlib.util.spec_from_file_location("stage", SCRIPT)
stage = importlib.util.module_from_spec(spec)
spec.loader.exec_module(stage)
COMMIT = "0123456789ab"


def sha(blob):
    return hashlib.sha256(blob).hexdigest()


def fixture(directory, version="0.9.2", stable=True):
    directory.mkdir(parents=True)
    entries = []
    for name in sorted(stage.expected_assets(version)):
        blob = ("EXE" + name).encode()
        (directory / name).write_bytes(blob)
        entries.append({"name": name, "sha256": sha(blob), "size": len(blob)})
    (directory / "RELEASE-METADATA.json").write_text(json.dumps({
        "schemaVersion": "2", "version": version, "commit": COMMIT,
        "cgoEnabled": False, "assets": entries,
    }))
    (directory / "SHA256SUMS").write_text("".join(
        f'{i["sha256"]}  {i["name"]}\n' for i in entries))
    if stable:
        msis = []
        for arch in stage.ARCHES:
            name = f"distroplane_{version}_windows_{arch}.msi"
            blob = bytes.fromhex("d0cf11e0a1b11ae1") + (name * 3).encode()
            (directory / name).write_bytes(blob)
            msis.append({"name": name, "sha256": sha(blob)})
        (directory / "MSI-SHA256SUMS").write_text("".join(
            f'{i["sha256"]}  {i["name"]}\n' for i in msis))


class StageTests(unittest.TestCase):
    def setUp(self):
        t = tempfile.TemporaryDirectory()
        self.addCleanup(t.cleanup)
        self.path = Path(t.name) / "dist"
        fixture(self.path)

    def create(self):
        stage.run(self.path, "0.9.2", COMMIT, "unsigned")

    def verify(self):
        stage.run(self.path, "0.9.2", COMMIT, "unsigned", verify=True)

    def test_stable_round_trip_and_complete_inventory(self):
        self.create()
        self.verify()
        document = json.loads((self.path / "RELEASE-STAGING.json").read_text())
        self.assertEqual(len(document["assets"]), 41)
        self.assertEqual(document["signingMode"], "unsigned")
        self.assertEqual([i["name"] for i in document["assets"]],
                         sorted(i["name"] for i in document["assets"]))
        with self.assertRaises(stage.StageError):
            self.create()

    def test_candidate_without_msi(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "rc"
            fixture(path, "0.9.3-rc.1", stable=False)
            stage.run(path, "0.9.3-rc.1", COMMIT, "not-applicable")
            stage.run(path, "0.9.3-rc.1", COMMIT, "not-applicable", verify=True)
            with self.assertRaises(stage.StageError):
                stage.run(path, "0.9.3-rc.1", COMMIT, "unsigned", verify=True)

    def test_reject_missing_corrupted_or_unexpected_assets(self):
        self.create()
        selected = self.path / "distroplane_0.9.2_linux_amd64.tar.gz"
        original = selected.read_bytes()
        selected.write_bytes(original + b"tampered")
        with self.assertRaises(stage.StageError):
            self.verify()
        selected.write_bytes(original)
        self.verify()
        selected.unlink()
        with self.assertRaises(OSError):
            self.verify()

    def test_reject_wrong_msi_and_malformed_checksum(self):
        msi = self.path / "distroplane_0.9.2_windows_arm64.msi"
        msi.write_bytes(b"not an MSI")
        with self.assertRaises(stage.StageError):
            self.create()
        msi.write_bytes(bytes.fromhex("d0cf11e0a1b11ae1") + b"wrong")
        with self.assertRaises(stage.StageError):
            self.create()
        fixture_path = self.path / "MSI-SHA256SUMS"
        fixture_path.write_text("bad\n")
        with self.assertRaises(stage.StageError):
            self.create()

    def test_reject_missing_manifest_extra_file_and_wrong_identity(self):
        (self.path / "unexpected").write_bytes(b"unexpected")
        with self.assertRaises(stage.StageError):
            self.create()
        (self.path / "unexpected").unlink()
        with self.assertRaises(stage.StageError):
            stage.run(self.path, "0.9.2", "bad", "unsigned")
        with self.assertRaises(stage.StageError):
            stage.run(self.path, "0.9.2", COMMIT, "not-applicable")
        self.create()
        (self.path / "RELEASE-STAGING.json").unlink()
        with self.assertRaises(OSError):
            self.verify()

    def test_reject_tampered_metadata_and_checksum_after_create(self):
        self.create()
        metadata = self.path / "RELEASE-METADATA.json"
        original = metadata.read_bytes()
        metadata.write_bytes(original.replace(b'"version": "0.9.2"',
                                              b'"version": "0.9.1"'))
        with self.assertRaises(stage.StageError):
            self.verify()
        metadata.write_bytes(original)
        checksum = self.path / "SHA256SUMS"
        checksum.write_bytes(checksum.read_bytes() + b"\n")
        with self.assertRaises(stage.StageError):
            self.verify()

    def test_reject_symlinked_staging_manifest(self):
        self.create()
        manifest = self.path / "RELEASE-STAGING.json"
        external = self.path.parent / "manifest.json"
        manifest.rename(external)
        manifest.symlink_to(external)
        with self.assertRaises(stage.StageError):
            self.verify()

    def test_reject_symlinked_asset(self):
        asset = self.path / "distroplane_0.9.2_windows_amd64.msi"
        copy = self.path.parent / "copy.msi"
        asset.rename(copy)
        asset.symlink_to(copy)
        with self.assertRaises(stage.StageError):
            self.create()


if __name__ == "__main__":
    unittest.main()
