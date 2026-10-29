function Install-VerifiedDistroplaneArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$AssetVersion,
        [Parameter(Mandatory = $true)][string]$Platform,
        [Parameter(Mandatory = $true)][string]$Architecture,
        [Parameter(Mandatory = $true)][bool]$InstallProviders
    )

    $extension = if ($Platform -eq 'windows') { '.exe' } else { '' }
    $expected = @("bin/distroplane$extension", 'PACKAGE-METADATA.json')
    foreach ($provider in @('homebrew', 'npm', 'sdkman', 'winget')) {
        $expected += "libexec/distroplane-provider-$provider$extension"
    }
    $stage = Join-Path $env:RUNNER_TEMP ("distroplane-package-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    $files = @{}
    try {
        if ($Platform -eq 'windows') {
            $zip = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
            try {
                foreach ($entry in $zip.Entries) {
                    $name = $entry.FullName
                    if ($name -cnotin $expected -or $files.ContainsKey($name)) {
                        throw "Unexpected or duplicate archive member '$name'."
                    }
                    $maxSize = if ($name -eq 'PACKAGE-METADATA.json') { 1048576 } else { 134217728 }
                    if ($entry.Length -gt $maxSize) { throw "Oversized archive member '$name'." }
                    $fileMode = ($entry.ExternalAttributes -shr 16) -band 0xFFF
                    $requiredMode = if ($name -eq 'PACKAGE-METADATA.json') { 420 } else { 493 }
                    if ($fileMode -ne $requiredMode) { throw "Unsafe permissions for '$name'." }
                    $target = Join-Path $stage ($name.Replace('/', [IO.Path]::DirectorySeparatorChar))
                    New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
                    $source = $entry.Open()
                    $destination = [IO.File]::Create($target)
                    try {
                        $source.CopyTo($destination)
                    } finally {
                        $destination.Dispose()
                        $source.Dispose()
                    }
                    $files[$name] = $target
                }
            } finally { $zip.Dispose() }
        } else {
            Add-Type -AssemblyName System.Formats.Tar
            $stream = [IO.File]::OpenRead($ArchivePath)
            $gzip = [IO.Compression.GZipStream]::new($stream, [IO.Compression.CompressionMode]::Decompress)
            $tar = [System.Formats.Tar.TarReader]::new($gzip, $false)
            try {
                while ($null -ne ($entry = $tar.GetNextEntry())) {
                    $name = $entry.Name
                    if ($name -cnotin $expected -or $files.ContainsKey($name) -or
                        $entry.EntryType.ToString() -notin @('RegularFile', 'V7RegularFile')) {
                        throw "Unsafe or unexpected archive member '$name'."
                    }
                    $maxSize = if ($name -eq 'PACKAGE-METADATA.json') { 1048576 } else { 134217728 }
                    if ($entry.Length -gt $maxSize) { throw "Oversized archive member '$name'." }
                    $requiredMode = if ($name -eq 'PACKAGE-METADATA.json') { 420 } else { 493 }
                    if ($entry.Mode -ne $requiredMode) { throw "Unsafe permissions for '$name'." }
                    $target = Join-Path $stage ($name.Replace('/', [IO.Path]::DirectorySeparatorChar))
                    New-Item -ItemType Directory -Path (Split-Path -Parent $target) -Force | Out-Null
                    $destination = [IO.File]::Create($target)
                    try {
                        $entry.DataStream.CopyTo($destination)
                    } finally { $destination.Dispose() }
                    $files[$name] = $target
                }
            } finally {
                $tar.Dispose()
                $gzip.Dispose()
                $stream.Dispose()
            }
        }
        if ($files.Count -ne $expected.Count) { throw 'Unified release archive is missing required executables.' }
        $metadata = Get-Content -LiteralPath $files['PACKAGE-METADATA.json'] -Raw | ConvertFrom-Json
        if ($metadata.schemaVersion -cne '1' -or $metadata.version -cne $AssetVersion -or
            $metadata.os -cne $Platform -or $metadata.arch -cne $Architecture -or
            $metadata.cgoEnabled -ne $false -or @($metadata.files).Count -ne 5) {
            throw 'Unified release package identity does not match the requested platform/version.'
        }
        $recorded = @{}
        foreach ($record in $metadata.files) {
            $name = [string]$record.path
            if ($name -cnotin $expected -or $name -eq 'PACKAGE-METADATA.json' -or $recorded.ContainsKey($name)) {
                throw 'Unexpected or duplicate package executable metadata.'
            }
            $path = $files[$name]
            if (-not $path -or [string]$record.mode -cne '0755' -or
                [long]$record.size -ne (Get-Item -LiteralPath $path).Length -or
                ([string]$record.sha256).ToLowerInvariant() -cne (Get-FileHash -Algorithm SHA256 -LiteralPath $path).Hash.ToLowerInvariant()) {
                throw "Unified package file checksum mismatch: $name"
            }
            $recorded[$name] = $true
        }
        if ($recorded.Count -ne 5) { throw 'Unified package executable metadata is incomplete.' }
        $bin = Join-Path $stage 'bin'
        $libexec = Join-Path $stage 'libexec'
        if (-not $IsWindows) {
            foreach ($name in $recorded.Keys) { Make-Executable -Path $files[$name] }
        }
        $cli = Join-Path $bin "distroplane$extension"
        $versionResult = & $cli version --json | ConvertFrom-Json
        if ($LASTEXITCODE -ne 0 -or $versionResult.version -cne $AssetVersion) {
            throw 'Installed CLI version differs from verified package version.'
        }
        if ($InstallProviders) {
            Add-PathForCurrentAndLaterSteps -Path $libexec
        }
        Add-PathForCurrentAndLaterSteps -Path $bin
        return $cli
    } catch {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
        throw
    }
}