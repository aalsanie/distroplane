$ErrorActionPreference = 'Stop'
if (Get-Variable PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

function Set-ActionOutput {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowEmptyString()][string]$Value = ''
    )
    if (-not $env:GITHUB_OUTPUT) {
        throw 'GITHUB_OUTPUT is not available.'
    }
    $delimiter = 'DISTROPLANE_' + [Guid]::NewGuid().ToString('N')
    Add-Content -LiteralPath $env:GITHUB_OUTPUT -Value "$Name<<$delimiter" -Encoding utf8
    Add-Content -LiteralPath $env:GITHUB_OUTPUT -Value $Value -Encoding utf8
    Add-Content -LiteralPath $env:GITHUB_OUTPUT -Value $delimiter -Encoding utf8
}

function Parse-BooleanInput {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowEmptyString()][string]$Value
    )
    switch ($Value.ToLowerInvariant()) {
        'true' { return $true }
        'false' { return $false }
        default { throw "$Name must be 'true' or 'false'." }
    }
}

function Resolve-LocalPath {
    param([Parameter(Mandatory = $true)][string]$Path)
    if ([IO.Path]::IsPathRooted($Path)) {
        return [IO.Path]::GetFullPath($Path)
    }
    return [IO.Path]::GetFullPath((Join-Path $env:GITHUB_WORKSPACE $Path))
}

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

function Install-DistroplaneRelease {
    param(
        [AllowEmptyString()][string]$RequestedVersion,
        [AllowEmptyString()][string]$ActionRef,
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][bool]$InstallProviders
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

    $platform = if ($IsWindows) {
        'windows'
    } elseif ($IsMacOS) {
        'darwin'
    } elseif ($IsLinux) {
        'linux'
    } else {
        throw 'Unsupported runner operating system.'
    }

    $architecture = switch ($env:RUNNER_ARCH) {
        'X64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "Unsupported runner architecture '$($env:RUNNER_ARCH)'." }
    }

    $extension = if ($IsWindows) { '.exe' } else { '' }
    $cliName = "distroplane_\${assetVersion}_\${platform}_\${architecture}\${extension}"
    $installDir = Join-Path $env:RUNNER_TEMP "distroplane-action/$assetVersion/$platform-$architecture"
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null

    $releaseBase = "https://github.com/$Repository/releases/download/$tag"
    $checksumPath = Join-Path $installDir 'SHA256SUMS'
    Invoke-WebRequest -Uri "$releaseBase/SHA256SUMS" -OutFile $checksumPath

    $entries = @{}
    foreach ($line in Get-Content -LiteralPath $checksumPath) {
        if ($line -notmatch '^([0-9a-fA-F]{64})  (.+)$') {
            throw "Invalid SHA256SUMS entry: $line"
        }
        $fileName = $Matches[2]
        if ([IO.Path]::GetFileName($fileName) -ne $fileName) {
            throw "Unsafe release asset name '$fileName'."
        }
        $entries[$fileName] = $Matches[1].ToLowerInvariant()
    }
    if (-not $entries.ContainsKey($cliName)) {
        throw "Release $tag does not contain $cliName."
    }

    $selected = @($cliName)
    if ($InstallProviders) {
        $suffix = "_\${assetVersion}_\${platform}_\${architecture}\${extension}"
        foreach ($fileName in $entries.Keys | Sort-Object) {
            if ($fileName.StartsWith('distroplane-provider-') -and $fileName.EndsWith($suffix)) {
                $selected += $fileName
            }
        }
    }

    foreach ($fileName in $selected | Select-Object -Unique) {
        $destination = Join-Path $installDir $fileName
        Invoke-WebRequest -Uri "$releaseBase/$fileName" -OutFile $destination
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $destination).Hash.ToLowerInvariant()
        $expected = $entries[$fileName]
        if ($actual -ne $expected) {
            throw "Checksum mismatch for $fileName."
        }
        Make-Executable -Path $destination
    }

    Add-PathForCurrentAndLaterSteps -Path $installDir
    return (Join-Path $installDir $cliName)
}

function Resolve-DistroplaneBinary {
    $local = $env:DISTROPLANE_BINARY.Trim()
    if ($local) {
        $path = Resolve-LocalPath -Path $local
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Distroplane binary '$path' does not exist."
        }
        Make-Executable -Path $path
        return $path
    }

    $installProviders = Parse-BooleanInput -Name 'install-providers' -Value $env:DISTROPLANE_INSTALL_PROVIDERS
    return Install-DistroplaneRelease -RequestedVersion $env:DISTROPLANE_VERSION -ActionRef $env:DISTROPLANE_ACTION_REF -Repository $env:DISTROPLANE_ACTION_REPOSITORY -InstallProviders $installProviders
}

