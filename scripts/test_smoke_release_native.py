"""Verify native execution smoke rejects altered or incorrectly labelled archives."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import platform
import tarfile
import tempfile
import unittest

MODULE = Path(__file__).with_name("smoke-release-native.py")
spec = importlib.util.spec_from_file_location("native_smoke", MODULE)
native = importlib.util.module_from_spec(spec)
spec.loader.exec_module(native)
COMMIT = "0123456789ab"
VERSION = "0.9.2"


def digest(blob):
    return hashlib.sha256(blob).hexdigest()


def executable(name):
    if name == "distroplane":
        return ("#!/usr/bin/env python3\nimport json\n"
                "print(json.dumps({'version':'0.9.2','commit':'0123456789ab'}))\n").encode()
    provider = name.removeprefix("distroplane-provider-")
    return ("#!/usr/bin/env python3\nimport json,sys\nrequest=json.load(sys.stdin)\n"
            "print(json.dumps({'requestId':request['requestId'],'status':'ok',"
            "'payload':{'provider':{'name':" + repr(provider) + ", 'version':'0.9.2'}}}))\n").encode()


def fixture(root, corrupt=None):
    os_name, arch = "linux", "amd64" if platform.machine().lower() == "x86_64" else "arm64"
    records, source = [], {}
    for key in ("distroplane", "distroplane-provider-npm", "distroplane-provider-homebrew",
                "distroplane-provider-sdkman", "distroplane-provider-winget"):
        member = ("bin/" if key == "distroplane" else "libexec/") + key
        data = executable(key)
        source[member] = data
        records.append({"path": member, "sha256": digest(data), "size": len(data), "mode": "0755"})
        (root / f"{key}_{VERSION}_{os_name}_{arch}").write_bytes(data)
    metadata = {"schemaVersion": "1", "version": VERSION, "commit": COMMIT,
                "os": os_name, "arch": arch, "cgoEnabled": False, "files": records}
    source["PACKAGE-METADATA.json"] = json.dumps(metadata).encode()
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:gz") as archive:
        for name, blob in source.items():
            entry = tarfile.TarInfo(name if name != corrupt else "../escape")
            entry.mode = 0o755 if name != "PACKAGE-METADATA.json" else 0o644
            entry.size = len(blob)
            archive.addfile(entry, io.BytesIO(blob))
    (root / f"distroplane_{VERSION}_{os_name}_{arch}.tar.gz").write_bytes(data.getvalue())


@unittest.skipUnless(platform.system() == "Linux" and
                     platform.machine().lower() in ("x86_64", "aarch64"), "native Linux test")
class NativeSmokeTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        fixture(self.root)

    def test_native_archive_runs_all_executables(self):
        native.run(self.root, VERSION, COMMIT, "linux")

    def test_refuses_mislabeled_runner(self):
        with self.assertRaises(native.SmokeError):
            native.run(self.root, VERSION, COMMIT, "darwin")

    def test_refuses_modified_standalone(self):
        arch = "amd64" if platform.machine().lower() == "x86_64" else "arm64"
        standalone = self.root / f"distroplane-provider-npm_{VERSION}_linux_{arch}"
        standalone.write_bytes(b"corrupt")
        with self.assertRaises(native.SmokeError):
            native.run(self.root, VERSION, COMMIT, "linux")

    def test_refuses_traversal_archive_member(self):
        with tempfile.TemporaryDirectory() as temp:
            unsafe = Path(temp)
            fixture(unsafe, corrupt="bin/distroplane")
            with self.assertRaises(native.SmokeError):
                native.run(unsafe, VERSION, COMMIT, "linux")


if __name__ == "__main__":
    unittest.main()
