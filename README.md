# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/aalsanie/distroplane?include_prereleases&sort=semver)](https://github.com/aalsanie/distroplane/releases)
[![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Distroplane distributes **existing release artifacts** to npm, SDKMAN, custom Homebrew taps, and WinGet. It separates an immutable publication plan from execution, records results in a durable journal, and reconciles uncertain outcomes without blindly repeating publication.

## CLI

Install the [CLI and providers](docs/release-verification.md), then create a [configuration](docs/configuration.md) and build the artifacts it references.

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane status --plan PLAN.json --journal run.journal
distroplane reconcile --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
distroplane evidence --plan PLAN.json --journal run.journal --output evidence.json
```

Use the path printed by `plan` in place of `PLAN.json`. `--credential REF=ENV` names an environment variable; it does not contain a secret. Preserve the plan, journal, artifacts, and provider binaries for recovery. See the [CLI guide](docs/cli.md) for states, exit codes, and operator-authorized retries.

## GitHub Actions

After the `v0.9.1` tag is published, an npm target with credential reference `npm-publish` can use:

```yaml
- id: plan
  uses: aalsanie/distroplane@v0.9.1
  with:
    command: plan

- uses: aalsanie/distroplane@v0.9.1
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    command: apply
    plan: ${{ steps.plan.outputs.plan-path }}
    credential-mappings: npm-publish=NPM_TOKEN
```

The example runs in one job. A separate apply job must restore the exact planned inputs and paths; the Action's optional plan artifact alone is not sufficient. See [GitHub Actions](docs/github-actions.md).

## Providers

| Provider | Publication boundary |
| --- | --- |
| [npm](providers/npm/README.md) | Publishes a package tarball to an npm-compatible registry |
| [SDKMAN](providers/sdkman/README.md) | Registers a hosted archive via the vendor API |
| [Homebrew](providers/homebrew/README.md) | Updates a formula or cask in a custom tap |
| [WinGet](providers/winget/README.md) | Opens a manifest pull request; publication is checked separately |

Distroplane does not build or host artifacts, guarantee exactly-once delivery, or roll back external changes.

## Engineering documentation

[Architecture](docs/architecture.md) · [Security model](docs/security-model.md) · [Reliability](docs/reliability-validation.md) · [Release verification](docs/release-verification.md) · [Contributing](CONTRIBUTING.md)

Apache-2.0.
