# WinGet provider

Generates WinGet version, installer, and default-locale manifests, pushes them to a writable fork, and opens a GitHub pull request against the destination repository. It supports one installer target per configuration. It does **not** upload the installer or verify WinGet client-index availability.

## Configuration

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

## Results

Submission returns `WAITING_EXTERNAL`. Reconciliation checks PR status and validation, then observes all planned manifest files at **one resolved commit** on the destination's base branch. Only byte-for-byte matching content is `PUBLISHED`; missing content stays `WAITING_EXTERNAL`, and conflicting content is `REJECTED`. A merged PR alone is not publication proof.
