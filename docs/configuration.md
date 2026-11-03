# Configuration

Run `distroplane init` to generate `distroplane.json` interactively. For automation, supply all inputs explicitly. For example, after creating an npm package tarball:

```sh
distroplane init --destination npm --release my-package-1.2.3 --artifact package.tgz --non-interactive
distroplane doctor --credential npm-publish=NPM_TOKEN
distroplane publish --credential npm-publish=NPM_TOKEN
```

Set `NPM_TOKEN` in the environment before running the last two commands. `npm-publish` is the credential reference in the config. `--credential REF=ENV` selects an environment-variable source; `credentialSources` can provide persistent reference-to-environment mappings.

See `distroplane init --help` for provider-specific `--set FIELD=VALUE` options, or the [configuration schema](../schemas/config-v2.schema.json) for direct JSON editing.

Destination-specific fields: [npm](../providers/npm/README.md) · [SDKMAN](../providers/sdkman/README.md) · [Homebrew](../providers/homebrew/README.md) · [WinGet](../providers/winget/README.md).
