param(
    [string]$Root = 'smoke',
    [ValidateSet('published', 'waiting_external')][string]$Mode = 'published'
)

$ErrorActionPreference = 'Stop'
$rootPath = [IO.Path]::GetFullPath((Join-Path $PWD $Root))
$binPath = Join-Path $rootPath 'bin'
New-Item -ItemType Directory -Path $binPath -Force | Out-Null

$extension = if ($IsWindows) { '.exe' } else { '' }
$cli = Join-Path $binPath "distroplane$extension"
$provider = Join-Path $binPath "distroplane-provider-fake$extension"

go build -trimpath -o $cli ./cmd/distroplane
if ($LASTEXITCODE -ne 0) {
    throw 'failed to build Distroplane CLI'
}
go build -trimpath -o $provider ./cmd/distroplane-provider-fake
if ($LASTEXITCODE -ne 0) {
    throw 'failed to build fake provider'
}

if (-not $IsWindows) {
    & chmod +x $cli $provider
    if ($LASTEXITCODE -ne 0) {
        throw 'failed to mark smoke binaries executable'
    }
}

$artifact = Join-Path $rootPath 'app.bin'
[IO.File]::WriteAllBytes($artifact, [Text.Encoding]::UTF8.GetBytes('artifact'))

$providerConfiguration = [ordered]@{
    mode = $Mode
}
if ($Mode -eq 'waiting_external') {
    $providerConfiguration['reconcileState'] = 'published'
}

$config = [ordered]@{
    schemaVersion = '1'
    release = [ordered]@{
        id = 'action-smoke'
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
                executable = $provider
            }
            configuration = $providerConfiguration
        }
    )
}
$configPath = Join-Path $rootPath 'distroplane.json'
$config | ConvertTo-Json -Depth 12 -Compress | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM

if ($env:GITHUB_OUTPUT) {
    "cli=$cli" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
    "provider=$provider" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
    "config=$configPath" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
    "root=$rootPath" | Add-Content -LiteralPath $env:GITHUB_OUTPUT -Encoding utf8
}
