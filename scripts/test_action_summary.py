"""Security and exact-upload semantics for post-upload Action summaries."""

import importlib.util
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).resolve().parents[1] / "action" / "summary.py"
MODULE = importlib.util.module_from_spec(importlib.util.spec_from_file_location("summary", SCRIPT))
MODULE.__spec__.loader.exec_module(MODULE)


def record(state="PUBLISHED", completed=True, reconcile=False, ambiguous=False, code=None):
    item = {"id": "npm-release", "state": state, "reconcileRequired": reconcile, "ambiguous": ambiguous}
    op = dict(item, id="publish-npm", targetId="npm-release")
    if code is not None:
        op["errorCode"] = code
    return {"outputSchemaVersion": "1", "planId": "plan-123", "runId": "run-123",
            "completed": completed, "cancelled": False, "targets": [item], "operations": [op]}


def env(command="publish", result=None, exit_code=0, execute="success"):
    if result is None:
        result = {"outputSchemaVersion": "1", "plan": {"outputSchemaVersion": "1", "planId": "plan-123",
                  "path": "/original/state/plan.json", "targets": 1, "operations": 1},
                  "approved": True, "run": record(), "journalPath": "/original/state/run.journal"}
    return {"SUMMARY_COMMAND": command, "SUMMARY_EXECUTE_OUTCOME": execute,
            "SUMMARY_EXIT_CODE": str(exit_code), "SUMMARY_RESULT_JSON": json.dumps(result),
            "SUMMARY_PLAN_ID": "plan-123", "SUMMARY_RUN_ID": "run-123",
            "GITHUB_REPOSITORY": "aalsanie/distroplane", "GITHUB_RUN_ID": "41800000001"}


def uploaded(data, kind="RECOVERY", aid="771"):
    root = "SUMMARY_" + kind
    data[root + "_OUTCOME"] = "success"
    data[root + "_ID"] = aid
    data[root + "_URL"] = f"https://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/{aid}"
    data[root + "_DIGEST"] = "sha256:" + "a" * 64
    return data


