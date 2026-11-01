"""Regression checks for strict executable-line and branch coverage gates."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("coverage_gate", ROOT / "coverage_gate.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class CoverageGateTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        src = self.root / "pkg"
        src.mkdir()
        (src / "sample.go").write_text(
            "package pkg\n\nfunc f() bool {\n"
            "    if true {\n"
            "        return true\n"
            "    }\n"
            "    return false\n"
            "}\n", encoding="utf-8",
        )
        self.profile = self.root / "coverage.out"
        self.profile.write_text(
            "mode: atomic\n"
            "github.com/aalsanie/distroplane/pkg/sample.go:3.15,5.20 2 1\n"
            "github.com/aalsanie/distroplane/pkg/sample.go:7.5,8.2 1 0\n",
            encoding="utf-8",
        )

    def test_executable_line_counts_are_de_duplicated(self):
        self.assertEqual(gate.line_counts(self.profile, self.root), (3, 4))

    def test_strictly_greater_than_90_percent(self):
        for hits, total in ((90, 100), (0, 1), (0, 0)):
            with self.subTest(hits=hits, total=total):
                with self.assertRaises(ValueError):
                    gate.require_above(hits, total, 90.0, "test")
        gate.require_above(901, 1000, 90.0, "test")

    def test_malformed_or_untrusted_profiles_are_rejected(self):
        content = self.profile.read_text(encoding="utf-8")
        for replacement in (
            "mode: invalid\n",
            content.replace("github.com/aalsanie/distroplane/", "github.com/untrusted/"),
            content.replace(" 2 1", " 2 1\ninvalid"),
            content.replace(":3.15,5.20", ":3.15,999.20"),
        ):
            with self.subTest(replacement=replacement[:60]):
                self.profile.write_text(replacement, encoding="utf-8")
                with self.assertRaises(ValueError):
                    gate.line_counts(self.profile, self.root)

    def test_branch_report_counter_format(self):
        good = "ok test\n\nBranch coverage: 181/200\n"
        match = gate.BRANCHES.search(good)
        self.assertIsNotNone(match)
        self.assertEqual((int(match[1]), int(match[2])), (181, 200))
        self.assertIsNone(gate.BRANCHES.search("Condition coverage: 181/200"))


if __name__ == "__main__":
    unittest.main()
