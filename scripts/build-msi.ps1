# Build a Windows MSI exclusively from a verified unified release archive.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][ValidateSet('amd64', 'arm64')][string]$Architecture,
    [Parameter(Mandatory)][string]$ReleaseDir,
    [Parameter(Mandatory)][string]$OutputDir,
    [string]$WixExe = 'wix',
    [switch]$RequireSignature
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($Version -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$') { throw 'MSI packages require a stable three-part version' }
if ($Version.Split('.')[0..2] | Where-Object { [int64]$_ -gt 65535 }) { throw 'MSI version segment exceeds Windows Installer limits' }
if ($RequireSignature -and (-not $env:MSI_SIGNING_PFX_BASE64 -or -not $env:MSI_SIGNING_PFX_PASSWORD)) {
    throw 'Release MSI signing requires MSI_SIGNING_PFX_BASE64 and MSI_SIGNING_PFX_PASSWORD'
}
$release = (Resolve-Path $ReleaseDir).Path
$out = [IO.Path]::GetFullPath($OutputDir)
$metaPath = Join-Path $release 'RELEASE-METADATA.json'
$checksums = Join-Path $release 'SHA256SUMS'
if (-not (Test-Path -PathType Leaf $metaPath) -or -not (Test-Path -PathType Leaf $checksums)) {
    throw 'Verified release metadata and SHA256SUMS are required'
}
$meta = Get-Content -Raw -LiteralPath $metaPath | ConvertFrom-Json
if ($meta.schemaVersion -ne '2' -or $meta.version -cne $Version -or $meta.cgoEnabled -ne $false -or
    $meta.commit -cnotmatch '^[0-9a-f]{12}$' -or @($meta.assets).Count -ne 36) {
    throw 'Release identity or inventory is inconsistent with MSI input'
}
$zipName = "distroplane_${Version}_windows_${Architecture}.zip"
$archive = Join-Path $release $zipName
$archiveEntry = @($meta.assets | Where-Object { $_.name -ceq $zipName })
if ($archiveEntry.Count -ne 1 -or -not (Test-Path -PathType Leaf $archive)) {
    throw "Missing verified unified Windows archive: $zipName"
}
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
if ($hash -cne $archiveEntry[0].sha256 -or (Get-Item $archive).Length -ne $archiveEntry[0].size) {
    throw 'Windows archive does not match release manifest'
}
$lines = @(Get-Content $checksums | Where-Object { $_ -match '^[0-9a-f]{64}  \S+$' })
if ($lines.Count -ne 36 -or @($lines | Where-Object { $_ -ceq "$hash  $zipName" }).Count -ne 1) {
    throw 'Invalid release checksum inventory'
}
$stage = Join-Path ([IO.Path]::GetTempPath()) ("distroplane-msi-" + [Guid]::NewGuid().ToString('N'))
$upgrade = if ($Architecture -eq 'amd64') {
    '708E3E44-72D8-4DC6-ACB9-FB5A7DD0A27E'
} else {
    'C937D854-AC1A-4966-9524-3927981DB7A6'
}
try {
    New-Item -Path $stage -ItemType Directory | Out-Null
    Expand-Archive -LiteralPath $archive -DestinationPath $stage
    $packagePath = Join-Path $stage 'PACKAGE-METADATA.json'
    $package = Get-Content -Raw -LiteralPath $packagePath | ConvertFrom-Json
    if ($package.schemaVersion -ne '1' -or $package.version -cne $Version -or
        $package.commit -cne $meta.commit -or $package.os -cne 'windows' -or
        $package.arch -cne $Architecture -or $package.cgoEnabled -ne $false) {
        throw 'Embedded package identity does not match release'
    }
    $wanted = @('bin/distroplane.exe') + @('homebrew','npm','sdkman','winget' |
        ForEach-Object { "libexec/distroplane-provider-$_.exe" })
    if (@($package.files).Count -ne 5) { throw 'MSI input must contain exactly five executables' }
    $seen = @{}
    foreach ($item in $package.files) {
        $name = [string]$item.path
        if ($name -cnotin $wanted -or $seen.ContainsKey($name) -or $item.mode -cne '0755') {
            throw "Unsafe MSI payload entry: $name"
        }
        $seen[$name] = $true
        $source = Join-Path $stage ($name.Replace('/', [IO.Path]::DirectorySeparatorChar))
        if (-not (Test-Path -PathType Leaf $source)) { throw "Missing $name" }
        $digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash.ToLowerInvariant()
        if ($digest -cne $item.sha256 -or (Get-Item $source).Length -ne $item.size) {
            throw "MSI payload integrity failure: $name"
        }
        $standalone = $name.Replace('bin/','').Replace('libexec/','').Replace('.exe','')
        $expectedName = "${standalone}_${Version}_windows_${Architecture}.exe"
        $standalonePath = Join-Path $release $expectedName
        if (-not (Test-Path -PathType Leaf $standalonePath) -or
            (Get-FileHash -Algorithm SHA256 -LiteralPath $standalonePath).Hash.ToLowerInvariant() -cne $digest) {
            throw "MSI payload differs from released standalone executable: $name"
        }
    }
    if ($seen.Count -ne 5) { throw 'Incomplete MSI payload' }
    $allFiles = @(Get-ChildItem $stage -Recurse -File)
    if ($allFiles.Count -ne 6) { throw 'Unexpected MSI payload file' }
    New-Item -Path $out -ItemType Directory -Force | Out-Null
    $msi = Join-Path $out "distroplane_${Version}_windows_${Architecture}.msi"
    & $WixExe build 'packaging/windows/Product.wxs' '-arch' $(if ($Architecture -eq 'amd64') { 'x64' } else { 'arm64' }) '-d' "ProductVersion=$Version" '-d' "UpgradeCode=$upgrade" '-d' "PayloadDir=$stage" '-o' $msi
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -PathType Leaf $msi)) { throw "WiX MSI build failed: $Architecture" }
    if ($env:MSI_SIGNING_PFX_BASE64) {
        if (-not $env:MSI_SIGNING_PFX_PASSWORD) { throw 'Signing password is missing' }
        $certificate = Join-Path $stage 'identity.pfx'
        try {
            [IO.File]::WriteAllBytes($certificate, [Convert]::FromBase64String($env:MSI_SIGNING_PFX_BASE64))
            & signtool.exe sign /fd SHA256 /tr https://timestamp.digicert.com /td SHA256 /f $certificate /p $env:MSI_SIGNING_PFX_PASSWORD $msi
            if ($LASTEXITCODE -ne 0) { throw 'Authenticode signing failed' }
        } finally {
            Remove-Item -LiteralPath $certificate -Force -ErrorAction SilentlyContinue
        }
    }
    if ($RequireSignature -or $env:MSI_SIGNING_PFX_BASE64) {
        $signature = Get-AuthenticodeSignature -LiteralPath $msi
        if ($signature.Status -ne 'Valid') { throw "MSI Authenticode signature invalid: $($signature.Status)" }
    }
    Write-Host "Built $msi"
} finally {
    if (Test-Path $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
}
