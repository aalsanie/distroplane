# WinGet provider

Generates version, installer, and default-locale manifests, pushes an update branch, and opens a GitHub pull request. It supports one installer per target and leaves review and distribution indexing to the destination repository.

Use `"provider": {"name": "winget"}` in the [shared configuration](../../docs/configuration.md), with this `configuration` object:

```json
{
  "artifact": "installer",
  "repository": "https://github.com/example/winget-pkgs.git",
  "authentication": {"credential": "winget-publish"},
  "package": {
    "identifier": "Example.Acme",
    "version": "1.2.3",
    "publisher": "Example",
    "name": "Acme",
    "license": "Apache-2.0",
    "shortDescription": "Acme command-line tool"
  },
  "installer": {
    "architecture": "x64",
    "type": "msi",
    "url": "https://example.com/releases/acme-1.2.3.msi"
  },
  "pullRequest": {
    "repository": "microsoft/winget-pkgs",
    "headOwner": "example"
  }
}
```

Replace `example` with your fork owner. `repository` is the writable clone/push destination; `pullRequest.repository` is the GitHub review destination. The fork and its base branch must already exist. `artifact` names the local installer; its SHA-256 becomes `InstallerSha256`. Upload those exact bytes to `installer.url` yourself.

## Requirements and options

Git 2 or later must be installed on `PATH`. Map `winget-publish` to a token that can push to the fork and open/read pull requests and validation checks on the review destination. The provider receives it as `DISTROPLANE_WINGET_TOKEN`. It uses HTTPS authentication without interactive Git prompts.

`0.9.0-rc.1` has a known Git discovery issue affecting this provider. Use a later release once the provider-host fix is available.

Defaults are `branch: "master"`, `manifestRoot: "manifests"`, `manifestVersion: "1.12.0"`, and `package.defaultLocale: "en-US"`. `updateBranch` is derived deterministically unless supplied. Optional package links are `packageUrl`, `publisherUrl`, and `releaseNotesUrl`. Installer URLs must use HTTPS.

Accepted architectures are `x86`, `x64`, `arm`, `arm64`, and `neutral`. Accepted installer types are `exe`, `msi`, `msix`, `inno`, `nullsoft`, `wix`, `burn`, `portable`, and `zip`. The provider emits a minimal manifest; destination policy can require fields it cannot generate. In particular, it has no installer-switch or nested ZIP-installer configuration.

`commit.message`, `commit.authorName`, and `commit.authorEmail` customize commits. `pullRequest.title`, `body`, and `api` customize submission; the API defaults to `https://api.github.com`.

## Pending publication and limits

A new submission returns `WAITING_EXTERNAL` with pull-request evidence. Reconciliation distinguishes validation pending, review pending, validation failure, closed requests, and merged requests. Failed validation or closure yields `REJECTED`; a merged request yields `PUBLISHED`. There is no client-index availability check.

`PUBLISHED` reflects repository state observed by Distroplane; it does not prove WinGet client-index availability. Destination validation remains authoritative.
