$ErrorActionPreference = 'Stop'
function Assert($Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
$repository = Split-Path $PSScriptRoot -Parent
$helper = Join-Path $PSScriptRoot 'restart.ps1'
. $helper

$fixture = Join-Path ([IO.Path]::GetTempPath()) ("MovieDesign launcher ' fixture " + [guid]::NewGuid().ToString('N'))
$fixture = [IO.Path]::GetFullPath($fixture)
New-Item -ItemType Directory -Path (Join-Path $fixture 'desktop\node_modules') -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $fixture 'gateway\internal\agent') -Force | Out-Null
$source = Join-Path $fixture 'gateway\internal\agent\media_prompt.go'
[IO.File]::WriteAllBytes($source, [byte[]]@(0, 239, 187, 191, 13, 10, 39, 255))
$originalHash = (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
$paths = @(Get-LauncherPaths $fixture)
$script:checks = 0
function Check([string]$Name, [scriptblock]$Body) {
    $script:records = @()
    $script:handles = @{}
    $script:queries = 0
    $script:opens = 0
    $script:kills = @()
    $script:closes = @()
    $script:commands = @()
    $script:launches = @()
    $script:queryDenied = $false
    $script:queryHook = $null
    $script:graceful = $false
    $script:stopFailure = $false
    $script:launchFailure = $false
    $script:earlyExit = $false
    $script:exitRaceAt = ''
    $script:liveCloseFailure = $false
    $script:pnpmExit = 0
    & $Body
    $script:checks++
    Write-Host "PASS $Name"
}
function Record([int]$ProcessId, [string]$Path) {
    [pscustomobject]@{ ProcessId = $ProcessId; ExecutablePath = $Path; CreationDate = [datetime]'2026-10-22T00:00:00Z' }
}
function Get-CimInstance {
    param($ClassName, $Filter, $ErrorAction)
    $script:queries++
    if ($script:queryDenied) { throw 'fixture CIM access denied' }
    if ($script:queryHook) { & $script:queryHook }
    $script:records
}
function New-LauncherProcess([int]$ProcessId) {
    $script:opens++
    if (-not $script:handles.ContainsKey($ProcessId)) {
        $handle = [pscustomobject]@{ Id = $ProcessId; Handle = 1; HasExited = $false; Killed = $false; Disposed = $false; ExitCode = 5 }
        $handle | Add-Member ScriptMethod CloseMainWindow {
            $script:closes += $this.Id
            if ($script:liveCloseFailure) { throw [InvalidOperationException]::new('fixture live close denied') }
            if ($script:exitRaceAt -eq 'close') {
                $this.HasExited = $true
                throw [InvalidOperationException]::new('fixture process exited before close')
            }
            return $true
        }
        $handle | Add-Member ScriptMethod WaitForExit {
            param($Timeout)
            if ($this.Id -eq 11 -and $script:exitRaceAt -eq 'wait') {
                $this.HasExited = $true
                throw [InvalidOperationException]::new('fixture process exited before wait')
            }
            if ($this.Killed -or $script:graceful) {
                $this.HasExited = $true
                if ($script:graceful) {
                    # Model normal main/sidecar exit; later snapshots no longer contain them.
                    $script:records = @()
                }
                return $true
            }
            return $false
        }
        $handle | Add-Member ScriptMethod Kill {
            if ($script:stopFailure) { throw 'fixture stop denied' }
            if ($script:exitRaceAt -eq 'kill') {
                $this.HasExited = $true
                throw [ComponentModel.Win32Exception]::new(5)
            }
            $script:kills += $this.Id
            $this.Killed = $true
        }
        $handle | Add-Member ScriptMethod Dispose { $this.Disposed = $true }
        $script:handles[$ProcessId] = $handle
    }
    $script:handles[$ProcessId]
}
function pnpm {
    $script:commands += ,@($args)
    Assert ([StringComparer]::OrdinalIgnoreCase.Equals((Get-Location).Path, (Join-Path $fixture 'desktop'))) 'pnpm working directory changed quoting.'
    $global:LASTEXITCODE = $script:pnpmExit
}
function git { throw 'Launcher must never mutate Git.' }
function Start-Process {
    param($FilePath, $WorkingDirectory, [switch]$PassThru, $ErrorAction)
    $script:launches += $FilePath
    Assert ($FilePath -eq $paths[0]) 'Wrong release executable.'
    Assert ($WorkingDirectory -eq (Split-Path $paths[0] -Parent)) 'Wrong release working directory.'
    if ($script:launchFailure) { throw 'fixture launch denied' }
    $handle = New-LauncherProcess 99
    if ($script:earlyExit) { $handle.Killed = $true }
    $handle
}

try {
    Check 'exact paths reject same-prefix siblings and arbitrary nested binaries' {
        Assert (Test-LauncherPath $paths[0] $paths) 'Own release rejected.'
        Assert (Test-LauncherPath $paths[3].ToUpperInvariant() $paths) 'Own debug rejected.'
        Assert (-not (Test-LauncherPath $paths[0].Replace($fixture, "$fixture-sibling") $paths)) 'Sibling matched.'
        Assert (-not (Test-LauncherPath (Join-Path $fixture 'other\moviedesign.exe') $paths)) 'Unowned executable matched.'
        $script:records = @(Record 10 $paths[0].Replace($fixture, "$fixture-sibling"))
        Assert ((Invoke-Launcher $fixture 'stop') -eq 0) 'Unrelated process blocked stop.'
        Assert ($script:opens -eq 0 -and $script:kills.Count -eq 0) 'Unrelated process was touched.'
    }
    Check 'unknown mode and help perform zero process actions' {
        Assert ((Invoke-Launcher $fixture 'bogus') -eq 2) 'Unknown mode succeeded.'
        Assert ((Invoke-Launcher $fixture '--help') -eq 0) 'Help failed.'
        Assert ($script:queries -eq 0 -and $script:opens -eq 0 -and $script:kills.Count -eq 0) 'Mode validation queried processes.'
    }
    Check 'CIM access denied returns nonzero and prevents build/launch' {
        $script:queryDenied = $true
        Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'CIM denial succeeded.'
        Assert ($script:commands.Count -eq 0 -and $script:launches.Count -eq 0) 'Continued after query denial.'
    }
    Check 'reused PID at force boundary receives zero kills' {
        $script:records = @(Record 11 $paths[0])
        $script:queryHook = {
            if ($script:queries -eq 3) {
                $script:records = @(Record 11 $paths[0])
                $script:records[0].CreationDate = $script:records[0].CreationDate.AddSeconds(1)
            }
        }
        Assert ((Invoke-Launcher $fixture 'stop') -eq 0) 'Recycled process blocked stop.'
        Assert ($script:closes.Count -eq 1 -and $script:kills.Count -eq 0) 'Recycled PID was killed.'
        Assert ($script:handles[11].Disposed) 'Pinned handle leaked.'
    }
    Check 'PID reuse while acquiring handle receives zero close/kill calls' {
        $script:records = @(Record 11 $paths[0])
        $script:queryHook = {
            if ($script:queries -eq 2) { $script:records = @(Record 11 $paths[0].Replace($fixture, "$fixture-sibling")) }
        }
        Assert ((Invoke-Launcher $fixture 'stop') -eq 0) 'Recycled acquisition blocked stop.'
        Assert ($script:closes.Count -eq 0 -and $script:kills.Count -eq 0) 'Recycled handle received action.'
    }
    Check 'graceful main lifecycle exit never forces main or children' {
        $script:graceful = $true
        $script:records = @(Record 12 $paths[1]; Record 11 $paths[0]; Record 13 $paths[2])
        Assert ((Invoke-Launcher $fixture 'stop') -eq 0) 'Graceful stop failed.'
        Assert ($script:closes.Count -eq 1 -and $script:closes[0] -eq 11 -and $script:kills.Count -eq 0) 'Graceful lifecycle force-killed processes.'
    }
    Check 'verified timeout forces only owned exact paths, including orphan sidecars' {
        $script:records = @(Record 12 $paths[1]; Record 11 $paths[0]; Record 13 $paths[2]; Record 14 $paths[0].Replace($fixture, "$fixture-sibling"))
        Assert ((Invoke-Launcher $fixture 'stop') -eq 0) 'Timeout cleanup failed.'
        Assert (($script:kills -join ',') -eq '11,12,13') 'Wrong kill order or ownership.'
        Assert ($script:queries -eq 7) 'Force boundary did not requery identities.'
    }
    Check 'stop failure returns nonzero and prevents launch' {
        $script:records = @(Record 12 $paths[1])
        $script:stopFailure = $true
        Assert ((Invoke-Launcher $fixture 'release') -eq 1) 'Failed stop succeeded.'
        Assert ($script:launches.Count -eq 0) 'Launched after stop failure.'
    }
    Check 'unavailable creation identity and force requery denial fail before kill' {
        $script:records = @(Record 12 $paths[1])
        $script:records[0].CreationDate = $null
        Assert ((Invoke-Launcher $fixture 'stop') -eq 1) 'Missing creation identity succeeded.'
        Assert ($script:opens -eq 0 -and $script:kills.Count -eq 0) 'Missing identity reached process actions.'
        $script:records = @(Record 12 $paths[1])
        $script:queries = 0
        $script:queryHook = { if ($script:queries -eq 3) { throw 'fixture force requery denied' } }
        Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'Force requery denial succeeded.'
        Assert ($script:kills.Count -eq 0 -and $script:commands.Count -eq 0 -and $script:launches.Count -eq 0) 'Force requery denial caused side effects.'
    }
    Check 'build failure prevents launch and preserves source bytes' {
        $script:pnpmExit = 9
        Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'Build failure succeeded.'
        Assert ($script:launches.Count -eq 0) 'Launched failed build.'
        Assert ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -eq $originalHash) 'Build changed source bytes.'
    }
    Check 'dependency install failure prevents build and launch' {
        Rename-Item -LiteralPath (Join-Path $fixture 'desktop\node_modules') -NewName 'node_modules.saved'
        try {
            $script:pnpmExit = 8
            Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'Install failure succeeded.'
            Assert ($script:commands.Count -eq 1 -and ($script:commands[0] -join ' ') -eq 'install --frozen-lockfile') 'Dependency failure continued build.'
            Assert ($script:launches.Count -eq 0) 'Dependency failure launched.'
        } finally { Rename-Item -LiteralPath (Join-Path $fixture 'desktop\node_modules.saved') -NewName 'node_modules' }
    }
    Check 'apostrophe/space root build preserves source and launches from exact directory' {
        $before = (Get-Location).Path
        Assert ((Invoke-Launcher $fixture 'build') -eq 0) 'Quoted root build failed.'
        Assert ($script:commands.Count -eq 1 -and $script:commands[0][0] -eq 'desktop:build') 'Build command changed.'
        Assert ($script:launches.Count -eq 1 -and (Get-Location).Path -eq $before) 'Launch count or caller directory changed.'
        Assert ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -eq $originalHash) 'Successful build changed source bytes.'
    }
    Check 'missing release auto-builds; dev retains existing pnpm command' {
        Assert ((Invoke-Launcher $fixture '') -eq 0) 'Default release failed.'
        Assert ($script:commands.Count -eq 1 -and $script:commands[0][0] -eq 'desktop:build') 'Missing release was not built.'
        Assert ((Invoke-Launcher $fixture 'dev') -eq 0) 'Dev failed.'
        Assert ($script:commands.Count -eq 2 -and $script:commands[1][0] -eq 'desktop:dev') 'Dev command changed.'
        Assert ($script:launches.Count -eq 1) 'Dev also launched release.'
    }
    Check 'existing release skips build and failed dev returns nonzero' {
        New-Item -ItemType Directory -Path (Split-Path $paths[0] -Parent) -Force | Out-Null
        [IO.File]::WriteAllText($paths[0], 'fixture; never executed')
        Assert ((Invoke-Launcher $fixture 'release') -eq 0) 'Existing release failed.'
        Assert ($script:commands.Count -eq 0 -and $script:launches.Count -eq 1) 'Existing release unexpectedly built.'
        $script:pnpmExit = 7
        Assert ((Invoke-Launcher $fixture 'dev') -eq 1) 'Failed dev reported success.'
        Assert ($script:launches.Count -eq 1) 'Failed dev launched release.'
    }
    Check 'confirmed pinned-process exit races succeed; live close and query errors remain failures' {
        $modes = @('release', 'build', 'dev')
        $boundaries = @('close', 'wait', 'kill')
        for ($index = 0; $index -lt $boundaries.Count; $index++) {
            $script:exitRaceAt = $boundaries[$index]
            $script:handles.Clear()
            $script:records = @(Record 11 $paths[0])
            Assert ((Invoke-Launcher $fixture $modes[$index]) -eq 0) "Natural exit at $($boundaries[$index]) aborted restart."
            Assert ($script:handles[11].Disposed -and $script:kills.Count -eq 0) 'Natural exit leaked handle or force-killed.'
        }
        Assert ($script:launches.Count -eq 2 -and $script:commands.Count -eq 2) 'Restart did not continue after natural exit.'
        $script:exitRaceAt = ''
        $script:liveCloseFailure = $true
        $script:handles.Clear()
        $script:records = @(Record 11 $paths[0])
        Assert ((Invoke-Launcher $fixture 'stop') -eq 1) 'Still-live close failure was swallowed.'
        Assert ($script:handles[11].Disposed -and -not $script:handles[11].HasExited) 'Live close failure lost pinned identity.'
        $script:liveCloseFailure = $false
        $script:handles.Clear()
        $script:queries = 0
        $script:queryHook = {
            if ($script:queries -eq 3) {
                $script:handles[11].HasExited = $true
                throw [InvalidOperationException]::new('fixture query failed during exit')
            }
        }
        Assert ((Invoke-Launcher $fixture 'stop') -eq 1) 'Query failure was swallowed after exit.'
        Assert ($script:handles[11].Disposed -and $script:kills.Count -eq 0) 'Query failure leaked handle or killed.'
    }
    Check 'launch failure and immediate exit return nonzero' {
        $script:launchFailure = $true
        Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'Launch exception reported success.'
        $script:launchFailure = $false
        $script:earlyExit = $true
        Assert ((Invoke-Launcher $fixture 'build') -eq 1) 'Immediate exit reported success.'
    }
    Check 'BAT forwards mode and empty default with quoted helper path and propagates exit' {
        $batRoot = Join-Path $fixture 'BAT fixture'
        New-Item -ItemType Directory -Path (Join-Path $batRoot 'scripts') -Force | Out-Null
        Copy-Item -LiteralPath (Join-Path $repository 'restart.bat') -Destination (Join-Path $batRoot 'restart.bat')
        @'
param([string]$Mode)
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'received.txt'), $Mode)
exit 23
'@ | Set-Content -LiteralPath (Join-Path $batRoot 'scripts\restart.ps1') -Encoding ASCII
        # The copied BAT calls only our fake helper, never the actual launcher.
        Push-Location -LiteralPath $batRoot
        try {
            & $env:ComSpec /d /c 'restart.bat build'
            Assert ($LASTEXITCODE -eq 23) 'BAT lost helper failure code.'
            Assert ([IO.File]::ReadAllText((Join-Path $batRoot 'scripts\received.txt')) -eq 'build') 'BAT lost mode.'
            & $env:ComSpec /d /c 'restart.bat'
            Assert ($LASTEXITCODE -eq 23) 'BAT default lost failure code.'
            Assert ([IO.File]::ReadAllText((Join-Path $batRoot 'scripts\received.txt')) -eq '') 'BAT default changed mode.'
        } finally { Pop-Location }
    }
    Write-Host "PASS $script:checks launcher checks; no real process stops, launches, builds, or network calls."
} finally {
    # Delete only this check's freshly-created, resolved fixture directory.
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    Assert ($fixture.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -and (Split-Path $fixture -Leaf).StartsWith("MovieDesign launcher ' fixture ")) 'Fixture cleanup escaped temp root.'
    Remove-Item -LiteralPath $fixture -Recurse -Force
}
