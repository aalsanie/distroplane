# Changelog

## [0.9.3]

- Build CLI and providers with Go 1.27.3; verify embedded compiler, module, target, CGO and trimpath before release packaging.

## [0.9.2] - 2026-10-29

- Add Windows x64 and ARM64 MSI installation, repair, upgrade and uninstall, with native lifecycle tests.
- Verify MSI hashes and build attestations. Installers remain unsigned.

## [0.9.1] - 2026-10-28

- Add guided configuration, publication, status, resume and diagnostics; retain digest-bound providers for recovery.
- Ship unified Linux, macOS and Windows archives with checksums, metadata and build attestations.
- Add an offline-capable verified Linux installer and reviewed Homebrew tap update process.

## [0.9.0] - 2026-10-25

- Bind journal schema v2 to immutable plan IDs and resume credential-blocked operations without duplicate dispatch.
- Require explicit operator approval for npm retries after ambiguous publication and reconciliation.
- Add SDKMAN State API JWT authentication, WinGet ZIP installer support and merged-manifest checks, and Homebrew JVM distribution support.
- Reject incompatible release-candidate journal schema v1; uncertain external outcomes must be resolved before restarting publication.

## [0.9.0-rc.2] - 2026-09-21

- Publish release-candidate native executables with SHA-256 checksums and build metadata.

## [0.9.0-rc.1] - 2026-09-19

- Publish initial release-candidate native executables with SHA-256 checksums and build metadata.
