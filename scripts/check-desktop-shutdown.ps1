$ErrorActionPreference = 'Stop'
function Assert($condition, [string]$message) { if (-not $condition) { throw $message } }
$runDir = Join-Path (Split-Path $PSScriptRoot -Parent) ('.runtime/h19-shutdown-check-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $runDir | Out-Null
$parseErrors = $null
$tree = [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'smoke-desktop.ps1'), [ref]$null, [ref]$parseErrors)
Assert (-not $parseErrors) 'Desktop smoke has parse errors.'
# Load the actual functions without running the desktop harness or using its ports.
$tree.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] }, $false) | ForEach-Object { Invoke-Expression $_.Extent.Text }
$script:queries = 0
$script:appendStderr = $true
function Get-CimInstance {
    param($ClassName, $Filter)
    $script:queries++
    if ($script:queries -eq 3) {
        Assert ([IO.File]::ReadAllText((Join-Path $runDir 'desktop-lifecycle.jsonl')).Contains('wait_returned')) 'Wait result must be recorded before collecting a timeout snapshot.'
        if ($script:appendStderr) { $owned.NativeStderrSink.SetLength(83) }
    }
    [pscustomobject]@{ ProcessId = 234; ParentProcessId = 123; Name = 'open-design-gateway.exe'; CreationDate = [datetime]'2026-10-23T00:00:00Z'; CommandLine = 'synthetic-secret-must-not-be-logged' }
    [pscustomobject]@{ ProcessId = 345; ParentProcessId = 123; Name = 'moviedesign-opencode.exe'; CreationDate = [datetime]'2026-10-23T00:00:00Z'; CommandLine = 'synthetic-secret-must-not-be-logged' }
}
$owned = [pscustomobject]@{ Id = 123; HasExited = $false; WaitMilliseconds = 0; NativeStderrSink = [IO.MemoryStream]::new() }
$owned.NativeStderrSink.SetLength(41)
$owned | Add-Member ScriptMethod CloseMainWindow { return $true }
$owned | Add-Member ScriptMethod WaitForExit { param($milliseconds) $this.WaitMilliseconds = $milliseconds; return $false }
$failure = $null
try { Stop-OwnedDesktop $owned } catch { $failure = $_.Exception.Message }
Assert ($failure -eq 'Desktop did not exit normally.') 'Timeout must retain its original failure.'
Assert ($owned.WaitMilliseconds -eq 10000) 'Normal exit must retain its original 10-second wait.'
$tracePath = Join-Path $runDir 'desktop-lifecycle.jsonl'
Assert (Test-Path -LiteralPath $tracePath) 'Timeout must preserve native shutdown phase evidence.'
$records = @(Get-Content -LiteralPath $tracePath | ForEach-Object { $_ | ConvertFrom-Json })
Assert (($records.stage -join ',') -eq 'close_requested,close_returned,wait_returned,timeout_children') 'Shutdown evidence lost its stage ordering.'
Assert (-not $records[2].details.exited -and $records[2].details.elapsedMs -ge 0) 'Timeout timing must describe the failed wait.'
Assert ($records[2].details.stderrBytes -eq 41 -and $owned.NativeStderrSink.Length -eq 83) 'Wait evidence must retain its stderr boundary before later cleanup output.'
Assert (($records[3].details.children.ProcessId -join ',') -eq '234,345') 'Timeout must snapshot its owned children.'
Assert (-not ([IO.File]::ReadAllText($tracePath)).Contains('synthetic-secret')) 'Timeout evidence must exclude command lines and secrets.'
Write-Output 'PASS timeout retains original failure/10-second wait and records phases before the owned-child snapshot.'
$owned.NativeStderrSink.Dispose()
$script:queries = 0
$script:appendStderr = $false
$failure = $null
try { Stop-OwnedDesktop $owned } catch { $failure = $_.Exception.Message }
Assert ($failure -eq 'Desktop did not exit normally.') 'Stderr metadata failure must not replace the original timeout.'
$records = @(Get-Content -LiteralPath $tracePath | ForEach-Object { $_ | ConvertFrom-Json })
Assert (@($records | Where-Object stage -eq 'stderr_snapshot_failed').Count -eq 1) 'Stderr metadata failure must remain visible in the trace.'
Assert ($null -eq ($records | Where-Object stage -eq 'wait_returned' | Select-Object -Last 1).details.stderrBytes) 'Unavailable stderr length must not be reported as a valid byte boundary.'
Write-Output 'PASS wait evidence fixes the stderr byte boundary; metadata failures remain visible and preserve the timeout.'

