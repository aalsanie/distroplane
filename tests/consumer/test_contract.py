"""Run the public CLI like an unrelated consumer and verify its evidence independently."""
import copy
import json
import os
import signal
import shutil
import time
from pathlib import Path
import subprocess
import tempfile
import unittest

from verify_evidence import journal_events, sha256, verify


def execute(binary, *args, expected=0, timeout=12):
    result = subprocess.run(
        [str(binary), *map(str, args)],
        capture_output=True, timeout=timeout, check=False,
    )
    if result.returncode != expected:
        raise AssertionError(
            f"CLI {args} returned {result.returncode}, expected {expected}\n"
            + result.stderr.decode(errors="replace")[-1200:]
        )
    if result.stdout:
        return json.loads(result.stdout.decode("utf-8"))
    return None


class ConsumerContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cli = Path(os.environ["DISTROPLANE_CONSUMER_CLI"]).resolve(strict=True)
        cls.provider = Path(os.environ["DISTROPLANE_CONSUMER_PROVIDER"]).resolve(strict=True)
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.artifact = cls.root / "app.bin"
        cls.artifact.write_bytes(b"independent cli evidence consumer\n")
        cls.config = cls.root / "distroplane.json"
        cls.config.write_text(json.dumps({
            "schemaVersion": "1",
            "release": {"id": "consumer-1.0.0", "artifacts": [
                {"name": "app", "source": str(cls.artifact), "mediaType": "application/octet-stream"}
            ]},
            "targets": [{
                "id": "fake",
                "provider": {"name": "fake", "executable": str(cls.provider)},
                "configuration": {"mode": "waiting_external", "reconcileState": "published"}
            }]
        }), encoding="utf-8")
        cls.journal = cls.root / "run.journal"
        cls.evidence_path = cls.root / "evidence.json"
        cls.evidence = None

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def test_01_cli_contract_and_approval_boundary(self):
        identity = execute(self.cli, "version", "--json")
        self.assertEqual(identity["outputSchemaVersion"], "1")
        self.assertTrue(identity["version"])
        self.assertTrue(identity["commit"])
        valid = execute(self.cli, "validate", "--config", self.config, "--json")
        self.assertTrue(valid["valid"])
        self.assertEqual(valid["targets"], 1)
        preview = execute(self.cli, "publish", "--config", self.config, "--dry-run", "--json")
        self.assertFalse(preview["approved"])
        self.assertEqual(preview["plan"]["outputSchemaVersion"], "1")
        self.assertEqual(preview["plan"]["targets"], 1)
        self.assertEqual(preview["plan"]["operations"], 1)
        self.assertFalse(self.journal.exists(), "dry run dispatched provider operations")
        error = execute(self.cli, "publish", "--config", self.config, "--json", expected=2)
        # A refused publication has no successful JSON response and cannot create a journal.
        self.assertIsNone(error)
        self.assertFalse(self.journal.exists())
        self.assertTrue(preview["plan"]["planId"])

    def test_02_pending_publish_reconcile_and_evidence(self):
        pending = execute(self.cli, "publish", "--config", self.config, "--yes", "--json", expected=4)
        self.assertEqual(pending["outputSchemaVersion"], "1")
        self.assertTrue(pending["approved"])
        self.assertEqual(pending["plan"]["operations"], 1)
        self.assertFalse(pending["run"]["completed"])
        self.assertEqual(pending["run"]["targets"][0]["state"], "WAITING_EXTERNAL")
        self.assertTrue(self.journal.exists() or Path(pending["journalPath"]).is_file())
        self.__class__.plan_id = pending["plan"]["planId"]
        self.__class__.plan_path = Path(pending["plan"]["path"])
        self.__class__.journal_path = Path(pending["journalPath"])
        self.__class__.run_id = pending["run"]["runId"]
        pending_evidence = execute(
            self.cli, "evidence", "--plan", self.plan_path, "--journal", self.journal_path,
            "--output", self.root / "pending-evidence.json", "--json",
        )
        verify((self.root / "pending-evidence.json").read_bytes(),
               expected_digest=pending_evidence["digest"],
               expected_plan=self.plan_id, expected_run=self.run_id,
               expected_state="WAITING_EXTERNAL", journal=self.journal_path,
               artifact=("app", self.artifact))

        resumed = execute(self.cli, "resume", "--config", self.config, "--json")
        self.assertEqual(resumed["outputSchemaVersion"], "1")
        self.assertEqual(resumed["planId"], self.plan_id)
        self.assertEqual(resumed["runId"], self.run_id)
        self.assertTrue(resumed["completed"])
        self.assertEqual(resumed["targets"][0]["state"], "PUBLISHED")
        self.assertEqual(resumed["operations"][0]["attempt"], 1,
                         "reconciliation introduced a second publication attempt")
        status = execute(self.cli, "status", "--plan", self.plan_path, "--journal", self.journal_path, "--json")
        self.assertEqual(status["runId"], self.run_id)
        self.assertTrue(status["completed"])
        exported = execute(
            self.cli, "evidence", "--plan", self.plan_path, "--journal", self.journal_path,
            "--output", self.evidence_path, "--json",
        )
        self.assertEqual(exported["planId"], self.plan_id)
        self.assertEqual(exported["runId"], self.run_id)
        self.assertEqual(exported["digest"], sha256(self.evidence_path.read_bytes()))
        self.__class__.evidence = self.evidence_path.read_bytes()
        bundle = verify(self.evidence,
                        expected_digest=exported["digest"],
                        expected_plan=self.plan_id,
                        expected_run=self.run_id,
                        expected_state="PUBLISHED",
                        journal=self.journal_path,
                        artifact=("app", self.artifact))
        self.assertEqual(bundle["release"]["id"], "consumer-1.0.0")
        self.assertEqual(bundle["targets"][0]["provider"]["name"], "fake")
        self.assertEqual(bundle["journal"]["reference"], self.journal_path.name)
        self.assertEqual(bundle["journal"]["schemaVersion"], 2)

    def test_03_completed_run_does_not_republish(self):
        before = self.journal_path.read_bytes()
        resume = execute(self.cli, "resume", "--config", self.config, "--json")
        self.assertEqual(resume["runId"], self.run_id)
        self.assertEqual(resume["planId"], self.plan_id)
        self.assertTrue(resume["completed"])
        self.assertEqual(self.journal_path.read_bytes(), before)
        execute(self.cli, "publish", "--config", self.config, "--yes", "--json", expected=3)
        self.assertEqual(self.journal_path.read_bytes(), before)

    def test_04_evidence_negative_mutations(self):
        original = json.loads(self.evidence)
        for label, change in [
            ("schema", lambda d: d.update(schemaVersion="2")),
            ("protocol", lambda d: d.update(protocolVersion="2")),
            ("plan", lambda d: d.update(planId="other")),
            ("run", lambda d: d["run"].update(id="other")),
            ("state", lambda d: d["targets"][0].update(state="UNSUPPORTED")),
            ("operation", lambda d: d["targets"][0]["operations"][0].update(state="FAILED")),
            ("artifact", lambda d: d["release"]["artifacts"][0].update(digest="sha256:" + "f" * 64)),
            ("journal", lambda d: d["journal"].update(digest="sha256:" + "0" * 64)),
            ("events", lambda d: d["journal"].update(events=0)),
            ("completed", lambda d: d["run"].update(completed=False)),
        ]:
            with self.subTest(case=label):
                changed = copy.deepcopy(original)
                change(changed)
                with self.assertRaises(ValueError):
                    verify(json.dumps(changed).encode(), expected_plan=self.plan_id,
                           expected_run=self.run_id, expected_state="PUBLISHED",
                           journal=self.journal_path, artifact=("app", self.artifact))

        with self.assertRaises(ValueError):
            verify(self.evidence, expected_digest="sha256:" + "0" * 64)
        with self.assertRaises(ValueError):
            verify(self.evidence.replace(b'"schemaVersion":"1"', b'"schemaVersion":"2"'),
                   expected_state="PUBLISHED")
        with self.assertRaises(ValueError):
            verify(b'{"schemaVersion":"1","schemaVersion":"1"}')
        with self.assertRaises(ValueError):
            verify(self.evidence + b" {}")

    def test_05_evidence_fails_on_mutated_bytes(self):
        with tempfile.TemporaryDirectory() as temp:
            journal_copy = Path(temp) / "tampered.journal"
            journal_copy.write_bytes(self.journal_path.read_bytes() + b"extra")
            with self.assertRaises(ValueError):
                verify(self.evidence, journal=journal_copy)
            artifact_copy = Path(temp) / "tampered.bin"
            artifact_copy.write_bytes(self.artifact.read_bytes() + b"extra")
            with self.assertRaises(ValueError):
                verify(self.evidence, artifact=("app", artifact_copy))

    def test_06_cli_refuses_changed_release_artifact_before_dispatch(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            copy = root / "app.bin"
            copy.write_bytes(b"before\n")
            config = root / "distroplane.json"
            source = json.loads(self.config.read_text())
            source["release"]["id"] = "consumer-tamper-1.0.0"
            source["release"]["artifacts"][0]["source"] = str(copy)
            source["targets"][0]["configuration"] = {"mode": "published"}
            config.write_text(json.dumps(source))
            plan = execute(self.cli, "plan", "--config", config, "--json")
            copy.write_bytes(b"tampered\n")
            journal = root / "run.journal"
            execute(self.cli, "apply", "--config", config, "--plan", plan["path"],
                    "--journal", journal, "--json", expected=3)
            self.assertFalse(journal.exists(), "tampered artifact reached publication")


    @unittest.skipIf(os.name != "posix", "controlled process-group crash requires POSIX")
    def test_07_crash_after_durable_dispatch_reconciles_without_republish(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            fixture = Path(__file__).resolve().parents[2] / "protocol/conformance/external_provider.py"
            provider = root / "distroplane-provider-external--normal"
            shutil.copyfile(fixture, provider)
            provider.chmod(0o700)
            artifact = root / "crash.bin"
            artifact.write_bytes(b"crash recovery fixture\n")
            audit = root / "effects.log"
            config = root / "distroplane.json"
            config.write_text(json.dumps({
                "schemaVersion": "1",
                "release": {"id": "crash-1.0.0", "artifacts": [{
                    "name": "app", "source": str(artifact),
                }]},
                "targets": [{
                    "id": "external",
                    "provider": {"name": "external", "executable": str(provider)},
                    "configuration": {"auditFile": str(audit), "blockAfterApply": True},
                }],
            }), encoding="utf-8")
            planned = execute(self.cli, "plan", "--config", config, "--json")
            plan = Path(planned["path"])
            plan_id = planned["planId"]
            journal = root / "run.journal"
            run_id = "run-consumer-crash"
            child = subprocess.Popen([
                str(self.cli), "apply", "--config", str(config),
                "--plan", str(plan), "--journal", str(journal),
                "--run", run_id, "--json",
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
            try:
                deadline = time.monotonic() + 12
                observed = False
                while time.monotonic() < deadline and child.poll() is None:
                    if audit.is_file() and audit.read_text() == "apply\n" and journal.is_file():
                        try:
                            events = journal_events(journal.read_bytes(), plan_id, run_id)
                            observed = any(e["type"] == "SIDE_EFFECT_DISPATCHED" for e in events)
                        except ValueError:
                            pass  # A concurrent writer may be between frames.
                        if observed:
                            break
                    time.sleep(0.05)
                self.assertTrue(observed, "side effect did not occur after a durable dispatch")
                self.assertIsNone(child.poll(), "publisher returned before forced interruption")
            finally:
                if child.poll() is None:
                    os.killpg(child.pid, signal.SIGKILL)
                child.communicate(timeout=8)

            self.assertEqual(audit.read_text(), "apply\n", "unexpected external publication count")
            events = journal_events(journal.read_bytes(), plan_id, run_id)
            self.assertIn("SIDE_EFFECT_DISPATCHED", [event["type"] for event in events])
            self.assertNotIn("PROVIDER_RESPONSE_RECEIVED", [event["type"] for event in events])

            snapshot = root / "interrupted-evidence.json"
            exported = execute(self.cli, "evidence", "--plan", plan, "--journal", journal,
                               "--output", snapshot, "--json")
            verify(snapshot.read_bytes(), expected_digest=exported["digest"],
                   expected_plan=plan_id, expected_run=run_id, expected_state="RUNNING",
                   journal=journal, artifact=("app", artifact))

            # The crashed process retained a durable 30-second lease. Resume must
            # await its expiry, reconcile with the external state, and never reapply.
            resumed = execute(self.cli, "resume", "--config", config, "--run", run_id,
                              "--json", timeout=75)
            self.assertEqual(resumed["planId"], plan_id)
            self.assertEqual(resumed["runId"], run_id)
            self.assertTrue(resumed["completed"])
            self.assertEqual(resumed["targets"][0]["state"], "PUBLISHED")
            self.assertEqual(resumed["operations"][0]["attempt"], 1)
            self.assertEqual(audit.read_text(), "apply\n", "recovery dispatched a second publication")
            completed = root / "completed-evidence.json"
            output = execute(self.cli, "evidence", "--plan", plan, "--journal", journal,
                             "--output", completed, "--json")
            verify(completed.read_bytes(), expected_digest=output["digest"],
                   expected_plan=plan_id, expected_run=run_id, expected_state="PUBLISHED",
                   journal=journal, artifact=("app", artifact))
            execute(self.cli, "resume", "--config", config, "--run", run_id, "--json")
            self.assertEqual(audit.read_text(), "apply\n", "completed replay dispatched a publication")



if __name__ == "__main__":
    unittest.main(verbosity=2)
