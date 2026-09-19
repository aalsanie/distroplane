param(
    [ValidateSet('success', 'checksum', 'missing')][string]$Mode = 'success',
    [string]$Root = 'install-smoke'
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'install.ps1')

$rootPath = [IO.Path]::GetFullPath((Join-Path $PWD $Root))
$releasePath = Join-Path $rootPath 'release'
$statePath = Join-Path $rootPath 'state'
New-Item -ItemType Directory -Path $releasePath -Force | Out-Null
New-Item -ItemType Directory -Path $statePath -Force | Out-Null

$version = '0.0.0-smoke'
$platform = Get-DistroplanePlatform
$architecture = Get-DistroplaneArchitecture
$extension = if ($IsWindows) { '.exe' } else { '' }
$cliName = "distroplane_${version}_${platform}_${architecture}${extension}"
$providerName = "distroplane-provider-fake_${version}_${platform}_${architecture}${extension}"
$cliPath = Join-Path $releasePath $cliName
$providerPath = Join-Path $releasePath $providerName

go build -trimpath -o $cliPath ./cmd/distroplane
if ($LASTEXITCODE -ne 0) {
    throw 'failed to build release-install CLI fixture'
}
go build -trimpath -o $providerPath ./cmd/distroplane-provider-fake
if ($LASTEXITCODE -ne 0) {
    throw 'failed to build release-install provider fixture'
}
if (-not $IsWindows) {
    & chmod +x $cliPath $providerPath
    if ($LASTEXITCODE -ne 0) {
        throw 'failed to mark release-install fixtures executable'
    }
}

$artifact = Join-Path $rootPath 'app.bin'
[IO.File]::WriteAllBytes($artifact, [Text.Encoding]::UTF8.GetBytes('artifact'))
$configPath = Join-Path $rootPath 'distroplane.json'
$config = [ordered]@{
    schemaVersion = '1'
    release = [ordered]@{
        id = 'action-install-smoke'
        artifacts = @(
            [ordered]@{
                name = 'app'
                source = 'app.bin'
                mediaType = 'application/octet-stream'
            }
        )
    }
    targets = @(
        [ordered]@{
            id = 'fake'
            provider = [ordered]@{
                name = 'fake'
            }
            configuration = [ordered]@{
                mode = 'published'
            }
        }
    )
}
$config | ConvertTo-Json -Depth 12 -Compress | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM

$checksumPath = Join-Path $releasePath 'SHA256SUMS'
$checksumLines = @(
    "$((Get-FileHash -Algorithm SHA256 -LiteralPath $cliPath).Hash.ToLowerInvariant())  $cliName",
    "$((Get-FileHash -Algorithm SHA256 -LiteralPath $providerPath).Hash.ToLowerInvariant())  $providerName"
)
$checksumLines | Set-Content -LiteralPath $checksumPath -Encoding ascii

switch ($Mode) {
    'checksum' {
        [IO.File]::AppendAllText($cliPath, 'tampered')
    }
    'missing' {
        @($checksumLines | Where-Object { -not $_.EndsWith("  $cliName") }) |
            Set-Content -LiteralPath $checksumPath -Encoding ascii
    }
}

$download = {
    param([string]$Uri, [string]$OutFile)
    $fileName = [IO.Path]::GetFileName(([Uri]$Uri).AbsolutePath)
    $source = Join-Path $releasePath $fileName
    if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "fixture release asset '$fileName' is unavailable"
    }
    Copy-Item -LiteralPath $source -Destination $OutFile -Force
}

if ($Mode -ne 'success') {
    try {
        Install-DistroplaneRelease -RequestedVersion '' -ActionRef "v$version" -Repository 'example/distroplane' -InstallProviders $true -DownloadFile $download | Out-Null
        throw "installer unexpectedly succeeded in $Mode mode"
    } catch {
        $message = $_.Exception.Message
        if ($Mode -eq 'checksum' -and $message -notmatch 'Checksum mismatch') {
            throw "unexpected checksum failure: $message"
        }
        if ($Mode -eq 'missing' -and $message -notmatch 'does not contain') {
            throw "unexpected missing-release failure: $message"
        }
        Write-Host "Observed expected $Mode installer failure."
        exit 0
    }
}

$installed = Install-DistroplaneRelease -RequestedVersion '' -ActionRef "v$version" -Repository 'example/distroplane' -InstallProviders $true -DownloadFile $download
$expectedCli = "distroplane$extension"
if ([IO.Path]::GetFileName($installed) -ne $expectedCli -or -not (Test-Path -LiteralPath $installed -PathType Leaf)) {
    throw "installer returned invalid CLI path '$installed'"
}

$resolvedCli = Get-Command $expectedCli -ErrorAction Stop
$resolvedProvider = Get-Command "distroplane-provider-fake$extension" -ErrorAction Stop
if (-not $resolvedCli.Source -or -not $resolvedProvider.Source) {
    throw 'canonical installed executables are not discoverable on PATH'
}

$output = & $installed plan --config $configPath --state-dir $statePath --json
if ($LASTEXITCODE -ne 0) {
    throw "installed CLI plan failed with exit code $LASTEXITCODE"
}
$result = $output | ConvertFrom-Json
if (-not $result.planId -or $result.targets -ne 1 -or $result.operations -ne 1) {
    throw 'installed CLI/provider discovery returned an invalid plan result'
}
