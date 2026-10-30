# Distroplane

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

[CLI](docs/cli.md) · [Configuration](docs/configuration.md) · [GitHub Action](docs/github-actions.md) · [Maven plugin](maven-plugin/README.md) · [Security](SECURITY.md)

Apache-2.0.
