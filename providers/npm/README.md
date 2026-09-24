# npm provider

Publishes an existing gzip tarball directly to an npm-compatible registry. It does not run `npm publish`, build a package, or read `.npmrc`.

```json
{
  "artifact": "package",
  "packagePath": "/absolute/path/to/package.tgz",
  "registry": "https://registry.npmjs.org/",
  "authentication": {"mode": "token", "credential": "npm-publish"}
}
```

`artifact` names a release artifact. `packagePath` must be an absolute path to the same bytes and the archive must contain `package/package.json` with a valid package name and semantic version.

Optional fields are `tag` (default `latest`) and `access` (`public` or `restricted`). Set `tag` explicitly for prereleases. HTTPS is required unless `allowInsecureHTTP: true` is set for a local test registry.

## Authentication

For token authentication, map the configured credential reference at execution:

```sh
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

For trusted publishing, set `authentication.mode` to `trusted-publishing`. The provider exchanges a supplied OIDC ID token for an npm token; it does not obtain the ID token itself. In GitHub Actions, grant `id-token: write`, request an ID token with audience `npm:registry.npmjs.org`, and map that token's environment variable to the configured credential reference.

See [npm trusted publishers](https://docs.npmjs.com/trusted-publishers/) for account/workflow setup. Obtain a fresh ID token for later reconciliation.

## Results

Before upload, apply checks whether the package/version already exists. Matching registry integrity and shasum are accepted as `PUBLISHED`; different content is `REJECTED`. A successful upload response is `PUBLISHED`. A lost response becomes ambiguous instead of triggering a blind upload retry.

Reconcile compares registry metadata with the local tarball. A missing version remains `WAITING_EXTERNAL`; reconciliation never uploads.

The provider does not generate npm provenance attestations or update dist-tags for an already matching version. Keep the tarball available while the run is pending.
