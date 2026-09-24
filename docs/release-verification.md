# Installation and release verification

The current release candidate is [v0.9.0-rc.2](https://github.com/aalsanie/distroplane/releases/tag/v0.9.0-rc.2). GitHub Actions users can use the [composite Action](github-actions.md), which installs matching CLI/provider binaries automatically.

## Release assets

v0.9.0-rc.2 provides the CLI and four official provider executables for:

| OS | Architectures | Suffix |
| --- | --- | --- |
| Linux | amd64, arm64 | `_linux_<arch>` |
| macOS | amd64, arm64 | `_darwin_<arch>` |
| Windows | amd64, arm64 | `_windows_<arch>.exe` |

The release also contains `SHA256SUMS` and `RELEASE-METADATA.json`. The checksum file covers every executable; metadata records version, source commit, build date, Go version, and CGO setting. These files are unsigned and are not cryptographic attestations.

## Manual installation

Download `SHA256SUMS` plus the CLI and required providers from the same release. Verify each downloaded executable against its exact entry in the manifest before use.

Linux/macOS example for the amd64 Linux CLI:

```sh
grep '  distroplane_0.9.0-rc.2_linux_amd64$' SHA256SUMS > selected.SHA256SUMS
sha256sum -c selected.SHA256SUMS   # or: shasum -a 256 -c selected.SHA256SUMS
chmod +x distroplane_0.9.0-rc.2_linux_amd64
```

Repeat the manifest-entry check for each provider binary you install.

Windows PowerShell example for the amd64 CLI:

```powershell
$entry = @(Get-Content SHA256SUMS | Where-Object { $_ -match '  distroplane_0\.9\.0-rc\.2_windows_amd64\.exe$' })
if ($entry.Count -ne 1 -or $entry[0] -notmatch '^([0-9a-f]{64})  ') { throw 'invalid checksum entry' }
$expected = $Matches[1]
$actual = (Get-FileHash -Algorithm SHA256 distroplane_0.9.0-rc.2_windows_amd64.exe).Hash.ToLowerInvariant()
if ($actual -ne $expected) { throw 'checksum mismatch' }
```

Put the CLI and provider executables on `PATH`, or configure explicit provider executable paths.

## Build from source

Use Go 1.27.1. Homebrew and WinGet also require Git at runtime.

```sh
go build -trimpath -o bin/ ./cmd/...
```

This also builds the development fake provider. An ordinary source build reports `0.0.0-dev`; it does not use `VERSION` automatically. Plan and execute with the same provider builds.

Maintainers can create the release layout with `scripts/build.sh` or `scripts/build.ps1` using explicit `VERSION`, `COMMIT`, and `BUILD_DATE`.

## Release process

The release workflow accepts an existing `v*` tag only when it matches `VERSION` and the tagged commit has successful main-branch CI and Action-smoke runs. It rebuilds and verifies release assets before creating the GitHub release.

This is a pre-1.0 interface. Protocol, configuration, plan, journal, and evidence versions are independent; unsupported versions are rejected. There is no automatic migration command.
