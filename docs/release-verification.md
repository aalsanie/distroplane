# Release verification

Distroplane release artifacts are built from a tagged source revision with `CGO_ENABLED=0`, `-trimpath`, explicit version/commit metadata, and a fixed build timestamp derived by the release workflow.

Each release contains:

- the Distroplane CLI for every supported OS/architecture;
- all official provider executables for the same targets;
- `SHA256SUMS` covering every executable;
- `RELEASE-METADATA.json` identifying version, commit, build timestamp, Go version, and CGO state.

## Verify an executable

On systems with `sha256sum`:

```sh
sha256sum -c SHA256SUMS
```

On macOS:

```sh
shasum -a 256 -c SHA256SUMS
```

On Windows PowerShell, compare `Get-FileHash -Algorithm SHA256` with the corresponding line in `SHA256SUMS`.

The GitHub Action performs this verification automatically before installing the CLI or provider binaries.

## Provider verification

Provider binaries are separate release assets and can be verified independently. Their filenames include provider name, release version, operating system, and architecture. The checksum manifest is authoritative for the exact bytes released.

New immutable plans also record the SHA-256 digest of the provider executable observed while planning. Apply/reconcile reject a changed binary before using it.

## Dependency and SBOM strategy

The Go module is intentionally standard-library-only. CI fails if an external Go module or package enters the dependency graph.

For the release-candidate line, the repository does not add an SBOM generator dependency solely to produce a document that would list no third-party Go modules. The machine-readable release metadata, source tag, `go.mod`, dependency-budget CI output, and Go build information embedded in binaries provide the dependency record. If the hosting platform can generate an SBOM without introducing a runtime/build dependency, that artifact may be attached in addition to these controls.

## Provenance strategy

The source tag, immutable Git commit, release workflow run, checksums, and machine-readable build metadata form the minimum release provenance chain. The workflow uses repository-scoped GitHub credentials only for creating the tag/release and does not give release binaries provider credentials.

Platform-native attestations may be added when available without weakening the zero-runtime-dependency or provider-isolation constraints. Consumers should verify attestations against the repository identity in addition to checking SHA-256 checksums; attestations do not replace checksum verification.

## Rebuilding

To rebuild the release layout from the tagged source:

```sh
VERSION=<version> COMMIT=<commit> BUILD_DATE=<timestamp> ./scripts/build.sh
```

Use the values in `RELEASE-METADATA.json`. Different Go toolchain patch versions are not claimed to produce byte-identical binaries; the metadata records the toolchain used by the release workflow.
