$ErrorActionPreference = 'Stop'

function Add-PathForCurrentAndLaterSteps {
    param([Parameter(Mandatory = $true)][string]$Path)
    $env:PATH = $Path + [IO.Path]::PathSeparator + $env:PATH
    Add-Content -LiteralPath $env:GITHUB_PATH -Value $Path -Encoding utf8
}

function Make-Executable {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not $IsWindows) {
        & chmod +x $Path
        if ($LASTEXITCODE -ne 0) {
            throw "Could not mark '$Path' executable."
        }
    }
}

function Get-DistroplanePlatform {
    if ($IsWindows) {
        return 'windows'
    }
    if ($IsMacOS) {
        return 'darwin'
    }
    if ($IsLinux) {
        return 'linux'
    }
    throw 'Unsupported runner operating system.'
}

function Get-DistroplaneArchitecture {
    switch ($env:RUNNER_ARCH) {
        'X64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "Unsupported runner architecture '$($env:RUNNER_ARCH)'." }
    }
}

function Install-DistroplaneRelease {
    param(
        [AllowEmptyString()][string]$RequestedVersion,
        [AllowEmptyString()][string]$ActionRef,
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][bool]$InstallProviders,
        [scriptblock]$DownloadFile = {
            param([string]$Uri, [string]$OutFile)
            Invoke-WebRequest -Uri $Uri -OutFile $OutFile
        }
    )

    $versionText = $RequestedVersion.Trim()
    if (-not $versionText) {
        $versionText = $ActionRef.Trim()
    }
    if (-not $versionText -or $versionText -notmatch '^v?[0-9A-Za-z][0-9A-Za-z._+-]*$') {
        throw "A released Distroplane version is required. Set 'version' or invoke the action with a v-prefixed release ref."
    }
    if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') {
        throw "repository must use OWNER/REPO form."
    }

    $tag = if ($versionText.StartsWith('v')) { $versionText } else { "v$versionText" }
    $assetVersion = if ($versionText.StartsWith('v')) { $versionText.Substring(1) } else { $versionText }
    $platform = Get-DistroplanePlatform
    $architecture = Get-DistroplaneArchitecture
    $extension = if ($IsWindows) { '.exe' } else { '' }

    $cliName = "distroplane_${assetVersion}_${platform}_${architecture}${extension}"
    $suffix = "_${assetVersion}_${platform}_${architecture}${extension}"
    $installDir = Join-Path $env:RUNNER_TEMP "distroplane-action/$assetVersion/$platform-$architecture"
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null

    $releaseBase = "https://github.com/$Repository/releases/download/$tag"
    $checksumPath = Join-Path $installDir 'SHA256SUMS'
    & $DownloadFile "$releaseBase/SHA256SUMS" $checksumPath

    $entries = @{}
    foreach ($line in Get-Content -LiteralPath $checksumPath) {
        if ($line -notmatch '^([0-9a-fA-F]{64})  (.+)$') {
            throw "Invalid SHA256SUMS entry: $line"
        }
        $fileName = $Matches[2]
        if ([IO.Path]::GetFileName($fileName) -ne $fileName) {
            throw "Unsafe release asset name '$fileName'."
        }
        if ($entries.ContainsKey($fileName)) {
            throw "Duplicate SHA256SUMS entry for '$fileName'."
        }
        $entries[$fileName] = $Matches[1].ToLowerInvariant()
    }
    if (-not $entries.ContainsKey($cliName)) {
        throw "Release $tag does not contain $cliName."
    }

    $selected = @($cliName)
    if ($InstallProviders) {
        foreach ($fileName in $entries.Keys | Sort-Object) {
            if ($fileName.StartsWith('distroplane-provider-') -and $fileName.EndsWith($suffix)) {
                $selected += $fileName
            }
        }
    }

    $canonicalCli = Join-Path $installDir "distroplane$extension"
    foreach ($fileName in $selected | Select-Object -Unique) {
        $destination = Join-Path $installDir $fileName
        & $DownloadFile "$releaseBase/$fileName" $destination
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $destination).Hash.ToLowerInvariant()
        $expected = $entries[$fileName]
        if ($actual -ne $expected) {
            throw "Checksum mismatch for $fileName."
        }
        Make-Executable -Path $destination

        $canonicalName = if ($fileName -eq $cliName) {
            "distroplane$extension"
        } else {
            $providerBase = $fileName.Substring(0, $fileName.Length - $suffix.Length)
            "$providerBase$extension"
        }
        $canonicalPath = Join-Path $installDir $canonicalName
        Copy-Item -LiteralPath $destination -Destination $canonicalPath -Force
        Make-Executable -Path $canonicalPath
    }

    Add-PathForCurrentAndLaterSteps -Path $installDir
    return $canonicalCli
}
