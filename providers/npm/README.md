# npm provider

Publishes an existing gzip tarball directly to an npm-compatible registry. It does not run `npm publish`, build a package, or read `.npmrc`. Create the archive with your build tooling, for example `npm pack`.

Use `"provider": {"name": "npm"}` in the [shared configuration](../../docs/configuration.md), with this `configuration` object:

```json
{
  "artifact": "package",
  "packagePath": "/absolute/path/to/package.tgz",
  "registry": "https://registry.npmjs.org/",
  "authentication": {"mode": "token", "credential": "npm-publish"}
}
```

`artifact` names a release artifact. `packagePath` must be an absolute path to the same bytes; it is required during planning, apply, and reconciliation. The archive must contain `package/package.json` with a valid package name and semantic version.

Optional fields are `tag` (default `latest`) and `access` (`public` or `restricted`). Set `tag` explicitly for prereleases: Distroplane does not infer it from the version. HTTPS is required unless `allowInsecureHTTP: true` is deliberately set for a local test registry. Registry URLs cannot contain credentials, query parameters, or fragments.

## Token authentication

Map the reference to an environment variable supplied by your secret store:

```sh
distroplane apply --plan PLAN.json --journal run.journal --credential npm-publish=NPM_TOKEN
```

The host delivers that value to this provider as `DISTROPLANE_NPM_TOKEN`. The token needs publication access to the configured package and registry. There is no interactive login or OTP prompt.

## Trusted publishing

Set `authentication.mode` to `trusted-publishing` and choose a reference such as `npm-identity`. Configure a trusted publisher for the package in npm. The provider exchanges a supplied OIDC ID token for an npm token through the [npm registry API](https://api-docs.npmjs.com/); it does not obtain the ID token itself.

For GitHub Actions, grant the job `permissions: id-token: write` and request a token immediately before apply. These steps assume a preceding plan step named `plan` and a configuration using the `npm-identity` reference:

```yaml
- name: Request npm identity
  shell: pwsh
  run: |
    if (-not $env:ACTIONS_ID_TOKEN_REQUEST_URL -or -not $env:ACTIONS_ID_TOKEN_REQUEST_TOKEN) {
      throw 'Grant this job permissions.id-token: write'
    }
    $uri = $env:ACTIONS_ID_TOKEN_REQUEST_URL + '&audience=npm%3Aregistry.npmjs.org'
    $headers = @{ Authorization = "Bearer $env:ACTIONS_ID_TOKEN_REQUEST_TOKEN" }
    $token = (Invoke-RestMethod -Uri $uri -Headers $headers).value
    if (-not $token) { throw 'GitHub returned no ID token' }
    Write-Host "::add-mask::$token"
    "NPM_ID_TOKEN=$token" >> $env:GITHUB_ENV
- uses: aalsanie/distroplane@v0.9.0-rc.1
  with:
    command: apply
    plan: ${{ steps.plan.outputs.plan-path }}
    credential-mappings: npm-identity=NPM_ID_TOKEN
    oidc: required
```

The token audience is `npm:registry.npmjs.org`. See [npm's trusted publisher setup](https://docs.npmjs.com/trusted-publishers/) for account and workflow requirements. Obtain a fresh ID token for later reconciliation. The provider receives it as `DISTROPLANE_NPM_ID_TOKEN`. A current [environment-isolation limitation](../../docs/security-model.md#provider-execution-and-credentials) can also expose GitHub's token-request credentials during provider discovery on Linux/macOS; use a dedicated npm publication job with only the required secrets and permissions.

## Results and limits

Apply looks up the package/version before uploading. An existing version is accepted only when registry integrity and shasum match the planned tarball; different content is `REJECTED`. A successful upload response produces `PUBLISHED`. A lost response becomes ambiguous and triggers observation rather than a blind upload retry.

Reconcile compares registry metadata with the local tarball. A missing version stays `WAITING_EXTERNAL`; reconciliation never uploads it. Evidence includes package/version, registry URL, SHA-256, integrity, and shasum.

This provider does not generate npm provenance attestations or update dist-tags for an already matching published version. Keep the tarball available while the run is pending.
