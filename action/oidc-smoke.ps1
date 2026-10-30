$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'oidc.ps1')

function Assert-Rejected {
    param([scriptblock]$Operation, [string]$Expected)
    $rejected = $false
    try { & $Operation | Out-Null } catch {
        $rejected = $true
        if ($_.Exception.Message -notmatch $Expected) {
            throw "Unexpected OIDC rejection: $($_.Exception.Message)"
        }
    }
    if (-not $rejected) { throw "OIDC validation unexpectedly accepted $Expected" }
}

function New-TestJWT {
    param([string]$Audience = 'npm:registry.npmjs.org', [string]$Issuer = 'https://token.actions.githubusercontent.com', [long]$Expires = 0)
    if (-not $Expires) { $Expires = [DateTimeOffset]::UtcNow.AddMinutes(5).ToUnixTimeSeconds() }
    $claims = @{ iss = $Issuer; aud = $Audience; exp = $Expires } | ConvertTo-Json -Compress
    $payload = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($claims)).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    return "eyJhbGciOiJub25lIn0.$payload.signature"
}

$root = Join-Path ([IO.Path]::GetTempPath()) ("distroplane-oidc-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $root -Force | Out-Null
try {
    $valid = @{
        schemaVersion = '1'
        release = @{ id = 'oidc-fixture'; artifacts = @(@{ name = 'pkg'; source = 'file.tgz' }) }
        targets = @(@{
            id = 'npm-release'
            provider = @{ name = 'npm' }
            configuration = @{
                registry = 'https://registry.npmjs.org/'
                authentication = @{ mode = 'trusted-publishing'; credential = 'npm-publish' }
            }
        })
    }
    $configPath = Join-Path $root 'config.json'
    $valid | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM

    if (Test-DistroplaneOIDC -Mode none -RequestUrl '' -RequestToken '') {
        throw 'OIDC none unexpectedly claimed availability'
    }
    if (-not (Test-DistroplaneOIDC -Mode required -RequestUrl 'https://example.invalid/' -RequestToken 'request-canary')) {
        throw 'OIDC required availability was not preserved'
    }
    Assert-Rejected { Test-DistroplaneOIDC -Mode npm -RequestUrl '' -RequestToken '' } 'id-token: write'
    Assert-Rejected { Test-DistroplaneOIDC -Mode 'azure' -RequestUrl '' -RequestToken '' } 'must be'
    Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish'
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'wrong' } 'exactly one matching'
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish' -Mappings 'npm-publish=NPM_TOKEN' } 'conflicts'
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish' -Mappings 'other=DISTROPLANE_ACTION_NPM_OIDC_TOKEN' } 'conflicts'
    $changed = $valid | ConvertTo-Json -Depth 12 | ConvertFrom-Json -AsHashtable
    $changed.targets[0].configuration.authentication.mode = 'token'
    $changed | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish' } 'trusted-publishing'
    $changed.targets[0].configuration.authentication.mode = 'trusted-publishing'
    $changed.targets[0].configuration.registry = 'https://other.registry.invalid/'
    $changed | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish' } 'official HTTPS npm registry'
    $v2 = @{
        schemaVersion = '2'
        release = @{ id = 'v2'; artifacts = @{ pkg = @{ source = 'file.tgz' } } }
        destinations = @{ npm = @{ artifact = 'pkg'; options = @{
            registry = 'https://registry.npmjs.org/'
            authentication = @{ mode = 'trusted-publishing'; credential = 'npm-publish' }
        } } }
    }
    $v2 | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM
    Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish'
    $v2.credentialSources = @{ 'npm-publish' = 'MY_TOKEN' }
    $v2 | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configPath -Encoding utf8NoBOM
    Assert-Rejected { Assert-DistroplaneNpmOIDCBinding -ConfigPath $configPath -CredentialRef 'npm-publish' } 'conflicts'
    $script:receivedUri = ''
    $script:receivedBearer = ''
    $fakeJwt = New-TestJWT
    $requester = {
        param([string]$Uri, [string]$Bearer)
        $script:receivedUri = $Uri
        $script:receivedBearer = $Bearer
        return @{ value = $fakeJwt }
    }
    $token = Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/token?job=1' -RequestToken 'request-canary' -Requester $requester
    if ($token -cne $fakeJwt -or $script:receivedBearer -cne 'request-canary' -or
        $script:receivedUri -cne 'https://pipelines.actions.githubusercontent.com/token?job=1&audience=npm%3Aregistry.npmjs.org') {
        throw 'OIDC request did not use the exact npm audience and request bearer'
    }
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'http://oidc.example.invalid/' -RequestToken 'request-canary' -Requester $requester } 'endpoint is invalid'
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/?audience=other' -RequestToken 'request-canary' -Requester $requester } 'endpoint is invalid'
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/' -RequestToken '' -Requester $requester } 'permission'
    $fakeJwt = New-TestJWT -Audience 'other-service'
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/' -RequestToken 'request-canary' -Requester $requester } 'issuer, audience, or expiration'
    $fakeJwt = New-TestJWT -Issuer 'https://evil.invalid'
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/' -RequestToken 'request-canary' -Requester $requester } 'issuer, audience, or expiration'
    $fakeJwt = New-TestJWT -Expires ([DateTimeOffset]::UtcNow.AddMinutes(-5).ToUnixTimeSeconds())
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/' -RequestToken 'request-canary' -Requester $requester } 'issuer, audience, or expiration'
    $deny = { param([string]$Uri, [string]$Bearer) throw "request-canary must never escape" }
    Assert-Rejected { Get-DistroplaneNpmOIDCToken -RequestUrl 'https://pipelines.actions.githubusercontent.com/' -RequestToken 'request-canary' -Requester $deny } 'request failed'
    Write-Host 'OIDC binding, audience, permission, failure, and redaction tests passed.'
} finally {
    Remove-Item -LiteralPath $root -Recurse -Force -ErrorAction SilentlyContinue
}
