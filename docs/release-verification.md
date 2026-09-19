# Installation and release verification

The [current release candidate](https://github.com/aalsanie/distroplane/releases/tag/v0.9.0-rc.1) is `0.9.0-rc.1`. GitHub Actions users can use [the composite Action](github-actions.md), which installs the matching CLI and official providers automatically.

## Release assets

Every release includes separate executables for the CLI and the `npm`, `sdkman`, `homebrew`, and `winget` providers on these targets:

| OS | Architectures | Filename suffix |
| --- | --- | --- |
| Linux | amd64, arm64 | `_linux_<arch>` |
| macOS | amd64, arm64 | `_darwin_<arch>` |
| Windows | amd64, arm64 | `_windows_<arch>.exe` |

For example: `distroplane_0.9.0-rc.1_linux_amd64` and `distroplane-provider-npm_0.9.0-rc.1_linux_amd64`. Release assets also include `SHA256SUMS` and `RELEASE-METADATA.json`. There are no installation archives or package-manager installers in this release.

`SHA256SUMS` covers every executable. `RELEASE-METADATA.json` records version, source commit, build date, Go version, and CGO setting. The checksum file and metadata are unsigned; the workflow does not currently publish cryptographic attestations. Checksums detect bytes that differ from the release manifest, and still require trust in the GitHub release source.

## Linux and macOS

This installs the CLI and npm provider into a local `bin` directory. Run it in a new download directory. Set `platform` to one of the table's OS/architecture pairs; Apple Silicon uses `darwin_arm64`.

```sh
set -eu
version=0.9.0-rc.1
platform=linux_amd64
base="https://github.com/aalsanie/distroplane/releases/download/v$version"
curl --fail --location --remote-name "$base/SHA256SUMS"
mkdir -p bin
for name in distroplane distroplane-provider-npm; do
  asset="${name}_${version}_${platform}"
  curl --fail --location --remote-name "$base/$asset"
  awk -v asset="$asset" '$2 == asset { print; count++ } END { if (count != 1) exit 1 }' \
    SHA256SUMS > "$asset.sha256"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c "$asset.sha256"
  else
    shasum -a 256 -c "$asset.sha256"
  fi
  install -m 755 "$asset" "bin/$name"
done
export PATH="$PWD/bin:$PATH"
distroplane version
```

Add other `distroplane-provider-<name>` entries to the loop as needed. Keep the selected install directory on `PATH` in subsequent shells, or configure explicit provider executable paths.

## Windows PowerShell

Run in a new download directory. Use `windows_arm64` for ARM64:

```powershell
$ErrorActionPreference = 'Stop'
$version = '0.9.0-rc.1'
$platform = 'windows_amd64'
$base = "https://github.com/aalsanie/distroplane/releases/download/v$version"
Invoke-WebRequest "$base/SHA256SUMS" -OutFile SHA256SUMS
New-Item -ItemType Directory -Force bin | Out-Null
foreach ($name in 'distroplane', 'distroplane-provider-npm') {
    $asset = "${name}_${version}_${platform}.exe"
    Invoke-WebRequest "$base/$asset" -OutFile $asset
    $entry = @(Get-Content SHA256SUMS | Where-Object { $_.EndsWith("  $asset") })
    if ($entry.Count -ne 1 -or $entry[0] -notmatch '^([0-9a-f]{64})  ') {
        throw "Missing or invalid checksum for $asset"
    }
    $expected = $Matches[1]
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $asset).Hash.ToLowerInvariant()
    if ($actual -ne $expected) { throw "Checksum mismatch: $asset" }
    Copy-Item -LiteralPath $asset -Destination "bin/$name.exe"
}
$env:PATH = (Resolve-Path bin).Path + [IO.Path]::PathSeparator + $env:PATH
distroplane version
```

## Build from source

Use Go 1.27.1, as declared in `go.mod`, and Git when using the Homebrew or WinGet providers:

```sh
go build -trimpath -o bin/ ./cmd/...
```

This builds the CLI, official providers, and development fake provider into `bin`. Add that directory to `PATH`. An ordinary build reports `0.0.0-dev`; it does not automatically use `VERSION` or match the released binary's digest. Plan and execute using the same provider builds.

`0.9.0-rc.1` has a known Git discovery issue affecting the Homebrew and WinGet providers. Use npm or SDKMAN with this release, or use a later release once the provider-host fix is available.

Maintainers can build the full release layout with `scripts/build.sh` or `scripts/build.ps1`. Set `VERSION`, `COMMIT`, and `BUILD_DATE` explicitly using the release metadata when rebuilding. These scripts replace their output directory; use a dedicated disposable directory. Their default version is `0.0.0-dev`, not the contents of `VERSION`.

## Release identity and compatibility

The release workflow checks out the pushed tag, requires it to match `v` plus the repository's `VERSION`, and requires successful CI and Action-smoke push runs on `main` for that commit. It builds with CGO disabled, trimmed source paths, injected version/commit metadata, and the source commit's timestamp. Candidate versions are marked as GitHub prereleases. The workflow consumes an existing tag; it does not create one.

CI runs native tests on Linux, macOS, and Windows, and release assets cover the supported OS/architecture targets above. See [reliability validation](reliability-validation.md) for test coverage and [performance measurements](performance-baseline.md) for the recorded baseline.

This is a pre-1.0 interface. Use matching CLI/provider releases, preserve the tools for unfinished runs, and read release notes before upgrading. Protocol/configuration/plan/journal/evidence versions are distinct; unsupported versions are rejected. There is no automatic migration command or promise that every older candidate's saved run can execute on a newer version.
