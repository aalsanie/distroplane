"""Tests for changelog release gating and notes extraction."""
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr
from pathlib import Path
from unittest.mock import patch

import changelog


class ChangelogTest(unittest.TestCase):
    def test_release_notes_extract_only_current_version(self):
        notes = changelog.release_notes("0.9.3", "# Changelog\n\n## [0.9.3]\n\n- Security fix.\n\n## [0.9.2] - 2026-10-29\n\n- MSI.\n")
        self.assertEqual(notes, "# Distroplane v0.9.3\n\n- Security fix.\n")
        self.assertNotIn("MSI.", notes)

    def test_reject_invalid_release_inputs(self):
        base = "# Changelog\n\n## [0.9.3]\n\n- Security fix.\n"
        cases = {
            "invalid version": ("0.9.3/extra", base),
            "missing title": ("0.9.3", base.replace("# Changelog", "# Releases")),
            "wrong current version": ("0.9.3", base.replace("[0.9.3]", "[0.9.2]")),
            "missing bullet": ("0.9.3", base.replace("- Security fix.", "Security fix.")),
            "duplicate version": ("0.9.3", base + "\n## [0.9.3]\n\n- Duplicate.\n"),
            "bad heading": ("0.9.3", base + "\n## [0.9.2] extra\n\n- Old.\n"),
            "empty old release": ("0.9.3", base + "\n## [0.9.2]\n\nNo changes.\n"),
        }
        for name, (version, text) in cases.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                changelog.release_notes(version, text)

    def test_cli_check_and_extract(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "VERSION").write_text("0.9.3\n", encoding="utf-8")
            (root / "CHANGELOG.md").write_text(
                "# Changelog\n\n## [0.9.3]\n\n- Patched Go compiler.\n", encoding="utf-8"
            )
            with patch.object(sys, "argv", ["changelog", "--root", tmp]):
                out = io.StringIO()
                with redirect_stdout(out):
                    self.assertEqual(changelog.main(), 0)
                self.assertIn("changelog check passed", out.getvalue())
            with patch.object(sys, "argv", ["changelog", "--root", tmp, "--extract"]):
                out = io.StringIO()
                with redirect_stdout(out):
                    self.assertEqual(changelog.main(), 0)
                self.assertEqual(out.getvalue(), "# Distroplane v0.9.3\n\n- Patched Go compiler.\n")
            (root / "VERSION").write_text("0.9.4\n", encoding="utf-8")
            with patch.object(sys, "argv", ["changelog", "--root", tmp]):
                with redirect_stderr(io.StringIO()):
                    self.assertEqual(changelog.main(), 1)

    def test_repository_history(self):
        root = Path(__file__).resolve().parents[1]
        content = (root / "CHANGELOG.md").read_text(encoding="utf-8")
        version = (root / "VERSION").read_text(encoding="utf-8").strip()
        self.assertIn(f"# Distroplane v{version}", changelog.release_notes(version, content))
        for tag in ("0.9.0-rc.1", "0.9.0-rc.2", "0.9.0", "0.9.1", "0.9.2"):
            self.assertIn(f"## [{tag}]", content)


if __name__ == "__main__":
    unittest.main()
