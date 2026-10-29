"""Adversarial tests for exact-workspace CI recovery bundles."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import stat
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

SCRIPT = Path(__file__).parents[1] / "action/recovery.py"
spec = importlib.util.spec_from_file_location("distroplane_recovery", SCRIPT)
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)


class HandoffTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.workspace = self.root / "workspace"
        self.workspace.mkdir()
        self.bundle = self.root / "recovery.zip"
        self.project = self.workspace / "project"
        self.project.mkdir()
        self.config = self.project / "distroplane.json"
        self.state = self.project / ".distroplane"
        (self.state / "plans").mkdir(parents=True)
        (self.state / "runs").mkdir()
        (self.state / "catalog/plans").mkdir(parents=True)
        (self.state / "catalog/runs").mkdir()
        (self.state / "providers/fake/digest").mkdir(parents=True)
        (self.project / "app.bin").write_bytes(b"build artifact")
        (self.state / "plans/plan.json").write_bytes(b"plan")
        (self.state / "runs/run.journal").write_bytes(b"durable journal")
        (self.state / "catalog/plans/entry.json").write_text('{"entry":1}')
        provider = self.state / "providers/fake/digest/distroplane-provider-fake"
        provider.write_bytes(b"provider")
        provider.chmod(0o500)
        (self.state / "providers/fake/digest/binding.json").write_text('{"binding":1}')
        (self.project / "bin").mkdir()
        (self.project / "bin/distroplane").write_bytes(b"cli")
        (self.project / "bin/distroplane").chmod(0o755)
        self.config.write_text(json.dumps({
            "schemaVersion": "1", "release": {"id": "demo", "artifacts": [
                {"name": "app", "source": "app.bin"}]}, "targets": [
                {"id": "fake", "provider": {"name": "fake", "executable": "bin/distroplane"},
                 "configuration": {"mode": "published"}}]}))

    def create(self, **kwargs):
        options = {"workspace": str(self.workspace), "config": str(self.config),
                   "plan": str(self.state / "plans/plan.json"),
                   "journal": str(self.state / "runs/run.journal"),
                   "binary": "", "output": str(self.bundle)}
        options.update(kwargs)
        with contextlib.redirect_stdout(io.StringIO()):
            recovery.create(type("Args", (), options)())

    def restore(self, workspace=None):
        options = {"workspace": str(workspace or self.workspace), "bundle": str(self.bundle)}
        with contextlib.redirect_stdout(io.StringIO()):
            recovery.restore(type("Args", (), options)())

    def rewrite_bundle(self, edit):
        with zipfile.ZipFile(self.bundle) as archive:
            data = {info.filename: archive.read(info) for info in archive.infolist()}
        edit(data)
        with zipfile.ZipFile(self.bundle, "w") as archive:
            for name, value in data.items():
                archive.writestr(name, value)

    def test_create_restore_exact_state_and_provider_mode(self):
        self.create()
        original = (self.state / "runs/run.journal").read_bytes()
        for name in (".distroplane", "app.bin", "bin", "distroplane.json"):
            value = self.project / name
            if value.is_dir():
                import shutil
                shutil.rmtree(value)
            else:
                value.unlink()
        self.restore()
        self.assertEqual((self.state / "runs/run.journal").read_bytes(), original)
        self.assertEqual((self.project / "app.bin").read_bytes(), b"build artifact")
        self.assertEqual(stat.S_IMODE((self.state / "providers/fake/digest/distroplane-provider-fake").stat().st_mode), 0o500)
        self.restore()  # Repeated restoration of identical content is safe.

    def test_rejects_digests_before_placing_any_files(self):
        self.create()
        (self.state / "runs/run.journal").unlink()
        self.rewrite_bundle(lambda data: data.__setitem__("files/project/.distroplane/runs/run.journal", b"attacker"))
        with self.assertRaisesRegex(recovery.RecoveryError, "digest mismatch|size mismatch"):
            self.restore()
        self.assertFalse((self.state / "runs/run.journal").exists())

    def test_rejects_missing_file(self):
        self.create()
        self.rewrite_bundle(lambda d: d.pop("files/project/app.bin"))
        with self.assertRaisesRegex(recovery.RecoveryError, "missing"):
            self.restore()

    def test_rejects_added_unexpected_archive_file(self):
        self.create()
        self.rewrite_bundle(lambda d: d.__setitem__("files/project/extra", b"bad"))
        with self.assertRaisesRegex(recovery.RecoveryError, "unexpected"):
            self.restore()

    def test_rejects_zip_traversal(self):
        self.create()
        def mutate(d):
            m = json.loads(d["manifest.json"])
            m["files"][0]["path"] = "../escape"
            d["manifest.json"] = json.dumps(m).encode()
        self.rewrite_bundle(mutate)
        with self.assertRaisesRegex(recovery.RecoveryError, "unsafe recovery entry"):
            self.restore()

    def test_rejects_moved_workspace(self):
        self.create()
        alternate = self.root / "different-workspace"
        alternate.mkdir()
        with self.assertRaisesRegex(recovery.RecoveryError, "workspace changed"):
            self.restore(alternate)

    def test_rejects_wrong_platform(self):
        self.create()
        with patch.dict(os.environ, {"RUNNER_ARCH": "other"}):
            with self.assertRaisesRegex(recovery.RecoveryError, "runner OS or architecture"):
                self.restore()

    def test_rejects_conflicting_files_before_changing_state(self):
        self.create()
        (self.state / "runs/run.journal").write_bytes(b"foreign journal")
        with self.assertRaisesRegex(recovery.RecoveryError, "conflicts"):
            self.restore()
        self.assertEqual((self.state / "runs/run.journal").read_bytes(), b"foreign journal")

    def test_rejects_symlink_input_and_restore_parent(self):
        self.create()
        (self.state / "runs/run.journal").unlink()
        (self.state / "runs/run.journal").symlink_to(self.project / "app.bin")
        with self.assertRaisesRegex(recovery.RecoveryError, "symlinks"):
            self.create()
        (self.state / "runs/run.journal").unlink()
        (self.state / "runs").rmdir()
        (self.state / "runs").symlink_to(self.project)
        with self.assertRaisesRegex(recovery.RecoveryError, "unsafe recovery directory"):
            self.restore()

    def test_rejects_source_outside_workspace(self):
        self.config.write_text(json.dumps({"release": {"artifacts": [{"source": str(self.root / "secret")}]}}))
        with self.assertRaisesRegex(recovery.RecoveryError, "outside GITHUB_WORKSPACE"):
            self.create()

    def test_supports_v2_artifact_maps(self):
        self.config.write_text(json.dumps({"schemaVersion": "2", "release": {"artifacts": {"a": {"source": "app.bin"}}}}))
        self.create(journal="")
        self.restore()

    def test_rejects_absent_claimed_journal(self):
        with self.assertRaisesRegex(recovery.RecoveryError, "cannot upload recovery without the journal"):
            self.create(journal="project/missing.journal")


if __name__ == "__main__":
    unittest.main()