class SummaryTests(unittest.TestCase):
    def test_planning_without_upload_is_not_publication(self):
        data = env("plan", {"outputSchemaVersion": "1", "planId": "plan-123",
                            "path": "/repo/state/plan.json", "targets": 1, "operations": 1})
        data["SUMMARY_RUN_ID"] = ""
        output = MODULE.render(data)
        self.assertIn("Planned; no publication attempted", output)
        self.assertIn("exact plan ID", output)
        self.assertNotIn("published", output.lower())
        self.assertIn("No verified recovery artifact", output)

    def test_uploaded_plan_is_review_only(self):
        data = env("plan", {"outputSchemaVersion": "1", "planId": "plan-123",
                            "path": "/plan", "targets": 1, "operations": 1})
        data["SUMMARY_RUN_ID"] = ""
        uploaded(data, "PLAN_ARTIFACT", "611")
        output = MODULE.render(data)
        self.assertIn("Uploaded plan #611", output)
        self.assertIn("not publication evidence", output)
        self.assertNotIn("Recovery artifact: [", output)

    def test_successful_publish_reports_recorded_not_public_visibility(self):
        output = MODULE.render(env())
        self.assertIn("Recorded as published; public availability not independently verified", output)
        self.assertIn("Verify public destination availability independently", output)
        self.assertIn("`plan-123`", output)
        self.assertIn("`run-123`", output)

    def test_pending_with_successful_recovery_requires_original_workspace(self):
        data = env(exit_code=4)
        state = record("WAITING_EXTERNAL", False, True)
        value = json.loads(data["SUMMARY_RESULT_JSON"])
        value["run"] = state
        data["SUMMARY_RESULT_JSON"] = json.dumps(value)
        uploaded(data)
        output = MODULE.render(data)
        self.assertIn("Waiting for external review", output)
        self.assertIn("Uploaded bundle #771", output)
        self.assertIn("same runner OS, CPU architecture", output)
        self.assertIn("exact absolute `GITHUB_WORKSPACE` path", output)
        self.assertIn("not automatically compatible", output)
        self.assertIn("do not republish", output)

    def test_pending_without_upload_does_not_imply_persistence(self):
        data = env("resume", record("WAITING_EXTERNAL", False, True), 4)
        output = MODULE.render(data)
        self.assertIn("No verified recovery artifact is available", output)
        self.assertIn("Runner-local files are not guaranteed", output)
        self.assertIn("preserve the original state", output)

    def test_pending_failed_or_skipped_upload(self):
        for outcome in ("failure", "skipped", "cancelled", "", "success"):
            data = env("apply", record("WAITING_EXTERNAL", False, True), 4)
            uploaded(data)
            data["SUMMARY_RECOVERY_OUTCOME"] = outcome
            if outcome == "success":
                data["SUMMARY_RECOVERY_ID"] = ""
            with self.subTest(outcome=outcome):
                output = MODULE.render(data)
                self.assertNotIn("Uploaded bundle", output)
                self.assertIn("No verified recovery artifact", output)

    def test_ambiguous_has_priority_over_other_states(self):
        value = record("WAITING_EXTERNAL", False, True, True)
        output = MODULE.render(env("reconcile", value, 4))
        self.assertIn("may already have occurred", output)
        self.assertIn("Reconcile the original recorded run", output)
        self.assertIn("ambiguous external effect", output)

    def test_rejection_and_failure_are_never_silent(self):
        for state, code in (("REJECTED", 5), ("FAILED", 6), ("CANCELLED", 6)):
            with self.subTest(state=state):
                value = record(state, True)
                if state == "CANCELLED":
                    value["cancelled"] = True
                output = MODULE.render(env("resume", value, code))
                self.assertIn("do not retry automatically", output)
                self.assertNotIn("Recorded as published", output)

    def test_mixed_terminal_pending_and_ambiguous_targets(self):
        data = record("REJECTED", False)
        data["targets"].append({"id": "winget", "state": "WAITING_EXTERNAL", "reconcileRequired": True, "ambiguous": True})
        data["operations"].append({"id": "winget-submit", "targetId": "winget", "state": "WAITING_EXTERNAL", "reconcileRequired": True, "ambiguous": True})
        output = MODULE.render(env("resume", data, 5))
        self.assertIn("Mixed terminal and outstanding", output)
        self.assertIn("`winget`", output)
        self.assertIn("`npm-release`", output)
        self.assertIn("Reconcile the original recorded run", output)

    def test_truncated_journal_cannot_be_published(self):
        value = record()
        value["journalTruncatedTail"] = True
        output = MODULE.render(env("resume", value, 0))
        self.assertIn("Journal truncated", output)
        self.assertNotIn("Recorded as published", output)

    def test_completed_flag_does_not_prove_published(self):
        value = record("WAITING_EXTERNAL", True, True)
        output = MODULE.render(env("resume", value, 4))
        self.assertIn("Waiting for external review", output)
        self.assertNotIn("Recorded as published", output)

    def test_invalid_and_missing_json_drop_identity(self):
        for raw in ("", "{", "[]", "{}", '{"outputSchemaVersion":"999"}',
                    '{"outputSchemaVersion":"1","planId":"plan-123"}',
                    json.dumps({"outputSchemaVersion": "1", "planId": "plan-123\nFAKE"}),
                    " " * (4 * 1024 * 1024 + 1)):
            data = env()
            data["SUMMARY_RESULT_JSON"] = raw
            with self.subTest(raw=raw[:40]):
                result = MODULE.render(data)
                self.assertIn("CLI result unavailable or inconsistent", result)
                self.assertNotIn("`run-123`", result)

    def test_wrong_output_identity_drops_trust(self):
        data = env()
        data["SUMMARY_RUN_ID"] = "run-other"
        output = MODULE.render(data)
        self.assertIn("publication status unknown", output)
        self.assertNotIn("`run-other`", output)

    def test_setup_failure_and_untrusted_exit_codes(self):
        for outcome, code in (("failure", "0"), ("skipped", "4"), ("cancelled", "6"), ("success", "evil"), ("success", "3")):
            data = env(execute=outcome)
            data["SUMMARY_EXIT_CODE"] = code
            with self.subTest(outcome=outcome, code=code):
                output = MODULE.render(data)
                self.assertIn("original recorded run", output)
                self.assertNotIn("Recorded as published", output)

    def test_invalid_artifact_urls_and_identity_are_never_links(self):
        tamper = [
            ("SUMMARY_RECOVERY_URL", "https://evil.example/actions/runs/41800000001/artifacts/771"),
            ("SUMMARY_RECOVERY_URL", "https://github.com@evil.example/aalsanie/distroplane/actions/runs/41800000001/artifacts/771"),
            ("SUMMARY_RECOVERY_URL", "https://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/771?token=SECRET-CANARY"),
            ("SUMMARY_RECOVERY_URL", "https://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/772"),
            ("SUMMARY_RECOVERY_URL", "https://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/771#secret"),
            ("SUMMARY_RECOVERY_URL", "https://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/771\n[secret](https://evil.example)"),
            ("SUMMARY_RECOVERY_ID", "771)\n## Fake"),
            ("SUMMARY_RECOVERY_DIGEST", "sha256:wrong"),
            ("GITHUB_REPOSITORY", "evil/repo"),
            ("GITHUB_RUN_ID", "not-a-run"),
            ("SUMMARY_RECOVERY_URL", "http://github.com/aalsanie/distroplane/actions/runs/41800000001/artifacts/771"),
            ("SUMMARY_RECOVERY_URL", "https://github.com:444/aalsanie/distroplane/actions/runs/41800000001/artifacts/771"),
        ]
        for key, value in tamper:
            data = uploaded(env("resume", record("WAITING_EXTERNAL", False, True), 4))
            data[key] = value
            with self.subTest(key=key, value=value[:70]):
                output = MODULE.render(data)
                self.assertNotIn("Uploaded bundle", output)
                self.assertIn("No verified recovery artifact", output)
                self.assertNotIn("SECRET-CANARY", output)

    def test_artifact_links_require_actual_upload_success(self):
        data = env()
        uploaded(data, "PLAN_ARTIFACT", "101")
        uploaded(data, "EVIDENCE", "202")
        output = MODULE.render(data)
        self.assertIn("Uploaded plan #101", output)
        self.assertIn("Uploaded evidence #202", output)
        data["SUMMARY_EVIDENCE_OUTCOME"] = "failure"
        self.assertNotIn("Uploaded evidence #202", MODULE.render(data))

    def test_html_and_markdown_injection_never_rendered(self):
        data = env("resume", record("WAITING_EXTERNAL", False, True), 4)
        obj = json.loads(data["SUMMARY_RESULT_JSON"])
        obj["targets"][0]["id"] = "<script>alert(1)</script>"
        obj["targets"][0]["state"] = "[PUBLISHED](https://evil.example)"
        data["SUMMARY_RESULT_JSON"] = json.dumps(obj)
        data["SUMMARY_PLAN_ID"] = "plan-123\n::warning::Injected"
        data["SUMMARY_COMMAND"] = "publish\n## Injected"
        data["SUMMARY_RECOVERY_URL"] = "https://evil.example/" + "TOKEN-CANARY"
        output = MODULE.render(data)
        for needle in ("<script>", "## Injected", "::warning::Injected", "TOKEN-CANARY", "evil.example"):
            self.assertNotIn(needle, output)
        self.assertIn("unknown command", output)

    def test_malformed_target_and_error_body_are_suppressed(self):
        data = env("resume", record(), 0)
        obj = json.loads(data["SUMMARY_RESULT_JSON"])
        obj["targets"][0]["ambiguous"] = "true"
        data["SUMMARY_RESULT_JSON"] = json.dumps(obj)
        self.assertIn("publication status unknown", MODULE.render(data))
        data["SUMMARY_RESULT_JSON"] = json.dumps({"outputSchemaVersion": "1", "error": {
            "kind": "error", "message": "TOKEN-CANARY", "hint": "<script>"}})
        output = MODULE.render(data)
        self.assertNotIn("TOKEN-CANARY", output)
        self.assertNotIn("<script>", output)

    def test_nonstrings_and_malformed_structured_states_fail_closed(self):
        for hostile in ([], {}, 3, None, "<script>", "READY\n::warning::evil"):
            data = env("resume", record(), 0)
            value = json.loads(data["SUMMARY_RESULT_JSON"])
            value["targets"][0]["state"] = hostile
            data["SUMMARY_RESULT_JSON"] = json.dumps(value)
            with self.subTest(hostile=str(hostile)):
                output = MODULE.render(data)
                self.assertIn("publication status unknown", output)
                self.assertNotIn("<script>", output)

    def test_final_step_order_and_bounded_inputs_are_explicit(self):
        action_path = SCRIPT.parent.parent / "action.yml"
        if not action_path.exists() and os.environ.get("GITHUB_ACTIONS") != "true":
            self.skipTest("full repository action.yml unavailable in isolated local checkout")
        action = action_path.read_text(encoding="utf-8")
        names = ["    - name: Upload recovery", "    - name: Upload plan",
                 "    - name: Upload evidence", "    - name: Summarize Distroplane result",
                 "    - name: Enforce Distroplane result"]
        indices = [action.index(name) for name in names]
        self.assertEqual(indices, sorted(indices))
        summary = action[indices[3]:indices[4]]
        self.assertIn("if: ${{ always() }}", summary)
        self.assertIn("steps.upload-recovery.outcome", summary)
        self.assertIn("steps.upload-plan.outcome", summary)
        self.assertIn("steps.upload-evidence.outcome", summary)
        self.assertIn("steps.execute.outcome", summary)
        self.assertNotIn("GITHUB_OUTPUT", summary)
        self.assertNotIn("GITHUB_TOKEN", summary)
        self.assertIn("action/summary.py", summary)

    def test_no_summary_file_is_safe_noop(self):
        data = env()
        environment = {**os.environ, **data}
        environment.pop("GITHUB_STEP_SUMMARY", None)
        proc = subprocess.run([sys.executable, str(SCRIPT)], env=environment,
                              capture_output=True, text=True, check=False)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(proc.stdout.strip(), "::warning::Distroplane summary unavailable.")

    def test_summary_is_append_only_and_never_changes_machine_outputs(self):
        data = env()
        with tempfile.TemporaryDirectory() as directory:
            filename = str(Path(directory) / "summary.md")
            Path(filename).write_text("existing step summary\n", encoding="utf-8")
            data["GITHUB_STEP_SUMMARY"] = filename
            before = data.copy()
            proc = subprocess.run([sys.executable, str(SCRIPT)], env={**os.environ, **data},
                                  capture_output=True, text=True, check=False)
            self.assertEqual(proc.returncode, 0)
            self.assertEqual(proc.stdout, "")
            self.assertIn("existing step summary\n### Distroplane: publish", Path(filename).read_text())
            self.assertEqual(before, data)

    def test_unwritable_summary_path_does_not_fail(self):
        data = env()
        with tempfile.TemporaryDirectory() as directory:
            data["GITHUB_STEP_SUMMARY"] = directory  # Directory is not appendable.
            proc = subprocess.run([sys.executable, str(SCRIPT)], env={**os.environ, **data},
                                  capture_output=True, text=True, check=False)
            self.assertEqual(proc.returncode, 0)
            self.assertEqual(proc.stdout.strip(), "::warning::Distroplane summary unavailable.")


if __name__ == "__main__":
    unittest.main()
