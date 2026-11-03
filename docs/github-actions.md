# GitHub Action

Run an explicit publication workflow after building the release artifact:

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
          version: 0.9.3
          command: publish
          approve: 'true'
          credential-mappings: npm-publish=NPM_TOKEN
          upload-recovery: 'true'
```

Pin the Action to a reviewed commit SHA for production. `approve: 'true'` authorizes publication. The input `version` selects the published CLI.

## Resume a pending release

Use the verified recovery bundle in a later job with the same absolute workspace path, OS, and CPU architecture:

```yaml
- uses: aalsanie/distroplane@main
  with:
    version: 0.9.3
    command: resume
    restore-recovery: 'true'
```

A pending outcome needs reconciliation with the original state. Check the Action's `pending`, `completed`, and `exit-code` outputs.

For npm trusted publishing, configure the npm trusted publisher for the workflow, grant `id-token: write`, and set `oidc: npm` plus `npm-oidc-ref: npm-publish` with `authentication.mode: trusted-publishing` in the destination.

All inputs and outputs are defined in [action.yml](../action.yml).