function Add-MultilineFlags {
    param(
        [Parameter(Mandatory = $true)][System.Collections.Generic.List[string]]$Arguments,
        [AllowEmptyString()][string]$Raw,
        [Parameter(Mandatory = $true)][string]$Flag,
        [Parameter(Mandatory = $true)][string]$FormatName
    )
    foreach ($line in ($Raw -split "\r?\n")) {
        $value = $line.Trim()
        if (-not $value) {
            continue
        }
        if ($value -notmatch '^[^=\s]+=.+' ) {
            throw "$FormatName entries must use NAME=VALUE form."
        }
        $Arguments.Add($Flag)
        $Arguments.Add($value)
    }
}

function Invoke-Distroplane {
    param(
        [Parameter(Mandatory = $true)][string]$Binary,
        [Parameter(Mandatory = $true)][System.Collections.Generic.List[string]]$Arguments
    )

    $id = [Guid]::NewGuid().ToString('N')
    $stdoutPath = Join-Path $env:RUNNER_TEMP "distroplane-$id.stdout"
    $stderrPath = Join-Path $env:RUNNER_TEMP "distroplane-$id.stderr"
    try {
        & $Binary @Arguments 1> $stdoutPath 2> $stderrPath
        $code = $LASTEXITCODE
        $stdout = if (Test-Path -LiteralPath $stdoutPath) { Get-Content -LiteralPath $stdoutPath -Raw } else { '' }
        $stderr = if (Test-Path -LiteralPath $stderrPath) { Get-Content -LiteralPath $stderrPath -Raw } else { '' }
        if ($stderr) {
            [Console]::Error.Write($stderr)
        }
        return [PSCustomObject]@{
            ExitCode = [int]$code
            Stdout = $stdout.Trim()
        }
    } finally {
        Remove-Item -LiteralPath $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
    }
}

if (-not $env:GITHUB_WORKSPACE) {
    throw 'GITHUB_WORKSPACE is not available.'
}
Set-Location -LiteralPath $env:GITHUB_WORKSPACE

$binary = Resolve-DistroplaneBinary

$oidcAvailable = [bool]($env:ACTIONS_ID_TOKEN_REQUEST_URL -and $env:ACTIONS_ID_TOKEN_REQUEST_TOKEN)
Set-ActionOutput -Name 'oidc-available' -Value $oidcAvailable.ToString().ToLowerInvariant()

$oidcMode = $env:DISTROPLANE_OIDC.Trim().ToLowerInvariant()
if ($oidcMode -notin @('none', 'required')) {
    throw "oidc must be 'none' or 'required'."
}
if ($oidcMode -eq 'required' -and -not $oidcAvailable) {
    throw "OIDC is required but unavailable. Grant the job 'permissions: id-token: write' and ensure the runner supports GitHub OIDC."
}

$command = $env:DISTROPLANE_COMMAND.Trim().ToLowerInvariant()
if ($command -notin @('plan', 'apply', 'reconcile')) {
    throw "command must be one of: plan, apply, reconcile."
}

$arguments = [System.Collections.Generic.List[string]]::new()
$arguments.Add($command)

switch ($command) {
    'plan' {
        if (-not $env:DISTROPLANE_CONFIG.Trim()) {
            throw 'config must not be empty.'
        }
        $arguments.Add('--config')
        $arguments.Add($env:DISTROPLANE_CONFIG)
        if ($env:DISTROPLANE_STATE_DIR.Trim()) {
            $arguments.Add('--state-dir')
            $arguments.Add($env:DISTROPLANE_STATE_DIR)
        }
    }
    'apply' {
        if (-not $env:DISTROPLANE_PLAN.Trim() -or -not $env:DISTROPLANE_JOURNAL.Trim()) {
            throw 'apply requires plan and journal inputs.'
        }
        $arguments.Add('--config')
        $arguments.Add($env:DISTROPLANE_CONFIG)
        $arguments.Add('--plan')
        $arguments.Add($env:DISTROPLANE_PLAN)
        $arguments.Add('--journal')
        $arguments.Add($env:DISTROPLANE_JOURNAL)
        if ($env:DISTROPLANE_RUN_ID.Trim()) {
            $arguments.Add('--run')
            $arguments.Add($env:DISTROPLANE_RUN_ID)
        }
        Add-MultilineFlags -Arguments $arguments -Raw $env:DISTROPLANE_CREDENTIAL_MAPPINGS -Flag '--credential' -FormatName 'credential-mappings'
    }
    'reconcile' {
        if (-not $env:DISTROPLANE_PLAN.Trim() -or -not $env:DISTROPLANE_JOURNAL.Trim()) {
            throw 'reconcile requires plan and journal inputs.'
        }
        $arguments.Add('--config')
        $arguments.Add($env:DISTROPLANE_CONFIG)
        $arguments.Add('--plan')
        $arguments.Add($env:DISTROPLANE_PLAN)
        $arguments.Add('--journal')
        $arguments.Add($env:DISTROPLANE_JOURNAL)
        Add-MultilineFlags -Arguments $arguments -Raw $env:DISTROPLANE_CREDENTIAL_MAPPINGS -Flag '--credential' -FormatName 'credential-mappings'
    }
}
$arguments.Add('--json')

