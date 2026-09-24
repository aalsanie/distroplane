# Configuration

`distroplane.json` names existing release artifacts and publication targets. It does not build artifacts or contain secret values.

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
        "authentication": {"mode": "token", "credential": "npm-publish"}
      }
    }
  ]
}
```

`schemaVersion` is currently `"1"`. `release.id` is a logical identifier; there is no separate core release-version field. Artifact names and target IDs must be unique. Artifact sources may be absolute or relative to the configuration directory.

`targets[].configuration` is interpreted by the selected provider. Provider fields are documented in the [npm](../providers/npm/README.md), [SDKMAN](../providers/sdkman/README.md), [Homebrew](../providers/homebrew/README.md), and [WinGet](../providers/winget/README.md) guides.

Planning computes artifact SHA-256 digests/sizes and provider executable digests. Do not place secret values in provider configuration because configuration is persisted into the plan.

## Provider discovery

With no explicit executable, Distroplane finds `distroplane-provider-<name>` on `PATH` (`.exe` on Windows). An absolute executable path selects that file; a relative path containing a path separator is resolved from the configuration directory.

Planning records provider identity, version, and executable digest. Apply and reconcile require matching provider binaries.

## Credentials

Providers declare credential references during planning. At execution, map each reference to an environment-variable name:

```sh
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

The host resolves only declared references and passes only the invoked provider's credentials. The equivalent Action input is `credential-mappings`.

## Validation

`validate` checks core structure. `plan` also checks artifacts, provider identity/capabilities, and provider-specific configuration. Neither publishes.

Configuration is UTF-8 JSON limited to 4 MiB. Duplicate keys, unknown core fields, duplicate artifact/target IDs, and empty required fields are rejected. Values are not expanded from environment variables, `~`, or GitHub expressions.

Targets are independent in configuration v1: there is no cross-target dependency field or target-selection flag.
