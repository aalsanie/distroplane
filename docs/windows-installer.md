# Windows installation

```powershell
winget install --id Aalsanie.Distroplane --exact
```

Or download the [Windows MSI](https://github.com/aalsanie/distroplane/releases/latest). Choose the x64 or ARM64 installer for your machine. The MSI includes Distroplane and all four providers, installs to `%ProgramFiles%\Distroplane`, and requires administrator approval.

## MSI lifecycle

Install, repair, upgrade, and uninstall affect package-owned files only. Project `.distroplane` plans, journals, and retained provider executables are never removed. x64 and ARM64 packages use separate upgrade identities.

## Building from source

Use pinned WiX Toolset 6.0.0 and verified release archives:

```powershell
./scripts/build.ps1
dotnet tool install --tool-path .tools/wix wix --version 6.0.0
./scripts/build-msi.ps1 -Version 0.9.2 -Architecture amd64 -ReleaseDir dist -OutputDir msi -WixExe .tools/wix/wix.exe
./scripts/test-msi.ps1 -Version 0.9.2 -Architecture amd64 -Msi msi/distroplane_0.9.2_windows_amd64.msi
```

Use `-Architecture arm64` and a native ARM64 test host for ARM64 acceptance.

## Signing

Stable release builds must set the `WINDOWS_MSI_SIGNING_MODE` GitHub Actions variable explicitly:

- `unsigned`: publish unsigned MSIs with a visible release warning; SmartScreen and managed devices may prompt or block.
- `pfx`: require a verified Authenticode signature on both MSIs using `MSI_SIGNING_PFX_BASE64` and `MSI_SIGNING_PFX_PASSWORD` secrets.

Release archives and MSIs retain SHA-256 inventories and build-provenance attestations. A checksum alone does not provide independent publisher identity.

[Release verification](release-verification.md)
