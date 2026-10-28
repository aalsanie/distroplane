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

**Public Windows MSI publication is blocked until release ownership has provided an approved Authenticode publisher identity and signing certificate.** The stable release job requires `MSI_SIGNING_PFX_BASE64` and `MSI_SIGNING_PFX_PASSWORD` repository secrets, uses a trusted timestamp, verifies Authenticode validity, and only then hands the packages to the release publishing job. Never put signing keys into source or artifacts. GitHub Actions provenance attests the published MSI alongside the other versioned release files; `MSI-SHA256SUMS` covers the two MSI files independently of the existing 36-asset archive contract. The release job checks exact Windows ZIP byte parity against its own reproducible build before publishing the signed MSI. Code signing does not guarantee a new publisher will have established Microsoft SmartScreen reputation; warn operators about initial reputation prompts, never instruct them to bypass a security warning.

Without a signing identity, **future stable release publication fails closed** instead of shipping unsigned MSI. The previously published `v0.9.1` release has no MSI and is not retroactively changed by these source revisions.

## WinGet community submission

WinGet identity **`Aalsanie.Distroplane` is provisional** until the publisher approves the package ID and publisher name. Generate version-specific WinGet manifests from the **actual signed MSI files**, using explicit approved identity values:

```powershell
python scripts/generate-winget-manifests.py --version 0.9.2 --msi-dir msi --package-id Aalsanie.Distroplane --publisher Aalsanie --output-dir winget-candidate
winget validate --manifest winget-candidate
```

Only substitute a released version for which both MSI URLs exist. Independently compare the two manifest digests against the **published MSI bytes**, verify their signatures, perform a local `winget install --manifest` and uninstall, then submit one version per PR to the official `microsoft/winget-pkgs` repository following its external contribution process. Community acceptance and public client propagation are separate from a successful local manifest validation. Do **not** advertise `winget install --id Aalsanie.Distroplane -e` until the community PR is merged and the public catalog client succeeds.
