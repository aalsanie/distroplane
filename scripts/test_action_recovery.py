"""Adversarial tests for exact-workspace CI recovery bundles."""
import contextlib
import hashlib
import importlib.util
import io
import json
import os
import stat
import struct
import shutil
import tempfile
import unittest
import zipfile
import zlib
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
        self.plan_id = "sha256-" + "a" * 64
        self.run_id = "original-run"
        self.plan_path = self.state / "plans/plan.json"
        self.journal_path = self.state / "runs/run.journal"
        self.key = hashlib.sha256(self.plan_id.encode()).hexdigest() + ".json"
        for directory in ("plans", "runs", "catalog/plans", "catalog/runs"):
            (self.state / directory).mkdir(parents=True)
        self.provider_data = b"provider"
        self.provider_digest = hashlib.sha256(self.provider_data).hexdigest()
        self.provider_dir = self.state / "providers/fake" / self.provider_digest
        self.provider_dir.mkdir(parents=True)
        for directory in (self.state / "catalog", self.state / "catalog/plans",
                          self.state / "catalog/runs", self.state / "providers",
                          self.state / "providers/fake", self.provider_dir):
            directory.chmod(0o700)
        self.provider = self.provider_dir / ("distroplane-provider-fake.exe" if os.name == "nt" else "distroplane-provider-fake")
        self.provider.write_bytes(self.provider_data)
        self.provider.chmod(0o500)
        self.receipt = self.provider_dir / "binding.json"
        self.receipt.write_text(json.dumps({"schemaVersion": "1", "name": "fake", "version": "1.0.0",
                                           "digest": "sha256:" + self.provider_digest, "trusted": False}))
        self.receipt.chmod(0o600)
        self.artifact = self.project / "app.bin"
        self.artifact.write_bytes(b"build artifact")
        self.artifact_digest = hashlib.sha256(self.artifact.read_bytes()).hexdigest()
        self.plan = {
            "schemaVersion": "2", "protocolVersion": "1", "planId": self.plan_id,
            "release": {"id": "demo", "artifacts": [
                {"name": "app", "source": str(self.artifact), "digest": "sha256:" + self.artifact_digest,
                 "size": self.artifact.stat().st_size}]},
            "targets": [{"id": "fake", "provider": {"name": "fake", "version": "1.0.0",
                                                  "digest": "sha256:" + self.provider_digest}}],
            "operations": []}
        self.write_plan()
        self.journal_path.write_bytes(self.journal(self.plan_id, self.run_id))
        self.entry = {"schemaVersion": "1", "planId": self.plan_id, "planPath": str(self.plan_path)}
        self.claim = dict(self.entry, runId=self.run_id, journalPath=str(self.journal_path))
        (self.state / ".catalog-required").write_bytes(b"1\n")
        (self.state / "catalog/plans" / self.key).write_text(json.dumps(self.entry))
        for name in (self.key, self.key + ".backup"):
            (self.state / "catalog/runs" / name).write_text(json.dumps(self.claim))
        for path in (self.state / "catalog").rglob("*.json*"):
            path.chmod(0o600)
        (self.project / "bin").mkdir()
        self.binary = self.project / "bin/distroplane"
        self.binary.write_bytes(b"cli")
        self.binary.chmod(0o755)
        self.config.write_text(json.dumps({
            "schemaVersion": "1", "release": {"id": "demo", "artifacts": [
                {"name": "app", "source": "app.bin"}]}, "targets": [
                {"id": "fake", "provider": {"name": "fake", "executable": "bin/distroplane"},
                 "configuration": {"mode": "published"}}]}))

    def write_plan(self):
        self.plan_path.write_text(json.dumps(self.plan))

    @staticmethod
    def journal(plan, run):
        raw = json.dumps({"schemaVersion": 2, "sequence": 1, "type": "RUN_STARTED",
                          "planId": plan, "runId": run}).encode()
        return b"DPJ1" + struct.pack(">II", len(raw), zlib.crc32(raw)) + raw

    def create(self, **kwargs):
        options = {"workspace": str(self.workspace), "config": str(self.config),
                   "plan": str(self.state / "plans/plan.json"),
                   "journal": str(self.state / "runs/run.journal"),
                   "binary": str(self.binary), "output": str(self.bundle),
                   "plan_id": self.plan_id, "run_id": self.run_id}
        options.update(kwargs)
        with contextlib.redirect_stdout(io.StringIO()):
            recovery.create(type("Args", (), options)())

    def restore(self, workspace=None, **kwargs):
        options = {"workspace": str(workspace or self.workspace), "bundle": str(self.bundle),
                   "config": str(self.config), "plan_id": self.plan_id, "run_id": self.run_id}
        options.update(kwargs)
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
                shutil.rmtree(value)
            else:
                value.unlink()
        self.restore()
        self.assertEqual((self.state / "runs/run.journal").read_bytes(), original)
        self.assertEqual((self.project / "app.bin").read_bytes(), b"build artifact")
        if os.name != "nt":
            self.assertEqual(stat.S_IMODE(self.provider.stat().st_mode), 0o500)
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
        self.plan["release"]["artifacts"][0]["source"] = str(self.root / "secret")
        self.write_plan()
        with self.assertRaisesRegex(recovery.RecoveryError, "outside GITHUB_WORKSPACE"):
            self.create()

    def test_supports_v2_artifact_maps(self):
        self.config.write_text(json.dumps({"schemaVersion": "2", "release": {"artifacts": {"a": {"source": "app.bin"}}}}))
        self.create()
        self.restore()

    def test_reviewed_plan_handoff_cannot_restore_a_claimed_run(self):
        self.create()
        with self.assertRaisesRegex(recovery.RecoveryError, "expected configuration, plan and run"):
            self.restore(run_id="")
        with self.assertRaisesRegex(recovery.RecoveryError, "already has a publication claim"):
            self.create(journal="", run_id="")

        for name in (self.key, self.key + ".backup"):
            (self.state / "catalog/runs" / name).unlink()
        self.journal_path.unlink()
        executable = self.project / "bin/distroplane-provider-fake"
        executable.write_bytes(self.provider_data)
        executable.chmod(0o700)
        config = json.loads(self.config.read_text())
        config["targets"][0]["provider"]["executable"] = str(executable)
        self.config.write_text(json.dumps(config))
        self.create(journal="", run_id="")
        with self.assertRaisesRegex(recovery.RecoveryError, "expected configuration, plan and run"):
            self.restore()
        shutil.rmtree(self.state)
        executable.unlink()
        self.restore(run_id="")
        self.assertEqual(executable.read_bytes(), self.provider_data)
        self.assertTrue((self.state / "catalog/runs").is_dir())
        self.assertFalse(self.journal_path.exists())
        self.restore(run_id="")

    def test_rejects_absent_claimed_journal(self):
        self.journal_path.unlink()
        with self.assertRaisesRegex(recovery.RecoveryError, "required recovery input is absent|without the original journal"):
            self.create()


    def test_bundle_excludes_unrelated_workspace_and_state_files(self):
        for name in (".git", ".private-context", "unrelated", ".distroplane/unrelated"):
            directory = self.project / name
            directory.mkdir(exist_ok=True)
            (directory / "canary").write_text("private-canary")
        (self.project / ".env").write_text("private-canary")
        (self.project / "environment.txt").write_text("private-canary")
        (self.provider_dir / "unrelated-secret").write_text("private-canary")
        (self.state / "runs/run.journal.lock").write_text("private-canary")
        self.create()
        with zipfile.ZipFile(self.bundle) as archive:
            self.assertNotIn(b"private-canary", b"".join(archive.read(name) for name in archive.namelist()))
            manifest = json.loads(archive.read("manifest.json"))
            self.assertEqual(manifest["planId"], self.plan_id)
            self.assertEqual(manifest["runId"], self.run_id)
            self.assertEqual(len(manifest["files"]), 11)

    def test_mapped_credential_canary_refuses_export(self):
        secret = "mapped-secret-canary-never-export"
        self.journal_path.write_bytes(self.journal(self.plan_id, self.run_id) + secret.encode())
        with patch.dict(os.environ, {"RECOVERY_CREDENTIAL_MAPPINGS": "auth=TEST_RECOVERY_TOKEN",
                                    "TEST_RECOVERY_TOKEN": secret}):
            with self.assertRaisesRegex(recovery.RecoveryError, "mapped credential value"):
                self.create()
        self.assertFalse(self.bundle.exists())

    def test_rejects_wrong_plan_run_and_config(self):
        self.create()
        for options in ({"plan_id": "sha256-" + "b" * 64}, {"run_id": "other-run"},
                        {"config": "project/other.json"}):
            with self.subTest(options=options):
                with self.assertRaisesRegex(recovery.RecoveryError, "expected configuration, plan and run"):
                    self.restore(**options)

    def test_wrong_os_rejected(self):
        self.create()
        with patch.dict(os.environ, {"RUNNER_OS": "other"}):
            with self.assertRaisesRegex(recovery.RecoveryError, "runner OS or architecture"):
                self.restore()

    def test_original_artifact_and_provider_required_on_export(self):
        for path in (self.artifact, self.provider, self.receipt):
            with self.subTest(path=path):
                original, mode = path.read_bytes(), stat.S_IMODE(path.stat().st_mode)
                path.unlink()
                with self.assertRaisesRegex(recovery.RecoveryError, "required recovery input is absent"):
                    self.create()
                path.write_bytes(original)
                path.chmod(mode)
        self.artifact.write_bytes(b"changed artifact")
        with self.assertRaisesRegex(recovery.RecoveryError, "bytes differ from plan"):
            self.create()
        self.artifact.write_bytes(b"build artifact")
        self.provider.chmod(0o700)
        self.provider.write_bytes(b"changed provider")
        with self.assertRaisesRegex(recovery.RecoveryError, "bytes differ from plan"):
            self.create()

    def test_rejects_self_consistent_missing_required_files(self):
        for path in (self.artifact, self.provider, self.receipt, self.journal_path):
            with self.subTest(path=path):
                self.create()
                relative = path.relative_to(self.workspace).as_posix()
                def mutate(data):
                    manifest = json.loads(data["manifest.json"])
                    manifest["files"] = [item for item in manifest["files"] if item["path"] != relative]
                    del data["files/" + relative]
                    data["manifest.json"] = json.dumps(manifest).encode()
                self.rewrite_bundle(mutate)
                with self.assertRaisesRegex(recovery.RecoveryError, "required recovery file is missing|without the original journal"):
                    self.restore()

    def test_rejects_self_consistent_wrong_journal_identity(self):
        self.create()
        relative = self.journal_path.relative_to(self.workspace).as_posix()
        def mutate(data):
            raw = self.journal(self.plan_id, "different-run")
            data["files/" + relative] = raw
            manifest = json.loads(data["manifest.json"])
            for item in manifest["files"]:
                if item["path"] == relative:
                    item.update(size=len(raw), sha256=hashlib.sha256(raw).hexdigest())
            data["manifest.json"] = json.dumps(manifest).encode()
        self.rewrite_bundle(mutate)
        with self.assertRaisesRegex(recovery.RecoveryError, "different plan or run"):
            self.restore()

    def test_rejects_self_consistent_unrelated_file(self):
        self.create()
        def mutate(data):
            manifest = json.loads(data["manifest.json"])
            name, raw = "project/unrelated.txt", b"unrelated"
            manifest["files"].append({"path": name, "size": len(raw),
                                      "sha256": hashlib.sha256(raw).hexdigest(), "mode": 0o600})
            data["files/" + name] = raw
            data["manifest.json"] = json.dumps(manifest).encode()
        self.rewrite_bundle(mutate)
        with self.assertRaisesRegex(recovery.RecoveryError, "outside the original run inventory"):
            self.restore()
        self.assertFalse((self.project / "unrelated.txt").exists())

    def test_truncated_bundle_never_restores(self):
        self.create()
        self.journal_path.unlink()
        data = self.bundle.read_bytes()
        self.bundle.write_bytes(data[:len(data) // 2])
        with self.assertRaises(zipfile.BadZipFile):
            self.restore()
        self.assertFalse(self.journal_path.exists())

    def test_recovery_preserves_prior_catalog_claims_without_prior_artifacts(self):
        old_id = "sha256-" + "b" * 64
        key = hashlib.sha256(old_id.encode()).hexdigest() + ".json"
        prior_plan = self.state / "plans/prior.json"
        prior_journal = self.state / "runs/prior.journal"
        prior = dict(self.plan, planId=old_id, release={"id": "prior", "artifacts": [
            {"name": "prior", "source": str(self.project / "no-longer-present.bin")}]})
        prior_plan.write_text(json.dumps(prior))
        prior_journal.write_bytes(self.journal(old_id, "prior-run"))
        entry = {"schemaVersion": "1", "planId": old_id, "planPath": str(prior_plan)}
        (self.state / "catalog/plans" / key).write_text(json.dumps(entry))
        (self.state / "catalog/runs" / key).write_text(json.dumps(dict(entry, runId="prior-run", journalPath=str(prior_journal))))
        self.create()
        with zipfile.ZipFile(self.bundle) as archive:
            for path in (prior_plan, prior_journal, self.state / "catalog/runs" / key):
                self.assertIn("files/" + path.relative_to(self.workspace).as_posix(), archive.namelist())
        shutil.rmtree(self.state)
        self.restore()
        self.assertTrue(prior_journal.is_file())

    def test_rejects_import_into_different_catalog(self):
        self.create()
        (self.state / "catalog/plans" / ("b" * 64 + ".json")).write_text("{}")
        with self.assertRaisesRegex(recovery.RecoveryError, "existing catalog differs"):
            self.restore()

    def test_npm_reconciliation_preserves_distinct_original_archive(self):
        package = self.project / "package.tgz"
        package.write_bytes(self.artifact.read_bytes())
        directory = self.state / "providers/npm" / self.provider_digest
        directory.mkdir(parents=True)
        directory.parent.chmod(0o700)
        directory.chmod(0o700)
        executable = directory / ("distroplane-provider-npm.exe" if os.name == "nt" else "distroplane-provider-npm")
        executable.write_bytes(self.provider_data)
        executable.chmod(0o500)
        receipt = directory / "binding.json"
        receipt.write_text(json.dumps({"schemaVersion": "1", "name": "npm", "version": "1.0.0",
                                       "digest": "sha256:" + self.provider_digest, "trusted": False}))
        receipt.chmod(0o600)
        self.plan["targets"][0]["provider"]["name"] = "npm"
        self.plan["operations"] = [{"provider": self.plan["targets"][0]["provider"], "providerPayload": {
            "artifact": "app", "packagePath": str(package), "sha256": "sha256:" + self.artifact_digest,
            "size": self.artifact.stat().st_size}}]
        self.write_plan()
        self.create()
        package.unlink()
        self.restore()
        self.assertEqual(package.read_bytes(), self.artifact.read_bytes())
        package.write_bytes(b"wrong archive")
        with self.assertRaisesRegex(recovery.RecoveryError, "bytes differ from plan"):
            self.create()


if __name__ == "__main__":
    unittest.main()