# Configuration

`distroplane.json` describes a release's existing artifacts and the targets that should publish them. It does not build artifacts or contain secret values. The CLI reads it from the current directory unless you pass `--config PATH`.

## Complete example

For an npm tarball already on disk:

```json
{
  "schemaVersion": "1",
  "release": {
    "id": "my-package-1.2.3",
    "artifacts": [
      {"name": "package", "source": "dist/package.tgz", "mediaType": "application/gzip"}
    ]
  },
  "targets": [
    {
      "id": "npm",
      "provider": {"name": "npm"},
      "configuration": {
        "artifact": "package",
        "packagePath": "/absolute/path/to/project/dist/package.tgz",
        "registry": "https://registry.npmjs.org/",
        "tag": "latest",
        "authentication": {"mode": "token", "credential": "npm-publish"}
      }
    }
  ]
}
```

Replace `packagePath` with the absolute path to the same file as `source`. On Windows, use `C:/project/dist/package.tgz` or escaped backslashes. Configuration does not expand environment variables, `~`, or GitHub expressions. The [README workflow](../README.md#github-actions-quick-start) sets the npm path after packing.

## Fields

| Field | Meaning |
| --- | --- |
| `schemaVersion` | Required string `"1"` |
| `release.id` | Your logical release identifier; no separate core `version` field |
| `release.artifacts` | At least one local artifact |
| `artifacts[].name` | Unique name used by target configuration |
| `artifacts[].source` | Local file path, absolute or relative to the configuration directory |
| `artifacts[].mediaType` | Optional media type |
| `targets` | At least one target; target IDs must be unique |
| `targets[].id` | Stable name for this publication destination |
| `targets[].provider.name` | Provider identity, such as `npm`, `sdkman`, `homebrew`, or `winget` |
| `targets[].provider.executable` | Optional executable path or command name |
| `targets[].configuration` | Required JSON value interpreted by that provider; official providers require an object |

Planning calculates artifact SHA-256 digests and sizes. Do not supply `digest` or `size` in configuration. Package versions belong in provider configuration, or in the tarball metadata for npm. They are not inferred from `release.id`.

## Provider discovery

If `executable` is omitted, Distroplane looks for `distroplane-provider-<name>` on `PATH` (`.exe` on Windows). The Action installs these names automatically.

An explicit absolute path selects that file. A relative path containing `/` or `\` is resolved from the configuration directory; a bare command name is looked up on `PATH`.

```json
"provider": {"name": "npm", "executable": "./bin/distroplane-provider-npm"}
```

Planning records the provider's reported version and executable digest. Apply and reconcile require matching binaries. There is no provider version constraint field in configuration: choose the installed release before planning.

## Credentials

`npm-publish` in the example is a reference. Map it to an environment variable when executing:

```sh
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

Set `NPM_TOKEN` through your shell or CI secret store. Repeat `--credential REF=ENV` for additional references. Use the same mappings for reconciliation when the plan declares credentials. The equivalent Action input is `credential-mappings`.

The provider requests specific credentials during planning; the host resolves only those requirements before execution. Putting a token directly into `configuration` would persist it in the plan. Never do that.

## Validation and limits

```sh
distroplane validate --config distroplane.json
distroplane plan --config distroplane.json
```

`validate` checks the core structure. `plan` also reads artifacts, checks provider identity and capabilities, and validates provider configuration through the provider. Neither command publishes a release.

Configuration is UTF-8 JSON, limited to 4 MiB. Duplicate JSON keys, unknown core fields, duplicate artifact names or target IDs, and empty required fields are rejected. Identifiers must not contain whitespace or control characters. Provider-specific validation is described in each provider guide.

Targets run independently in the current configuration format. There is no cross-target dependency field, target-selection flag, artifact download step, or shared provider-configuration block. Use separate configuration files and plans for distinct approval or credential boundaries. Execution concurrency is a CLI/Action option.

Provider guides: [npm](../providers/npm/README.md), [SDKMAN](../providers/sdkman/README.md), [Homebrew](../providers/homebrew/README.md), [WinGet](../providers/winget/README.md).
