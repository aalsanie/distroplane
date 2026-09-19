# SDKMAN provider

Registers a candidate/version and download URL through the SDKMAN vendor API. It does not upload archives, announce a release, or change the default version. You need vendor access for the candidate.

Use `"provider": {"name": "sdkman"}` in the [shared configuration](../../docs/configuration.md), with this `configuration` object:

```json
{
  "artifact": "archive",
  "candidate": "acme",
  "version": "1.2.3",
  "url": "https://example.com/releases/acme-1.2.3.zip",
  "platform": "UNIVERSAL",
  "authentication": {
    "consumerKeyCredential": "sdkman-key",
    "consumerTokenCredential": "sdkman-token"
  }
}
```

Replace `candidate` with your registered candidate. `artifact` names the local archive; host the same bytes at `url` before execution. The provider includes its SHA-256 in the vendor request.

## Credentials and options

Map both distinct references to environment variables from your secret store:

```sh
distroplane apply --plan PLAN.json --journal run.journal \
  --credential sdkman-key=SDKMAN_CONSUMER_KEY \
  --credential sdkman-token=SDKMAN_CONSUMER_TOKEN
```

The provider receives `DISTROPLANE_SDKMAN_CONSUMER_KEY` and `DISTROPLANE_SDKMAN_CONSUMER_TOKEN`. The CLI requires these planned credential mappings for reconciliation too, although the provider's public state query is unauthenticated.

`platform` defaults to `UNIVERSAL`. Other accepted values are `LINUX_64`, `LINUX_ARM64`, `LINUX_32`, `LINUX_ARM32SF`, `LINUX_ARM32HF`, `MAC_OSX`, `MAC_ARM64`, and `WINDOWS_64`.

Optional `vendor` selects a vendor-specific release; `stateDistribution` identifies that distribution during public state lookup. `checksums` accepts additional checksum entries; a supplied `SHA-256` must match the selected artifact. Supported keys are `MD5`, `SHA-1`, `SHA-224`, `SHA-256`, `SHA-384`, and `SHA-512`.

`api` defaults to `https://vendors.sdkman.io`, and `stateApi` to `https://state.sdkman.io`. HTTPS is required unless `allowInsecureHTTP` is enabled for a local test service.

## Results and verification limits

A successful vendor response currently returns `PUBLISHED` with provider state `accepted` and evidence verification `vendor-api-accepted`. This confirms API acceptance; apply does not observe public availability. The executor does not later reconcile an already published target, so it does not turn that acceptance into a public availability check automatically.

A lost submission response or unresolved duplicate triggers reconciliation. Public state is compared using candidate/version/platform, download URL, and SHA-256 when available. A missing release stays `WAITING_EXTERNAL`; conflicting identity is `REJECTED`.

Evidence records `state-url-only` if SDKMAN omits the checksum, and a limitation if a vendor distribution cannot be distinguished. Inspect these fields when deciding what the result proves.
