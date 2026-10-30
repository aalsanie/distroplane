# Installation and release verification

This document describes release integrity checks and installation paths for releases built from the current source. Check [GitHub releases](https://github.com/aalsanie/distroplane/releases) for the version and actual downloadable assets. An unreleased source revision does not imply that its binaries or Homebrew formula are available.

## Release assets

For each release version, the build produces the CLI and npm, SDKMAN, Homebrew, and WinGet provider executables:

| OS | Architectures | Asset suffix |
| --- | --- | --- |
| Linux | amd64, arm64 | `_linux_<arch>` |
| macOS | amd64, arm64 | `_darwin_<arch>` |
| Windows | amd64, arm64 | `_windows_<arch>.exe` |

Asset names are `distroplane_<version>_<os>_<arch>` and `distroplane-provider-<name>_<version>_<os>_<arch>` (plus `.exe` on Windows). Each release also includes six unified archives, named `distroplane_<version>_<os>_<arch>.tar.gz` (Linux/macOS) and `distroplane_<version>_windows_<arch>.zip`. The archive layout is `bin/distroplane` (or `.exe`), `libexec/distroplane-provider-<name>` (four executables), and `PACKAGE-METADATA.json`, which records the exact embedded byte digests and executable modes.

`SHA256SUMS` covers all 36 assets. `RELEASE-METADATA.json` (schema 2) records the version, source commit, Go toolchain, and per-asset digests. CI checks the embedded files and exact inventory before a release. Release workflow artifacts receive GitHub OIDC/Sigstore build provenance attestations; consumers can verify a downloaded asset with `gh attestation verify ASSET --repo aalsanie/distroplane --source-ref refs/tags/v<VERSION>`. Release-local checksums alone are not an independent trust anchor; publishing access and the GitHub Actions signing workflow remain trust boundaries.

For releases produced by the staged publication workflow, `RELEASE-STAGING.json` is an additional release asset. It binds the tagged source commit and the explicit MSI signing policy to an exact, sorted inventory of the 36 executables/archives, two Windows MSIs (stable releases only), checksum files, and release metadata. The staging manifest itself is verified before public publication; it is not a replacement for GitHub provenance or independent publisher signatures. The release stays a GitHub **draft** until all staged assets have been uploaded and independently compared with the staging inventory. Native installation jobs must execute the packaged CLI and all four providers on Linux, macOS, and Windows for each available amd64/arm64 runner.

## Homebrew tap publication

A stable tagged release proposes a change to `aalsanie/homebrew-tap` **after** the release is published. This requires the repository secret `HOMEBREW_TAP_TOKEN` with permission to create branches and pull requests in the tap. The update uses published `RELEASE-METADATA.json`, `SHA256SUMS`, and the four macOS/Linux unified archives; it verifies their bytes, embedded CLI/provider digests, and tagged source commit. An absent archive or incomplete release blocks the update. Existing formula edits, closed review PRs, and downgrades are never overwritten. A successful proposal is **not** an installed or approved release: the tap formula must pass macOS Intel/ARM and Linuxbrew audit/install/test/uninstall checks before a maintainer merges its PR.

The `v0.9.0` release predates unified archives. The `aalsanie/homebrew-tap` formula for `v0.9.2` has been merged and is available through `brew install aalsanie/tap/distroplane` on supported hosts. The maintained formula still requires clean-host verification when preparing a new release; a formula PR alone does not publish an update.

## Direct Linux installation

For a released version with unified archives, download the versioned installer script from the matching source tag and run it without Go or Homebrew. For the published `v0.9.2` release:

```sh
curl --fail --location --show-error https://raw.githubusercontent.com/aalsanie/distroplane/v0.9.2/scripts/install-linux.py -o install-linux.py
python3 install-linux.py --version 0.9.2
$HOME/.local/bin/distroplane version
```

The installer requires Python 3.8+ and verifies release metadata, checksums, package file digests, and native provider startup before atomically switching the managed CLI link. An ordinary installation uses `$HOME/.local` and does not require root. Ensure `$HOME/.local/bin` is on `PATH`. For an authorized system-wide installation, run the installer with `--prefix /usr/local` using appropriate privileges; never run a downloaded script without inspecting and verifying its source.

For a proxy-assisted download, configure the standard HTTPS proxy environment variables before invoking Python. To install fully offline, first transfer `RELEASE-METADATA.json`, `SHA256SUMS`, and the selected `distroplane_<VERSION>_linux_<arch>.tar.gz` from the **same immutable release**; run `python3 install-linux.py --version <VERSION> --offline-dir DIR`. The installer rejects redirections to unapproved hosts, incorrect architectures, unsafe archives, and tampered release inventories. To uninstall the managed command, run `python3 install-linux.py --prefix <PREFIX> --uninstall`; the versioned executables and project `.distroplane` state remain intact for unfinished-run recovery.

Repeated installation of the same version compares its installed bytes with the verified archive. Upgrades and explicit downgrades install side-by-side in `lib/distroplane/versions`; a failed install cannot replace the current CLI link. Release-local checksums are not an independent trust anchor.

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

## Publishing a stable release

Confirm `VERSION` matches the intended stable release and its checked-in versioned release notes pass `scripts/check-release-notes.sh`. Merge release-preparation changes into `main`, then verify that **the exact intended commit** has successful main-branch CI and GitHub Action smoke runs; review the provider integration results. Tag **that same commit** with the matching `v`-prefixed version. The release workflow rejects a tag that differs from `VERSION`, lacks valid versioned release notes, lacks the required successful workflow runs for its commit, or already has a published release. It rebuilds assets, checks checksums and metadata, runs a release-artifact smoke test, and publishes the GitHub release with the checked-in notes. Merging preparation changes does not tag or publish a release.

## Compatibility with release candidates

Journal schema v2 binds the initial `RUN_STARTED` event to the immutable plan ID. Journals from earlier `v0.9.0` candidates using schema v1 are rejected by `apply`, `reconcile`, `status`, and `evidence`; there is **no migration command**. Before initiating a replacement run, independently inspect the destination for prior publication and address any uncertain side effects. Never attach an old journal to a new plan.

Protocol, configuration, plan, journal, and evidence formats have independent versions. Unsupported versions are rejected; do not assume cross-version persistence compatibility.
