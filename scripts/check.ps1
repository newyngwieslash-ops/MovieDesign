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
& (Join-Path $PSScriptRoot 'build-opencode.ps1')
& node --test (Join-Path $PSScriptRoot 'check-m2-recovery.mjs') (Join-Path $PSScriptRoot 'check-m2-ui.mjs') (Join-Path $PSScriptRoot 'check-editor-lifecycle.mjs') (Join-Path $PSScriptRoot 'check-canvas-recovery.mjs') (Join-Path $PSScriptRoot 'check-task-ui.mjs') (Join-Path $PSScriptRoot 'check-chat-ui.mjs')
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
