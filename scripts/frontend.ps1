param([ValidateSet('dev', 'build')][string]$Mode = 'build')
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'build-opencode.ps1')
& (Join-Path $PSScriptRoot 'build-gateway.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Gateway preparation failed.' }
Push-Location (Join-Path (Split-Path $PSScriptRoot -Parent) 'desktop')
try {
    & pnpm $Mode
    if ($LASTEXITCODE -ne 0) { throw "Frontend $Mode failed." }
} finally { Pop-Location }
