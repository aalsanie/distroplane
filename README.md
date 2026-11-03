# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml) [![CLI Release](https://img.shields.io/github/v/release/aalsanie/distroplane?label=CLI)](https://github.com/aalsanie/distroplane/releases/latest) [![Maven Central](https://img.shields.io/maven-central/v/io.github.aalsanie/distroplane-maven-plugin?label=Maven%20plugin)](https://central.sonatype.com/artifact/io.github.aalsanie/distroplane-maven-plugin)

Distribute prebuilt artifacts to npm, SDKMAN, Homebrew taps, and WinGet.

## Start

Install on macOS or Linux with Homebrew:

```sh
brew install aalsanie/tap/distroplane
```

[Windows MSI and other installation methods](docs/install.md).

From your project directory, after building an artifact:

```sh
distroplane init
distroplane doctor
distroplane publish
```

The CLI guides configuration, checks readiness, previews the release, and requests approval.

For an existing release:

```sh
distroplane status
distroplane resume
```

Preserve `.distroplane/` and the original artifacts until publication is settled. [Recovery and exit codes](docs/recovery.md).

## Integrations

- **Maven:** [Published plugin 1.0.1](https://repo1.maven.org/maven2/io/github/aalsanie/distroplane-maven-plugin/1.0.1/) · [Usage](maven-plugin/README.md)
- **GitHub Actions:** [Workflow example and recovery](docs/github-actions.md)

[Noninteractive configuration](docs/configuration.md) · [Security](SECURITY.md) · [Changelog](CHANGELOG.md)

Apache-2.0.
