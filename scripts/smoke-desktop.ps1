param([ValidateSet('debug', 'release')][string]$Mode = 'release', [ValidateSet('m1', 'm2', 'editor', 'editor-product')][string]$Smoke = 'm1')
$ErrorActionPreference = 'Stop'
if ($Smoke -eq 'editor' -and $Mode -ne 'debug') { throw 'Editor Canvas harness requires -Mode debug and the existing Vite dev server.' }
$projectRoot = Split-Path $PSScriptRoot -Parent
$binary = Join-Path $projectRoot "desktop/src-tauri/target/$Mode/moviedesign.exe"
$runDir = Join-Path $projectRoot ('.runtime/desktop-' + $Mode + '-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
if (Get-NetTCPConnection -State Listen -LocalPort 8001 -ErrorAction SilentlyContinue) { throw 'Port 8001 is occupied; no existing process will be stopped.' }
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
    if (-not $owned.CloseMainWindow()) { throw 'Could not close main window normally.' }
    if (-not $owned.WaitForExit(10000)) { throw 'Desktop did not exit normally.' }
    if (Get-Process -Id $gateway.ProcessId -ErrorAction SilentlyContinue) { throw 'Gateway survived desktop shutdown.' }
    if (Get-NetTCPConnection -State Listen -LocalPort 8001 -ErrorAction SilentlyContinue) { throw 'Gateway port was not released.' }
}
$process = [System.Diagnostics.Process]::Start($info)
try {
    $scriptName = if ($Smoke -in @('editor', 'editor-product')) { 'smoke-editor.mjs' } elseif ($Smoke -eq 'm2') { 'smoke-m2.mjs' } else { 'smoke-desktop.mjs' }
    $phase = if ($Smoke -eq 'editor-product') { 'product' } else { 'initial' }
    & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
    if ($LASTEXITCODE -ne 0) { throw 'WebView smoke failed.' }
    $browser = Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object { $_.CommandLine -like "*$profile*" }
    if (-not $browser) { throw 'Test-only WebView profile override was not observed in browser command line.' }
    $browser | Select-Object ProcessId, ParentProcessId, CommandLine | ConvertTo-Json -Depth 3 | Set-Content (Join-Path $runDir 'webview-profile.json')
    Stop-OwnedDesktop $process
    if ($Smoke -in @('m2', 'editor-product')) {
        $process.Dispose()
        $process = [System.Diagnostics.Process]::Start($info)
        $phase = if ($Smoke -eq 'editor-product') { 'product-recovery' } else { 'recovery' }
        & node (Join-Path $PSScriptRoot $scriptName) $debugPort $runDir $origin $phase
        if ($LASTEXITCODE -ne 0) { throw 'WebView restart recovery failed.' }
        Stop-OwnedDesktop $process
    }
    Write-Output "Desktop lifecycle passed: $Mode $Smoke; no surviving gateway. Evidence: $runDir"
} finally {
    if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
    $process.Dispose()
}
