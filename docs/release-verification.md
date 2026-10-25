# Installation and release verification

This document describes the **v0.9.0 release procedure and expected artifacts**. Until the stable tag is published, install a version from [GitHub releases](https://github.com/aalsanie/distroplane/releases); documentation on `main` does not imply `v0.9.0` binaries are already available.

## Release assets

For each release version, the build produces the CLI and npm, SDKMAN, Homebrew, and WinGet provider executables:

| OS | Architectures | Asset suffix |
| --- | --- | --- |
| Linux | amd64, arm64 | `_linux_<arch>` |
| macOS | amd64, arm64 | `_darwin_<arch>` |
| Windows | amd64, arm64 | `_windows_<arch>.exe` |

Asset names are `distroplane_<version>_<os>_<arch>` and `distroplane-provider-<name>_<version>_<os>_<arch>` (plus `.exe` on Windows). Each release also includes `SHA256SUMS` for all executables and `RELEASE-METADATA.json` with the version, source commit, build date, Go version, and CGO setting. The checksum manifest and metadata are **unsigned**.

## Manual installation

Download the executable(s) and `SHA256SUMS` from the **same** release, verify each exact filename, then add the CLI and installed providers to `PATH` (or configure explicit provider paths). For example, after `v0.9.0` is published:

```sh
asset=distroplane_0.9.0_linux_amd64
awk -v file="$asset" '$2 == file {print}' SHA256SUMS > selected.SHA256SUMS
test "$(wc -l < selected.SHA256SUMS)" -eq 1
sha256sum -c selected.SHA256SUMS  # on macOS: shasum -a 256 -c
chmod +x "$asset"
```

For the Windows amd64 CLI:

```powershell
$asset = 'distroplane_0.9.0_windows_amd64.exe'
$entry = @(Get-Content SHA256SUMS | Where-Object { $_ -match '^[0-9a-fA-F]{64}  distroplane_0\.9\.0_windows_amd64\.exe$' })
if ($entry.Count -ne 1) { throw 'missing or duplicate checksum entry' }
$expected = $entry[0].Substring(0, 64).ToLowerInvariant()
$actual = (Get-FileHash -Algorithm SHA256 $asset).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'checksum mismatch' }
```

Verify provider executables as well. A manifest fetched from the same release authenticates downloads against accidental corruption, **not** against a compromised release publisher.

## Building from source

Use Go 1.27.1. Homebrew and WinGet need Git at runtime.

```sh
go build -trimpath -o bin/ ./cmd/...
```

This builds the development fake provider too. An ordinary source build reports `0.0.0-dev` unless version information is injected through linker flags; it does not infer the version from `VERSION`. Production release layouts are built by `scripts/build.sh` or `scripts/build.ps1` with `VERSION`, `COMMIT`, and `BUILD_DATE`.

## Publishing v0.9.0

Confirm `VERSION` is `0.9.0` and the checked-in [v0.9.0 release notes](releases/v0.9.0.md) have passed `scripts/check-release-notes.sh`. Merge the release-preparation changes into `main`, then verify the **exact intended commit** has successful main-branch CI and GitHub Action smoke runs; review the provider integration results. Tag **that same commit** as `v0.9.0`. The release workflow rejects a tag that differs from `VERSION`, lacks valid versioned release notes, lacks the required successful workflow runs for its commit, or already has a published release. It rebuilds assets, checks checksums and metadata, runs a release-artifact smoke test, and publishes the GitHub release with the checked-in notes. Merging preparation changes does not tag or publish a release.

## Compatibility with release candidates

Journal schema v2 binds the initial `RUN_STARTED` event to the immutable plan ID. Journals from earlier `v0.9.0` candidates using schema v1 are rejected by `apply`, `reconcile`, `status`, and `evidence`; there is **no migration command**. Before initiating a replacement run, independently inspect the destination for prior publication and address any uncertain side effects. Never attach an old journal to a new plan.

Protocol, configuration, plan, journal, and evidence formats have independent versions. Unsupported versions are rejected; do not assume cross-version persistence compatibility.
