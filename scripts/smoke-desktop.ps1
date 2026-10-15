param([ValidateSet('debug', 'release')][string]$Mode = 'release')
$ErrorActionPreference = 'Stop'
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
$info.EnvironmentVariables['WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS'] = "--remote-debugging-port=$debugPort --remote-debugging-address=127.0.0.1"
$origin = if ($Mode -eq 'debug') { 'http://localhost:1420' } else { 'http://tauri.localhost' }
$process = [System.Diagnostics.Process]::Start($info)
try {
    & node (Join-Path $PSScriptRoot 'smoke-desktop.mjs') $debugPort $runDir $origin
    if ($LASTEXITCODE -ne 0) { throw 'WebView smoke failed.' }
    $gateway = Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object Name -eq 'open-design-gateway.exe'
    if ($null -eq $gateway) { throw 'Owned gateway process not found.' }
    if (-not $process.CloseMainWindow()) { throw 'Could not close main window normally.' }
    if (-not $process.WaitForExit(10000)) { throw 'Desktop did not exit normally.' }
    if (Get-Process -Id $gateway.ProcessId -ErrorAction SilentlyContinue) { throw 'Gateway survived desktop shutdown.' }
    if (Get-NetTCPConnection -State Listen -LocalPort 8001 -ErrorAction SilentlyContinue) { throw 'Gateway port was not released.' }
    Write-Output "Desktop lifecycle passed: $Mode; no surviving gateway."
} finally {
    if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
    $process.Dispose()
}
