#!/usr/bin/env python3
"""Generate a Homebrew formula only from complete, verified published release assets."""
import argparse
import hashlib
import io
import json
import re
import sys
import tarfile
from pathlib import Path

PLATFORMS = (("darwin", "arm64"), ("darwin", "amd64"), ("linux", "arm64"), ("linux", "amd64"))
PROVIDERS = ("homebrew", "npm", "sdkman", "winget")
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")
DIGEST = re.compile(r"[0-9a-f]{64}")
ASSET_LIMIT = 512 * 1024 * 1024
BINARY_LIMIT = 128 * 1024 * 1024


class FormulaError(ValueError):
    pass


def strict_json(data):
    def distinct(pairs):
        value = {}
        for k, v in pairs:
            if k in value:
                raise FormulaError(f"duplicate JSON member: {k}")
            value[k] = v
        return value
    try:
        return json.loads(data, object_pairs_hook=distinct)
    except (ValueError, UnicodeDecodeError) as exc:
        raise FormulaError(f"invalid release JSON: {exc}") from exc


def read_file(directory, name, limit=ASSET_LIMIT):
    if "/" in name or "\\" in name or name.startswith("."):
        raise FormulaError("unsafe release asset path")
    path = directory / name
    if path.is_symlink() or not path.is_file():
        raise FormulaError(f"missing or unsafe release asset: {name}")
    if path.stat().st_size > limit:
        raise FormulaError(f"oversized release asset: {name}")
    data = path.read_bytes()
    if len(data) != path.stat().st_size:
        raise FormulaError(f"asset changed while reading: {name}")
    return data


def digest(data):
    return hashlib.sha256(data).hexdigest()


def check_manifest(directory, version):
    if not VERSION.fullmatch(version):
        raise FormulaError("formula updates require a stable semantic release version")
    data = strict_json(read_file(directory, "RELEASE-METADATA.json", 1024 * 1024))
    if not isinstance(data, dict) or data.get("schemaVersion") != "2" or data.get("version") != version or data.get("cgoEnabled") is not False:
        raise FormulaError("unexpected release identity or metadata schema")
    if not re.fullmatch(r"[0-9a-f]{12}", str(data.get("commit", ""))):
        raise FormulaError("release commit must be a pinned 12-digit hexadecimal")
    assets = data.get("assets")
    if not isinstance(assets, list) or len(assets) != 36:
        raise FormulaError("release inventory must contain all 36 built assets")
    expected_names = set()
    for platform in ("darwin", "linux", "windows"):
        for arch in ("amd64", "arm64"):
            suffix = ".zip" if platform == "windows" else ".tar.gz"
            expected_names.add(f"distroplane_{version}_{platform}_{arch}{suffix}")
            for binary in ("distroplane",) + tuple(f"distroplane-provider-{p}" for p in PROVIDERS):
                extension = ".exe" if platform == "windows" else ""
                expected_names.add(f"{binary}_{version}_{platform}_{arch}{extension}")
    indexed = {}
    last_name = ""
    for asset in assets:
        if not isinstance(asset, dict) or set(asset) != {"name", "sha256", "size"}:
            raise FormulaError("malformed release asset inventory")
        name, sha, size = asset["name"], asset["sha256"], asset["size"]
        if not isinstance(name, str) or not name > last_name or not DIGEST.fullmatch(str(sha)) or type(size) is not int or size <= 0:
            raise FormulaError("untrusted, duplicate or unordered release asset")
        indexed[name] = (sha, size)
        last_name = name
    if set(indexed) != expected_names:
        raise FormulaError("release inventory contains missing or unexpected asset names")
    want_sums = "".join(f"{sha}  {name}\n" for name, (sha, _) in indexed.items())
    if read_file(directory, "SHA256SUMS", 1024 * 1024) != want_sums.encode("ascii"):
        raise FormulaError("SHA256SUMS differs from the release inventory")
    return data, indexed


