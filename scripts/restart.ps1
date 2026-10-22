param([string]$Mode = 'release')

function Get-LauncherPaths([string]$Root) {
    $rootPath = [IO.Path]::GetFullPath($Root)
    foreach ($configuration in 'release', 'debug') {
        foreach ($name in 'moviedesign.exe', 'open-design-gateway.exe', 'moviedesign-opencode.exe') {
            [IO.Path]::GetFullPath((Join-Path $rootPath "desktop\src-tauri\target\$configuration\$name"))
        }
    }
}

function Test-LauncherPath([string]$Path, [string[]]$Paths) {
    if (-not $Path -or -not [IO.Path]::IsPathRooted($Path)) { return $false }
    $fullPath = [IO.Path]::GetFullPath($Path)
    foreach ($ownedPath in $Paths) {
        if ([StringComparer]::OrdinalIgnoreCase.Equals($fullPath, $ownedPath)) { return $true }
    }
    return $false
}

function Get-LauncherProcesses {
    Get-CimInstance Win32_Process -Filter "Name='moviedesign.exe' OR Name='open-design-gateway.exe' OR Name='moviedesign-opencode.exe'" -ErrorAction Stop
}

function Test-LauncherIdentity($Expected, $Actual) {
    return ($null -ne $Actual -and $Expected.ProcessId -eq $Actual.ProcessId -and
        $null -ne $Expected.CreationDate -and $Expected.CreationDate -eq $Actual.CreationDate -and
        [StringComparer]::OrdinalIgnoreCase.Equals($Expected.ExecutablePath, $Actual.ExecutablePath))
}

function New-LauncherProcess([int]$ProcessId) {
    [Diagnostics.Process]::GetProcessById($ProcessId)
}

function Open-LauncherProcess($Record) {
    try { $process = New-LauncherProcess $Record.ProcessId }
    catch [ArgumentException] { return $null } # Already exited.
    try {
        # Pin the OS handle before checking identity; Kill then cannot target a recycled PID.
        $null = $process.Handle
        $current = @(Get-LauncherProcesses | Where-Object { $_.ProcessId -eq $Record.ProcessId })
        if ($process.HasExited -or $current.Count -ne 1 -or -not (Test-LauncherIdentity $Record $current[0])) {
            $process.Dispose()
            return $null
        }
        return $process
    } catch {
        $process.Dispose()
        throw
    }
}

function Stop-LauncherProcesses([string[]]$Paths) {
    $owned = @(Get-LauncherProcesses | Where-Object { Test-LauncherPath $_.ExecutablePath $Paths })
    foreach ($record in $owned) {
        if ($null -eq $record.CreationDate) { throw "Process identity unavailable (PID $($record.ProcessId))." }
    }
    # Close mains first so their existing lifecycle can shut down their sidecars.
    $ordered = @($owned | Where-Object { [IO.Path]::GetFileName($_.ExecutablePath) -ieq 'moviedesign.exe' }) +
        @($owned | Where-Object { [IO.Path]::GetFileName($_.ExecutablePath) -ine 'moviedesign.exe' })
    foreach ($record in $ordered) {
        $process = Open-LauncherProcess $record
        if ($null -eq $process) { continue }
        try {
            if ([IO.Path]::GetFileName($record.ExecutablePath) -ieq 'moviedesign.exe') {
                $null = $process.CloseMainWindow()
                if ($process.WaitForExit(10000)) { continue }
            }
            if ($process.HasExited) { continue }
            # Recheck immediately before force; holding the handle also closes the check/kill PID race.
            $current = @(Get-LauncherProcesses | Where-Object { $_.ProcessId -eq $record.ProcessId })
            if ($current.Count -ne 1 -or -not (Test-LauncherIdentity $record $current[0])) { continue }
            $process.Kill()
            if (-not $process.WaitForExit(5000)) { throw "Stop timed out (PID $($record.ProcessId))." }
        } finally { $process.Dispose() }
    }
}

function Invoke-LauncherPnpm([string]$Directory, [string[]]$PnpmArguments) {
    Push-Location -LiteralPath $Directory -ErrorAction Stop
    try {
        & pnpm @PnpmArguments | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "pnpm $($PnpmArguments -join ' ') failed (exit $LASTEXITCODE)." }
    } finally { Pop-Location }
}

function Start-LauncherRelease([string]$Executable) {
    $process = Start-Process -FilePath $Executable -WorkingDirectory (Split-Path $Executable -Parent) -PassThru -ErrorAction Stop
    if ($null -eq $process) { throw 'Release launch returned no process.' }
    try {
        if ($process.WaitForExit(500)) { throw "Release exited during startup (exit $($process.ExitCode))." }
    } finally { $process.Dispose() }
}

function Invoke-Launcher([string]$Root, [string]$Mode = 'release') {
    if (-not $Mode) { $Mode = 'release' }
    if ($Mode -in @('help', '-h', '--help', '/?')) {
        Write-Host 'Usage: restart.bat [release|build|dev|stop] (default: release; missing release builds first)'
        return 0
    }
    if ($Mode -notin @('release', 'build', 'dev', 'stop')) {
        Write-Host "[MovieDesign] Unknown mode: $Mode. Use release, build, dev, or stop."
        return 2
    }
    try {
        $rootPath = [IO.Path]::GetFullPath($Root)
        $desktop = Join-Path $rootPath 'desktop'
        $release = Join-Path $desktop 'src-tauri\target\release\moviedesign.exe'
        Write-Host '[MovieDesign] Stopping owned repository instances...'
        Stop-LauncherProcesses @(Get-LauncherPaths $rootPath)
        if ($Mode -eq 'stop') {
            Write-Host '[MovieDesign] Repository instances stopped.'
            return 0
        }
        if ($Mode -eq 'dev' -or $Mode -eq 'build' -or -not (Test-Path -LiteralPath $release -PathType Leaf)) {
            if (-not (Test-Path -LiteralPath (Join-Path $desktop 'node_modules') -PathType Container)) {
                Invoke-LauncherPnpm $desktop @('install', '--frozen-lockfile')
            }
            if ($Mode -eq 'dev') {
                Invoke-LauncherPnpm $desktop @('desktop:dev')
                return 0
            }
            Invoke-LauncherPnpm $desktop @('desktop:build')
        }
        Start-LauncherRelease $release
        Write-Host '[MovieDesign] Release launched.'
        return 0
    } catch {
        Write-Host "[MovieDesign] Failed: $($_.Exception.Message)"
        return 1
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    $ErrorActionPreference = 'Stop'
    exit (Invoke-Launcher (Split-Path $PSScriptRoot -Parent) $Mode)
}
