# Fail closed unless the release owner explicitly authorizes the Windows MSI trust mode.
[CmdletBinding()]
param([AllowEmptyString()][string]$Mode = '')
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($Mode -cnotin @('unsigned', 'pfx')) {
    throw 'Set WINDOWS_MSI_SIGNING_MODE to exactly unsigned or pfx before a stable release'
}
$cert = -not [string]::IsNullOrEmpty($env:MSI_SIGNING_PFX_BASE64)
$password = -not [string]::IsNullOrEmpty($env:MSI_SIGNING_PFX_PASSWORD)
if ($Mode -ceq 'pfx' -and (-not $cert -or -not $password)) {
    throw 'PFX mode requires both Authenticode signing secrets'
}
if ($Mode -ceq 'unsigned' -and ($cert -or $password)) {
    throw 'Unsigned mode cannot carry PFX signing credentials'
}
Write-Output $Mode
