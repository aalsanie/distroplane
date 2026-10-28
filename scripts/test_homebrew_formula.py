"""Offline verification of release-to-Homebrew formula generation."""
import copy
import hashlib
import importlib.util
import io
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("homebrew_formula.py")
spec = importlib.util.spec_from_file_location("homebrew_formula", SCRIPT)
formula = importlib.util.module_from_spec(spec)
spec.loader.exec_module(formula)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def build_release(root, version="1.2.3", mutate=None):
    inventory = []
    for platform in ("linux", "darwin", "windows"):
        for arch in ("amd64", "arm64"):
            paths = ["bin/distroplane"] + [f"libexec/distroplane-provider-{p}" for p in formula.PROVIDERS]
            files = []
            stream = io.BytesIO()
            with tarfile.open(fileobj=stream, mode="w:gz") as archive:
                for path in sorted(paths):
                    content = (path + platform + arch + version).encode()
                    info = tarfile.TarInfo(path)
                    info.size = len(content)
                    info.mode = 0o755
                    archive.addfile(info, io.BytesIO(content))
                    files.append({"path": path, "sha256": sha(content), "size": len(content), "mode": "0755"})
                pkg = {"schemaVersion": "1", "version": version, "commit": "0123456789ab",
                       "os": platform, "arch": arch, "cgoEnabled": False, "files": files}
                payload = json.dumps(pkg).encode()
                info = tarfile.TarInfo("PACKAGE-METADATA.json")
                info.size, info.mode = len(payload), 0o644
                archive.addfile(info, io.BytesIO(payload))
            data = stream.getvalue()
            if mutate is not None and (platform, arch) == ("darwin", "arm64"):
                data = mutate(data)
            suffix = ".zip" if platform == "windows" else ".tar.gz"
            name = f"distroplane_{version}_{platform}_{arch}{suffix}"
            (root / name).write_bytes(data)
            inventory.append({"name": name, "sha256": sha(data), "size": len(data)})
            for binary in ("distroplane", "distroplane-provider-homebrew", "distroplane-provider-npm",
                           "distroplane-provider-sdkman", "distroplane-provider-winget"):
                name = f"{binary}_{version}_{platform}_{arch}" + (".exe" if platform == "windows" else "")
                payload = name.encode()
                inventory.append({"name": name, "sha256": sha(payload), "size": len(payload)})
    inventory.sort(key=lambda x: x["name"])
    metadata = {"schemaVersion": "2", "version": version, "commit": "0123456789ab",
                "cgoEnabled": False, "assets": inventory}
    (root / "RELEASE-METADATA.json").write_text(json.dumps(metadata))
    (root / "SHA256SUMS").write_text("".join(f'{a["sha256"]}  {a["name"]}\n' for a in inventory))


class FormulaTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.directory = Path(tmp.name)
        build_release(self.directory)

    def test_generate_all_architectures(self):
        rendered = formula.generate(self.directory, "1.2.3")
        self.assertEqual(rendered.count("on_arm do"), 2)
        self.assertEqual(rendered.count("on_intel do"), 2)
        self.assertEqual(rendered.count("sha256 "), 4)
        self.assertIn('libexec.install Dir["libexec/distroplane-provider-*"]', rendered)
        self.assertIn('bin.install "bin/distroplane"', rendered)
        self.assertEqual(rendered, formula.generate(self.directory, "1.2.3"))
        proc = subprocess.run(["ruby", "-c"], input=rendered, text=True, capture_output=True)
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_non_overwriting_command(self):
        output = self.directory / "Formula.rb"
        args = [sys.executable, str(SCRIPT), "--release-dir", str(self.directory),
                "--version", "1.2.3", "--output", str(output)]
        first = subprocess.run(args, capture_output=True, text=True)
        self.assertEqual(first.returncode, 0, first.stderr)
        saved = output.read_bytes()
        self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)
        self.assertEqual(output.read_bytes(), saved)
        output.unlink()
        (self.directory / "SHA256SUMS").write_text("tampered")
        self.assertNotEqual(subprocess.run(args, capture_output=True).returncode, 0)
        self.assertFalse(output.exists())

    def test_reject_unpublished_or_unstable_versions(self):
        for version in ("1.2.4", "1.2.3-rc.1", "../1.2.3", "1.2.3\n", ""):
            with self.subTest(version=version), self.assertRaises(formula.FormulaError):
                formula.generate(self.directory, version)

    def test_missing_and_tampered_archives(self):
        path = self.directory / "distroplane_1.2.3_linux_amd64.tar.gz"
        saved = path.read_bytes()
        path.unlink()
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")
        path.write_bytes(saved)
        path.write_bytes(saved + b"corrupted")
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")
        build_release(self.directory, mutate=lambda _: b"not an archive")
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")

    def test_embedded_binary_integrity(self):
        def edit(archive_bytes, corrupt_binary):
            out = io.BytesIO()
            with tarfile.open(fileobj=io.BytesIO(archive_bytes), mode="r:gz") as archive:
                with tarfile.open(fileobj=out, mode="w:gz") as writer:
                    for member in archive:
                        data = archive.extractfile(member).read()
                        if corrupt_binary and member.name == "bin/distroplane":
                            data += b"tampered"
                        if not corrupt_binary and member.name == "PACKAGE-METADATA.json":
                            data = data.replace(b'"version": "1.2.3"', b'"version": "9.9.9"')
                        member.size = len(data)
                        writer.addfile(member, io.BytesIO(data))
            return out.getvalue()
        for change in (True, False):
            build_release(self.directory, mutate=lambda b: edit(b, change))
            with self.assertRaises(formula.FormulaError):
                formula.generate(self.directory, "1.2.3")

    def test_manifest_and_checksum_corruption(self):
        manifest_path = self.directory / "RELEASE-METADATA.json"
        original = json.loads(manifest_path.read_text())
        for update in (
            lambda x: x.update(version="1.2.4"),
            lambda x: x.update(commit="invalid"),
            lambda x: x.update(cgoEnabled=True),
            lambda x: x["assets"].pop(),
            lambda x: x["assets"].reverse(),
            lambda x: x["assets"][0].update(name="unrecognized"),
            lambda x: x["assets"][0].update(sha256="bad"),
        ):
            with self.subTest(change=repr(update)):
                data = copy.deepcopy(original)
                update(data)
                manifest_path.write_text(json.dumps(data))
                with self.assertRaises(formula.FormulaError):
                    formula.generate(self.directory, "1.2.3")
        manifest_path.write_text(json.dumps(original))
        (self.directory / "SHA256SUMS").write_text("not matching")
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")

    def test_duplicate_metadata_and_symlinked_archive(self):
        (self.directory / "RELEASE-METADATA.json").write_text('{"schemaVersion":"2","schemaVersion":"2"}')
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")
        build_release(self.directory)
        archive = self.directory / "distroplane_1.2.3_darwin_arm64.tar.gz"
        renamed = self.directory / "original"
        archive.rename(renamed)
        archive.symlink_to(renamed)
        with self.assertRaises(formula.FormulaError):
            formula.generate(self.directory, "1.2.3")

    def test_archive_traversal_not_allowed(self):
        archive_path = self.directory / "distroplane_1.2.3_darwin_arm64.tar.gz"
        out = io.BytesIO()
        with tarfile.open(archive_path, mode="r:gz") as source:
            with tarfile.open(fileobj=out, mode="w:gz") as target:
                for member in source:
                    data = source.extractfile(member).read()
                    if member.name == "bin/distroplane":
                        member.name = "../escape"
                    target.addfile(member, io.BytesIO(data))
        with self.assertRaises(formula.FormulaError):
            formula.verify_archive(out.getvalue(), "1.2.3", "0123456789ab", "darwin", "arm64")


if __name__ == "__main__":
    unittest.main()
