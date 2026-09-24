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

## Results

A new submission is `WAITING_EXTERNAL`. Reconciliation distinguishes pending validation/review, failed validation, closed requests, merged requests, and destination publication.

A merged pull request remains `WAITING_EXTERNAL` until every planned manifest is observed byte-for-byte in `pullRequest.repository` on the configured base branch at one resolved commit. Exact content is `PUBLISHED`; missing files remain `WAITING_EXTERNAL`; differing files are `REJECTED`.

Fork state and pull-request status are submission evidence, not publication proof. WinGet client-index availability is not checked.
