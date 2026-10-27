# Configuration

`distroplane.json` identifies prebuilt release artifacts and the destinations that should receive them. The core does not build or upload artifacts; each provider defines its own publication behavior.

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

Configuration v1 uses `schemaVersion: "1"` and remains supported without automatic rewriting. `release.id` is a logical identifier; there is no separate core release-version field. Artifact names and target IDs must be unique. Artifact sources may be absolute or relative to the configuration directory.

`targets[].configuration` is interpreted by the selected provider. Provider fields are documented in the [npm](../providers/npm/README.md), [SDKMAN](../providers/sdkman/README.md), [Homebrew](../providers/homebrew/README.md), and [WinGet](../providers/winget/README.md) guides.

Planning computes artifact SHA-256 digests/sizes and provider executable digests. Provider configuration and operation payloads are persisted in the immutable plan: supply credential **references**, never secret values. For npm, `packagePath` must be an absolute path to the same tarball bytes as the named artifact.

## Configuration v2

Configuration v2 (`"schemaVersion": "2"`) uses a named artifact map and provider-keyed destinations. The destination `artifact` selects a named release artifact; its `options` hold the provider’s existing settings (except `artifact`).

```json
{
  "schemaVersion": "2",
  "release": {"id": "my-package-1.2.3", "artifacts": {"package": {"source": "dist/package.tgz", "mediaType": "application/gzip"}}},
  "destinations": {
    "npm": {
      "artifact": "package",
      "options": {"packagePath": "dist/package.tgz", "registry": "https://registry.npmjs.org/", "authentication": {"mode": "token", "credential": "npm-publish"}}
    }
  }
}
```

Official destination keys are `npm`, `sdkman`, `homebrew`, and `winget`. A provider value may be one destination object (its ID defaults to the provider key), or a nonempty array of objects, each with a distinct explicit `id`. Multiple destinations can share an artifact. No remote URLs, package identities, or credential values are inferred.

| Provider | Required `options` metadata |
|---|---|
| npm | `packagePath`, `registry`, `authentication.mode` (`token` or `trusted-publishing`), `authentication.credential` |
| sdkman | `candidate`, `version`, `url`, `authentication.mode`; distinct credential references for `state` or `legacy` mode |
| homebrew | `repository`, `path`, `mode`, `manifest` (type, name, description, homepage, url, version and type-specific fields); `pullRequest.repository` when applicable |
| winget | `repository`, `package` (identifier, version, publisher, name, license, shortDescription), `installer` (architecture, type, url), `authentication.credential`, `pullRequest.repository` |

Artifact sources are resolved relative to the configuration directory by the planner. V2 also makes a relative npm `options.packagePath` absolute against that directory before planning. Other destination-specific paths retain their provider semantics. Literal paths only: no environment, home-directory or shell interpolation.

Structural validation rejects unknown fields, invalid field types, missing metadata, duplicate IDs, malformed URLs, and unsupported providers with field-path diagnostics. Provider planning still verifies the full provider semantics and artifact bytes. Credential **references** are configuration, while credential availability is an execution prerequisite; never put secret values in the file.

V1 and v2 normalize into the same planner model. Equivalent release, artifact, destination, and provider configuration inputs yield the same semantic plan; configuration files are not rewritten or migrated automatically.

## Provider discovery

With no explicit executable, Distroplane first checks providers bundled with the CLI, then finds `distroplane-provider-<name>` on `PATH` (`.exe` on Windows). An absolute executable path selects that file; a relative path containing a path separator is resolved from the configuration directory.

Planning records provider identity, version, and executable digest. Apply and reconcile require matching provider binaries.

## Credentials

Providers declare credential references during planning. At execution, map each reference to an environment-variable name:

```sh
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

The host resolves only declared references and passes only the invoked provider's credentials. The equivalent Action input is `credential-mappings`. See the [CLI recovery guide](cli.md) for what happens when a credential is missing.

For schema v2, `credentialSources` optionally maps a declared credential reference to an **environment variable name**, never a credential value:

```json
"credentialSources": {"npm-publish": "MY_NPM_TOKEN"}
```

The source precedence is `--credential REF=ENV`, then `credentialSources`, then a purpose-specific default. With a trusted CLI-associated installed provider executable, a single destination per provider can use `NPM_TOKEN` for npm token mode; `SDKMAN_VENDOR_EMAIL` and `SDKMAN_VENDOR_PASSWORD` for SDKMAN State API; `SDKMAN_CONSUMER_KEY` and `SDKMAN_CONSUMER_TOKEN` for SDKMAN legacy; `HOMEBREW_GITHUB_TOKEN` for Homebrew; and `WINGET_GITHUB_TOKEN` for WinGet. Multiple destinations for a provider must explicitly map separate references for separate accounts. There is no implicit `GITHUB_TOKEN`/`GH_TOKEN` access or local npm trusted-publishing OIDC acquisition. Missing credentials remain blocked before dispatch. No credential values are saved in config, plan, journal, or evidence.

## Validation

`validate` checks core structure. `plan` also checks artifacts, provider identity/capabilities, and provider-specific configuration. Neither publishes.

Configuration is UTF-8 JSON limited to 4 MiB. Duplicate keys, unknown core fields, duplicate artifact/target IDs, and empty required fields are rejected. Values are not expanded from environment variables, `~`, or GitHub expressions.

Targets are independent in configuration v1: there is no cross-target dependency field or target-selection flag.