$result = Invoke-Distroplane -Binary $binary -Arguments $arguments
Set-ActionOutput -Name 'exit-code' -Value ([string]$result.ExitCode)
Set-ActionOutput -Name 'pending' -Value (($result.ExitCode -eq 4).ToString().ToLowerInvariant())
Set-ActionOutput -Name 'result-json' -Value $result.Stdout

$planID = ''
$planPath = ''
$runID = ''
$completed = 'false'
if ($result.Stdout) {
    try {
        $parsed = $result.Stdout | ConvertFrom-Json
    } catch {
        throw "Distroplane returned non-JSON stdout while --json was requested."
    }
    if ($parsed.PSObject.Properties.Name -contains 'planId') {
        $planID = [string]$parsed.planId
    }
    if ($parsed.PSObject.Properties.Name -contains 'path') {
        $planPath = [string]$parsed.path
    }
    if ($parsed.PSObject.Properties.Name -contains 'runId') {
        $runID = [string]$parsed.runId
    }
    if ($parsed.PSObject.Properties.Name -contains 'completed') {
        $completed = ([bool]$parsed.completed).ToString().ToLowerInvariant()
    }
}
Set-ActionOutput -Name 'plan-id' -Value $planID
Set-ActionOutput -Name 'plan-path' -Value $planPath
Set-ActionOutput -Name 'run-id' -Value $runID
Set-ActionOutput -Name 'completed' -Value $completed

$uploadEvidence = Parse-BooleanInput -Name 'upload-evidence' -Value $env:DISTROPLANE_UPLOAD_EVIDENCE
$requestedEvidencePath = $env:DISTROPLANE_EVIDENCE_PATH.Trim()
$shouldGenerateEvidence = $command -in @('apply', 'reconcile') -and ($requestedEvidencePath -or $uploadEvidence)
$evidencePath = ''
$evidenceDigest = ''

if ($shouldGenerateEvidence) {
    $journalPath = Resolve-LocalPath -Path $env:DISTROPLANE_JOURNAL
    if (Test-Path -LiteralPath $journalPath -PathType Leaf) {
        if ($requestedEvidencePath) {
            $evidencePath = Resolve-LocalPath -Path $requestedEvidencePath
        } else {
            $evidencePath = Join-Path $env:RUNNER_TEMP 'distroplane-evidence/release-evidence.json'
        }

        $evidenceArguments = [System.Collections.Generic.List[string]]::new()
        $evidenceArguments.Add('evidence')
        $evidenceArguments.Add('--plan')
        $evidenceArguments.Add($env:DISTROPLANE_PLAN)
        $evidenceArguments.Add('--journal')
        $evidenceArguments.Add($env:DISTROPLANE_JOURNAL)
        $evidenceArguments.Add('--output')
        $evidenceArguments.Add($evidencePath)
        Add-MultilineFlags -Arguments $evidenceArguments -Raw $env:DISTROPLANE_ATTESTATIONS -Flag '--attestation' -FormatName 'attestations'
        $evidenceArguments.Add('--json')

        $evidenceResult = Invoke-Distroplane -Binary $binary -Arguments $evidenceArguments
        if ($evidenceResult.ExitCode -ne 0) {
            throw "Evidence export failed with exit code $($evidenceResult.ExitCode)."
        }
        if (-not $evidenceResult.Stdout) {
            throw 'Evidence export returned no JSON output.'
        }
        $evidenceSummary = $evidenceResult.Stdout | ConvertFrom-Json
        $evidenceDigest = [string]$evidenceSummary.digest
        $evidencePath = [string]$evidenceSummary.path
    } elseif ($result.ExitCode -in @(0, 4, 5, 6)) {
        throw "Distroplane returned state exit code $($result.ExitCode), but journal '$journalPath' does not exist."
    }
}

Set-ActionOutput -Name 'evidence-path' -Value $evidencePath
Set-ActionOutput -Name 'evidence-digest' -Value $evidenceDigest
