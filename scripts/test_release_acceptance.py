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
        assert data["schemaVersion"] == "1" and len(data["cases"]) == 12
        return data["cases"]

    def test_recorded_states_keep_same_run_and_do_not_infer_publication(self):
        outcomes = {
            "published": "Recorded as published; public availability not independently verified",
            "external": "Waiting for external review or reconciliation",
            "ambiguous": "Uncertain external outcome",
            "credential": "Blocked before completion",
            "blocked": "Blocked before completion",
            "rejected": "Rejected by a destination",
            "failed": "Failed or cancelled",
            "mixed": "Mixed terminal and outstanding target outcomes",
            "truncated": "Journal truncated",
        }
        for case in self.cases():
            with self.subTest(case=case["name"]):
                data = env("resume", case["state"], case["exit"])
                data["SUMMARY_PLAN_ID"] = "plan-acceptance"
                data["SUMMARY_RUN_ID"] = "run-acceptance"
                result = MODULE.render(data)
                self.assertIn(outcomes[case["kind"]], result)
                self.assertIn("**Plan:** `plan-acceptance`", result)
                self.assertIn("**Run:** `run-acceptance`", result)
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
                data["SUMMARY_PLAN_ID"] = "plan-acceptance"
                data["SUMMARY_RUN_ID"] = "run-acceptance"
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
        data["SUMMARY_PLAN_ID"] = "plan-acceptance"
        data["SUMMARY_RUN_ID"] = "run-acceptance"
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
