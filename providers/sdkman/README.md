# SDKMAN provider

The SDKMAN provider publishes a Distroplane release artifact through the secured SDKMAN vendor API and reconciles ambiguous outcomes against SDKMAN's public state API.

Example target configuration:

```json
{
  "artifact": "archive",
  "candidate": "groovy",
  "version": "4.0.28",
  "url": "https://downloads.example/groovy-4.0.28.zip",
  "platform": "UNIVERSAL",
  "authentication": {
    "consumerKeyCredential": "sdkman-consumer-key",
    "consumerTokenCredential": "sdkman-consumer-token"
  }
}
```

`vendor`, `platform`, `checksums`, and `stateDistribution` are provider-specific optional fields. `api` and `stateApi` default to the production SDKMAN endpoints. `allowInsecureHTTP` exists only for local deterministic test servers and should not be enabled for real publication.

The provider always binds the selected Distroplane artifact digest into the SDKMAN `SHA-256` checksum. A configured `SHA-256` must match that artifact digest. Additional SDKMAN-supported checksums may be supplied.

Two generic credential requirements are emitted during planning. At execution time the provider receives them only as `DISTROPLANE_SDKMAN_CONSUMER_KEY` and `DISTROPLANE_SDKMAN_CONSUMER_TOKEN` in the isolated provider process.

A successful vendor API response is evidence that SDKMAN accepted the release. Reconciliation queries public SDKMAN state and compares candidate, version, download URL, and SHA-256 when the state API exposes it. Evidence explicitly records weaker verification when the state API omits a checksum or when a vendor-specific distribution cannot be uniquely identified.
