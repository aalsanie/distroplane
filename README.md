# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml)
[![GitHub Action smoke](https://github.com/aalsanie/distroplane/actions/workflows/action-smoke.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/action-smoke.yml)
[![Release](https://img.shields.io/github/v/release/aalsanie/distroplane?include_prereleases&sort=semver)](https://github.com/aalsanie/distroplane/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/aalsanie/distroplane)](https://github.com/aalsanie/distroplane/blob/main/go.mod)
[![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Lightweight release distribution for npm, SDKMAN, Homebrew taps, and WinGet. Plan publication, apply it, and reconcile pending or uncertain outcomes without blindly repeating a publish.

- Deterministic plans bound to artifact and provider hashes.
- Durable execution journal with resumable state.
- Reconciliation for pending or ambiguous publication outcomes.
- JSON evidence for published, pending, rejected, and failed targets.
- Same CLI locally or through GitHub Actions.

## Quick start

[Install Distroplane and the providers you need](docs/release-verification.md), then create a [configuration](docs/configuration.md).

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane status --plan PLAN.json --journal run.journal
distroplane reconcile --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

Use the path returned by `plan` in place of `PLAN.json`. Preserve the plan and journal until the run is complete.

## Providers

| Provider | Publication flow |
| --- | --- |
| [npm](providers/npm/README.md) | Packed npm tarball to an npm registry; token or supplied OIDC token |
| [SDKMAN](providers/sdkman/README.md) | Candidate/version registration through the vendor API |
| [Homebrew](providers/homebrew/README.md) | Formula or cask in a custom tap, by direct push or GitHub pull request |
| [WinGet](providers/winget/README.md) | Installer manifests submitted through a GitHub pull request |

## GitHub Actions

The [composite Action](docs/github-actions.md) installs the matching checksum-verified CLI and official providers and exposes the same plan/apply/reconcile workflow used locally.

## Documentation

[CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [GitHub Actions](docs/github-actions.md) · [Architecture](docs/architecture.md) · [Security](docs/security-model.md) · [Release verification](docs/release-verification.md) · [Contributing](CONTRIBUTING.md)

## Status

Current release: **0.9.0-rc.2**. Pre-1.0. [Apache-2.0](LICENSE).
