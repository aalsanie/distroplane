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

`artifact` names an artifact in the core release configuration. `packagePath` must be absolute and must reference the same bytes. The archive must contain `package/package.json` with a valid package name and semantic version; packages marked private are refused. Optional `tag` defaults to `latest`; optional `access` is `public` or `restricted`. Use an explicit prerelease tag when appropriate. Registries require HTTPS unless `allowInsecureHTTP` is enabled for local testing.

## Authentication

With `mode: "token"`, map the credential reference to an environment variable at apply/reconcile time:

```sh
distroplane apply --config distroplane.json --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

`mode: "trusted-publishing"` exchanges a supplied npm-audience OIDC ID token for an npm token using the [registry API](https://api-docs.npmjs.com/). Configure an existing package for direct-publish permission, not stage-only permission. The registry must permit lookup of the planned version. The provider does **not** obtain the ID token itself. In the GitHub Action, set `oidc: npm` and `npm-oidc-ref` to this destination's unique credential reference; the Action explicitly requests a fresh GitHub ID token with audience `npm:registry.npmjs.org` under `permissions: id-token: write`. Only the official npm HTTPS registry is supported by automatic acquisition. The Action maps the token to a transient environment source for the npm provider and never persists it. Plain CLI invocation still requires an externally supplied ID token. A later reconciliation needs a fresh token.

## Publication and recovery

| Condition | Result |
| --- | --- |
| Registry/package/version already has matching SHA-512 integrity and SHA-1 shasum | `PUBLISHED`; no upload |
| Same coordinate, different content | `REJECTED` |
| HTTP 200/201 with affirmative publish acknowledgement | `PUBLISHED` |
| Lost or malformed acknowledgement, timeout, or unconfirmed conflict | Reconcile the original archive; never retry the upload on uncertainty |
| Version absent on reconciliation | `WAITING_EXTERNAL` |

Direct publication and OIDC exchange are exercised against controlled HTTP services. Live registry acceptance must be verified for your trusted publisher configuration. Stage-only publishing, provenance attestations, and changing dist-tags on existing versions are unsupported. Preserve the original tarball and use fresh credentials for recovery.
