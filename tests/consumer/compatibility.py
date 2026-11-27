import copy
import json
import os
import shutil
import struct
import subprocess
import tempfile
import unittest
import zlib
from pathlib import Path

from jsonschema import Draft202012Validator

from verify_evidence import journal_events, sha256, verify

ROOT = Path(__file__).resolve().parents[2]


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8"))


def run(binary, *args, expected=0):
    result = subprocess.run([str(binary), *map(str, args)], capture_output=True,
                            check=False, timeout=30)
    if result.returncode != expected:
        raise AssertionError(f"{args}: {result.returncode} != {expected}: {result.stderr!r}")
    return result


def output(binary, *args, expected=0):
    result = run(binary, *args, expected=expected)
    if result.stderr and expected == 0:
        raise AssertionError(f"unexpected diagnostic: {result.stderr!r}")
    return json.loads(result.stdout)


def frame(event):
    payload = json.dumps(event, separators=(",", ":")).encode("utf-8")
    return b"DPJ1" + struct.pack(">II", len(payload), zlib.crc32(payload)) + payload


def v2_from_fixture(legacy):
    target = legacy["targets"][0]
    options = copy.deepcopy(target["configuration"])
    artifact = options.pop("artifact")
    artifacts = {}
    for item in legacy["release"]["artifacts"]:
        artifacts[item["name"]] = {k: v for k, v in item.items() if k != "name"}
    return {"schemaVersion": "2",
            "release": {"id": legacy["release"]["id"], "artifacts": artifacts},
            "destinations": {target["provider"]["name"]: {
                "id": target["id"], "artifact": artifact, "options": options}}}


