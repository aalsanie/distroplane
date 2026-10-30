"""Prevent manual release rehearsals from gaining publication privileges."""
import re
import unittest
from pathlib import Path

WORKFLOW = Path(__file__).resolve().parents[1] / ".github/workflows/release.yml"


def job(name, contents):
    pattern = rf"(?ms)^  {re.escape(name)}:\n(.*?)(?=^  [a-z][a-z-]*:\n|\Z)"
    match = re.search(pattern, contents)
    if match is None:
        raise AssertionError(f"missing workflow job: {name}")
    return match.group(1)


class ReleaseWorkflowTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.content = WORKFLOW.read_text(encoding="utf-8")

    def test_rehearsal_requires_main_and_confirmation(self):
        self.assertIn("workflow_dispatch:", self.content)
        self.assertIn("confirmation:", self.content)
        for name in ("windows-installer", "stage"):
            section = job(name, self.content)
            self.assertIn("REHEARSE", section)
            self.assertIn("refs/heads/main", section)
        self.assertIn("Record non-publishing release rehearsal", self.content)

    def test_publication_only_uses_tag_push(self):
        publish = job("publish", self.content)
        self.assertIn("if: ${{ github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v') }}", publish)
        self.assertNotIn("workflow_dispatch", publish)
        tap = job("homebrew-tap-update", self.content)
        self.assertIn("github.event_name == 'push'", tap)
        self.assertNotIn("workflow_dispatch", tap)
        self.assertIn("contents: write", publish)
        before_jobs = self.content.split("\njobs:", 1)[0]
        self.assertIn("contents: read", before_jobs)
        self.assertNotIn("contents: write", before_jobs)
        self.assertNotIn("id-token: write", before_jobs)

    def test_exact_source_and_native_checks(self):
        self.assertEqual(self.content.count("ref: ${{ github.sha }}"), 6)
        self.assertNotIn("ref: ${{ github.ref }}", self.content)
        self.assertIn('main_sha="$(gh api "repos/$GITHUB_REPOSITORY/git/ref/heads/main"', job("stage", self.content))
        self.assertIn("require_success \"ci.yml\" \"CI\"", job("stage", self.content))
        self.assertIn("require_success \"action-smoke.yml\" \"GitHub Action smoke\"", job("stage", self.content))
        self.assertIn("$version = (Get-Content VERSION -Raw).Trim()", job("native-install", self.content))
        self.assertIn("needs: [stage, native-install]", job("rehearsal-report", self.content))
        self.assertIn("needs: [stage, native-install]", job("publish", self.content))

    def test_public_downloads_verified_before_tap_update(self):
        consumer = job("public-smoke", self.content)
        self.assertIn("needs: publish", consumer)
        self.assertIn("github.event_name == 'push'", consumer)
        self.assertIn("gh release download", consumer)
        self.assertIn("gh attestation verify", consumer)
        self.assertIn("scripts/smoke-release-native.py", consumer)
        self.assertIn("Missing or ambiguous published checksum", consumer)
        tap = job("homebrew-tap-update", self.content)
        self.assertIn("needs: [publish, public-smoke]", tap)


if __name__ == "__main__":
    unittest.main()
