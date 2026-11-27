# Install Distroplane

## Homebrew (macOS)

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
curl -fL https://raw.githubusercontent.com/aalsanie/distroplane/v0.9.3/scripts/install-linux.py -o install-linux.py
python3 install-linux.py --version 0.9.3
~/.local/bin/distroplane version
```

Python 3.8+ is required for this installer only. The installer verifies downloads and installs CLI/providers together without root. For an offline install, use `--offline-dir DIR` containing the release archive, `RELEASE-METADATA.json`, and `SHA256SUMS`. Use `--uninstall` to remove the managed command; project recovery state remains intact.

## Manual archive installation

Download the matching unified `.tar.gz` (Linux/macOS) or `.zip` (Windows) from the release, verify its checksum and provenance, and unpack into a user-owned directory. Keep `bin/` and `libexec/` together. Invoke `bin/distroplane` (`bin/distroplane.exe` on Windows) directly or add its absolute `bin` directory to your shell's PATH. No Go, Node.js, npm or Java runtime is required for the CLI. Homebrew and WinGet publication require Git; the npm and SDKMAN providers do not.

## Verify

```sh
distroplane version
distroplane help
```

Use `distroplane init` and `distroplane doctor` from your project directory to check local readiness. A populated credential source does not prove authorization.

The latest [GitHub release](https://github.com/aalsanie/distroplane/releases/latest) contains native binaries, checksums, and provenance attestations for Linux, macOS, and Windows on x64/ARM64. Verify checksums and provenance when installing from standalone downloads. A checksum published with the artifact is not an independent signature.

## Installation verification

The required CI release-build matrix runs the candidate archives on native Linux, macOS and Windows (x64 and ARM64). It exercises local setup, provider discovery and retained-run recovery with no development tools or credentials on PATH. macOS tests install the generated formula from staged bytes; Linux tests offline installation, interruption and uninstall/reinstall; Windows tests MSI install, repair, upgrade and uninstall. Staged package tests are not public catalog availability checks. Verify Homebrew and WinGet catalog availability after publication.

| Component | Version under test | CI environment |
| --- | --- | --- |
| Native CLI + four official providers | Same candidate version from `VERSION` | Six native OS/architecture targets |
| GitHub Action | Source revision with local CLI and version-pinned archive fixtures | Linux, macOS, Windows |
| Maven plugin | 1.0.1-SNAPSHOT; CLI 0.9.3 controlled cache fixture | Maven 3.9+, JDK 17/21 on Linux, macOS, Windows |
| Gradle plugin | 1.0.0-SNAPSHOT; CLI 0.9.3 controlled cache fixture | Gradle 8.14.6/9.8.2, JDK 17/21 on Linux, macOS, Windows |

Adapter fixture results do not certify the published plugin binaries against candidate release bytes. See [Maven](../maven-plugin/README.md), [Gradle](../gradle-plugin/README.md) and [Action](github-actions.md) for versioned usage.

## Release maintainers

Before tagging, manually run the **Release** workflow on `main` with confirmation `REHEARSE`. It builds and verifies the same staged assets and six native installation targets as the publishing workflow, but does not create a GitHub release or update package catalogs. Review the successful run and its staged artifact inventory before approving publication.

A stable release must have matching `VERSION` and a current `CHANGELOG.md` entry. Merge the changes, verify CI and Action smoke on the **exact commit**, then tag that commit with `v<VERSION>`. The release workflow checks asset integrity, staged inventories, native execution, and the Windows MSI signing policy before publication. No tag should be pushed before the checks pass. See [Windows signing details](windows-installer.md).
