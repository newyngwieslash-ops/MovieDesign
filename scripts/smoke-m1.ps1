$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$binary = Join-Path $projectRoot 'desktop/src-tauri/binaries/open-design-gateway-x86_64-pc-windows-msvc.exe'
$runDir = Join-Path $projectRoot ('.runtime/smoke-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $runDir | Out-Null

function Start-Gateway([string]$DataDir, [int]$Port) {
    $info = New-Object System.Diagnostics.ProcessStartInfo
    $info.FileName = $binary
    $info.Arguments = "--data-dir `"$DataDir`" --port $Port --origin http://tauri.localhost"
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    return [System.Diagnostics.Process]::Start($info)
}

function Read-Reply($Process) {
    $pending = $Process.StandardOutput.ReadLineAsync()
    if (-not $pending.Wait(10000)) { throw 'Gateway RPC timed out.' }
    if ($null -eq $pending.Result) { throw 'Gateway exited without a reply.' }
    return ($pending.Result | ConvertFrom-Json)
}

function Send-Rpc($Process, [int]$Id, [string]$Method) {
    $Process.StandardInput.WriteLine((@{jsonrpc='2.0'; id=$Id; method=$Method} | ConvertTo-Json -Compress))
    $Process.StandardInput.Flush()
    $reply = Read-Reply $Process
    if ($reply.id -ne $Id -or $reply.jsonrpc -ne '2.0' -or $reply.error) { throw 'Invalid RPC response.' }
    return $reply.result
}

Add-Type -AssemblyName System.Net.Http
$http = New-Object System.Net.Http.HttpClient
$http.Timeout = [TimeSpan]::FromSeconds(5)
$process = $null
$conflict = $null
try {
    $process = Start-Gateway $runDir 0
    $info = Send-Rpc $process 1 'gateway.info'
    $health = Send-Rpc $process 2 'gateway.health'
    if ($health.status -ne 'ok' -or -not $health.idle -or -not $health.safe_to_suspend) { throw 'Unexpected health contract.' }
    foreach ($origin in @('http://tauri.localhost', 'https://evil.example', 'null', '')) {
        $request = New-Object System.Net.Http.HttpRequestMessage([System.Net.Http.HttpMethod]::Get, "http://127.0.0.1:$($info.port)/api/health")
        if ($origin) { $request.Headers.Add('Origin', $origin) }
        $response = $http.SendAsync($request).GetAwaiter().GetResult()
        $expected = if ($origin -eq 'http://tauri.localhost') { 200 } else { 403 }
        if ([int]$response.StatusCode -ne $expected) { throw "Unexpected Origin result for '$origin'." }
        $response.Dispose()
        $request.Dispose()
    }
    $conflict = Start-Gateway (Join-Path $runDir 'conflict') $info.port
    if (-not $conflict.WaitForExit(10000) -or $conflict.ExitCode -eq 0) { throw 'Port collision did not fail.' }
    $shutdown = Send-Rpc $process 3 'gateway.shutdown'
    if (-not $shutdown.ok -or -not $process.WaitForExit(5000) -or $process.ExitCode -ne 0) { throw 'Shutdown failed.' }
    if (-not (Test-Path -LiteralPath (Join-Path $runDir 'moviedesign.db'))) { throw 'Database was not created.' }
    $process.Dispose()
    $process = Start-Gateway $runDir 0
    $null = Send-Rpc $process 4 'gateway.info'
    $process.StandardInput.Close()
    if (-not $process.WaitForExit(5000) -or $process.ExitCode -ne 0) { throw 'Parent pipe EOF did not shut down gateway.' }
    Write-Output "M1 smoke passed: health, Origin, port collision, restart, shutdown, parent EOF. Evidence: $runDir"
} finally {
    foreach ($child in @($process, $conflict)) {
        if ($null -ne $child) { if (-not $child.HasExited) { $child.Kill(); $child.WaitForExit() }; $child.Dispose() }
    }
    $http.Dispose()
}
