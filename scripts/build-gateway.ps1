$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$targetTriple = (& rustc --print host-tuple).Trim()
if ($LASTEXITCODE -ne 0 -or $targetTriple -ne 'x86_64-pc-windows-msvc') { throw 'M1 build currently supports Windows x64 only.' }
$binaryDir = Join-Path $projectRoot 'desktop/src-tauri/binaries'
New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null
$previousCgo = $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    Push-Location (Join-Path $projectRoot 'gateway')
    & go build -trimpath -o (Join-Path $binaryDir "open-design-gateway-$targetTriple.exe") ./cmd/open-design-gateway
    if ($LASTEXITCODE -ne 0) { throw 'Gateway build failed.' }
} finally {
    Pop-Location
    $env:CGO_ENABLED = $previousCgo
}
