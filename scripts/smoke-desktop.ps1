param([ValidateSet('debug', 'release')][string]$Mode = 'release', [ValidateSet('m1', 'm2', 'editor', 'editor-product', 'canvas', 'providers', 'questions', 'generation', 'agent', 'agent-prepare', 'chat', 'chat-fix', 'tools', 'skills', 'storyboard', 'pipeline', 'video', 'interruptions', 'updates')][string]$Smoke = 'm1')
$ErrorActionPreference = 'Stop'
if ($Smoke -eq 'editor' -and $Mode -ne 'debug') { throw 'Editor Canvas harness requires -Mode debug and the existing Vite dev server.' }
$projectRoot = Split-Path $PSScriptRoot -Parent
$binary = Join-Path $projectRoot "desktop/src-tauri/target/$Mode/moviedesign.exe"
$runDir = Join-Path $projectRoot ('.runtime/desktop-' + $Mode + '-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
$portOwners = Get-NetTCPConnection -LocalPort 8001,4096 -ErrorAction SilentlyContinue | Where-Object { $_.State -In @('Listen', 'Bound', 'Established', 'SynSent', 'SynReceived', 'CloseWait') -and ($_.State -eq 'Listen' -or (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue)) }
if ($portOwners) { throw ('Port 8001 or 4096 is occupied (including outbound bindings); no existing process will be stopped. PID: ' + (($portOwners.OwningProcess | Sort-Object -Unique) -join ', ')) }
$debugListener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 0)
$debugListener.Start()
$debugPort = $debugListener.LocalEndpoint.Port
$debugListener.Stop()
$info = New-Object System.Diagnostics.ProcessStartInfo
$info.FileName = $binary
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.RedirectStandardError = $true
$info.EnvironmentVariables['MOVIEDESIGN_DATA_DIR'] = $runDir
if ($Smoke -eq 'updates') {
    # Controlled policy cache only. Production's signed manifest/package endpoint
    # stays native-owned; this smoke never installs an application update.
    $info.EnvironmentVariables['MOVIEDESIGN_UPDATE_POLICY_URL'] = 'http://127.0.0.1:9/unavailable-policy'
    $cache = @{ policy = @{ schema_version = 1; enabled = $true; min_supported_version = '2.0.0'; manual_download_url = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest'; manual_only = @{ platforms = @('win32'); max_version_exclusive = '2.0.0'; reason = @{ zh = 'Controlled offline update policy'; en = 'Controlled offline update policy' } } }; cachedAt = [DateTime]::UtcNow.ToString('o'); metadata = @{ region = 'global'; channel = 'stable'; platform = 'win32' } } | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText((Join-Path $runDir 'update-policy-cache.json'), $cache, (New-Object System.Text.UTF8Encoding($false)))
}
$profile = Join-Path $runDir 'webview-profile'
$info.EnvironmentVariables['WEBVIEW2_USER_DATA_FOLDER'] = $profile
$info.EnvironmentVariables['WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS'] = "--remote-debugging-port=$debugPort --remote-debugging-address=127.0.0.1"
$origin = if ($Mode -eq 'debug') { 'http://localhost:1420' } else { 'http://tauri.localhost' }
function Write-DesktopTrace($owned, [string]$stage, $details) {
    try {
        $record = @{ atUtc = [DateTimeOffset]::UtcNow.ToString('o'); processId = $owned.Id; stage = $stage; details = $details } | ConvertTo-Json -Depth 6 -Compress
        [IO.File]::AppendAllText((Join-Path $runDir 'desktop-lifecycle.jsonl'), $record + [Environment]::NewLine, (New-Object Text.UTF8Encoding($false)))
    } catch { Write-Warning ('Desktop evidence write failed: ' + $_.Exception.GetType().Name) }
}
function Start-OwnedDesktop {
    $stderrPath = Join-Path $runDir ('native-stderr-' + [guid]::NewGuid().ToString('N') + '.log')
    $sink = [IO.FileStream]::new($stderrPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::ReadWrite, 1, $true)
    $owned = $null
    try {
        $owned = [Diagnostics.Process]::Start($info)
        $copy = $owned.StandardError.BaseStream.CopyToAsync($sink)
        # A CLR delegate observes a delayed pipe fault without requiring a PowerShell runspace.
        $observeError = [Delegate]::CreateDelegate([Func[Threading.Tasks.Task, AggregateException]], [Threading.Tasks.Task].GetProperty('Exception').GetGetMethod())
        $observation = $copy.ContinueWith($observeError, [Threading.Tasks.TaskContinuationOptions]::OnlyOnFaulted)
        $owned | Add-Member -NotePropertyMembers @{ NativeStderrCopy = $copy; NativeStderrSink = $sink; NativeStderrObservation = $observation }
        Write-DesktopTrace $owned 'started' @{ stderr = [IO.Path]::GetFileName($stderrPath) }
        return $owned
    } catch {
        if ($owned) { if (-not $owned.HasExited) { $owned.Kill(); $null = $owned.WaitForExit(5000) }; $owned.Dispose() }
        $sink.Dispose()
        throw
    }
}
function Dispose-OwnedDesktop($owned) {
    try {
        try { Write-DesktopTrace $owned 'stderr_capture' @{ complete = $owned.NativeStderrCopy.Wait(500) } }
        catch { Write-DesktopTrace $owned 'stderr_capture_failed' @{ errorType = $_.Exception.GetType().Name } }
    } finally {
        # Do not wait for EOF from a descendant that inherited the stderr pipe.
        try { $owned.StandardError.Dispose() }
        catch { Write-DesktopTrace $owned 'stderr_capture_failed' @{ part = 'reader'; errorType = $_.Exception.GetType().Name } }
        finally {
            try { $owned.NativeStderrSink.Dispose() }
            catch { Write-DesktopTrace $owned 'stderr_capture_failed' @{ part = 'sink'; errorType = $_.Exception.GetType().Name } }
            finally { $owned.Dispose() }
        }
    }
}
function Request-OwnedDesktopClose($owned) {
    Write-DesktopTrace $owned 'close_requested' @{}
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $posted = $owned.CloseMainWindow()
    Write-DesktopTrace $owned 'close_returned' @{ posted = $posted; elapsedMs = $timer.ElapsedMilliseconds }
    $timer.Restart()
    $exited = $false
    if ($posted) { $exited = $owned.WaitForExit(10000) }
    $timer.Stop()
    $stderrBytes = $null
    try { $stderrBytes = $owned.NativeStderrSink.get_Length() }
    catch { Write-DesktopTrace $owned 'stderr_snapshot_failed' @{ errorType = $_.Exception.GetType().Name } }
    Write-DesktopTrace $owned 'wait_returned' @{ exited = $exited; elapsedMs = $timer.ElapsedMilliseconds; stderrBytes = $stderrBytes }
    if ($posted -and -not $exited) {
        try {
            $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($owned.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe') | Select-Object ProcessId, ParentProcessId, Name, CreationDate)
            Write-DesktopTrace $owned 'timeout_children' @{ children = $children }
        } catch { Write-DesktopTrace $owned 'timeout_snapshot_failed' @{ errorType = $_.Exception.GetType().Name } }
    }
    return @{ posted = $posted; exited = $exited }
}
function Stop-OwnedDesktop($owned) {
    $gateway = Get-CimInstance Win32_Process -Filter "ParentProcessId=$($owned.Id)" | Where-Object Name -eq 'open-design-gateway.exe'
    if ($null -eq $gateway) { throw 'Owned gateway process not found.' }
    $agent = Get-CimInstance Win32_Process -Filter "ParentProcessId=$($owned.Id)" | Where-Object Name -eq 'moviedesign-opencode.exe'
    if ($null -eq $agent) { throw 'Owned opencode process not found.' }
    $result = Request-OwnedDesktopClose $owned
    if (-not $result.posted) { throw 'Could not close main window normally.' }
    if (-not $result.exited) { throw 'Desktop did not exit normally.' }
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
$process = Start-OwnedDesktop
try {
    if ($Smoke -eq 'agent-prepare') {
        & node (Join-Path $PSScriptRoot 'smoke-agent-prepare.mjs') $debugPort $runDir $origin
        if ($LASTEXITCODE -ne 0) { throw 'Agent preparation failure isolation smoke failed.' }
        $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
        if ($children.Count -ne 1 -or $children[0].Name -ne 'open-design-gateway.exe') { throw 'Preparation failure did not prevent model child launch.' }
        if ($unrelatedPrepare.Pending()) { throw 'Preparation failure contacted unrelated 4096 listener.' }
        $result = Request-OwnedDesktopClose $process
        if (-not $result.exited) { throw 'Preparation-failure desktop did not exit normally.' }
        if (Get-Process -Id $children[0].ProcessId -ErrorAction SilentlyContinue) { throw 'Owned gateway survived.' }
        if (-not $unrelatedPrepare.Server.IsBound) { throw 'Unrelated listener was stopped.' }
        Write-Output "Agent preparation failure isolated; gateway health/M4 reads available; no model child/contact; normal exit: $runDir"
        return
    }
    $scriptName = if ($Smoke -eq 'updates') { 'smoke-updates.mjs' } elseif ($Smoke -eq 'interruptions') { 'smoke-interruptions.mjs' } elseif ($Smoke -eq 'video') { 'smoke-video.mjs' } elseif ($Smoke -eq 'pipeline') { 'smoke-pipeline.mjs' } elseif ($Smoke -eq 'storyboard') { 'smoke-storyboard.mjs' } elseif ($Smoke -eq 'skills') { 'smoke-skills.mjs' } elseif ($Smoke -eq 'tools') { 'smoke-tools.mjs' } elseif ($Smoke -in @('chat', 'chat-fix', 'skills', 'storyboard')) { 'smoke-chat.mjs' } elseif ($Smoke -eq 'agent') { 'smoke-agent.mjs' } elseif ($Smoke -eq 'generation') { 'smoke-generation.mjs' } elseif ($Smoke -eq 'questions') { 'smoke-questions.mjs' } elseif ($Smoke -eq 'providers') { 'smoke-providers.mjs' } elseif ($Smoke -eq 'canvas') { 'smoke-canvas.mjs' } elseif ($Smoke -in @('editor', 'editor-product')) { 'smoke-editor.mjs' } elseif ($Smoke -eq 'm2') { 'smoke-m2.mjs' } else { 'smoke-desktop.mjs' }
    $phase = if ($Smoke -eq 'editor-product') { 'product' } else { 'initial' }
    & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
    if ($LASTEXITCODE -ne 0) { throw 'WebView smoke failed.' }
    $browser = Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object { $_.CommandLine -like "*$profile*" }
    if (-not $browser) { throw 'Test-only WebView profile override was not observed in browser command line.' }
    $browser | Select-Object ProcessId, ParentProcessId, CommandLine | ConvertTo-Json -Depth 3 | Set-Content (Join-Path $runDir 'webview-profile.json')
    Stop-OwnedDesktop $process
    if ($Smoke -in @('m2', 'editor-product', 'canvas', 'providers', 'questions', 'generation', 'agent', 'chat', 'chat-fix', 'tools', 'skills', 'storyboard', 'pipeline', 'video', 'interruptions', 'updates')) {
        Dispose-OwnedDesktop $process
        $process = Start-OwnedDesktop
        $phase = if ($Smoke -eq 'editor-product') { 'product-recovery' } elseif ($Smoke -eq 'chat-fix') { 'fix' } else { 'recovery' }
        & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
        if ($LASTEXITCODE -ne 0) { throw 'WebView restart recovery failed.' }
        Stop-OwnedDesktop $process
    }
    if ($Smoke -in @('chat', 'chat-fix', 'skills', 'storyboard', 'pipeline', 'video', 'interruptions')) {
        Dispose-OwnedDesktop $process
        $process = Start-OwnedDesktop
        $phase = if ($Smoke -eq 'chat-fix') { 'fix-restart' } else { 'restart' }
        & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
        if ($LASTEXITCODE -ne 0) { throw 'Chat physical restart recovery failed.' }
        Stop-OwnedDesktop $process
    }
    if ($Smoke -eq 'agent') {
        $unrelated = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, 4096)
        $unrelated.Start()
        try {
            Dispose-OwnedDesktop $process
            $process = Start-OwnedDesktop
            $status = $null
            for ($attempt = 0; $attempt -lt 100; $attempt++) {
                try { $status = Invoke-RestMethod 'http://127.0.0.1:8001/api/agent/status' -Headers @{ Origin = $origin }; if ($status.message -match '4096') { break } } catch { }
                Start-Sleep -Milliseconds 200
            }
            if (-not $status -or $status.host_ready -or $status.message -notmatch '4096') { throw 'Agent port conflict was not reported independently from gateway health.' }
            if ($unrelated.Pending()) { throw 'Application contacted unrelated 4096 listener.' }
            $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
            if ($children.Count -ne 1 -or $children[0].Name -ne 'open-design-gateway.exe') { throw 'Conflict did not prevent model child launch.' }
            $result = Request-OwnedDesktopClose $process
            if (-not $result.exited) { throw 'Conflict-mode desktop did not exit normally.' }
            if (Get-Process -Id $children[0].ProcessId -ErrorAction SilentlyContinue) { throw 'Conflict-mode owned gateway survived.' }
            if (-not $unrelated.Server.IsBound) { throw 'Unrelated listener was stopped.' }
            $status | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $runDir 'agent-port-conflict.json')
        } finally { $unrelated.Stop() }
    }
    Write-Output "Desktop lifecycle passed: $Mode $Smoke; no surviving owned gateway or opencode. Evidence: $runDir"
} finally {
    if (-not $process.HasExited) {
        Write-DesktopTrace $process 'cleanup_started' @{}
        $ownedChildren = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -In @('open-design-gateway.exe','moviedesign-opencode.exe'))
        $null = Request-OwnedDesktopClose $process
        if (-not $process.HasExited) { Write-DesktopTrace $process 'cleanup_force_desktop' @{}; $process.Kill(); $process.WaitForExit() }
        foreach ($ownedChild in $ownedChildren) { $current = Get-CimInstance Win32_Process -Filter "ProcessId=$($ownedChild.ProcessId)"; if ($current -and $current.ParentProcessId -eq $process.Id -and $current.CreationDate -eq $ownedChild.CreationDate) { Write-DesktopTrace $process 'cleanup_force_child' @{ processId = $ownedChild.ProcessId; name = $ownedChild.Name; createdAt = $ownedChild.CreationDate }; Stop-Process -Id $ownedChild.ProcessId } }
        Write-DesktopTrace $process 'cleanup_completed' @{ exited = $process.HasExited }
    }
    Dispose-OwnedDesktop $process
    if ($unrelatedPrepare) { $unrelatedPrepare.Stop() }
}
