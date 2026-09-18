$ErrorActionPreference = 'Stop'

$Version = if ($env:VERSION) { $env:VERSION } else { '0.0.0-dev' }
$Commit = if ($env:COMMIT) { $env:COMMIT } else {
    try { (git rev-parse --short=12 HEAD).Trim() } catch { 'unknown' }
}
$BuildDate = if ($env:BUILD_DATE) { $env:BUILD_DATE } else { [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ') }
$Out = if ($env:OUT_DIR) { $env:OUT_DIR } else { 'dist' }

if (Test-Path $Out) { Remove-Item -Recurse -Force $Out }
New-Item -ItemType Directory -Path $Out | Out-Null

$CliLdFlags = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.buildDate=$BuildDate"
$ProviderLdFlags = "-s -w -X main.version=$Version"
$Targets = @(
    @('linux','amd64'), @('linux','arm64'),
    @('darwin','amd64'), @('darwin','arm64'),
    @('windows','amd64'), @('windows','arm64')
)
$Providers = @('npm', 'sdkman', 'homebrew', 'winget')

$OldCgo = $env:CGO_ENABLED
$OldGoos = $env:GOOS
$OldGoarch = $env:GOARCH
try {
    $env:CGO_ENABLED = '0'
    foreach ($Target in $Targets) {
        $Os, $Arch = $Target
        $Ext = if ($Os -eq 'windows') { '.exe' } else { '' }

        $CliName = "distroplane_${Version}_${Os}_${Arch}${Ext}"
        Write-Host "building $CliName"
        $env:GOOS = $Os
        $env:GOARCH = $Arch
        go build -trimpath -ldflags $CliLdFlags -o (Join-Path $Out $CliName) ./cmd/distroplane
        if ($LASTEXITCODE -ne 0) { throw "go build failed for distroplane $Os/$Arch" }

        foreach ($Provider in $Providers) {
            $ProviderName = "distroplane-provider-${Provider}_${Version}_${Os}_${Arch}${Ext}"
            Write-Host "building $ProviderName"
            go build -trimpath -ldflags $ProviderLdFlags -o (Join-Path $Out $ProviderName) "./cmd/distroplane-provider-${Provider}"
            if ($LASTEXITCODE -ne 0) { throw "go build failed for distroplane-provider-${Provider} $Os/$Arch" }
        }
    }
} finally {
    $env:CGO_ENABLED = $OldCgo
    $env:GOOS = $OldGoos
    $env:GOARCH = $OldGoarch
}

$Lines = Get-ChildItem $Out -File -Filter 'distroplane*' | Sort-Object Name | ForEach-Object {
    $Hash = (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLowerInvariant()
    "$Hash  $($_.Name)"
}
Set-Content -Path (Join-Path $Out 'SHA256SUMS') -Value $Lines -Encoding ascii
