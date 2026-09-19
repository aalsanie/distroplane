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

. (Join-Path $PSScriptRoot 'install.ps1')
. (Join-Path $PSScriptRoot 'oidc.ps1')

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

$oidcAvailable = Test-DistroplaneOIDC -Mode $env:DISTROPLANE_OIDC -RequestUrl $env:ACTIONS_ID_TOKEN_REQUEST_URL -RequestToken $env:ACTIONS_ID_TOKEN_REQUEST_TOKEN
Set-ActionOutput -Name 'oidc-available' -Value $oidcAvailable.ToString().ToLowerInvariant()

$command = $env:DISTROPLANE_COMMAND.Trim().ToLowerInvariant()
if ($command -notin @('plan', 'apply', 'reconcile')) {
    throw "command must be one of: plan, apply, reconcile."
}

$concurrencyText = $env:DISTROPLANE_CONCURRENCY.Trim()
$concurrency = 0
if ($concurrencyText) {
    if (-not [int]::TryParse($concurrencyText, [ref]$concurrency) -or $concurrency -lt 0) {
        throw "concurrency must be a non-negative integer."
    }
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
        $arguments.Add('--concurrency')
        $arguments.Add([string]$concurrency)
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
        $arguments.Add('--concurrency')
        $arguments.Add([string]$concurrency)
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

# Native Distroplane exit codes are interpreted by the composite action's
# enforcement step after outputs and optional evidence have been produced.
exit 0
