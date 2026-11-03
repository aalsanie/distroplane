"""Verify that Gradle Plugin Portal publication remains explicitly authorized."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github/workflows/gradle-plugin-publish.yml"
BUILD = ROOT / "gradle-plugin/build.gradle.kts"


class GradlePublishWorkflowTest(unittest.TestCase):
    def setUp(self):
        self.workflow = WORKFLOW.read_text(encoding="utf-8")
        self.build = BUILD.read_text(encoding="utf-8")

    def test_publication_only_runs_on_explicit_confirmed_dispatch(self):
        self.assertIn("  workflow_dispatch:", self.workflow)
        self.assertNotIn("  push:", self.workflow)
        self.assertNotIn("  schedule:", self.workflow)
        self.assertIn("$CONFIRMATION\" != 'PUBLISH'", self.workflow)
        self.assertIn("refs/heads/main", self.workflow)
        self.assertIn("gradle-plugin/v${VERSION}", self.workflow)
        self.assertIn("git cat-file -t FETCH_HEAD", self.workflow)

    def test_published_commit_is_tested_and_unchanged_after_approval(self):
        self.assertIn('actions/workflows/ci.yml/runs?event=push&branch=main&head_sha=', self.workflow)
        self.assertIn('"$source_sha" != "$main_sha"', self.workflow)
        self.assertIn('"$(git rev-parse HEAD)"', self.workflow)
        self.assertIn('ref: ${{ needs.preflight.outputs.source_sha }}', self.workflow)
        self.assertIn('Reverify immutable release source after approval', self.workflow)
        self.assertIn("git ls-remote origin refs/heads/main", self.workflow)

    def test_requires_review_and_keeps_credentials_out_of_preflight(self):
        self.assertIn("environments/gradle-plugin", self.workflow)
        self.assertIn(".can_admins_bypass == false", self.workflow)
        self.assertIn("name: gradle-plugin", self.workflow)
        before_publish, publish = self.workflow.split("  publish:\n", 1)
        self.assertNotIn("secrets.GRADLE_PUBLISH_", before_publish)
        self.assertIn("secrets.GRADLE_PUBLISH_KEY", publish)
        self.assertIn("secrets.GRADLE_PUBLISH_SECRET", publish)
        self.assertIn("cancel-in-progress: false", self.workflow)

    def test_validation_is_before_publication_and_coverage_remains_strict(self):
        self.assertIn("clean check publishPlugins --validate-only", self.workflow)
        self.assertIn("gradle -p gradle-plugin publishPlugins --no-daemon", self.workflow)
        self.assertLess(self.workflow.index("clean check publishPlugins --validate-only"),
                        self.workflow.index("gradle -p gradle-plugin publishPlugins --no-daemon"))
        self.assertIn('minimum = "0.9001".toBigDecimal()', self.build)
        self.assertIn('counter = "BRANCH"', self.build)
        self.assertIn('counter = "LINE"', self.build)

    def test_publication_identity_and_compatibility(self):
        self.assertIn('id("com.gradle.plugin-publish") version "2.2.1"', self.build)
        self.assertIn('providers.environmentVariable("DISTROPLANE_GRADLE_PLUGIN_VERSION")', self.build)
        self.assertIn('"1.0.0-SNAPSHOT"', self.build)
        self.assertIn('id = "io.github.aalsanie.distroplane"', self.build)
        self.assertIn('configurationCache = false', self.build)
        self.assertIn('isolatedProjects = false', self.build)
        self.assertIn('website = "https://github.com/aalsanie/distroplane"', self.build)
        self.assertIn('vcsUrl = "https://github.com/aalsanie/distroplane"', self.build)


if __name__ == "__main__":
    unittest.main()
