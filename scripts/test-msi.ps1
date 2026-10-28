# Native MSI install, versioned upgrade, provider execution and uninstall acceptance.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][ValidateSet('amd64', 'arm64')][string]$Architecture,
    [Parameter(Mandatory)][string]$Msi,
    [string]$PreviousMsi
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$arch = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
$expected = if ($Architecture -eq 'arm64') { 'Arm64' } else { 'X64' }
if ($arch -cne $expected) { throw "Cannot claim native MSI testing on $arch; expected $expected" }
$msiPath = (Resolve-Path $Msi).Path
$oldMsi = if ($PreviousMsi) { (Resolve-Path $PreviousMsi).Path } else { '' }
$programFiles = [Environment]::GetFolderPath('ProgramFiles')
$installed = Join-Path $programFiles 'Distroplane'
$bin = Join-Path $installed 'bin'
$cli = Join-Path $bin 'distroplane.exe'
$providers = Join-Path $installed 'libexec'
$state = Join-Path $env:USERPROFILE '.distroplane\msi-lifecycle-test'
$marker = Join-Path $state 'run.journal'
New-Item -ItemType Directory -Path $state -Force | Out-Null
[IO.File]::WriteAllText($marker, 'retained across MSI lifecycle')
function Invoke-Msi([string]$Operation, [string]$Package, [string]$LogName) {
    $log = Join-Path $env:RUNNER_TEMP $LogName
    if (-not $env:RUNNER_TEMP) { $log = Join-Path ([IO.Path]::GetTempPath()) $LogName }
    $arguments = "/$Operation `"$Package`" /qn /norestart /l*v `"$log`""
    $p = Start-Process -FilePath msiexec.exe -ArgumentList $arguments -Wait -PassThru
    if ($p.ExitCode -ne 0) {
        Get-Content -Tail 100 -LiteralPath $log | Write-Host
        throw "msiexec /$Operation exit code $($p.ExitCode)"
    }
}
function Assert-Installed([string]$wantVersion) {
    if (-not (Test-Path -PathType Leaf $cli)) { throw 'MSI did not install Distroplane CLI' }
    $identity = & $cli version --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $identity.version -cne $wantVersion) { throw 'Installed CLI identity mismatch' }
    foreach ($name in @('homebrew','npm','sdkman','winget')) {
        $path = Join-Path $providers "distroplane-provider-$name.exe"
        if (-not (Test-Path -PathType Leaf $path)) { throw "Missing $name provider" }
        $request = '{"protocolVersion":"1","requestId":"msi-smoke","operation":"describe","payload":{}}'
        $response = $request | & $path | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or $response.status -cne 'ok' -or
            $response.payload.provider.name -cne $name -or $response.payload.provider.version -cne $wantVersion) {
            throw "Installed provider failed describe: $name"
        }
    }
    $machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    if (-not ($machinePath -split ';' | Where-Object {
        $_.TrimEnd('\') -ieq $bin.TrimEnd('\')
    })) { throw 'MSI did not register its CLI directory in machine PATH' }
}
try {
    if ($oldMsi) {
        Invoke-Msi 'i' $oldMsi 'distroplane-previous-install.log'
        if (-not (Test-Path -PathType Leaf $cli)) { throw 'Previous MSI was not installed' }
    }
    Invoke-Msi 'i' $msiPath 'distroplane-install.log'
    Assert-Installed $Version
    if ((Get-Content -Raw -LiteralPath $marker) -cne 'retained across MSI lifecycle') {
        throw 'MSI upgrade deleted release state'
    }
    # Reinstall/repair must leave a complete package and user state.
    Invoke-Msi 'i' $msiPath 'distroplane-repair.log'
    Assert-Installed $Version
} finally {
    if (Test-Path -PathType Leaf $msiPath) {
        Invoke-Msi 'x' $msiPath 'distroplane-uninstall.log'
    }
}
if (Test-Path -PathType Leaf $cli) { throw 'MSI uninstall left installed CLI behind' }
if ((Get-Content -Raw -LiteralPath $marker) -cne 'retained across MSI lifecycle') {
    throw 'MSI uninstall deleted release state'
}
$machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
if ($machinePath -split ';' | Where-Object { $_.TrimEnd('\') -ieq $bin.TrimEnd('\') }) {
    throw 'MSI uninstall left an owned PATH entry behind'
}
Write-Host "Native Windows $Architecture MSI install/upgrade/uninstall passed"
