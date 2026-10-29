#!/usr/bin/env python3
"""Create reviewable WinGet manifests from real, already-built MSI bytes.

Submission is deliberately separate from generation: public catalog acceptance
requires publisher identity approval and a published, verified installer.
"""
import argparse
import hashlib
import re
from pathlib import Path

VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+", re.ASCII)
IDENT = re.compile(r"[A-Za-z][A-Za-z0-9-]*\.[A-Za-z][A-Za-z0-9.-]*", re.ASCII)
PUBLISHER = re.compile(r"[A-Za-z0-9][A-Za-z0-9 .,'&()-]{0,127}", re.ASCII)
ARCHES = {"amd64": "x64", "arm64": "arm64"}


class ManifestError(ValueError):
    pass


def validate_identity(version, package_id, publisher):
    if not VERSION.fullmatch(version):
        raise ManifestError("WinGet requires a stable, three-part release version")
    if not IDENT.fullmatch(package_id) or ".." in package_id or package_id.endswith("."):
        raise ManifestError("the approved WinGet package ID is invalid")
    if not PUBLISHER.fullmatch(publisher):
        raise ManifestError("approved publisher name contains unsafe YAML characters")


def digest_file(path):
    if path.is_symlink() or not path.is_file():
        raise ManifestError(f"missing or unsafe MSI: {path.name}")
    if not 0 < path.stat().st_size <= 512 * 1024 * 1024:
        raise ManifestError(f"empty or oversized MSI: {path.name}")
    data = path.read_bytes()
    if len(data) != path.stat().st_size or data[:8] != bytes.fromhex("d0cf11e0a1b11ae1"):
        raise ManifestError(f"not a valid Windows Installer OLE container: {path.name}")
    return hashlib.sha256(data).hexdigest().upper()


def generate(directory, version, package_id, publisher):
    validate_identity(version, package_id, publisher)
    checksums = {}
    for arch in ARCHES:
        name = f"distroplane_{version}_windows_{arch}.msi"
        checksums[arch] = digest_file(directory / name)
    filename_prefix = f"https://github.com/aalsanie/distroplane/releases/download/v{version}/"
    installer = (
        "# yaml-language-server: $schema=https://aka.ms/winget-manifest.installer.1.10.0.schema.json\n"
        f"PackageIdentifier: {package_id}\n"
        f"PackageVersion: {version}\n"
        "InstallerType: msi\n"
        "Scope: machine\n"
        "ElevationRequirement: elevationRequired\n"
        "UpgradeBehavior: install\n"
        "InstallModes:\n"
        "  - interactive\n"
        "  - silent\n"
        "  - silentWithProgress\n"
        "Installers:\n"
    )
    for arch, target in ARCHES.items():
        installer += (
            f"  - Architecture: {target}\n"
            f"    InstallerUrl: {filename_prefix}distroplane_{version}_windows_{arch}.msi\n"
            f"    InstallerSha256: {checksums[arch]}\n"
        )
    installer += "ManifestType: installer\nManifestVersion: 1.10.0\n"
    locale = (
        "# yaml-language-server: $schema=https://aka.ms/winget-manifest.defaultLocale.1.10.0.schema.json\n"
        f"PackageIdentifier: {package_id}\n"
        f"PackageVersion: {version}\n"
        "PackageLocale: en-US\n"
        f"Publisher: {publisher}\n"
        "PackageName: Distroplane\n"
        "License: Apache-2.0\n"
        "ShortDescription: A local-first release distribution control plane\n"
        "PackageUrl: https://github.com/aalsanie/distroplane\n"
        "ManifestType: defaultLocale\n"
        "ManifestVersion: 1.10.0\n"
    )
    version_yaml = (
        "# yaml-language-server: $schema=https://aka.ms/winget-manifest.version.1.10.0.schema.json\n"
        f"PackageIdentifier: {package_id}\n"
        f"PackageVersion: {version}\n"
        "DefaultLocale: en-US\n"
        "ManifestType: version\n"
        "ManifestVersion: 1.10.0\n"
    )
    return {
        f"{package_id}.yaml": version_yaml,
        f"{package_id}.installer.yaml": installer,
        f"{package_id}.locale.en-US.yaml": locale,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--msi-dir", type=Path, required=True)
    parser.add_argument("--package-id", required=True, help="Explicitly approved WinGet ID")
    parser.add_argument("--publisher", required=True, help="Publisher identity from the installer")
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    try:
        rendered = generate(args.msi_dir, args.version, args.package_id, args.publisher)
        path = args.output_dir / args.package_id[0].lower() / args.package_id.replace(".", "/") / args.version
        path.mkdir(parents=True, exist_ok=False)
        for filename, content in rendered.items():
            (path / filename).write_text(content, encoding="utf-8", newline="\n")
    except (ManifestError, OSError) as error:
        parser.exit(1, f"WinGet manifest: {error}\n")


if __name__ == "__main__":
    main()
