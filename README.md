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

Pending means publication is not settled. Keep `.distroplane/`, the original configuration and artifact bytes; resume the same run instead of publishing again.

For the next release, build to a new artifact path. With a CLI supporting `--update` (current source, not 0.9.3):

```sh
distroplane init --update
distroplane doctor
distroplane publish
```

Select earlier runs with `status --release ID` or `resume --release ID`. [CLI usage](docs/cli.md).

## Maven plugin

<details>
<summary>Maven configuration and usage</summary>

Add the [published Maven plugin](https://central.sonatype.com/artifact/io.github.aalsanie/distroplane-maven-plugin/1.0.1) to `<build><plugins>` in your `pom.xml`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.1</version>
  <configuration>
    <cliVersion>0.9.3</cliVersion>
    <config>distroplane.json</config>
  </configuration>
</plugin>
```

With a prebuilt artifact and `distroplane.json`, run `mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:plan` to review a release. [Maven usage](maven-plugin/README.md).

</details>

## Gradle plugin

<details>
<summary>Gradle configuration and usage</summary>

Apply the [published Gradle plugin](https://plugins.gradle.org/plugin/io.github.aalsanie.distroplane/1.0.0) to the root project:

```groovy
plugins {
    id 'io.github.aalsanie.distroplane' version '1.0.0'
}

distroplane {
    cliVersion = '0.9.3'
    config = 'distroplane.json'
}
```

With a prebuilt artifact and `distroplane.json`, run `gradle distroplanePlan`. [Gradle usage](gradle-plugin/README.md).

</details>

## GitHub Action

<details>
<summary>Workflow example</summary>

Publish a prebuilt artifact from a GitHub Actions workflow:

```yaml
- uses: aalsanie/distroplane@73e97ccbeb0c05ed1a82d836f2d3bba3ff7bc0f4 # v0.9.3
  env:
    NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
  with:
    version: 0.9.3
    command: publish
    approve: 'true'
    credential-mappings: npm-publish=NPM_TOKEN
```

Requires `distroplane.json` in the checked-out repository and a configured npm destination. [OIDC and recovery](docs/github-actions.md).

</details>

[CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [Security](SECURITY.md) · [Changelog](CHANGELOG.md)

Apache-2.0.
