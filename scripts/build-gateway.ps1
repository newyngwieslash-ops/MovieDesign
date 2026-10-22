$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$prompt = [IO.File]::ReadAllText((Join-Path $projectRoot 'agent/media-agent.md')).Replace("`r`n", "`n").Replace("`r", "`n")
$literal = ConvertTo-Json -InputObject $prompt -Compress
[IO.File]::WriteAllText((Join-Path $projectRoot 'gateway/internal/agent/media_prompt.go'), "// Code generated from agent/media-agent.md by build-gateway.ps1; DO NOT EDIT.`npackage agent`nconst mediaPrompt = $literal`n", [Text.UTF8Encoding]::new($false))
$targetTriple = (& rustc --print host-tuple).Trim()
$skillRoot = Join-Path $projectRoot 'agent/skills'
$skillLines = @('// Code generated from agent/skills by build-gateway.ps1; DO NOT EDIT.', 'package agent', 'var bundledSkills = map[string]string{')
foreach ($file in Get-ChildItem -LiteralPath $skillRoot -Recurse -File | Sort-Object FullName) {
    $relative = $file.FullName.Substring($skillRoot.Length + 1).Replace('\', '/')
    $key = ConvertTo-Json -InputObject $relative -Compress
    $value = ConvertTo-Json -InputObject ([IO.File]::ReadAllText($file.FullName)).Replace("`r`n", "`n").Replace("`r", "`n") -Compress
    $skillLines += "${key}: $value,"
}
$skillLines += '}'
[IO.File]::WriteAllText((Join-Path $projectRoot 'gateway/internal/agent/skills_bundle.go'), ($skillLines -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
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
