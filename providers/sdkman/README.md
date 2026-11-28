# SDKMAN provider

Registers a previously hosted release archive with SDKMAN. The provider does not upload the archive, announce versions, or change default tags. The **State API** is the default for new vendor accounts; explicit legacy mode supports existing Consumer-Key/Consumer-Token integrations.

## State API

Run `distroplane init --destination sdkman`; enter your existing candidate, version and public download URL. The corresponding script options are `sdkman.candidate`, `sdkman.version` and `sdkman.url` via `--set`.

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

## Legacy vendor API

Existing accounts can set `authentication.mode: "legacy"` with `consumerKeyCredential` and `consumerTokenCredential` (mapped through `--credential REF=ENV`). This uses the legacy `/release` endpoint at `https://vendors.sdkman.io` by default (`api` overrides it). Existing configurations containing these references without `mode` also select legacy behavior. New integrations should use the State API; SDKMAN has superseded the legacy interface.

## Publication and recovery

| Condition | Result |
| --- | --- |
| State API: matching candidate/version/platform/distribution, URL and exposed checksum | `PUBLISHED`; no registration |
| State API: HTTP 204; legacy API: successful registration response | `PUBLISHED` with vendor-acceptance evidence, not public-index proof |
| Conflicting observed URL, checksum or distribution | `REJECTED` |
| Lost response or unconfirmed conflict | Reconcile; never repeat an uncertain registration |
| Version absent on reconciliation | `WAITING_EXTERNAL` |
| State metadata omits SHA-256 | URL-only evidence, explicitly marked weaker |

Both modes have controlled API tests. The State API can update an existing version; preflight observation cannot prevent concurrent external writers. Coordinate release ownership. Legacy vendor-specific observations require `stateDistribution` to distinguish distributions.

HTTPS is required unless `allowInsecureHTTP` is enabled for private/local testing. See SDKMAN's [Vendor API Integration Guide](https://github.com/sdkman/sdkman-state/wiki/Vendor-API-Integration-Guide), [API Reference](https://github.com/sdkman/sdkman-state/wiki/API-Reference), and [migration notes](https://github.com/sdkman/sdkman-state/wiki/Migrating-from-the-old-Vendor-API).
