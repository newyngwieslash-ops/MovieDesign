$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
& powershell -NoProfile -File (Join-Path $PSScriptRoot 'check-desktop-shutdown.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Desktop shutdown evidence checks failed.' }
& powershell -NoProfile -File (Join-Path $PSScriptRoot 'check-restart.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Launcher ownership/recovery checks failed.' }
& powershell -NoProfile -File (Join-Path $PSScriptRoot 'check-release.ps1')
if ($LASTEXITCODE -ne 0) { throw 'Release version/signature guard checks failed.' }
& (Join-Path $PSScriptRoot 'build-media-tools.ps1')
$env:MOVIEDESIGN_FFPROBE_TEST_BINARY = Join-Path $projectRoot 'desktop/src-tauri/binaries/media/ffprobe.exe'
Push-Location (Join-Path $projectRoot 'gateway')
try {
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go vet failed.' }
} finally { Pop-Location }
& (Join-Path $PSScriptRoot 'build-gateway.ps1')
& (Join-Path $PSScriptRoot 'build-opencode.ps1')
& node --test (Join-Path $PSScriptRoot 'check-m2-recovery.mjs') (Join-Path $PSScriptRoot 'check-m2-ui.mjs') (Join-Path $PSScriptRoot 'check-editor-lifecycle.mjs') (Join-Path $PSScriptRoot 'check-canvas-recovery.mjs') (Join-Path $PSScriptRoot 'check-task-ui.mjs') (Join-Path $PSScriptRoot 'check-chat-ui.mjs') (Join-Path $PSScriptRoot 'check-storyboard-ui.mjs') (Join-Path $PSScriptRoot 'check-update-ui.mjs')
if ($LASTEXITCODE -ne 0) { throw 'M2 import recovery checks failed.' }
Push-Location (Join-Path $projectRoot 'desktop')
try {
    & node --experimental-strip-types --test packages/editor/test/*.test.ts src/features/editor/annotations.test.ts src/features/canvas/canvas.test.ts src/features/providers/recovery.test.ts src/features/tasks/tasks.test.ts src/events.test.ts src/features/questions/pending.test.ts src/features/chat/messages.test.ts
    if ($LASTEXITCODE -ne 0) { throw 'Editor/canvas/provider recovery checks failed.' }
    & pnpm build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
    & cargo test --manifest-path src-tauri/Cargo.toml
    if ($LASTEXITCODE -ne 0) { throw 'Rust tests failed.' }
} finally { Pop-Location }
& (Join-Path $PSScriptRoot 'smoke-m1.ps1')
& (Join-Path $PSScriptRoot 'verify-lineage.ps1')
& (Join-Path $PSScriptRoot 'release.ps1') -DryRun
