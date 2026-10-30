# GitHub Action

Publish a prebuilt artifact from a GitHub Actions workflow using the same `distroplane.json` as the CLI.

```yaml
name: Publish
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: aalsanie/distroplane@main
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          version: 0.9.2
          command: publish
          approve: 'true'
          credential-mappings: npm-publish=NPM_TOKEN
          upload-recovery: 'true'
```

Build your artifact **before** this step. In production, pin `uses:` to a reviewed commit SHA; the current Action interface is not included in the older release tags. Set `version` to an actually published CLI release. `approve: 'true'` authorizes real publication.

## Pending releases

`upload-recovery: 'true'` saves the verified plan, journal, artifacts and retained providers as a protected workflow artifact. A subsequent job can continue without creating a new release:

```yaml
- uses: aalsanie/distroplane@main
  with:
    version: 0.9.2
    command: resume
    restore-recovery: 'true'
```

The restored workspace must have the **same absolute path, OS and CPU architecture**. Never use a missing or corrupted recovery artifact as permission to republish. A pending result is not a completed release.

## npm trusted publishing

Configure the npm package's trusted publisher for this GitHub workflow, grant the job `id-token: write`, then use `oidc: npm` and `npm-oidc-ref: npm-publish` instead of an npm token mapping. The destination must use `authentication.mode: trusted-publishing`. `oidc: required` only checks availability; it does not acquire a token.

All other inputs and outputs are defined in [action.yml](../action.yml). [CLI](cli.md) · [Configuration](configuration.md)
