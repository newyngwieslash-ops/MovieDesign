param([ValidateSet('debug', 'release')][string]$Mode = 'release', [ValidateSet('m1', 'm2', 'editor', 'editor-product', 'canvas', 'providers', 'questions', 'generation', 'agent', 'agent-prepare', 'chat', 'chat-fix', 'tools', 'skills', 'storyboard', 'pipeline')][string]$Smoke = 'm1')
$ErrorActionPreference = 'Stop'
if ($Smoke -eq 'editor' -and $Mode -ne 'debug') { throw 'Editor Canvas harness requires -Mode debug and the existing Vite dev server.' }
$projectRoot = Split-Path $PSScriptRoot -Parent
$binary = Join-Path $projectRoot "desktop/src-tauri/target/$Mode/moviedesign.exe"
$runDir = Join-Path $projectRoot ('.runtime/desktop-' + $Mode + '-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
$portOwners = Get-NetTCPConnection -LocalPort 8001,4096 -ErrorAction SilentlyContinue | Where-Object State -In @('Listen', 'Bound', 'Established', 'SynSent', 'SynReceived', 'CloseWait')
if ($portOwners) { throw ('Port 8001 or 4096 is occupied (including outbound bindings); no existing process will be stopped. PID: ' + (($portOwners.OwningProcess | Sort-Object -Unique) -join ', ')) }
$debugListener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
$debugListener.Start()
$debugPort = $debugListener.LocalEndpoint.Port
$debugListener.Stop()
$info = New-Object System.Diagnostics.ProcessStartInfo
$info.FileName = $binary
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.EnvironmentVariables['MOVIEDESIGN_DATA_DIR'] = $runDir
$profile = Join-Path $runDir 'webview-profile'
$info.EnvironmentVariables['WEBVIEW2_USER_DATA_FOLDER'] = $profile
$info.EnvironmentVariables['WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS'] = "--remote-debugging-port=$debugPort --remote-debugging-address=127.0.0.1"
$origin = if ($Mode -eq 'debug') { 'http://localhost:1420' } else { 'http://tauri.localhost' }
function Stop-OwnedDesktop($owned) {
    $gateway = Get-CimInstance Win32_Process -Filter "ParentProcessId=$($owned.Id)" | Where-Object Name -eq 'open-design-gateway.exe'
    if ($null -eq $gateway) { throw 'Owned gateway process not found.' }
    $agent = Get-CimInstance Win32_Process -Filter "ParentProcessId=$($owned.Id)" | Where-Object Name -eq 'moviedesign-opencode.exe'
    if ($null -eq $agent) { throw 'Owned opencode process not found.' }
    if (-not $owned.CloseMainWindow()) { throw 'Could not close main window normally.' }
    if (-not $owned.WaitForExit(10000)) { throw 'Desktop did not exit normally.' }
    if (Get-Process -Id $gateway.ProcessId -ErrorAction SilentlyContinue) { throw 'Gateway survived desktop shutdown.' }
    if (Get-Process -Id $agent.ProcessId -ErrorAction SilentlyContinue) { throw 'OpenCode survived desktop shutdown.' }
    if (Get-NetTCPConnection -State Listen -LocalPort 8001 -ErrorAction SilentlyContinue) { throw 'Gateway port was not released.' }
    if (Get-NetTCPConnection -State Listen -LocalPort 4096 -ErrorAction SilentlyContinue) { throw 'OpenCode port was not released.' }
}
$unrelatedPrepare = $null
if ($Smoke -eq 'agent-prepare') {
    Set-Content -LiteralPath (Join-Path $runDir 'agent-host') -Value 'controlled unusable agent profile'
    $unrelatedPrepare = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 4096)
    $unrelatedPrepare.Start()
}
$process = [System.Diagnostics.Process]::Start($info)
try {
    if ($Smoke -eq 'agent-prepare') {
        & node (Join-Path $PSScriptRoot 'smoke-agent-prepare.mjs') $debugPort $runDir $origin
        if ($LASTEXITCODE -ne 0) { throw 'Agent preparation failure isolation smoke failed.' }
        $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
        if ($children.Count -ne 1 -or $children[0].Name -ne 'open-design-gateway.exe') { throw 'Preparation failure did not prevent model child launch.' }
        if ($unrelatedPrepare.Pending()) { throw 'Preparation failure contacted unrelated 4096 listener.' }
        if (-not $process.CloseMainWindow() -or -not $process.WaitForExit(10000)) { throw 'Preparation-failure desktop did not exit normally.' }
        if (Get-Process -Id $children[0].ProcessId -ErrorAction SilentlyContinue) { throw 'Owned gateway survived.' }
        if (-not $unrelatedPrepare.Server.IsBound) { throw 'Unrelated listener was stopped.' }
        Write-Output "Agent preparation failure isolated; gateway health/M4 reads available; no model child/contact; normal exit: $runDir"
        return
    }
    $scriptName = if ($Smoke -eq 'pipeline') { 'smoke-pipeline.mjs' } elseif ($Smoke -eq 'storyboard') { 'smoke-storyboard.mjs' } elseif ($Smoke -eq 'skills') { 'smoke-skills.mjs' } elseif ($Smoke -eq 'tools') { 'smoke-tools.mjs' } elseif ($Smoke -in @('chat', 'chat-fix', 'skills', 'storyboard')) { 'smoke-chat.mjs' } elseif ($Smoke -eq 'agent') { 'smoke-agent.mjs' } elseif ($Smoke -eq 'generation') { 'smoke-generation.mjs' } elseif ($Smoke -eq 'questions') { 'smoke-questions.mjs' } elseif ($Smoke -eq 'providers') { 'smoke-providers.mjs' } elseif ($Smoke -eq 'canvas') { 'smoke-canvas.mjs' } elseif ($Smoke -in @('editor', 'editor-product')) { 'smoke-editor.mjs' } elseif ($Smoke -eq 'm2') { 'smoke-m2.mjs' } else { 'smoke-desktop.mjs' }
    $phase = if ($Smoke -eq 'editor-product') { 'product' } else { 'initial' }
    & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
    if ($LASTEXITCODE -ne 0) { throw 'WebView smoke failed.' }
    $browser = Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object { $_.CommandLine -like "*$profile*" }
    if (-not $browser) { throw 'Test-only WebView profile override was not observed in browser command line.' }
    $browser | Select-Object ProcessId, ParentProcessId, CommandLine | ConvertTo-Json -Depth 3 | Set-Content (Join-Path $runDir 'webview-profile.json')
    Stop-OwnedDesktop $process
    if ($Smoke -in @('m2', 'editor-product', 'canvas', 'providers', 'questions', 'generation', 'agent', 'chat', 'chat-fix', 'tools', 'skills', 'storyboard', 'pipeline')) {
        $process.Dispose()
        $process = [System.Diagnostics.Process]::Start($info)
        $phase = if ($Smoke -eq 'editor-product') { 'product-recovery' } elseif ($Smoke -eq 'chat-fix') { 'fix' } else { 'recovery' }
        & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
        if ($LASTEXITCODE -ne 0) { throw 'WebView restart recovery failed.' }
        Stop-OwnedDesktop $process
    }
    if ($Smoke -in @('chat', 'chat-fix', 'skills', 'storyboard', 'pipeline')) {
        $process.Dispose()
        $process = [System.Diagnostics.Process]::Start($info)
        $phase = if ($Smoke -eq 'chat-fix') { 'fix-restart' } else { 'restart' }
        & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
        if ($LASTEXITCODE -ne 0) { throw 'Chat physical restart recovery failed.' }
        Stop-OwnedDesktop $process
    }
    if ($Smoke -eq 'agent') {
        $unrelated = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 4096)
        $unrelated.Start()
        try {
            $process.Dispose()
            $process = [System.Diagnostics.Process]::Start($info)
            $status = $null
            for ($attempt = 0; $attempt -lt 100; $attempt++) {
                try { $status = Invoke-RestMethod 'http://127.0.0.1:8001/api/agent/status' -Headers @{ Origin = $origin }; if ($status.message -match '4096') { break } } catch { }
                Start-Sleep -Milliseconds 200
            }
            if (-not $status -or $status.host_ready -or $status.message -notmatch '4096') { throw 'Agent port conflict was not reported independently from gateway health.' }
            if ($unrelated.Pending()) { throw 'Application contacted unrelated 4096 listener.' }
            $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
            if ($children.Count -ne 1 -or $children[0].Name -ne 'open-design-gateway.exe') { throw 'Conflict did not prevent model child launch.' }
            if (-not $process.CloseMainWindow() -or -not $process.WaitForExit(10000)) { throw 'Conflict-mode desktop did not exit normally.' }
            if (Get-Process -Id $children[0].ProcessId -ErrorAction SilentlyContinue) { throw 'Conflict-mode owned gateway survived.' }
            if (-not $unrelated.Server.IsBound) { throw 'Unrelated listener was stopped.' }
            $status | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $runDir 'agent-port-conflict.json')
        } finally { $unrelated.Stop() }
    }
    Write-Output "Desktop lifecycle passed: $Mode $Smoke; no surviving owned gateway or opencode. Evidence: $runDir"
} finally {
    if (-not $process.HasExited) {
        $ownedChildren = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
        if ($process.CloseMainWindow()) { $null = $process.WaitForExit(10000) }
        if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
        foreach ($ownedChild in $ownedChildren) { $current = Get-CimInstance Win32_Process -Filter "ProcessId=$($ownedChild.ProcessId)"; if ($current -and $current.ParentProcessId -eq $process.Id -and $current.CreationDate -eq $ownedChild.CreationDate) { Stop-Process -Id $ownedChild.ProcessId } }
    }
    $process.Dispose()
    if ($unrelatedPrepare) { $unrelatedPrepare.Stop() }
}
