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

Build your release artifact and prepare `distroplane.json` before using an integration.

<details>
<summary><strong>Maven plugin</strong> · 1.0.1</summary>

Add the plugin under `<build><plugins>` in `pom.xml`:

```xml
<plugin>
  <groupId>io.github.aalsanie</groupId>
  <artifactId>distroplane-maven-plugin</artifactId>
  <version>1.0.1</version>
</plugin>
```

Validate and review the release before explicitly approving publication:

```sh
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:validate
mvn io.github.aalsanie:distroplane-maven-plugin:1.0.1:plan
mvn -Ddistroplane.approve=true io.github.aalsanie:distroplane-maven-plugin:1.0.1:publish
```

[Maven guide](maven-plugin/README.md) · [Maven Central](https://repo1.maven.org/maven2/io/github/aalsanie/distroplane-maven-plugin/1.0.1/)

</details>

<details>
<summary><strong>Gradle plugin</strong> · 1.0.1</summary>

Add the plugin to `build.gradle`:

```groovy
plugins {
    id 'io.github.aalsanie.distroplane' version '1.0.1'
}

distroplane {
    config = 'distroplane.json'
}
```

Validate and review the release:

```sh
gradle distroplaneValidate
gradle distroplanePlan
```

To run `distroplanePublish`, explicitly configure `approve = true` or `confirmPlan = '<plan ID>'` after reviewing the plan.

[Gradle guide](gradle-plugin/README.md) · [Gradle Plugin Portal](https://plugins.gradle.org/plugin/io.github.aalsanie.distroplane)

</details>

<details>
<summary><strong>GitHub Action</strong></summary>

Run an explicit publishing workflow after producing your artifact and configuration:

```yaml
name: Publish
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: aalsanie/distroplane@main
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          version: 0.9.3
          command: publish
          approve: 'true'
          credential-mappings: npm-publish=NPM_TOKEN
          upload-recovery: 'true'
```

Pin the Action to a reviewed commit SHA for production. This example uses an npm destination with `NPM_TOKEN` configured as a repository secret.

[GitHub Action guide](docs/github-actions.md) · [Action inputs](action.yml)

</details>

[Noninteractive configuration](docs/configuration.md) · [Security](SECURITY.md) · [Changelog](CHANGELOG.md)

Apache-2.0.
