# Install Distroplane

## Homebrew (macOS or Linux)

```sh
brew install aalsanie/tap/distroplane
```

## Windows

```powershell
winget install --id Aalsanie.Distroplane --exact
```

Alternatively download the appropriate [Windows MSI](https://github.com/aalsanie/distroplane/releases/latest). The MSI installer runs without Go or WiX. See [Windows installer details](windows-installer.md).

## Direct Linux installation

Without Homebrew, use the version-matched installer for the [published release](https://github.com/aalsanie/distroplane/releases/latest):

```sh
curl -fL https://raw.githubusercontent.com/aalsanie/distroplane/v0.9.2/scripts/install-linux.py -o install-linux.py
python3 install-linux.py --version 0.9.2
~/.local/bin/distroplane version
```

Python 3.8+ is required. The installer verifies downloads and installs CLI/providers together without root. For an offline install, use `--offline-dir DIR` containing the release archive, `RELEASE-METADATA.json`, and `SHA256SUMS`. Use `--uninstall` to remove the managed command; project recovery state remains intact.

## Verify

```sh
distroplane version
distroplane help
```

The latest [GitHub release](https://github.com/aalsanie/distroplane/releases/latest) contains native binaries, checksums, and provenance attestations for Linux, macOS, and Windows on x64/ARM64. Verify checksums and provenance when installing from standalone downloads. A checksum published with the artifact is not an independent signature.

## Release maintainers

Before tagging, manually run the **Release** workflow on `main` with confirmation `REHEARSE`. It builds and verifies the same staged assets and six native installation targets as the publishing workflow, but does not create a GitHub release or update package catalogs. Review the successful run and its staged artifact inventory before approving publication.

A stable release must have matching `VERSION` and versioned release notes. Merge the changes, verify CI and Action smoke on the **exact commit**, then tag that commit with `v<VERSION>`. The release workflow checks asset integrity, staged inventories, native execution, and the Windows MSI signing policy before publication. No tag should be pushed before the checks pass. See [Windows signing details](windows-installer.md).
