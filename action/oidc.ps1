$ErrorActionPreference = 'Stop'

function Test-DistroplaneOIDC {
    param(
        [Parameter(Mandatory = $true)][string]$Mode,
        [AllowNull()][AllowEmptyString()][string]$RequestUrl = '',
        [AllowNull()][AllowEmptyString()][string]$RequestToken = ''
    )
    $normalized = $Mode.Trim().ToLowerInvariant()
    if ($normalized -notin @('none', 'required', 'npm')) {
        throw "oidc must be 'none', 'required', or 'npm'."
    }
    $available = [bool]($RequestUrl -and $RequestToken)
    if ($normalized -ne 'none' -and -not $available) {
        throw "OIDC is required but unavailable. Grant the job 'permissions: id-token: write' on a GitHub-hosted runner."
    }
    return $available
}

# Only the selected npm trusted-publishing destination may receive the minted ID
# token. Neither a generic provider nor a token-mode npm destination qualifies.
function Assert-DistroplaneNpmOIDCBinding {
    param(
        [Parameter(Mandatory = $true)][string]$ConfigPath,
        [Parameter(Mandatory = $true)][string]$CredentialRef,
        [AllowEmptyString()][string]$Mappings = ''
    )
    if ($CredentialRef -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$') {
        throw 'npm-oidc-ref must identify a valid credential reference.'
    }
    if (-not (Test-Path -LiteralPath $ConfigPath -PathType Leaf)) {
        throw 'npm OIDC configuration file was not found.'
    }
    $config = Get-Content -LiteralPath $ConfigPath -Raw -ErrorAction Stop | ConvertFrom-Json -AsHashtable -ErrorAction Stop
    $matchingTargets = 0
    $allReferences = @()
    $npmTargets = @()
    if ($config.schemaVersion -eq '1') {
        foreach ($target in @($config.targets)) {
            if ($target.provider.name -eq 'npm') {
                $npmTargets += $target.configuration
            }
            if ($null -ne $target.configuration.authentication) {
                $allReferences += [string]$target.configuration.authentication.credential
            }
        }
    } elseif ($config.schemaVersion -eq '2') {
        if ($config.destinations.Contains('npm')) {
            $npmTargets = @($config.destinations.npm)
            foreach ($target in $npmTargets) {
                $targetOptions = $target.options
                if ($null -ne $targetOptions.authentication) {
                    $allReferences += [string]$targetOptions.authentication.credential
                }
            }
        }
        foreach ($provider in @($config.destinations.Keys)) {
            if ($provider -eq 'npm') { continue }
            foreach ($target in @($config.destinations[$provider])) {
                if ($null -ne $target.options.authentication) {
                    $allReferences += [string]$target.options.authentication.credential
                }
            }
        }
        if ($null -ne $config.credentialSources -and $config.credentialSources.Contains($CredentialRef)) {
            throw 'npm OIDC credential reference conflicts with configured credentialSources.'
        }
    } else {
        throw 'npm OIDC requires a supported schema v1/v2 configuration.'
    }
    foreach ($target in $npmTargets) {
        $options = if ($config.schemaVersion -eq '2') { $target.options } else { $target }
        $auth = $options.authentication
        if ($null -eq $auth -or [string]$auth.credential -cne $CredentialRef) { continue }
        if ($auth.mode -cne 'trusted-publishing') {
            throw 'npm OIDC credential reference must use trusted-publishing authentication.'
        }
        try { $registry = [Uri]([string]$options.registry) } catch { throw 'npm OIDC requires the official HTTPS npm registry.' }
        if ($registry.Scheme -cne 'https' -or $registry.Host -cne 'registry.npmjs.org' -or
            -not $registry.IsDefaultPort -or $registry.UserInfo -or $registry.Query -or $registry.Fragment -or
            $registry.AbsolutePath -notin @('', '/')) {
            throw 'npm OIDC requires the official HTTPS npm registry.'
        }
        $matchingTargets++
    }
    if ($matchingTargets -ne 1 -or @($allReferences | Where-Object { $_ -ceq $CredentialRef }).Count -ne 1) {
        throw 'npm OIDC requires exactly one matching npm trusted-publishing destination and an exclusive credential reference.'
    }
    foreach ($line in ($Mappings -split "\r?\n")) {
        $pair = $line.Trim()
        if (-not $pair) { continue }
        if ($pair -notmatch '^([^=\s]+)=([A-Za-z_][A-Za-z0-9_]*)$') {
            throw 'credential-mappings must contain valid REF=ENV entries.'
        }
        if ($Matches[1] -ceq $CredentialRef -or $Matches[2] -ceq 'DISTROPLANE_ACTION_NPM_OIDC_TOKEN') {
            throw 'npm OIDC reference or environment conflicts with explicit credential-mappings.'
        }
    }
}

# The request bearer is used ONLY with the GitHub OIDC endpoint; the returned
# npm-audience JWT is passed to the CLI through a transient environment source.
function Get-DistroplaneNpmOIDCToken {
    param(
        [Parameter(Mandatory = $true)][string]$RequestUrl,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RequestToken,
        [scriptblock]$Requester = {
            param([string]$Uri, [string]$Bearer)
            Invoke-RestMethod -Method Get -Uri $Uri -Headers @{ Authorization = "Bearer $Bearer"; Accept = 'application/json' } -TimeoutSec 15 -MaximumRedirection 0 -ErrorAction Stop
        }
    )
    if (-not $RequestToken.Trim()) { throw 'GitHub OIDC request permission is unavailable (id-token: write required).' }
    try {
        $uri = [Uri]$RequestUrl
        if ($uri.Scheme -cne 'https' -or $uri.Host -notmatch '(^|\.)actions\.githubusercontent\.com$' -or
            -not $uri.IsDefaultPort -or $uri.UserInfo -or $uri.Fragment -or
            $uri.Query -match '(^|[?&])audience=') {
            throw 'bad URI'
        }
        $separator = if ($uri.Query) { '&' } else { '?' }
        $requestUri = $uri.AbsoluteUri + $separator + 'audience=' + [Uri]::EscapeDataString('npm:registry.npmjs.org')
    } catch {
        throw 'GitHub OIDC request endpoint is invalid.'
    }
    try {
        $response = & $Requester $requestUri $RequestToken
    } catch {
        throw 'GitHub OIDC token request failed; check id-token: write permission and runner connectivity.'
    }
    $jwt = [string]$response.value
    $segments = $jwt.Split('.')
    if ($segments.Count -ne 3 -or $jwt.Length -gt 16384 -or $jwt.Length -lt 20) {
        throw 'GitHub OIDC returned an invalid ID token.'
    }
    try {
        $encoded = $segments[1].Replace('-', '+').Replace('_', '/')
        $encoded = $encoded.PadRight($encoded.Length + ((4 - $encoded.Length % 4) % 4), '=')
        $claims = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($encoded)) | ConvertFrom-Json
        if ($claims.iss -cne 'https://token.actions.githubusercontent.com' -or
            $claims.aud -isnot [string] -or $claims.aud -cne 'npm:registry.npmjs.org' -or
            [long]$claims.exp -le [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()) {
            throw 'bad claims'
        }
    } catch {
        throw 'GitHub OIDC returned an ID token with invalid issuer, audience, or expiration.'
    }
    return $jwt
}
