# npm provider

Publishes an existing gzip tarball to an npm-compatible registry through its HTTP API. It does not run `npm publish`, build artifacts, or read `.npmrc`.

## Configuration

```json
{
  "artifact": "package",
  "packagePath": "/absolute/path/to/package.tgz",
  "registry": "https://registry.npmjs.org/",
  "authentication": {"mode": "token", "credential": "npm-publish"}
}
```

`artifact` names an artifact in the core release configuration. `packagePath` must be absolute and must reference the same bytes. The archive must contain `package/package.json` with a valid package name and semantic version. Optional `tag` defaults to `latest`; optional `access` is `public` or `restricted`. Use an explicit prerelease tag when appropriate. Registries require HTTPS unless `allowInsecureHTTP` is enabled for local testing.

## Authentication

With `mode: "token"`, map the credential reference to an environment variable at apply/reconcile time:

```sh
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

`mode: "trusted-publishing"` exchanges a supplied npm-audience OIDC ID token for an npm token. The provider does **not** obtain the ID token itself. In the GitHub Action, set `oidc: npm` and `npm-oidc-ref` to this destination's unique credential reference; the Action explicitly requests a fresh GitHub ID token with audience `npm:registry.npmjs.org` under `permissions: id-token: write`. Only the official npm HTTPS registry is supported by automatic acquisition. The Action maps the token to a transient environment source for the npm provider and never persists it. Plain CLI invocation still requires an externally supplied ID token. A later reconciliation needs a fresh token.

## Result and recovery semantics

Before upload, `apply` checks the registry for the planned version. Identical integrity and shasum yield `PUBLISHED`; conflicting content yields `REJECTED`. A successful upload response yields `PUBLISHED`. A lost or uncertain upload result is **ambiguous**, not a reason for an automatic retry.

`reconcile` reads registry metadata without uploading. If the version is absent, the operation remains `WAITING_EXTERNAL`; absence does not prove an earlier timed-out publish cannot still complete. An explicit one-shot retry requires the [operator authorization workflow](../../docs/cli.md#explicit-retry-of-an-ambiguous-npm-publish).

The provider does not change dist-tags on an already matching version or create npm provenance attestations. Preserve the tarball until the run finishes.
