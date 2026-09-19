$ErrorActionPreference = 'Stop'

function Test-DistroplaneOIDC {
    param(
        [Parameter(Mandatory = $true)][string]$Mode,
        [AllowNull()][AllowEmptyString()][string]$RequestUrl = '',
        [AllowNull()][AllowEmptyString()][string]$RequestToken = ''
    )

    $normalized = $Mode.Trim().ToLowerInvariant()
    if ($normalized -notin @('none', 'required')) {
        throw "oidc must be 'none' or 'required'."
    }

    $available = [bool]($RequestUrl -and $RequestToken)
    if ($normalized -eq 'required' -and -not $available) {
        throw "OIDC is required but unavailable. Grant the job 'permissions: id-token: write' and ensure the runner supports GitHub OIDC."
    }
    return $available
}