class Compatibility(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cli = Path(os.environ["DISTROPLANE_CONSUMER_CLI"]).resolve(strict=True)
        cls.legacy = Path(os.environ["DISTROPLANE_LEGACY_BIN"]).resolve(strict=True)
        cls.old_cli = cls.legacy / "distroplane"
        cls.old_provider = cls.legacy / "distroplane-provider-fake"

    def test_inventory_preserves_scalar_reader(self):
        inventory = read_json(ROOT / "protocol/schema/v1/compatibility.json")
        versions = inventory["schemaVersions"]
        self.assertEqual(versions, {"config": "1", "plan": "1", "evidence": "1",
                                    "journal": 2, "cliOutput": "1"})
        self.assertEqual(inventory["supportedConfigVersions"], ["1", "2"])
        self.assertEqual(set(inventory["normalizedStates"]), {
            "PLANNED", "READY", "RUNNING", "BLOCKED", "WAITING_EXTERNAL",
            "PUBLISHED", "REJECTED", "FAILED", "CANCELLED"})
        self.assertEqual(set(inventory["exitCodes"].values()), set(range(7)))

    def test_protocol_schema_and_examples(self):
        schema = read_json(ROOT / "protocol/schema/v1/protocol.schema.json")
        Draft202012Validator.check_schema(schema)
        full = Draft202012Validator(schema)
        examples = read_json(ROOT / "protocol/examples/v1/messages.json")
        for example in examples:
            with self.subTest(example=example["name"]):
                role = Draft202012Validator({"$defs": schema["$defs"],
                                             "$ref": "#/$defs/" + example["kind"]})
                msg = example["message"]
                full.validate(msg)
                role.validate(msg)
                future = copy.deepcopy(msg)
                future["futureEnvelope"] = {"extension": True}
                if "payload" in future:
                    future["payload"]["futureField"] = True
                role.validate(future)
                for key, value in (("protocolVersion", "2"), ("operation", "unknown"),
                                   ("requestId", "")):
                    invalid = dict(msg, **{key: value})
                    self.assertFalse(role.is_valid(invalid))
                if example["name"] == "describe response":
                    invalid = copy.deepcopy(msg)
                    invalid["payload"]["capabilities"].append("unknown")
                    self.assertFalse(role.is_valid(invalid))
                if example["name"] == "apply response":
                    invalid = copy.deepcopy(msg)
                    invalid["payload"]["result"]["state"] = "BLOCKED"
                    self.assertFalse(role.is_valid(invalid))

    def test_config_schema_matches_cli_fixtures(self):
        schema = read_json(ROOT / "schemas/config-v2.schema.json")
        Draft202012Validator.check_schema(schema)
        validator = Draft202012Validator(schema)
        fixtures = sorted((ROOT / "internal/config/testdata/compatibility").glob("*.json"))
        self.assertEqual(len(fixtures), 7)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"

            def check(value, accepted):
                path.write_text(json.dumps(value), encoding="utf-8")
                result = run(self.cli, "validate", "--config", path, "--json",
                             expected=0 if accepted else 3)
                if accepted:
                    self.assertEqual(json.loads(result.stdout)["schemaVersion"],
                                     value["schemaVersion"])
                else:
                    self.assertEqual(result.stdout, b"")
                    self.assertEqual(json.loads(result.stderr)["error"]["kind"], "invalid_config")

            for fixture in fixtures:
                with self.subTest(fixture=fixture.name):
                    old = read_json(fixture)
                    check(old, True)
                    value = v2_from_fixture(old)
                    validator.validate(value)
                    check(value, True)
                    for mutation in (dict(value, schemaVersion="99"),
                                     dict(value, unknown=True),
                                     dict(value, destinations={})):
                        self.assertFalse(validator.is_valid(mutation))
                        check(mutation, False)
                    unknown = copy.deepcopy(value)
                    next(iter(unknown["destinations"].values()))["options"]["unknown"] = "x"
                    self.assertFalse(validator.is_valid(unknown))
                    check(unknown, False)
                    array = copy.deepcopy(value)
                    name, destination = next(iter(array["destinations"].items()))
                    array["destinations"][name] = [destination, dict(destination, id="second")]
                    validator.validate(array)
                    check(array, True)
                    if name == "homebrew":
                        empty_binary = copy.deepcopy(value)
                        empty_binary["destinations"][name]["options"]["manifest"]["binary"] = ""
                        self.assertFalse(validator.is_valid(empty_binary))
                        check(empty_binary, False)

    def test_previous_release_recovery_and_refusals(self):
        self.assertEqual(output(self.old_cli, "version", "--json")["version"], "0.9.3")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "app.bin"
            artifact.write_bytes(b"historical release artifact\n")
            provider = root / "distroplane-provider-fake"
            shutil.copy2(self.old_provider, provider)
            config = root / "distroplane.json"
            config.write_text(json.dumps({
                "schemaVersion": "1", "release": {"id": "old-release", "artifacts": [
                    {"name": "app", "source": str(artifact)}]},
                "targets": [{"id": "fake", "provider": {"name": "fake",
                             "executable": str(provider)},
                             "configuration": {"mode": "waiting_external",
                                               "reconcileState": "published"}}]
            }), encoding="utf-8")
            pending = output(self.old_cli, "publish", "--config", config, "--yes", "--json",
                             expected=4)
            plan = Path(pending["plan"]["path"])
            journal = Path(pending["journalPath"])
            plan_bytes, journal_bytes = plan.read_bytes(), journal.read_bytes()
            doc = json.loads(plan_bytes)
            plan_id = doc["planId"]
            run_id = pending["run"]["runId"]
            self.assertEqual(doc["schemaVersion"], "1")
            self.assertEqual(doc["targets"][0]["provider"]["digest"], sha256(provider.read_bytes()))
            self.assertEqual(pending["run"]["targets"][0]["state"], "WAITING_EXTERNAL")
            events = journal_events(journal_bytes, plan_id, run_id)
            self.assertEqual(sum(e["type"] == "SIDE_EFFECT_DISPATCHED" for e in events), 1)
            provider.unlink()
            status = output(self.cli, "status", "--plan", plan, "--journal", journal,
                            "--json", expected=4)
            self.assertEqual(status["planId"], plan_id)
            self.assertEqual(status["runId"], run_id)
            self.assertEqual(journal.read_bytes(), journal_bytes)

            def refuse(saved_plan, saved_journal):
                before = {p: p.read_bytes() for p in (plan, journal, saved_plan, saved_journal)}
                result = run(self.cli, "apply", "--config", config, "--plan", saved_plan,
                             "--journal", saved_journal, "--json", expected=3)
                self.assertEqual(result.stdout, b"")
                error = json.loads(result.stderr)
                self.assertEqual(error["outputSchemaVersion"], "1")
                self.assertEqual(error["error"]["code"], "STATE_CORRUPT")
                for p, data in before.items():
                    self.assertEqual(p.read_bytes(), data)

            bad_plan = root / "bad-plan.json"
            bad_key = copy.deepcopy(doc)
            bad_key["operations"][0]["idempotencyKey"] = "invalid"
            bad_capability = copy.deepcopy(doc)
            bad_capability["targets"][0]["requiredCapabilities"].append("unknown")
            for changed in (dict(doc, schemaVersion="99"), dict(doc, futureField=True),
                            dict(doc, protocolVersion="99"), bad_key, bad_capability):
                bad_plan.write_text(json.dumps(changed), encoding="utf-8")
                refuse(bad_plan, journal)

            bad_journal = root / "bad.journal"
            for changed in (dict(events[0], schemaVersion=99),
                            dict(events[0], schemaVersion=1),
                            dict(events[0], type="FUTURE_EVENT"),
                            dict(events[0], futureField=True)):
                bad_journal.write_bytes(frame(changed) + b"DP")
                refuse(plan, bad_journal)
            bad_event = copy.deepcopy(events[1])
            bad_event["operationId"] = "unknown-operation"
            bad_journal.write_bytes(frame(events[0]) + frame(bad_event) + b"DP")
            refuse(plan, bad_journal)

            # The old release created the original retained executable and journal.
            # Recovery must work without the originally configured executable.
            resumed = output(self.cli, "resume", "--config", config, "--json")
            self.assertEqual(resumed["planId"], plan_id)
            self.assertEqual(resumed["runId"], run_id)
            self.assertTrue(resumed["completed"])
            self.assertEqual(resumed["operations"][0]["attempt"], 1)
            self.assertEqual(resumed["targets"][0]["state"], "PUBLISHED")
            self.assertEqual(plan.read_bytes(), plan_bytes)
            self.assertTrue(journal.read_bytes().startswith(journal_bytes))
            recovered = journal_events(journal.read_bytes(), plan_id, run_id)
            self.assertEqual(sum(e["type"] == "SIDE_EFFECT_DISPATCHED" for e in recovered), 1)
            bundle = root / "evidence.json"
            exported = output(self.cli, "evidence", "--plan", plan, "--journal", journal,
                              "--output", bundle, "--json")
            verify(bundle.read_bytes(), expected_digest=exported["digest"],
                   expected_plan=plan_id, expected_run=run_id, expected_state="PUBLISHED",
                   journal=journal, artifact=("app", artifact))
            final = journal.read_bytes()
            old_status = output(self.old_cli, "status", "--plan", plan, "--journal",
                                journal, "--json")
            self.assertEqual(old_status["planId"], plan_id)
            self.assertEqual(old_status["runId"], run_id)
            self.assertTrue(old_status["completed"])
            self.assertEqual(journal.read_bytes(), final)

    def test_init_output_and_refusal(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "app.tgz"
            artifact.write_bytes(b"fixture artifact")
            config = root / "distroplane.json"
            args = ("init", "--destination", "npm", "--release", "init-fixture",
                    "--artifact", artifact, "--output", config, "--non-interactive", "--json")
            initialized = output(self.cli, *args)
            self.assertEqual(initialized["outputSchemaVersion"], "1")
            self.assertEqual(Path(initialized["configPath"]), config)
            self.assertEqual(initialized["destinations"], ["npm"])
            self.assertEqual(Path(initialized["artifact"]["path"]), artifact)
            self.assertEqual(initialized["artifact"]["size"], artifact.stat().st_size)
            self.assertTrue(initialized["artifact"]["sha256"])
            Draft202012Validator(read_json(ROOT / "schemas/config-v2.schema.json")).validate(
                read_json(config))
            before = config.read_bytes()
            refused = run(self.cli, *args, expected=3)
            self.assertEqual(refused.stdout, b"")
            self.assertEqual(json.loads(refused.stderr)["error"]["kind"], "config_exists")
            self.assertEqual(config.read_bytes(), before)

    def test_cli_error_and_doctor_envelopes(self):
        with tempfile.TemporaryDirectory() as directory:
            missing = Path(directory) / "missing.json"
            result = run(self.cli, "validate", "--config", missing, "--json", expected=3)
            self.assertEqual(result.stdout, b"")
            error = json.loads(result.stderr)
            self.assertEqual(error["outputSchemaVersion"], "1")
            self.assertEqual(error["error"]["kind"], "invalid_config")
            self.assertEqual(error["error"]["code"], "MISSING_CONFIG")
            self.assertIsInstance(error["error"]["message"], str)
            self.assertIsInstance(error["error"]["hint"], str)
            doctor = output(self.cli, "doctor", "--config", missing, "--json", expected=3)
            self.assertEqual(doctor["outputSchemaVersion"], "1")
            self.assertFalse(doctor["healthy"])
            self.assertTrue(any(check["code"] == "MISSING_CONFIG" for check in doctor["checks"]))
            for check in doctor["checks"]:
                for key in ("code", "severity", "subject", "message"):
                    self.assertIsInstance(check[key], str)


if __name__ == "__main__":
    unittest.main(verbosity=2)
