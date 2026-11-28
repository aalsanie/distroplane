"""Check that user-facing documentation links stay valid."""
import re
import unittest
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
LINK = re.compile(r'(?<!!)\[[^\]]+\]\(([^\s)]+)(?:\s+"[^"]*")?\)')


def markdown_files():
    for name in ("README.md", "SECURITY.md", "CONTRIBUTING.md"):
        yield ROOT / name
    for folder in ("docs", "providers", "maven-plugin", "gradle-plugin"):
        yield from sorted((ROOT / folder).rglob("*.md"))


class DocumentationTest(unittest.TestCase):
    def test_local_links(self):
        for source in markdown_files():
            fenced = False
            for line_number, line in enumerate(source.read_text(encoding="utf-8").splitlines(), 1):
                if line.lstrip().startswith("```"):
                    fenced = not fenced
                    continue
                if fenced:
                    continue
                for match in LINK.finditer(line):
                    url = urlsplit(match.group(1))
                    if url.scheme or url.netloc or not url.path:
                        continue
                    target = (source.parent / unquote(url.path)).resolve()
                    with self.subTest(file=str(source.relative_to(ROOT)), line=line_number, link=match.group(1)):
                        self.assertTrue(target.is_relative_to(ROOT) and target.is_file())

    def test_release_examples_use_project_version(self):
        version = (ROOT / "VERSION").read_text(encoding="utf-8").strip()
        self.assertRegex(version, r"^[0-9]+\.[0-9]+\.[0-9]+$")
        expected = {
            "README.md": (r"(?m)^\s+version: ([0-9]+\.[0-9]+\.[0-9]+)$", 1),
            "docs/github-actions.md": (r"(?m)^\s+version: ([0-9]+\.[0-9]+\.[0-9]+)$", 2),
            "docs/windows-installer.md": (r"-Version ([0-9]+\.[0-9]+\.[0-9]+)", 2),
            "docs/release-verification.md": (r"install-linux\.py --version ([0-9]+\.[0-9]+\.[0-9]+)", 1),
        }
        for name, (pattern, count) in expected.items():
            with self.subTest(file=name):
                contents = (ROOT / name).read_text(encoding="utf-8")
                self.assertEqual(re.findall(pattern, contents), [version] * count)
        windows = (ROOT / "docs/windows-installer.md").read_text(encoding="utf-8")
        self.assertIn(f"$env:VERSION = '{version}'", windows)
        linux = (ROOT / "docs/release-verification.md").read_text(encoding="utf-8")
        self.assertIn(f"/v{version}/scripts/install-linux.py", linux)
        # The published adapter examples use the independently tested CLI pin.
        adapter_cli = "0.9.3"
        for name in ("README.md", "maven-plugin/README.md"):
            contents = (ROOT / name).read_text(encoding="utf-8")
            self.assertEqual(re.findall(r"<cliVersion>([^<]+)</cliVersion>", contents), [adapter_cli])
            self.assertEqual(re.findall(r"<artifactId>distroplane-maven-plugin</artifactId>\s*<version>([^<]+)</version>", contents), ["1.0.1"])
        for name in ("README.md", "gradle-plugin/README.md"):
            contents = (ROOT / name).read_text(encoding="utf-8")
            self.assertEqual(re.findall(r"cliVersion = '([^']+)'", contents), [adapter_cli])
            self.assertEqual(re.findall(r"id 'io\.github\.aalsanie\.distroplane' version '([^']+)'", contents), ["1.0.0"])

    def test_action_examples_use_reviewed_immutable_refs(self):
        for name, count in (("README.md", 1), ("docs/github-actions.md", 2)):
            contents = (ROOT / name).read_text(encoding="utf-8")
            refs = re.findall(r"uses: aalsanie/distroplane@([^\s]+)", contents)
            self.assertEqual(len(refs), count)
            for ref in refs:
                self.assertRegex(ref, r"^[0-9a-f]{40}$")
            self.assertIn("approve: 'true'", contents)
            self.assertIn("credential-mappings: npm-publish=NPM_TOKEN", contents)
        readme = (ROOT / "README.md").read_text(encoding="utf-8")
        workflow = (ROOT / ".github/workflows/action-smoke.yml").read_text(encoding="utf-8")
        snippet = re.search(r"(?ms)^```yaml\n(.*?)^```$", readme).group(1)
        for line in snippet.splitlines():
            value = line.strip().removeprefix("- ")
            if value.startswith(("uses:", "version:", "command:", "approve:", "credential-mappings:")):
                self.assertIn(value, workflow)
        self.assertEqual(re.search(r"(?ms)^```xml\n(.*?)^```$", readme).group(1),
                         re.search(r"(?ms)^```xml\n(.*?)^```$",
                                   (ROOT / "maven-plugin/README.md").read_text(encoding="utf-8")).group(1))
        self.assertEqual(re.search(r"(?ms)^```groovy\n(.*?)^```$", readme).group(1),
                         re.search(r"(?ms)^```groovy\n(.*?)^```$",
                                   (ROOT / "gradle-plugin/README.md").read_text(encoding="utf-8")).group(1))
        contents = (ROOT / "docs/github-actions.md").read_text(encoding="utf-8")
        for name in ("run-id", "recovery-plan-id", "recovery-artifact-id"):
            self.assertIn(name + ": ${{ needs.publish.outputs.", contents)

    def test_readme_starts_with_install_and_publish(self):
        contents = (ROOT / "README.md").read_text(encoding="utf-8")
        self.assertIn("brew install aalsanie/tap/distroplane", contents)
        self.assertIn("distroplane init", contents)
        self.assertIn("distroplane publish", contents)
        self.assertIn("distroplane resume", contents)


if __name__ == "__main__":
    unittest.main()
