# Homebrew tap provider

Generates a formula or cask and updates a **custom tap**, either by direct Git push or by opening a GitHub pull request. It does not upload archives, submit to Homebrew Core, or run Homebrew policy checks.

## Formula configuration

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

The formula embeds the local artifact's SHA-256. Publish identical immutable bytes at `manifest.url` before applying. Git must be on `PATH`; supply a repository write token through the declared credential reference. Git system/global configuration and interactive authentication prompts are disabled. The default branch is `main`.

For review mode, set `mode: "pull-request"` and `pullRequest.repository` to the GitHub `owner/repo`. The update branch is deterministic unless `updateBranch` is supplied; it is created in the configured tap, not a separate fork. For casks, set `manifest.type: "cask"` and the applicable `installKind`/`installSource`. The generated cask supports a single install entry.

## JVM application distributions

A Gradle-style application ZIP contains a launcher in `bin/` and runtime JARs in `lib/`. A formula that installs only the launcher will not run correctly. Opt in using these manifest fields:

```json
{
  "type": "formula",
  "name": "Demo",
  "description": "Demo JVM CLI",
  "homepage": "https://example.com/demo",
  "url": "https://example.com/demo-1.2.3.zip",
  "version": "1.2.3",
  "binary": "demo",
  "installLayout": "jvm-distribution",
  "javaVersion": "21"
}
```

Supported `javaVersion` values are `8`, `11`, `17`, `21`, and `25`. The formula declares the corresponding `openjdk@VERSION` dependency. Installation validates a `bin/<binary>` launcher and `lib/*.jar`, at the archive root or within one top-level directory; missing or ambiguous layouts fail installation. It installs the **complete distribution** into `libexec` and creates a Java-aware launcher wrapper, preserving the relative runtime classpath. `JAVA_HOME` can override the default Java installation.

This is opt-in; ordinary single-binary formulas remain unchanged. A macOS integration test installs and executes a generated JVM distribution formula with separate runtime JARs. It does not stand in for Homebrew Core review or broad policy compliance.

## Results

Matching planned content already on the tap's base branch is `PUBLISHED`. Direct mode commits and pushes that content. Pull-request mode reports `WAITING_EXTERNAL` until reconciliation observes the planned content on the base branch. A closed PR without that content or a conflicting update branch is `REJECTED`. Reconciliation only observes; it never pushes or opens another PR.
