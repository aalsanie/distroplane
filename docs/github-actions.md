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
    outputs:
      plan: ${{ steps.publish.outputs.plan-id }}
      run: ${{ steps.publish.outputs.run-id }}
      pending: ${{ steps.publish.outputs.pending }}
      recovery: ${{ steps.publish.outputs.recovery-artifact-id }}
    steps:
      - uses: actions/checkout@v7
      - uses: aalsanie/distroplane@main
        id: publish
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          version: ${{ vars.DISTROPLANE_VERSION }}
          command: publish
          approve: 'true'
          credential-mappings: npm-publish=NPM_TOKEN
          upload-recovery: 'true'
          recovery-retention-days: '30'
  resume:
    needs: publish
    if: needs.publish.outputs.pending == 'true'
    runs-on: ubuntu-latest
    steps:
      - uses: aalsanie/distroplane@main
        env:
          NPM_TOKEN: ${{ secrets.NPM_TOKEN }}
        with:
          version: ${{ vars.DISTROPLANE_VERSION }}
          command: resume
          restore-recovery: 'true'
          recovery-artifact-id: ${{ needs.publish.outputs.recovery }}
          recovery-plan-id: ${{ needs.publish.outputs.plan }}
          run-id: ${{ needs.publish.outputs.run }}
          reconcile-only: 'true'
          credential-mappings: npm-publish=NPM_TOKEN
```

Build your artifact **before** this step. In production, pin `uses:` to a reviewed commit SHA; the current Action interface is not included in the older release tags. Set the repository variable `DISTROPLANE_VERSION` to an actually published compatible CLI release supporting `publish`, `runs` and `resume`; do not use an older CLI that lacks these commands. `approve: 'true'` authorizes real publication.

## Pending releases

The example performs read-only reconciliation in a separate job with fresh credentials. It selects the exact recovery artifact, plan and run from the trusted publication job in the same workflow run. The original runner's OS, architecture and absolute `GITHUB_WORKSPACE` must match. If credentials were missing before dispatch, omit `reconcile-only` only after restoring the approved credential source.

The bundle contains the original configuration, catalog records and referenced plans/journals, selected run artifacts and retained provider executables/receipts, plus an explicitly selected local CLI binary if used. It is not a full workspace backup. npm recovery includes the exact original tarball even when its path differs from the release artifact. Custom providers needing other files must retain those separately.

Recovery bundles use schema 2. An existing run requires `recovery-plan-id`, `run-id` and `command: resume`. An unclaimed reviewed plan may instead be restored with `command: publish` and matching `confirm-plan` and `recovery-plan-id`; a claimed run cannot be used for that approval. For older bundles, preserve the original state and recover with compatible trusted tools; never create a replacement run. Checksums establish consistency, not origin trust: never restore or execute an archive from an untrusted job or artifact source. The requested retention is 30 days in this example, subject to repository policy; after expiry recover from a trusted backup. Never republish merely because the bundle has expired.

## npm trusted publishing

Configure the npm package's trusted publisher for this GitHub workflow, grant the job `id-token: write`, then use `oidc: npm` and `npm-oidc-ref: npm-publish` instead of an npm token mapping. The destination must use `authentication.mode: trusted-publishing`. `oidc: required` only checks availability; it does not acquire a token.

All other inputs and outputs are defined in [action.yml](../action.yml). [CLI](cli.md) · [Configuration](configuration.md)
