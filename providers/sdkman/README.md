# SDKMAN provider

Publishes SDK candidate/version metadata through the **SDKMAN! State API** for new vendor accounts. It does not upload archives, announce releases, or change default tags.

## Current State API (recommended)

The State API uses vendor email and password to request a short-lived JWT with `POST /login`, followed by a bearer-authenticated `POST /versions`. SDKMAN currently documents a **204 No Content** response on successful publication.

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

The `mode` field defaults to `state` if no legacy consumer-key credentials are present. The State API endpoint defaults to `https://state.sdkman.io`. Custom `stateApi` URLs are supported for private instances/testing. Use `--credential sdkman-vendor-email=SDKMAN_VENDOR_EMAIL --credential sdkman-vendor-password=SDKMAN_VENDOR_PASSWORD`, with the vendor email and password set in those environment variables. The two planned credential references are resolved at execution time; **no email, password, or JWT is stored in a plan, journal, or evidence**. The token is scoped to one invocation, not persisted or cached across runs.

`artifact` identifies a local archive. Host those same bytes at `url` before executing. State API requests include `candidate`, `version`, `platform`, `url`, and `sha256sum`; optional checksums are `MD5` and `SHA-512` (`md5sum`/`sha512sum`). Optional `stateDistribution` maps to the API's `distribution` field, using its canonical uppercase enum. For non-Java candidates use `platform: "UNIVERSAL"` and omit `stateDistribution`. Platform names are normalized to the current State API scheme, e.g. `LINUX_X64` and `MAC_X64` (legacy `LINUX_64` and `MAC_OSX` inputs are accepted and normalized). The State API does **not** support direct publishing to the `java` candidate.

Before publishing, Distroplane looks up the candidate/version/platform. An already matching release is accepted without sending a second publish request; a conflicting URL, checksum, or distribution is rejected. This protects against accidental overwrites by the State API's create-or-update endpoint, though it **cannot eliminate races with concurrent publishers**. Use exclusive release coordination in CI.

A successful `204` is reported as `PUBLISHED` with `vendor-api-accepted` evidence (API acceptance, **not** independently observed public state). Failed or lost responses after dispatch are classified conservatively; ambiguous publication outcomes require reconciliation rather than a blind repeat. Reconciliation uses the public version lookup and never publishes. If the version is not yet visible, it stays `WAITING_EXTERNAL`; if visible, the URL, platform, distribution and exposed SHA-256 are checked. Missing checksum produces explicitly limited `state-url-only` evidence.

## Existing legacy vendor API accounts

For accounts that still use `Consumer-Key` / `Consumer-Token` and `POST /release`, the legacy integration is preserved as an **explicit compatibility mode**:

```json
{
  "artifact": "archive",
  "candidate": "acme",
  "version": "1.2.3",
  "url": "https://example.com/releases/acme-1.2.3.zip",
  "authentication": {
    "mode": "legacy",
    "consumerKeyCredential": "sdkman-key",
    "consumerTokenCredential": "sdkman-token"
  }
}
```

Previously saved configurations without a `mode` field that contain the two consumer credential references still select the legacy path. Existing saved legacy plans remain executable. The legacy API default remains `https://vendors.sdkman.io` and can be set via `api`. Supply the two consumer secrets using `--credential REF=ENV` at execution.

SDKMAN describes the legacy API as superseded and advises onboarding/migrating to the State API. The legacy path is retained only to avoid disrupting any still-provisioned accounts; SDKMAN does not promise continued acceptance of the older credentials.

For both modes HTTPS is required unless `allowInsecureHTTP` is enabled for local testing. `stateApi` controls public reconciliation; `api` affects only legacy publishing. State checks do not prove immediate global client availability. Keep the artifact and immutable plan available throughout the release.

Official references: [Vendor API Integration Guide](https://github.com/sdkman/sdkman-state/wiki/Vendor-API-Integration-Guide), [State API Reference](https://github.com/sdkman/sdkman-state/wiki/API-Reference), [Migration Guide](https://github.com/sdkman/sdkman-state/wiki/Migrating-from-the-old-Vendor-API).
