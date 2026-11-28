# WinGet provider

Generates WinGet version, installer, and default-locale manifests, pushes them to a writable fork, and opens a GitHub pull request against the destination repository. It supports one installer target per configuration. It does **not** upload the installer or verify WinGet client-index availability.

## Configuration

Run `distroplane init --destination winget:msi` or `distroplane init --destination winget:zip` and supply your writable fork, existing package identifier, version, publisher, name, actual license, description, installer URL, architecture and review repository. ZIP setup also asks for the nested installer type and path.

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

`repository` must be writable; `pullRequest.repository` is the review/publication destination. The fork/base branch must already exist. Git and a token with fork push and GitHub PR permissions are required. Defaults: base `branch: "master"`, `manifestRoot: "manifests"`, schema `manifestVersion: "1.12.0"`, locale `en-US`. The generated manifest set is minimal and may still fail external repository policy.

## ZIP installers

For a ZIP artifact, set `installer.type: "zip"`, an absolute `archivePath` to **the same bytes** as the planned artifact, `nestedInstallerType` (`portable`, `exe`, or `msi`), and `nestedInstallerFiles`. For example:

```json
{
  "installer": {
    "architecture": "x64",
    "type": "zip",
    "url": "https://example.com/releases/acme-1.2.3.zip",
    "archivePath": "C:/release/acme-1.2.3.zip",
    "nestedInstallerType": "portable",
    "nestedInstallerFiles": [
      {"relativeFilePath": "bin/acme.exe", "portableCommandAlias": "acme"}
    ]
  }
}
```

At planning time, the provider verifies ZIP size/SHA-256 and exact-case nested file paths. It rejects traversal, drive/absolute paths, reserved Windows names, symlinks, special or encrypted entries, and case-insensitive name collisions. `portable` supports one or more `.exe` files and optional unique command aliases; nested `exe` and `msi` require one file of the corresponding type and do not accept aliases. The local `archivePath` is **not** emitted into the manifest or operation payload. Host the identical ZIP at `installer.url` yourself.

WinGet's [installer manifest schema](https://github.com/microsoft/winget-pkgs/blob/master/doc/manifest/schema/1.12.0/installer.md) defines the emitted nested-installer fields. The Windows integration test executes a nested ZIP fixture and, when `winget` is available, runs `winget validate`; external review and index publication are separate.

## Publication and recovery

| Condition | Result |
| --- | --- |
| Destination repository/base branch contains all three exact planned manifests at one resolved commit | `PUBLISHED` |
| Conflicting destination content, conflicting update branch, closed unmerged PR, or failed validation | `REJECTED` |
| Submission, pending checks/review, or merged PR without destination content | `WAITING_EXTERNAL` |
| Branch moves during observation or submission acknowledgement is lost | Reconcile without pushing or opening another PR |

A matching fork, an accepted PR or a passing check alone is not publication proof. Local repository and controlled API tests cover submission, review, source-branch deletion and destination observation; external policy and client-index availability remain outside that evidence.