def verify_archive(blob, version, commit, platform, arch):
    expected_paths = ["bin/distroplane"] + [f"libexec/distroplane-provider-{x}" for x in PROVIDERS]
    expected = set(expected_paths) | {"PACKAGE-METADATA.json"}
    observed = {}
    try:
        with tarfile.open(fileobj=io.BytesIO(blob), mode="r:gz") as archive:
            for member in archive:
                if member.name not in expected or member.name in observed or not member.isfile():
                    raise FormulaError(f"unsafe archive entry: {member.name}")
                if member.size <= 0 or member.size > (1024 * 1024 if member.name == "PACKAGE-METADATA.json" else BINARY_LIMIT):
                    raise FormulaError(f"invalid embedded file size: {member.name}")
                if member.mode != (0o644 if member.name == "PACKAGE-METADATA.json" else 0o755):
                    raise FormulaError(f"unsafe embedded file permissions: {member.name}")
                f = archive.extractfile(member)
                if f is None:
                    raise FormulaError("unreadable archive member")
                with f:
                    content = f.read(member.size + 1)
                if len(content) != member.size:
                    raise FormulaError("archive member changed size")
                observed[member.name] = content
    except (tarfile.TarError, OSError, EOFError) as exc:
        raise FormulaError(f"invalid unified release archive: {exc}") from exc
    if set(observed) != expected:
        raise FormulaError("unified archive must contain the CLI, four providers and metadata")
    package = strict_json(observed["PACKAGE-METADATA.json"])
    if not isinstance(package, dict) or any(package.get(k) != v for k, v in (("schemaVersion", "1"), ("version", version), ("commit", commit), ("os", platform), ("arch", arch), ("cgoEnabled", False))):
        raise FormulaError("archive package identity does not match published release")
    files = package.get("files")
    if not isinstance(files, list) or len(files) != len(expected_paths):
        raise FormulaError("missing embedded package file manifest")
    checked = set()
    for item in files:
        if not isinstance(item, dict) or set(item) != {"path", "sha256", "size", "mode"}:
            raise FormulaError("invalid embedded file manifest")
        name = item["path"]
        if name not in expected_paths or name in checked or item["mode"] != "0755" or item["size"] != len(observed[name]) or item["sha256"] != digest(observed[name]):
            raise FormulaError("embedded executable digest or manifest mismatch")
        checked.add(name)
    if len(checked) != len(expected_paths):
        raise FormulaError("missing executable in embedded file manifest")


def generate(directory, version):
    metadata, indexed = check_manifest(directory, version)
    versions = {}
    for platform, arch in PLATFORMS:
        filename = f"distroplane_{version}_{platform}_{arch}.tar.gz"
        if filename not in indexed:
            raise FormulaError(f"release is missing unified archive: {filename}")
        raw = read_file(directory, filename)
        expected_sha, expected_size = indexed[filename]
        if len(raw) != expected_size or digest(raw) != expected_sha:
            raise FormulaError(f"published unified archive checksum mismatch: {filename}")
        verify_archive(raw, version, metadata["commit"], platform, arch)
        versions[(platform, arch)] = (filename, expected_sha)

    blocks = []
    for platform, homebrew_platform in (("darwin", "macos"), ("linux", "linux")):
        nested = []
        for arch, homebrew_arch in (("arm64", "arm"), ("amd64", "intel")):
            filename, sha = versions[(platform, arch)]
            nested.extend((f"    on_{homebrew_arch} do", f'      url "https://github.com/aalsanie/distroplane/releases/download/v{version}/{filename}"', f'      sha256 "{sha}"', "    end"))
        blocks.append(f"  on_{homebrew_platform} do\n" + "\n".join(nested) + "\n  end")
    return ("class Distroplane < Formula\n"
            '  desc "Distribute verified release artifacts to registries and package managers"\n'
            '  homepage "https://github.com/aalsanie/distroplane"\n'
            f'  version "{version}"\n'
            '  license "Apache-2.0"\n\n'
            + "\n\n".join(blocks) + "\n\n"
            '  def install\n'
            '    bin.install "bin/distroplane"\n'
            '    libexec.install Dir["libexec/distroplane-provider-*"]\n'
            '  end\n\n'
            '  test do\n'
            '    assert_match version.to_s, shell_output("#{bin}/distroplane version")\n'
            '    %w[homebrew npm sdkman winget].each do |provider|\n'
            '      file = libexec/"distroplane-provider-#{provider}"\n'
            '      assert_path_exists file\n'
            '      assert_predicate file, :executable?\n'
            '    end\n'
            '  end\n'
            'end\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--release-dir", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output", required=True, type=Path)
    options = parser.parse_args()
    try:
        result = generate(options.release_dir, options.version)
        with options.output.open("x", encoding="utf-8", newline="\n") as stream:
            stream.write(result)
    except (FormulaError, OSError) as exc:
        print(f"homebrew formula: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
