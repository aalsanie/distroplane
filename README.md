# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/aalsanie/distroplane?include_prereleases&sort=semver)](https://github.com/aalsanie/distroplane/releases)
[![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Lightweight tool for recoverable release distribution to npm, SDKMAN, Homebrew taps, and WinGet. It plans existing artifacts, records publication state, and reconciles pending or uncertain outcomes before retrying side effects.

## GitHub Actions

For an npm target configured with the credential reference `npm-publish`:

```yaml
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
```

See [GitHub Actions](docs/github-actions.md) for plan handoff, reconciliation, OIDC, and evidence.

## CLI

[Install Distroplane](docs/release-verification.md), create a [configuration](docs/configuration.md), then:

```sh
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
| [npm](providers/npm/README.md) | npm-compatible registry |
| [SDKMAN](providers/sdkman/README.md) | SDKMAN vendor API |
| [Homebrew](providers/homebrew/README.md) | Custom tap by push or pull request |
| [WinGet](providers/winget/README.md) | Manifest pull request |

## Documentation

[CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [Architecture](docs/architecture.md) · [Security](docs/security-model.md) · [Release verification](docs/release-verification.md) · [Contributing](CONTRIBUTING.md)

Apache-2.0.
