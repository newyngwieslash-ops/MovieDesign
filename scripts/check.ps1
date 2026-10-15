$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
Push-Location (Join-Path $projectRoot 'gateway')
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed.' }
} finally { Pop-Location }
& (Join-Path $PSScriptRoot 'build-gateway.ps1')
Push-Location (Join-Path $projectRoot 'desktop')
try {
    & pnpm build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    & cargo test --manifest-path src-tauri/Cargo.toml
    if ($LASTEXITCODE -ne 0) { throw 'Rust tests failed.' }
} finally { Pop-Location }
& (Join-Path $PSScriptRoot 'smoke-m1.ps1')
