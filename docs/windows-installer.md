# Windows MSI packaging and WinGet submission

Windows installers are per-machine packages built with WiX Toolset **6.0.0** (pinned .NET tool). The MSI installs `distroplane.exe` into `%ProgramFiles%\Distroplane\bin` and the four verified provider executables into `%ProgramFiles%\Distroplane\libexec`. The installer adds only the CLI bin folder to machine `PATH`, removes its own entry on uninstall, and requires elevation. No Go or WiX installation is required on consumer machines.

## Installer lifecycle

The x64 and ARM64 MSIs have separate, immutable upgrade identities. Major upgrades preserve the installed scope, replace only package-owned files, refuse downgrades, and do not touch per-user or per-project `.distroplane` configuration, plans, journals, evidence, or retained provider executables. An architecture change must be treated as an explicit uninstall/reinstall, not an automatic cross-architecture upgrade.

On a supported Windows build host, first run `scripts/build.ps1` to generate and verify the release archives. Install the pinned WiX .NET tool with `dotnet tool install --tool-path .tools/wix wix --version 6.0.0`, then build with:

```powershell
./scripts/build-msi.ps1 -Version 0.9.1 -Architecture amd64 -ReleaseDir dist -OutputDir msi -WixExe .tools/wix/wix.exe
```

This creates a **local unsigned test MSI**, not a distributable signed release. Execute native acceptance tests only on the matching Windows architecture:

```powershell
./scripts/test-msi.ps1 -Version 0.9.1 -Architecture amd64 -Msi msi/distroplane_0.9.1_windows_amd64.msi
```

GitHub CI additionally tests a synthetic predecessor MSI upgrade, repair, provider describe calls, machine PATH registration, and uninstall while retaining user state. The ARM64 test runs on a native Windows ARM64 host.

## Signing and supply chain policy

Stable releases require the Actions variable **`WINDOWS_MSI_SIGNING_MODE`** to be explicitly configured under **Settings → Secrets and variables → Actions → Variables**. An unset or invalid mode blocks the release. Allowed case-sensitive values:

- `unsigned`: explicitly distribute **unsigned** MSI installers; signing secrets must be absent. The release job verifies both installers are unsigned and appends a visible warning to its published release notes. Windows SmartScreen or organizational policy may warn or block installation. This mode provides SHA-256 checksums and GitHub Actions provenance, **not** Authenticode publisher identity.
- `pfx`: require a valid Authenticode signature on both MSIs. This mode requires the existing `MSI_SIGNING_PFX_BASE64` and `MSI_SIGNING_PFX_PASSWORD` repository secrets for an approved exportable PFX. Timestamp and signature validation failures block publication. Hardware-backed or managed signing requires a separate approved integration.

Never put signing keys into source control. Both modes retain versioned release-archive byte parity, `MSI-SHA256SUMS` for the two MSI files, and GitHub Actions attestations. The existing 36-asset release metadata contract is unchanged. The already published `v0.9.1` release must not be modified.

## WinGet community submission

WinGet identity **`Aalsanie.Distroplane` is provisional** until the publisher approves the package ID and publisher name. Generate version-specific WinGet manifests from the **actual published MSI files**, using explicit approved identity values:

```powershell
python scripts/generate-winget-manifests.py --version 0.9.2 --msi-dir msi --package-id Aalsanie.Distroplane --publisher Aalsanie --output-dir winget-candidate
winget validate --manifest winget-candidate/a/Aalsanie/Distroplane/0.9.2
```

Only substitute a released version for which both MSI URLs exist. Independently compare the manifest hashes against the **published MSI bytes**. For `pfx` releases, verify both Authenticode signatures; for `unsigned`, confirm there are no signatures and disclose the absence of publisher authentication. Perform local `winget validate --manifest`, `winget install --manifest`, and uninstall tests, then submit one version per PR to `microsoft/winget-pkgs`. MSI submissions do not have a blanket Authenticode signing requirement, but Microsoft's unattended-install and security checks still apply and can reject a package. **Do not** advertise `winget install --id Aalsanie.Distroplane -e` until the community PR has merged and public catalog installation succeeds.
