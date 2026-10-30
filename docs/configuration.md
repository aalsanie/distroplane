# Configuration

Run `distroplane init` to create `distroplane.json`. It finds prebuilt artifacts in common build folders and asks for missing information. To skip prompts, specify the destination, release, artifact, and required metadata using `distroplane init --help`.

A minimal npm example (using a package tarball created by `npm pack`):

```json
{
  "schemaVersion": "2",
  "release": {
    "id": "my-package-1.2.3",
    "artifacts": {
      "package": {"source": "dist/package.tgz"}
    }
  },
  "destinations": {
    "npm": {
      "artifact": "package",
      "options": {
        "packagePath": "dist/package.tgz",
        "registry": "https://registry.npmjs.org/",
        "authentication": {"mode": "token", "credential": "npm-publish"}
      }
    }
  }
}
```

Set `NPM_TOKEN` in your environment; never paste a token into JSON. `npm-publish` is a **credential reference**, not the token itself. For multiple accounts, map credentials explicitly with `credentialSources` or `--credential REF=ENV`.

Other destinations: [SDKMAN](../providers/sdkman/README.md) · [Homebrew](../providers/homebrew/README.md) · [WinGet](../providers/winget/README.md). Each needs its own artifact and destination metadata; Distroplane never guesses your package name, release URL, or credentials.

Use `distroplane doctor` to check readiness, then `distroplane publish` to review and approve.

[CLI and recovery](cli.md)
