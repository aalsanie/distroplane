"""Safety checks for offline public-consumer documentation validation."""
import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE = Path(__file__).with_name('verify-readiness.py')
spec = importlib.util.spec_from_file_location('verify_readiness', MODULE)
verify = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verify)


class ReadinessContractTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'docs').mkdir()
        (self.root / 'maven-plugin/examples/npm').mkdir(parents=True)
        (self.root / 'internal/config/testdata/compatibility').mkdir(parents=True)
        (self.root / 'README.md').write_text('# Test\ndistroplane init\ndistroplane publish\ndistroplane resume\n[Docs](docs/production-readiness.md)\n')
        (self.root / 'docs/production-readiness.md').write_text('# Readiness\n')
        (self.root / 'docs/security-incident-response.md').write_text('# Incident\n')
        (self.root / 'maven-plugin/examples/npm/pom.xml').write_text('<distroplane.plugin.version>1.0.0</distroplane.plugin.version>')
        (self.root / 'maven-plugin/examples/npm/distroplane.json').write_text('{"schemaVersion":"2"}')

    def test_valid_fixture(self):
        count, problems = verify.verify_readiness(self.root)
        self.assertEqual(problems, [])
        self.assertEqual(count, 1)

    def test_rejects_missing_or_outside_path(self):
        readme = self.root / 'README.md'
        readme.write_text(readme.read_text() + '[Bad](docs/gone.md)\n[Escape](../outside.md)\n')
        self.assertEqual(len(verify.verify_links(self.root)[1]), 2)

    def test_ignores_fenced_documentation_and_remote_links(self):
        readme = self.root / 'README.md'
        readme.write_text(readme.read_text() + '```md\n[fixture](missing.md)\n```\n[GitHub](https://github.com/example/repo)\n')
        self.assertEqual(verify.verify_links(self.root)[1], [])

    def test_rejects_unverified_plugin_placeholder(self):
        (self.root / 'maven-plugin/examples/npm/pom.xml').write_text('<distroplane.plugin.version>0.0.0-NOT-PUBLISHED</distroplane.plugin.version>')
        self.assertTrue(any('Maven plugin' in p for p in verify.verify_readiness(self.root)[1]))

    def test_rejects_malformed_json(self):
        (self.root / 'maven-plugin/examples/npm/distroplane.json').write_text('{')
        self.assertTrue(any('invalid JSON' in p for p in verify.verify_readiness(self.root)[1]))

    def test_rejects_premature_catalog_claim(self):
        readme = self.root / 'README.md'
        readme.write_text(readme.read_text() + 'winget install --id Aalsanie.Distroplane\n')
        self.assertTrue(any('WinGet' in p for p in verify.verify_readiness(self.root)[1]))

    def test_rejects_missing_human_readiness_evidence(self):
        (self.root / 'docs/production-readiness.md').unlink()
        self.assertTrue(any('production readiness acceptance matrix' in p for p in verify.verify_readiness(self.root)[1]))


if __name__ == '__main__':
    unittest.main()
