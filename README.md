# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml)
[![GitHub Action smoke](https://github.com/aalsanie/distroplane/actions/workflows/action-smoke.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/action-smoke.yml)
[![Release](https://img.shields.io/github/v/release/aalsanie/distroplane?include_prereleases&sort=semver)](https://github.com/aalsanie/distroplane/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/aalsanie/distroplane)](https://github.com/aalsanie/distroplane/blob/main/go.mod)
[![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Distroplane plans and tracks release distribution to npm, SDKMAN, Homebrew taps, and WinGet. Review what will be published before applying it, resume interrupted runs, and export one record of each target's known state. Use the same CLI locally or through GitHub Actions; no service is required.

## What it does

- Saves a deterministic plan bound to artifact hashes and provider binaries.
- Publishes through separate provider executables using named credential references.
- Records execution so pending reviews and uncertain outcomes can be reconciled later.
- Exports JSON evidence for published, pending, rejected, and failed targets.

## GitHub Actions

Once your release artifact and `distroplane.json` are ready, publishing is a plan/apply flow:

```yaml
name: Publish package
on: workflow_dispatch
permissions:
  contents: read

jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - id: plan
        uses: aalsanie/distroplane@v0.9.0-rc.2
        with:
          command: plan
      - uses: aalsanie/distroplane@v0.9.0-rc.2
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          command: apply
          plan: ${{ steps.plan.outputs.plan-path }}
          credential-mappings: npm-publish=NPM_TOKEN
          upload-evidence: 'true'
```

Planning needs no publication credentials. Apply uses credential mappings declared by the plan. Provider processes receive a minimal tool/runtime environment plus only the declared credential mappings; unrelated parent environment values are not inherited. The Action installs and checksum-verifies the matching CLI and official providers automatically. [Action details](docs/github-actions.md) cover artifact preparation, approvals, plan handoff, OIDC, and reconciliation.

## Configuration

Save this as `distroplane.json` and point it at your packed tarball. The npm provider requires `packagePath` to be absolute at runtime; the package name and version come from `package/package.json` inside the tarball.

```json
{
  "schemaVersion": "1",
  "release": {
    "id": "my-package-1.2.3",
    "artifacts": [{"name": "package", "source": "package.tgz"}]
  },
  "targets": [{
    "id": "npm",
    "provider": {"name": "npm"},
    "configuration": {
      "artifact": "package",
      "packagePath": "/absolute/path/to/package.tgz",
      "registry": "https://registry.npmjs.org/",
      "authentication": {"mode": "token", "credential": "npm-publish"}
    }
  }]
}
```

Credentials are named references, never token values. See [configuration](docs/configuration.md) for paths, provider selection, and validation.

## CLI

[Install the CLI and the providers you need](docs/release-verification.md), then use the path printed by `plan` wherever `PLAN.json` appears below. Supply `NPM_TOKEN` through your shell or CI secret store.

```sh
distroplane plan --config distroplane.json
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane status --plan PLAN.json --journal run.journal
distroplane reconcile --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

Run `reconcile` when a target is pending or its outcome is uncertain. Preserve the plan and journal between runs. The [CLI guide](docs/cli.md) explains exit codes, recovery, and automation.

## Providers

| Provider | Current publication flow |
| --- | --- |
| [npm](providers/npm/README.md) | Packed npm tarball to an npm registry; token or supplied OIDC token |
| [SDKMAN](providers/sdkman/README.md) | Candidate/version registration through the vendor API |
| [Homebrew](providers/homebrew/README.md) | Formula or cask in a custom tap, by direct push or GitHub pull request |
| [WinGet](providers/winget/README.md) | Installer manifests submitted through a GitHub pull request |

## Documentation

[GitHub Actions](docs/github-actions.md) · [CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [Installation and releases](docs/release-verification.md) · [Architecture and protocol](docs/architecture.md) · [Security model](docs/security-model.md) · [Contributing](CONTRIBUTING.md)

## Status and license

Current release: **0.9.0-rc.2**, a pre-1.0 release candidate. [Apache-2.0](LICENSE).
