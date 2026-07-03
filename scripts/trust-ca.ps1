<#
.SYNOPSIS
    Trust wha's reverse-proxy internal CA root on Windows.

.DESCRIPTION
    Phones can't be scripted (a website/script cannot silently install a root
    CA into a mobile trust store); use the /setup page's per-device steps for
    those. This script is for Windows desktops only. Run it from an elevated
    (Administrator) PowerShell prompt.

.PARAMETER Source
    Either an https:// URL to the wha /setup/root.crt endpoint, or a local
    file path to an already-downloaded root.crt.

.EXAMPLE
    scripts/trust-ca.ps1 https://wha.local/setup/root.crt
.EXAMPLE
    scripts/trust-ca.ps1 C:\Users\me\Downloads\root.crt
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Source
)

$ErrorActionPreference = "Stop"

$currentPrincipal = New-Object Security.Principal.WindowsPrincipal(
    [Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "This script must be run as Administrator (it writes to the Local Machine trust store)."
    exit 1
}

$certPath = $Source
$tempFile = $null
try {
    if ($Source -match '^https?://') {
        $tempFile = Join-Path $env:TEMP "wha-root-ca.crt"
        Write-Host "Downloading $Source ..."
        # The proxy's cert is Caddy's own internal CA, not yet trusted — skip
        # verification for this one-time bootstrap fetch only.
        Invoke-WebRequest -Uri $Source -OutFile $tempFile -SkipCertificateCheck
        $certPath = $tempFile
    }

    if (-not (Test-Path $certPath)) {
        Write-Error "Certificate not found: $certPath"
        exit 1
    }

    Write-Host "Importing $certPath into Cert:\LocalMachine\Root ..."
    Import-Certificate -FilePath $certPath -CertStoreLocation Cert:\LocalMachine\Root | Out-Null
    Write-Host "Done. Restart your browser to pick up the new trust."
}
finally {
    if ($tempFile -and (Test-Path $tempFile)) {
        Remove-Item $tempFile -Force
    }
}
