# Distroplane

[![CI](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/ci.yml) [![CodeQL](https://github.com/aalsanie/distroplane/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/aalsanie/distroplane/actions/workflows/codeql.yml) [![CLI Release](https://img.shields.io/github/v/release/aalsanie/distroplane?label=CLI)](https://github.com/aalsanie/distroplane/releases/latest) [![Maven Central](https://img.shields.io/maven-central/v/io.github.aalsanie/distroplane-maven-plugin?label=Maven%20plugin)](https://central.sonatype.com/artifact/io.github.aalsanie/distroplane-maven-plugin) [![License](https://img.shields.io/github/license/aalsanie/distroplane)](LICENSE)

Publish prebuilt releases to npm, SDKMAN, Homebrew, and WinGet.

## Install

```sh
brew install aalsanie/tap/distroplane
```

On Windows:

```powershell
winget install --id Aalsanie.Distroplane --exact
```

[Other installation options](docs/release-verification.md).

## Use

Build your artifact first, then run:

```sh
distroplane init
distroplane doctor
distroplane publish
```

Follow the prompts to choose a destination, provide its required metadata and approve the release. Credentials stay in environment variables, not configuration files.

To check or continue an existing release:

```sh
distroplane status
distroplane resume
```

Do not delete `.distroplane/` while a release may still need recovery.

## Maven plugin

Add the [published Maven plugin](https://central.sonatype.com/artifact/io.github.aalsanie/distroplane-maven-plugin/1.0.0) to `<build><plugins>` in your `pom.xml`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.0</version>
  <configuration>
    <cliVersion>0.9.2</cliVersion>
    <config>distroplane.json</config>
  </configuration>
</plugin>
```

With a prebuilt artifact and `distroplane.json`, run `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.0:plan` to review a release. [Maven usage](maven-plugin/README.md).

## GitHub Action

Publish a prebuilt artifact from a GitHub Actions workflow:

```yaml
- uses: aalsanie/distroplane@main
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    version: 0.9.2
    command: publish
    approve: 'true'
    credential-mappings: npm-publish=NPM_TOKEN
```

Requires `distroplane.json` in the checked-out repository and a configured npm destination. Pin the Action to a reviewed commit in production. [GitHub Action usage](docs/github-actions.md).

[CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [Security](SECURITY.md)

Apache-2.0.
