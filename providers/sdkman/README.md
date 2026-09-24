# SDKMAN provider

Registers a candidate/version and download URL through the SDKMAN vendor API. It does not upload archives, announce a release, or change the default version.

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

`artifact` names the local archive. Host the same bytes at `url` before execution; the provider includes the local SHA-256 in the vendor request.

Map the two credential references at apply/reconcile. `platform` defaults to `UNIVERSAL`; supported platform values are validated by the provider. Optional fields include `vendor`, `stateDistribution`, additional `checksums`, `api`, and `stateApi`. HTTPS is required unless `allowInsecureHTTP` is enabled for a local test service.

## Results

A successful vendor response is recorded as `PUBLISHED` with verification `vendor-api-accepted`. This proves vendor API acceptance, not public SDKMAN availability; already-published targets are not later re-observed automatically.

A lost submission response or unresolved duplicate requires reconciliation. Public state is compared using candidate/version/platform, download URL, and SHA-256 when available. A missing release remains `WAITING_EXTERNAL`; conflicting identity is `REJECTED`.

Evidence records when SDKMAN omits the checksum or when a vendor distribution cannot be distinguished. Inspect those fields when deciding what the result proves.
