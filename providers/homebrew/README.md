# Homebrew tap provider

Generates a formula or cask and commits it to a custom tap by direct push or GitHub pull request. It does not upload release archives or automate Homebrew Core submissions.

A minimal formula target configuration is:

```json
{
  "artifact": "archive",
  "repository": "https://github.com/example/homebrew-tap.git",
  "path": "Formula/acme.rb",
  "mode": "direct",
  "manifest": {
    "type": "formula",
    "name": "Acme",
    "description": "Acme command-line tool",
    "homepage": "https://example.com/acme",
    "url": "https://example.com/releases/acme-1.2.3.tar.gz",
    "version": "1.2.3",
    "binary": "acme"
  },
  "authentication": {"credential": "tap-publish"}
}
```

`artifact` selects the local archive whose SHA-256 is embedded in the generated file. Host those bytes at `manifest.url` before applying. `branch` defaults to `main`.

Git must be on `PATH`. Map the configured credential to a token with repository write access. Git system/global configuration and interactive credential prompts are disabled.

## Pull requests and casks

For review mode, set `mode` to `pull-request` and configure `pullRequest.repository` with the GitHub `owner/repo`. The update branch is deterministic unless `updateBranch` is supplied. The base and update branches are in the configured tap; there is no separate fork-owner option.

For casks, use `manifest.type: "cask"`, a lowercase cask name, and `installKind`/`installSource`. Formula generation supports one `bin.install` entry; cask generation supports one install entry.

## Results

Exact planned content already on the base branch is `PUBLISHED`. Direct mode commits and pushes it. Pull-request mode returns `WAITING_EXTERNAL`; reconciliation checks the base branch for the planned content. A closed pull request without that content, or a conflicting update branch, is `REJECTED`.

The provider does not run Homebrew installer, test, or policy checks.
