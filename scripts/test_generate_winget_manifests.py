import hashlib
import importlib.util
import tempfile
import unittest
from pathlib import Path

SPEC = importlib.util.spec_from_file_location(
    "generate_winget_manifests", Path(__file__).with_name("generate-winget-manifests.py")
)
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


class WinGetManifestTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.path = Path(tmp.name)
        self.data = bytes.fromhex("d0cf11e0a1b11ae1") + (b"msi-fixture" * 4)
        for arch in ("amd64", "arm64"):
            (self.path / f"distroplane_1.2.3_windows_{arch}.msi").write_bytes(self.data + arch.encode())

    def test_exact_installer_hashes_and_four_field_identifiers(self):
        docs = module.generate(self.path, "1.2.3", "Aalsanie.Distroplane", "Aalsanie")
        self.assertEqual(len(docs), 3)
        content = docs["Aalsanie.Distroplane.installer.yaml"]
        self.assertEqual(content.count("InstallerUrl:"), 2)
        self.assertIn("Architecture: x64", content)
        self.assertIn("Architecture: arm64", content)
        self.assertIn("Scope: machine", content)
        for arch in ("amd64", "arm64"):
            filename = f"distroplane_1.2.3_windows_{arch}.msi"
            want = hashlib.sha256((self.path / filename).read_bytes()).hexdigest().upper()
            self.assertIn(want, content)
            self.assertIn(filename, content)

    def test_reject_bad_identity_and_missing_or_tampered_installer(self):
        for version, package_id, publisher in (
            ("1.2.3-rc.1", "Aalsanie.Distroplane", "Aalsanie"),
            ("1.2.3", "Aalsanie..Distroplane", "Aalsanie"),
            ("1.2.3", "Aalsanie.Distroplane", "Aalsanie:\nspoof"),
        ):
            with self.subTest(version=version, package_id=package_id), self.assertRaises(module.ManifestError):
                module.generate(self.path, version, package_id, publisher)
        missing = self.path / "distroplane_1.2.3_windows_arm64.msi"
        missing.unlink()
        with self.assertRaises(module.ManifestError):
            module.generate(self.path, "1.2.3", "Aalsanie.Distroplane", "Aalsanie")
        missing.write_bytes(b"tampered")
        with self.assertRaises(module.ManifestError):
            module.generate(self.path, "1.2.3", "Aalsanie.Distroplane", "Aalsanie")


if __name__ == "__main__":
    unittest.main()
