# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/aalsanie/distroplane?include_prereleases&sort=semver)](https://github.com/aalsanie/distroplane/releases)
[![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Distroplane publishes **existing build artifacts** to npm, SDKMAN, custom Homebrew taps and WinGet review repositories. Plans bind artifact and provider digests; execution writes a durable journal and reconciles uncertain outcomes before another side effect. It does not build/host artifacts, guarantee exactly-once publication, or undo external releases.

## Install

Latest published CLI release: [v0.9.2](https://github.com/aalsanie/distroplane/releases/tag/v0.9.2).

- **macOS and Linuxbrew:** `brew install aalsanie/tap/distroplane` from the [published Homebrew tap](https://github.com/aalsanie/homebrew-tap). Confirm host support and test the installation with `distroplane version`.
- **Linux (without Homebrew):** Use the [checksum-verifying direct installer](docs/release-verification.md#direct-linux-installation) or the verified release archive; no Go toolchain is required.
- **Windows:** Download the appropriate [v0.9.2 MSI](https://github.com/aalsanie/distroplane/releases/tag/v0.9.2) and follow the [Windows installer guide](docs/windows-installer.md). MSIs for this release are **not Authenticode-signed**; verify checksums and provenance. The [WinGet community submission](https://github.com/microsoft/winget-pkgs/pull/464554) remains pending, so do not assume public catalog installation works.

## First release

Create a prebuilt artifact, then initialize your destination configuration with the metadata for your actual package:

```sh
distroplane init --destination npm --release my-package-1.2.3 --artifact dist/package.tgz
distroplane doctor --config distroplane.json
distroplane publish --config distroplane.json
```

`init` may require provider-specific `--set` fields or an interactive choice; it never invents package identity or credentials. `publish` reviews the immutable plan and requires exact confirmation before dispatch. For automation, use `--yes` or `--confirm-plan` only after independent review. Configure the required credential environment variable without writing its value into configuration or logs.

Recover a pending or blocked run **without creating a new publication**:

```sh
distroplane status --config distroplane.json
distroplane resume --config distroplane.json
```

Keep the project `.distroplane/` state, provider snapshots, original artifacts and credentials necessary to settle a run. See the [CLI and recovery guide](docs/cli.md), [configuration reference](docs/configuration.md), and [security model](docs/security-model.md).

## Integrations and providers

The [GitHub Action](docs/github-actions.md) supports reviewed publication and verified cross-job recovery. The published [Maven plugin `1.0.0`](maven-plugin/README.md) invokes the same native CLI and supports fresh Maven Central consumers; its version is independent of the CLI.

Provider options: [npm](providers/npm/README.md), [SDKMAN](providers/sdkman/README.md), [Homebrew](providers/homebrew/README.md), [WinGet](providers/winget/README.md). A created provider PR or vendor API acceptance is **not** necessarily public consumer availability.

[Architecture](docs/architecture.md) · [Release verification](docs/release-verification.md) · [Reliability](docs/reliability-validation.md) · [Security policy](SECURITY.md) · [Contributing](CONTRIBUTING.md)

Apache-2.0.
