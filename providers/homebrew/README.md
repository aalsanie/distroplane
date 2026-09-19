# Homebrew tap provider

Generates a formula or cask and commits it to a custom tap. It supports a direct push or a GitHub pull request. It does not upload the release archive or automate Homebrew Core submissions.

Use `"provider": {"name": "homebrew"}` in the [shared configuration](../../docs/configuration.md). A minimal formula target uses:

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

`artifact` selects the local archive whose SHA-256 is embedded in the generated file. Host those exact bytes at `manifest.url` before applying. `repository` must already contain the base branch; `branch` defaults to `main`. `path` is relative to the tap repository.

## Git and credentials

Git 2 or later must be installed on the executor's `PATH`. Map `tap-publish` to a token with write access to the tap; the provider receives `DISTROPLANE_HOMEBREW_TOKEN` and uses an HTTPS authorization header. Do not embed credentials in the repository URL. Git's global/system configuration and interactive credential prompts are disabled.

`0.9.0-rc.1` has a known Git discovery issue affecting this provider. Use a later release once the provider-host fix is available.

## Pull requests and casks

For review before publication, set `mode` to `pull-request` and add:

```json
"pullRequest": {"repository": "example/homebrew-tap"}
```

The credential must also permit pull requests in that repository. The provider derives a deterministic update branch, or you can set `updateBranch`. The base and update branches are in the configured tap; there is no separate fork-owner option. `pullRequest.api` defaults to `https://api.github.com`; `title` and `body` are optional.

For a cask, set `path` to a path such as `Casks/acme.rb` and use `manifest.type: "cask"`, a lowercase `manifest.name` such as `acme`, and `installKind` (`app`, `pkg`, or `binary`) with `installSource` such as `Acme.app`. Omit `binary` and `license`, which are formula-only fields. Formula generation supports one `bin.install` entry; cask generation supports one install entry.

Optional `commit.message`, `commit.authorName`, and `commit.authorEmail` override the generated commit metadata. Formula `manifest.license` is optional.

## Results

An exact file already on the base branch is `PUBLISHED`. Direct mode commits and pushes the planned file. Pull-request submission is `WAITING_EXTERNAL`; reconciliation checks the base branch for the planned content. A closed pull request without that content, or a conflicting update branch, is `REJECTED`.

The provider does not run Homebrew's installer, tests, or policy checks. Review the generated formula/cask in the plan or pull request before relying on it.
