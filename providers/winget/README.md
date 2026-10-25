# WinGet provider

Generates version, installer, and default-locale manifests, pushes an update branch, and opens a GitHub pull request. It supports one installer per target.

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

`repository` is the writable clone/push destination; `pullRequest.repository` is the review and publication destination. The fork and base branch must already exist. `artifact` names the local installer and its SHA-256 becomes `InstallerSha256`; host those bytes at `installer.url`.

Git must be on `PATH`. Map the configured credential to a token that can push to the fork and read/open pull requests and validation checks.

Defaults are `branch: "master"`, `manifestRoot: "manifests"`, `manifestVersion: "1.12.0"`, and `package.defaultLocale: "en-US"`. Architectures and installer types are provider-validated. The generated manifest is intentionally minimal and may not satisfy every destination policy.

## ZIP archives (portable and nested installers)

ZIP archives require the nested installer metadata from the WinGet 1.12 schema. Supported nested types are `portable` (one or more `.exe` files), `exe` (one `.exe`), and `msi` (one `.msi`). Other nested layouts fail at planning rather than producing an un-installable manifest. Non-ZIP installers retain their existing configuration and output.

For an archive containing `bin/acme.exe`:

```json
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
```

`archivePath` must be an absolute path to the **same ZIP bytes** declared by the release artifact. During planning the provider checks the ZIP size and SHA-256 against the immutable artifact digest, verifies that every configured nested executable exists with exactly matching case, and rejects unsafe/ambiguous ZIP entries: traversals, absolute/drive paths, Windows reserved names, symlinks, encrypted or special entries, and case-insensitive collisions. Nested paths use forward slashes without `.` or `..` segments. File names must end in `.exe` for portable or executable installers and `.msi` for nested MSI installations. Portable command aliases are optional but must be unique, safe Windows command names of at most 40 characters; aliases are forbidden for other nested installer types. The local `archivePath` is **never included in the generated manifest or operation payload**.

The ZIP manifest renders `NestedInstallerType` and each `NestedInstallerFiles` entry according to Microsoft's [WinGet installer manifest schema](https://github.com/microsoft/winget-pkgs/blob/master/doc/manifest/schema/1.12.0/installer.md). The downloaded ZIP at `installer.url` must be byte-for-byte identical to `archivePath`; Distroplane does not upload that file. The Windows integration fixture builds and runs a real executable from the selected nested ZIP path, then submits the corresponding manifests to a simulated repository. If `winget` is present on the Windows runner, it also runs `winget validate` on the generated manifest set. Repository policy and actual package-index acceptance remain external checks.

## Results

A new submission is `WAITING_EXTERNAL`. Reconciliation distinguishes pending validation/review, failed validation, closed requests, merged requests, and destination publication.

A merged pull request remains `WAITING_EXTERNAL` until every planned manifest is observed byte-for-byte in `pullRequest.repository` on the configured base branch at one resolved commit. Exact content is `PUBLISHED`; missing files remain `WAITING_EXTERNAL`; differing files are `REJECTED`.

Fork state and pull-request status are submission evidence, not publication proof. WinGet client-index availability is not checked.
