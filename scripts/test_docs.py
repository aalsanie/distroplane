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
    for folder in ("docs", "providers", "maven-plugin"):
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

    def test_readme_starts_with_install_and_publish(self):
        contents = (ROOT / "README.md").read_text(encoding="utf-8")
        self.assertIn("brew install aalsanie/tap/distroplane", contents)
        self.assertIn("distroplane init", contents)
        self.assertIn("distroplane publish", contents)
        self.assertIn("distroplane resume", contents)


if __name__ == "__main__":
    unittest.main()
