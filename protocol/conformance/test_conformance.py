#!/usr/bin/env python3
"""Black-box v1 protocol tests against external and native provider executables."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

FIXTURE = Path(__file__).with_name("external_provider.py")
LIMIT = 1 << 20
CAPABILITIES = {"plan", "apply", "reconcile"}
ERRORS = {
    "PROTOCOL_ERROR", "CONFIGURATION_ERROR", "AUTHENTICATION_ERROR",
    "AUTHORIZATION_ERROR", "TRANSIENT_EXTERNAL_ERROR", "PERMANENT_EXTERNAL_ERROR",
    "REJECTED", "TIMEOUT", "CANCELLED", "PROVIDER_INTERNAL_ERROR", "AMBIGUOUS_OUTCOME",
}
BIN = None
CLI = None


def request(op, payload, request_id="external-correlation-1", version="1"):
    return {"protocolVersion": version, "requestId": request_id, "operation": op, "payload": payload}


def plan_payload(configuration=None):
    return {
        "release": {"id": "v1.2.3", "artifacts": [{
            "name": "package", "digest": "sha256:" + "a" * 64, "size": 42,
        }]},
        "target": {"id": "external-target", "configuration": configuration or {}},
    }


def execution_payload(configuration=None):
    return {
        "planId": "plan-sha256", "targetId": "external-target",
        "operationId": "publish", "idempotencyKey": "unchanged-across-reconcile",
        "attempt": 1, "providerPayload": configuration or {},
    }


def raw_run(command, raw, *, timeout=4, environment=None):
    return subprocess.run(
        command, input=raw, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        timeout=timeout, env=environment, check=False,
    )


def invoke(command, message, **kwargs):
    return raw_run(command, json.dumps(message, separators=(",", ":")).encode(), **kwargs)


class ProtocolConformance(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if CLI is None or BIN is None:
            raise AssertionError("--cli and --bin-dir are required")
        cls.extension = ".exe" if os.name == "nt" else ""
        cls.cli = Path(CLI).resolve()
        cls.bin = Path(BIN).resolve()
        for path in [cls.cli] + [cls.bin / ("distroplane-provider-" + name + cls.extension)
                                  for name in ("fake", "npm", "sdkman", "homebrew", "winget")]:
            if not path.is_file():
                raise AssertionError("missing independent binary: " + str(path))
        if os.name == "nt":
            raise unittest.SkipTest("external script executable integration requires a POSIX runner")

    def run_external(self, message, mode="normal", **kwargs):
        return invoke([sys.executable, str(FIXTURE), "--mode", mode], message, **kwargs)

    def check_envelope(self, process, sent, status="ok", expected_version=None):
        self.assertEqual(process.returncode, 0, process.stderr.decode(errors="replace")[-1500:])
        self.assertLessEqual(len(process.stdout), LIMIT)
        self.assertTrue(process.stdout)
        decoded = json.loads(process.stdout.decode("utf-8"), object_pairs_hook=self.no_duplicates)
        self.assertEqual(decoded["protocolVersion"], expected_version or sent["protocolVersion"])
        self.assertEqual(decoded["requestId"], sent["requestId"])
        self.assertEqual(decoded["operation"], sent["operation"])
        self.assertEqual(decoded["status"], status)
        if status == "ok":
            self.assertIn("payload", decoded)
            self.assertNotIn("error", decoded)
        else:
            self.assertNotIn("payload", decoded)
            failure = decoded["error"]
            self.assertIn(failure["code"], ERRORS)
            self.assertTrue(isinstance(failure["message"], str) and failure["message"])
            self.assertIs(type(failure["retryable"]), bool)
        return decoded

    @staticmethod
    def no_duplicates(pairs):
        obj = {}
        for key, value in pairs:
            if key in obj:
                raise ValueError("duplicate field: " + key)
            obj[key] = value
        return obj

    def test_cross_language_describe_and_capability_advertisement(self):
        message = request("describe", {}, "non-go-describe-001")
        response = self.check_envelope(self.run_external(message), message)["payload"]
        self.assertEqual(response["provider"], {"name": "external", "version": "1.0.0"})
        self.assertIn("1", response["protocolVersions"])
        self.assertEqual(set(response["capabilities"]), CAPABILITIES)

    def test_cross_language_plan_apply_reconcile(self):
        with tempfile.TemporaryDirectory() as tmp:
            audit = str(Path(tmp) / "effects.log")
            cfg = {"auditFile": audit, "mode": "waiting", "custom": {"future": 17}}
            plan_msg = request("plan", plan_payload(cfg), "plan-external-2")
            planned = self.check_envelope(self.run_external(plan_msg), plan_msg)["payload"]
            operations = planned["operations"]
            self.assertEqual(len(operations), 1)
            self.assertEqual(operations[0]["id"], "publish")
            self.assertTrue(operations[0]["sideEffecting"])
            self.assertEqual(operations[0]["providerPayload"], cfg)
            self.assertGreater(operations[0]["timeoutMillis"], 0)

            execution = execution_payload(operations[0]["providerPayload"])
            apply_msg = request("apply", execution, "apply-external-3")
            applied = self.check_envelope(self.run_external(apply_msg), apply_msg)["payload"]["result"]
            self.assertEqual(applied["state"], "WAITING_EXTERNAL")
            self.assertTrue(applied["evidence"])
            self.assertEqual(Path(audit).read_text(), "apply\n")

            reconcile_msg = request(
                "reconcile", dict(execution, attempt=2, previous=applied), "reconcile-external-4"
            )
            reconciled = self.check_envelope(
                self.run_external(reconcile_msg), reconcile_msg
            )["payload"]["result"]
            self.assertEqual(reconciled["state"], "PUBLISHED")
            self.assertEqual(Path(audit).read_text(), "apply\n", "reconcile repeated a side effect")
            self.assertEqual(execution["idempotencyKey"], reconcile_msg["payload"]["idempotencyKey"])

    def test_cross_language_structured_errors_and_unknown_contract(self):
        transient = request("apply", execution_payload({"mode": "transient"}))
        error = self.check_envelope(self.run_external(transient), transient, "error")["error"]
        self.assertEqual(error["code"], "TRANSIENT_EXTERNAL_ERROR")
        self.assertTrue(error["retryable"])
        for changed in (request("describe", {}, version="2"),
                        request("unknown-future", {})):
            response = self.check_envelope(self.run_external(changed), changed, "error", expected_version="1")
            self.assertEqual(response["error"]["code"], "PROTOCOL_ERROR")

    def test_cross_language_rejects_bad_request_framing(self):
        valid = json.dumps(request("describe", {})).encode()
        for raw in (b"not-json", b"{}", valid + b"{}", b"[]",
                    b'{"protocolVersion":"1","protocolVersion":"1","requestId":"x","operation":"describe","payload":{}}',
                    b" " * (LIMIT + 1), b"\xff"):
            with self.subTest(raw=raw[:45]):
                outcome = raw_run([sys.executable, str(FIXTURE)], raw)
                self.assertNotEqual(outcome.returncode, 0)
                self.assertEqual(outcome.stdout, b"")

    def test_native_provider_describe_and_invalid_plan(self):
        for name in ("fake", "npm", "sdkman", "homebrew", "winget"):
            binary = str(self.bin / ("distroplane-provider-" + name + self.extension))
            with self.subTest(provider=name):
                describe = request("describe", {}, "native-" + name)
                info = self.check_envelope(invoke([binary], describe), describe)["payload"]
                self.assertEqual(info["provider"]["name"], name)
                self.assertTrue(info["provider"]["version"])
                self.assertIn("1", info["protocolVersions"])
                self.assertEqual(set(info["capabilities"]), CAPABILITIES)

                invalid = request("plan", {}, "invalid-" + name)
                response = self.check_envelope(invoke([binary], invalid), invalid, "error")
                self.assertEqual(response["error"]["code"], "PROTOCOL_ERROR")
                future = request("describe", {}, "future-" + name, version="2")
                response = self.check_envelope(invoke([binary], future), future, "error", expected_version="1")
                self.assertEqual(response["error"]["code"], "PROTOCOL_ERROR")

    def test_native_safe_apply_and_reconcile_states(self):
        binary = str(self.bin / ("distroplane-provider-fake" + self.extension))
        plan = request("plan", plan_payload({"mode": "waiting_external"}), "native-plan")
        operations = self.check_envelope(invoke([binary], plan), plan)["payload"]["operations"]
        self.assertEqual(len(operations), 1)
        self.assertTrue(operations[0]["sideEffecting"])
        execution = execution_payload(operations[0]["providerPayload"])
        applied = request("apply", execution, "native-apply")
        result = self.check_envelope(invoke([binary], applied), applied)["payload"]["result"]
        self.assertEqual(result["state"], "WAITING_EXTERNAL")
        self.assertTrue(result["evidence"])
        observed = request("reconcile", dict(execution, previous=result), "native-reconcile")
        response = self.check_envelope(invoke([binary], observed), observed)["payload"]["result"]
        self.assertEqual(response["state"], "WAITING_EXTERNAL")
        self.assertTrue(response["evidence"])

    def test_native_unknown_envelope_fields_are_ignored(self):
        binary = str(self.bin / ("distroplane-provider-fake" + self.extension))
        message = request("describe", {})
        message["futureExtension"] = {"v": 1}
        self.check_envelope(invoke([binary], message), message)

    def test_native_provider_rejects_oversized_and_duplicate_requests(self):
        binary = str(self.bin / ("distroplane-provider-fake" + self.extension))
        for raw in (
            b" " * (LIMIT + 1),
            b'{"protocolVersion":"1","requestId":"r","requestId":"other","operation":"describe","payload":{}}',
            b'{"protocolVersion":"1","requestId":"r","operation":"describe","payload":{}} {}',
        ):
            with self.subTest(raw=raw[:70]):
                outcome = raw_run([binary], raw)
                self.assertNotEqual(outcome.returncode, 0)
                self.assertEqual(outcome.stdout, b"")

    def test_native_provider_process_failure_fixtures(self):
        binary = str(self.bin / ("distroplane-provider-fake" + self.extension))
        desc = request("describe", {}, "failure-fixture")
        wire = json.dumps(desc).encode()
        for mode in ("empty", "malformed", "oversized", "trailing", "mismatched_request_id", "crash"):
            with self.subTest(mode=mode):
                environment = dict(os.environ, DISTROPLANE_FAKE_PROCESS_MODE=mode)
                result = raw_run([binary], wire, environment=environment)
                if mode == "crash":
                    self.assertNotEqual(result.returncode, 0)
                elif mode == "mismatched_request_id":
                    self.assertNotEqual(json.loads(result.stdout)["requestId"], desc["requestId"])
                elif mode == "oversized":
                    self.assertGreater(len(result.stdout), LIMIT)
                elif mode == "empty":
                    self.assertEqual(result.stdout, b"")
                elif mode == "malformed":
                    with self.assertRaises(json.JSONDecodeError):
                        json.loads(result.stdout)
                else:
                    with self.assertRaises(json.JSONDecodeError):
                        json.loads(result.stdout)
        environment = dict(os.environ, DISTROPLANE_FAKE_PROCESS_MODE="timeout",
                           DISTROPLANE_FAKE_DELAY_MS="3000")
        with self.assertRaises(subprocess.TimeoutExpired):
            raw_run([binary], wire, timeout=0.15, environment=environment)

    def cli_fixture(self, root, mode="normal", configuration=None):
        executable = Path(root) / ("distroplane-provider-external--" + mode)
        shutil.copyfile(FIXTURE, executable)
        executable.chmod(0o700)
        artifact = Path(root) / "release.bin"
        artifact.write_bytes(b"controlled external provider artifact\n")
        config = Path(root) / "distroplane.json"
        config.write_text(json.dumps({
            "schemaVersion": "1",
            "release": {"id": "external-1.0.0", "artifacts": [{
                "name": "package", "source": str(artifact),
            }]},
            "targets": [{
                "id": "external-target",
                "provider": {"name": "external", "executable": str(executable)},
                "configuration": configuration or {},
            }],
        }), encoding="utf-8")
        return config

    def cli_plan(self, root, mode="normal", configuration=None):
        config = self.cli_fixture(root, mode, configuration)
        process = subprocess.run(
            [str(self.cli), "plan", "--config", str(config),
             "--state-dir", str(Path(root) / "plans"), "--json"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=8, check=False,
        )
        return process, config

    def test_cli_uses_independent_provider_and_reconciles_without_republish(self):
        with tempfile.TemporaryDirectory() as tmp:
            audit = Path(tmp) / "effects.log"
            planned, config = self.cli_plan(tmp, configuration={"mode": "waiting", "auditFile": str(audit)})
            self.assertEqual(planned.returncode, 0, planned.stderr.decode(errors="replace"))
            saved = json.loads(planned.stdout)
            self.assertTrue(Path(saved["path"]).is_file())
            journal = str(Path(tmp) / "run.journal")
            base = [str(self.cli), "--config", str(config), "--plan", saved["path"],
                    "--journal", journal, "--json"]
            applied = subprocess.run([base[0], "apply"] + base[1:],
                                     capture_output=True, timeout=8, check=False)
            self.assertEqual(applied.returncode, 4, applied.stderr.decode(errors="replace"))
            self.assertEqual(audit.read_text(), "apply\n")
            reconciled = subprocess.run([base[0], "reconcile"] + base[1:],
                                        capture_output=True, timeout=8, check=False)
            self.assertEqual(reconciled.returncode, 0, reconciled.stderr.decode(errors="replace"))
            self.assertEqual(audit.read_text(), "apply\n", "CLI reconciliation republished")

    def test_cli_rejects_invalid_external_provider_responses(self):
        cases = ("wrong-id", "wrong-version", "wrong-operation", "malformed",
                 "oversized", "empty", "crash", "dual-envelope",
                 "unsupported-version", "unknown-capability", "missing-capability",
                 "invalid-plan")
        for mode in cases:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                result, _ = self.cli_plan(tmp, mode)
                self.assertNotEqual(result.returncode, 0, mode)
                self.assertTrue(result.stderr.strip(), mode)

    def test_external_provider_timeout_is_bounded(self):
        message = request("describe", {})
        with self.assertRaises(subprocess.TimeoutExpired):
            self.run_external(message, mode="timeout", timeout=0.15)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cli", required=True)
    parser.add_argument("--bin-dir", required=True)
    arguments = parser.parse_args()
    CLI, BIN = arguments.cli, arguments.bin_dir
    unittest.main(argv=[sys.argv[0]], verbosity=2)