foreach ($failedPart in @('reader', 'sink')) {
    $script:failedPart = $failedPart
    $script:disposedParts = @()
    $reader = [pscustomobject]@{}
    $reader | Add-Member ScriptMethod Dispose { $script:disposedParts += 'reader'; if ($script:failedPart -eq 'reader') { throw [IO.IOException]::new('controlled reader dispose failure') } }
    $sink = [pscustomobject]@{}
    $sink | Add-Member ScriptMethod Dispose { $script:disposedParts += 'sink'; if ($script:failedPart -eq 'sink') { throw [IO.IOException]::new('controlled sink dispose failure') } }
    $done = [Threading.Tasks.TaskCompletionSource[bool]]::new()
    $done.SetResult($true)
    $capture = [pscustomobject]@{ Id = 987; StandardError = $reader; NativeStderrSink = $sink; NativeStderrCopy = $done.Task }
    $capture | Add-Member ScriptMethod Dispose { $script:disposedParts += 'process' }
    $failure = $null
    try { try { throw 'Desktop did not exit normally.' } finally { Dispose-OwnedDesktop $capture } } catch { $failure = $_.Exception.Message }
    Assert ($failure -eq 'Desktop did not exit normally.') 'Capture dispose failure must not replace the primary timeout.'
    Assert (($script:disposedParts -join ',') -eq 'reader,sink,process') 'Capture dispose failure must still release every owned part.'
    $fault = Get-Content -LiteralPath $tracePath | ForEach-Object { $_ | ConvertFrom-Json } | Where-Object stage -eq 'stderr_capture_failed' | Select-Object -Last 1
    Assert ($fault.details.part -eq $failedPart) 'Secondary capture dispose failure must remain visible.'
}
Write-Output 'PASS reader/sink dispose faults remain secondary, retain the original timeout, and release all owned parts.'

$info = New-Object Diagnostics.ProcessStartInfo
$info.FileName = Join-Path $PSHOME 'powershell.exe'
$info.UseShellExecute = $false
$info.CreateNoWindow = $true
$info.RedirectStandardError = $true
$paths = @()
foreach ($marker in @('first-owned-stderr', 'second-owned-stderr')) {
    $fixture = "[Console]::Error.WriteLine('$marker')"
    $info.Arguments = '-NoProfile -EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($fixture))
    $child = Start-OwnedDesktop
    $stderrPath = $child.NativeStderrSink.Name
    $paths += $stderrPath
    try { Assert ($child.WaitForExit(5000)) 'Controlled stderr writer did not exit.' }
    finally { if (-not $child.HasExited) { $child.Kill(); $null = $child.WaitForExit(5000) }; Dispose-OwnedDesktop $child }
    Assert (([IO.File]::ReadAllText($stderrPath)).Trim() -eq $marker) 'Each launch must capture only its own stderr.'
}
Assert ($paths[0] -ne $paths[1]) 'Repeated launches must not overwrite stderr.'
Write-Output 'PASS two real controlled launches retain separate complete stderr files.'

# CLR-only writer/hold avoids PowerShell cmdlet progress/CLIXML on the tested stderr pipe.
# A still-open pipe models a descendant retaining stderr after the desktop exits.
$fixture = "[Console]::Error.WriteLine('held-stderr'); [Console]::Error.Flush(); [Threading.Thread]::Sleep(4000); [Console]::Error.WriteLine('after-capture-close')"
$markerLength = [Text.Encoding]::UTF8.GetByteCount("held-stderr`r`n")
$info.Arguments = '-NoProfile -EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($fixture))
$child = Start-OwnedDesktop
$stderrPath = $child.NativeStderrSink.Name
$copy = $child.NativeStderrCopy
$observation = $child.NativeStderrObservation
$pinned = [Diagnostics.Process]::GetProcessById($child.Id)
$null = $pinned.Handle
$disposed = $false
try {
    $ready = [Diagnostics.Stopwatch]::StartNew()
    while ($child.NativeStderrSink.Length -lt $markerLength -and $ready.ElapsedMilliseconds -lt 3000) { Start-Sleep -Milliseconds 20 }
    Assert ($child.NativeStderrSink.Length -ge $markerLength) 'Controlled open-pipe writer did not become ready.'
    $timer = [Diagnostics.Stopwatch]::StartNew()
    Dispose-OwnedDesktop $child
    $disposed = $true
    $captureMs = $timer.ElapsedMilliseconds
    Assert ($captureMs -lt 1500 -and -not $pinned.HasExited) 'Capture cleanup must not wait for open-pipe EOF.'
    Assert (([IO.File]::ReadAllText($stderrPath)).Trim() -eq 'held-stderr') 'Capture cleanup must retain bytes already written.'
    Assert ($pinned.WaitForExit(5000)) 'Controlled open-pipe writer did not exit.'
    try { $null = $copy.Wait(1000) } catch { } # The CLR fault observer below must also complete successfully.
    Assert $copy.IsCompleted 'Copy task must settle after its owned writer exits.'
    if ($copy.IsFaulted) { Assert ($observation.Wait(1000) -and -not $observation.IsFaulted) 'A delayed stderr fault must be observed without a PowerShell runspace.' }
    Write-Output ('PASS capture closed in {0} ms without waiting for EOF; delayed copy fault observed={1}.' -f $captureMs, $copy.IsFaulted)
} finally {
    if (-not $pinned.HasExited) { $pinned.Kill(); $null = $pinned.WaitForExit(5000) }
    if (-not $disposed) { Dispose-OwnedDesktop $child }
    $pinned.Dispose()
}
Write-Output ('Desktop shutdown evidence checks passed: ' + $runDir)
