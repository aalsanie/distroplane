"""GitHub draft-release integrity and fail-closed publication regression tests."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("github_release_gate", ROOT / "github-release-gate.py")
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

REPO = "aalsanie/distroplane"
TAG = "v1.0.0"


class ReleaseGateTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.directory = Path(temp.name)
        self.archive = self.directory / "distroplane_1.0.0_linux_amd64.tar.gz"
        self.archive.write_bytes(b"released archive fixture")
        self.asset = {
            "name": self.archive.name,
            "state": "uploaded",
            "size": self.archive.stat().st_size,
            "digest": "sha256:" + hashlib.sha256(self.archive.read_bytes()).hexdigest(),
        }
        self.release = {"id": 123, "tag_name": TAG, "draft": True,
                        "assets": [self.asset.copy()]}

    def test_preflight_rejects_both_drafts_and_published_releases(self):
        for draft in (True, False):
            with self.subTest(draft=draft), mock.patch.object(
                    gate, "find_release", return_value=dict(self.release, draft=draft)):
                with self.assertRaisesRegex(ValueError, "already exists"):
                    gate.check(REPO, TAG)
        with mock.patch.object(gate, "find_release", return_value=None):
            gate.check(REPO, TAG)

    def test_draft_lookup_uses_release_listing_not_public_tag_endpoint(self):
        def respond(repo, path, **kwargs):
            self.assertEqual(repo, REPO)
            self.assertTrue(path.startswith("releases?per_page=100&page="))
            page = int(path.rsplit("=", 1)[-1])
            if page == 1:
                return [{"tag_name": "v0.9.3", "id": n} for n in range(100)]
            if page == 2:
                return [self.release]
            self.fail("unexpected page")
        with mock.patch.object(gate, "api", side_effect=respond) as api:
            self.assertEqual(gate.find_release(REPO, TAG)["id"], 123)
            self.assertEqual(api.call_count, 2)

    def test_duplicate_tag_lookup_is_rejected(self):
        with mock.patch.object(gate, "api", return_value=[self.release, self.release]):
            with self.assertRaisesRegex(ValueError, "duplicate"):
                gate.find_release(REPO, TAG)

    def test_correct_draft_is_verified_before_id_based_publication(self):
        published = dict(self.release, draft=False, published_at="2026-11-01T12:00:00Z")
        with mock.patch.object(gate, "find_release", return_value=self.release), \
             mock.patch.object(gate, "api", side_effect=[self.release, published]) as api:
            gate.publish(REPO, TAG, self.directory)
            self.assertEqual(api.call_args_list, [
                mock.call(REPO, "releases/123"),
                mock.call(REPO, "releases/123", publish=True),
            ])

    def test_invalid_draft_never_becomes_public(self):
        cases = {
            "wrong-tag": dict(self.release, tag_name="v0.8.0"),
            "already-public": dict(self.release, draft=False),
            "missing-asset": dict(self.release, assets=[]),
            "extra-asset": dict(self.release, assets=self.release["assets"] +
                                [dict(self.asset, name="malicious.bin")]),
            "duplicate-assets": dict(self.release, assets=[self.asset, self.asset]),
            "missing-digest": dict(self.release, assets=[dict(self.asset, digest=None)]),
            "wrong-digest": dict(self.release, assets=[
                dict(self.asset, digest="sha256:" + "0" * 64)]),
            "wrong-size": dict(self.release, assets=[dict(self.asset, size=1)]),
            "not-uploaded": dict(self.release, assets=[dict(self.asset, state="starter")]),
        }
        for name, release in cases.items():
            with self.subTest(name=name), \
                 mock.patch.object(gate, "find_release", return_value=self.release), \
                 mock.patch.object(gate, "api", return_value=release) as api:
                with self.assertRaises(ValueError):
                    gate.publish(REPO, TAG, self.directory)
                api.assert_called_once_with(REPO, "releases/123")

    def test_replaced_release_id_fails_closed(self):
        with mock.patch.object(gate, "find_release", return_value=self.release), \
             mock.patch.object(gate, "api", return_value=dict(self.release, id=99)) as api:
            with self.assertRaisesRegex(ValueError, "identity changed"):
                gate.publish(REPO, TAG, self.directory)
            api.assert_called_once_with(REPO, "releases/123")

    def test_publication_response_must_confirm_same_release_id_and_tag(self):
        for result in (dict(self.release, draft=False, published_at=None),
                       dict(self.release, id=55, draft=False, published_at="2026-11-01T12:00:00Z"),
                       dict(self.release, draft=True, published_at="2026-11-01T12:00:00Z")):
            with self.subTest(result=result), \
                 mock.patch.object(gate, "find_release", return_value=self.release), \
                 mock.patch.object(gate, "api", side_effect=[self.release, result]):
                with self.assertRaises(ValueError):
                    gate.publish(REPO, TAG, self.directory)

    def test_symlink_and_empty_staging_are_rejected(self):
        link = self.directory / "linked.tar.gz"
        try:
            link.symlink_to(self.archive)
        except (OSError, NotImplementedError):
            self.skipTest("symlinks unsupported")
        with self.assertRaisesRegex(ValueError, "unsafe"):
            gate.verify_upload(self.release, TAG, self.directory)
        link.unlink()
        self.archive.unlink()
        with self.assertRaisesRegex(ValueError, "unsafe"):
            gate.verify_upload(self.release, TAG, self.directory)

    def test_gh_api_failure_and_malformed_json_fail_closed(self):
        for response in (
            mock.Mock(returncode=1, stderr="unavailable", stdout=""),
            mock.Mock(returncode=0, stderr="", stdout="not-json"),
        ):
            with mock.patch.object(gate.subprocess, "run", return_value=response):
                with self.assertRaises((RuntimeError, ValueError)):
                    gate.api(REPO, "releases/123")
        with mock.patch.object(gate.subprocess, "run",
                               return_value=mock.Mock(returncode=0, stderr="",
                                                      stdout=json.dumps(self.release))) as run:
            gate.api(REPO, "releases/123", publish=True)
            self.assertEqual(run.call_args.args[0][-4:],
                             ["--method", "PATCH", "-F", "draft=false"])


if __name__ == "__main__":
    unittest.main()
