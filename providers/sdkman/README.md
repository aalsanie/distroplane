# SDKMAN provider

Registers a previously hosted release archive with SDKMAN. The provider does not upload the archive, announce versions, or change default tags. The **State API** is the default for new vendor accounts; explicit legacy mode supports existing Consumer-Key/Consumer-Token integrations.

## State API

```json
{
  "artifact": "archive",
  "candidate": "acme",
  "version": "1.2.3",
  "url": "https://example.com/releases/acme-1.2.3.zip",
  "platform": "UNIVERSAL",
  "authentication": {
    "mode": "state",
    "emailCredential": "sdkman-vendor-email",
    "passwordCredential": "sdkman-vendor-password"
  }
}
```

Host the **same artifact bytes** at `url` before applying. The provider includes the artifact's SHA-256 in `POST /versions`, along with candidate, version, platform, URL, and optional `stateDistribution` (`distribution`). Optional supported checksums are MD5 and SHA-512. Use `UNIVERSAL` for non-Java candidates; SDKMAN does not support publishing directly to the `java` candidate through this API. Platform input names are normalized (including legacy `LINUX_64` and `MAC_OSX` spellings).

The provider authenticates through `POST /login` with vendor email/password and uses the returned short-lived bearer token for one invocation. Supply `--credential sdkman-vendor-email=SDKMAN_VENDOR_EMAIL --credential sdkman-vendor-password=SDKMAN_VENDOR_PASSWORD`. Credential values and the JWT are not written to plans or journals. The default State API base URL is `https://state.sdkman.io` (`stateApi` overrides it).

Before submission, `apply` looks up the candidate/version/platform: matching remote metadata is accepted; conflicting URL, checksum, or distribution is rejected. The remote API can update an existing version, so the preflight check cannot prevent races with another publisher. Coordinate SDKMAN release ownership externally.

HTTP `204` on `POST /versions` means **vendor API acceptance**, reported as `PUBLISHED` with `vendor-api-accepted` evidence; it is not proof of public client availability. Lost responses remain ambiguous. Read-only reconciliation compares observed URL/platform/distribution and exposed SHA-256. A version not yet visible remains `WAITING_EXTERNAL`; if no checksum is returned, evidence explicitly records weaker `state-url-only` verification.

## Legacy vendor API

Existing accounts can set `authentication.mode: "legacy"` with `consumerKeyCredential` and `consumerTokenCredential` (mapped through `--credential REF=ENV`). This uses the legacy `/release` endpoint at `https://vendors.sdkman.io` by default (`api` overrides it). Existing configurations containing these references without `mode` also select legacy behavior. New integrations should use the State API; SDKMAN has superseded the legacy interface.

HTTPS is required unless `allowInsecureHTTP` is enabled for private/local testing. See SDKMAN's [Vendor API Integration Guide](https://github.com/sdkman/sdkman-state/wiki/Vendor-API-Integration-Guide), [API Reference](https://github.com/sdkman/sdkman-state/wiki/API-Reference), and [migration notes](https://github.com/sdkman/sdkman-state/wiki/Migrating-from-the-old-Vendor-API).
