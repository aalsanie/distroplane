"""Recorded publication outcomes shared with CLI and build-tool acceptance tests."""

import json
import unittest
from pathlib import Path

from test_action_summary import MODULE, env, uploaded


class ReleaseAcceptanceTests(unittest.TestCase):
    @staticmethod
    def cases():
        fixture = Path(__file__).resolve().parents[1] / "tests/consumer/release-guidance-cases.json"
        data = json.loads(fixture.read_text(encoding="utf-8"))
        assert data["schemaVersion"] == "1" and len(data["cases"]) == 19
        return data["cases"]

    def test_recorded_states_keep_same_run_and_do_not_infer_publication(self):
        outcomes = {
            "published": "Recorded as published; public availability not independently verified",
            "external": "Waiting for external review or reconciliation",
            "ambiguous": "Uncertain external outcome",
            "credential": "unavailable credential",
            "blocked": "unresolved prerequisite",
            "rejected": "Rejected by a destination",
            "failed": "Failed or cancelled",
            "mixed": "Mixed terminal and outstanding target outcomes",
            "truncated": "Journal truncated",
            "noop": "Completed no-op",
            "legacy": "Unsupported retry authorization",
            "incomplete": "Recorded run incomplete",
        }
        for case in self.cases():
            with self.subTest(case=case["name"]):
                data = env("resume", case["state"], case["exit"])
                data["SUMMARY_PLAN_ID"] = case["state"]["planId"]
                data["SUMMARY_RUN_ID"] = case["state"]["runId"]
                result = MODULE.render(data)
                self.assertIn(outcomes[case["kind"]], result)
                self.assertIn(f"**Plan:** `{MODULE.display_identity(case['state']['planId'])}`", result)
                self.assertIn(f"**Run:** `{MODULE.display_identity(case['state']['runId'])}`", result)
                expectation = case["expected"]
                self.assertIn("**Publication may already exist (recorded run):** "
                              + str(expectation["mayExist"]).lower(), result)
                self.assertIn(expectation["evidence"], result.lower())
                actions = {"reconcile": "reconcile-only", "resume": "resume the same run",
                           "status": "Inspect the original recorded run" if case["kind"] == "incomplete"
                           else "Inspect each recorded target outcome",
                           "manual": "manual recovery" if case["kind"] == "legacy" else "human decision",
                           "preserve": "durable prefix", "evidence": "recorded evidence",
                           "no-op": "no publication was attempted"}
                self.assertIn(actions[expectation["primary"]], result)
                self.assertIn("**Next:**", result)
                self.assertNotIn("publish --yes", result)
                self.assertNotIn("delete .distroplane", result)
                self.assertNotIn("SECRET-CANARY", result)
                if case["kind"] != "published":
                    self.assertNotIn("Recorded as published", result)
                if case["kind"] == "mixed":
                    self.assertIn("`npm`", result)
                    self.assertIn("`winget`", result)
                if case["kind"] == "ambiguous":
                    self.assertIn("may already have happened", result)
                if case["exit"] == 4:
                    self.assertIn("No verified recovery artifact is available", result)

    def test_pending_recovery_link_needs_verified_upload_and_same_identity(self):
        for case in self.cases():
            if case["exit"] != 4:
                continue
            with self.subTest(case=case["name"]):
                data = env("resume", case["state"], case["exit"])
                data["SUMMARY_PLAN_ID"] = case["state"]["planId"]
                data["SUMMARY_RUN_ID"] = case["state"]["runId"]
                uploaded(data, "RECOVERY", "771")
                summary = MODULE.render(data)
                self.assertIn("Uploaded bundle #771", summary)
                self.assertIn("same runner OS, CPU architecture", summary)
                self.assertIn("exact absolute `GITHUB_WORKSPACE`", summary)
                data["SUMMARY_RECOVERY_OUTCOME"] = "failure"
                self.assertNotIn("Uploaded bundle", MODULE.render(data))
                data["SUMMARY_RECOVERY_OUTCOME"] = "success"
                data["SUMMARY_RUN_ID"] = "different-run"
                fallback = MODULE.render(data)
                self.assertIn("publication status unknown", fallback)
                self.assertNotIn("`different-run`", fallback)

    def test_untrusted_additive_provider_fields_cannot_inject_summary(self):
        case = self.cases()[0]
        data = env("resume", case["state"], case["exit"])
        data["SUMMARY_PLAN_ID"] = case["state"]["planId"]
        data["SUMMARY_RUN_ID"] = case["state"]["runId"]
        payload = json.loads(data["SUMMARY_RESULT_JSON"])
        payload["providerMessage"] = "SECRET-CANARY\n::warning::publish --yes"
        data["SUMMARY_RESULT_JSON"] = json.dumps(payload)
        output = MODULE.render(data)
        self.assertIn("Recorded as published", output)
        self.assertNotIn("SECRET-CANARY", output)
        self.assertNotIn("::warning::", output)
        self.assertNotIn("publish --yes", output)


if __name__ == "__main__":
    unittest.main()
