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

For casks, use `manifest.type: "cask"`, a lowercase cask name, and `installKind`/`installSource`. Default formula generation still uses the original single `bin.install` operation; cask generation supports one install entry.

## JVM application ZIP distributions (opt-in)

Gradle application distributions need both the launcher under `bin/` and the sibling `lib/*.jar` runtime classpath. To preserve the complete installation, opt in to `installLayout: "jvm-distribution"` and explicitly declare the supported Java runtime version:

```json
{
  "artifact": "archive",
  "repository": "https://github.com/example/homebrew-tap.git",
  "path": "Formula/demo.rb",
  "mode": "direct",
  "manifest": {
    "type": "formula",
    "name": "Demo",
    "description": "Demo JVM command-line tool",
    "homepage": "https://example.com/demo",
    "license": "Apache-2.0",
    "url": "https://example.com/releases/demo-1.2.3.zip",
    "version": "1.2.3",
    "binary": "demo",
    "installLayout": "jvm-distribution",
    "javaVersion": "21"
  }
}
```

Supported explicit Java versions are `8`, `11`, `17`, `21`, and `25`, mapped to a runtime `openjdk@VERSION` dependency. Match `javaVersion` to the application's minimum supported runtime and verify that the corresponding JDK formula is available in the target Homebrew installation. A Java version cannot be provided for ordinary single-binary formulas or casks.

The formula accepts either a staging root containing `bin/<binary>` and `lib/*.jar`, or exactly one direct child directory containing that structure (as in a Gradle `installDist`/distribution ZIP). It **fails installation** if the launcher and runtime JARs are absent or the layout is ambiguous. It installs **all** distribution contents under `libexec` (including sibling JARs and other files), then exposes `bin/<binary>` using Homebrew's `write_env_script` with `Language::Java.overridable_java_home_env`. Because the real script runs from `libexec/bin`, relative application-home and classpath resolution continue to work. The wrapper uses the configured JDK by default while allowing a user-specified `JAVA_HOME`.

Only `installLayout: "jvm-distribution"` triggers the new formula structure. Existing single-binary output remains byte-for-byte unchanged. Plans remain deterministic and still pin the local artifact's SHA-256; the provider does not upload or regenerate a ZIP. Ensure the hosted URL serves the same immutable bytes as the planned artifact. Direct and pull-request publication retain their existing idempotent reconciliation.

A macOS CI integration test compiles a Java application with its support class in **a separate JAR**, builds a Gradle-shaped ZIP, generates the formula with the real archive SHA-256, installs it using native Homebrew, verifies the `libexec` files and executes the CLI, then publishes and reconciles the exact formula in a local tap. The integration uses `brew install --ignore-dependencies` with an existing runner JDK and `JAVA_HOME` to avoid network-dependent JDK installation; the formula's declared runtime dependency remains present. Live Homebrew Core review and its broader formula policy are outside this provider's scope.

## Results

Exact planned content already on the base branch is `PUBLISHED`. Direct mode commits and pushes it. Pull-request mode returns `WAITING_EXTERNAL`; reconciliation checks the base branch for the planned content. A closed pull request without that content, or a conflicting update branch, is `REJECTED`.

The provider does not run Homebrew installer, test, or policy checks.
