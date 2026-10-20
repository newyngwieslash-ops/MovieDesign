@echo off
setlocal EnableExtensions
chcp 65001 >nul

set "ROOT=%~dp0"
if "%ROOT:~-1%"=="\" set "ROOT=%ROOT:~0,-1%"
set "MODE=%~1"
if "%MODE%"=="" set "MODE=release"

if /I "%MODE%"=="help" goto :usage
if /I "%MODE%"=="-h" goto :usage
if /I "%MODE%"=="--help" goto :usage
if /I "%MODE%"=="/?" goto :usage

echo [MovieDesign] Stopping existing repository instances (if any)...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "$root = [IO.Path]::GetFullPath('%ROOT%');" ^
  "$names = @('moviedesign.exe', 'open-design-gateway.exe', 'moviedesign-opencode.exe');" ^
  "$procs = Get-CimInstance Win32_Process | Where-Object { $names -contains $_.Name -and $_.ExecutablePath -and ([IO.Path]::GetFullPath($_.ExecutablePath)).StartsWith($root, [StringComparison]::OrdinalIgnoreCase) };" ^
  "if ($procs) {" ^
  "  foreach ($p in $procs) { Write-Host ('  -> Stopping ' + $p.Name + ' (PID ' + $p.ProcessId + ')'); Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue }" ^
  "  Start-Sleep -Milliseconds 800" ^
  "} else {" ^
  "  Write-Host '  -> No running MovieDesign processes found.'" ^
  "}"
if errorlevel 1 (
    echo [MovieDesign] Warning: failed to query or stop existing processes.
)

if /I "%MODE%"=="stop" (
    echo [MovieDesign] All repository instances stopped.
    exit /b 0
)

if /I "%MODE%"=="dev" goto :run_dev
if /I "%MODE%"=="build" goto :run_build
if /I "%MODE%"=="release" goto :run_release

echo [MovieDesign] Unknown mode: %MODE%
goto :usage

:run_dev
echo [MovieDesign] Starting development mode (pnpm desktop:dev)...
cd /d "%ROOT%\desktop"
if not exist "node_modules" (
    echo [MovieDesign] Installing frontend dependencies...
    call pnpm install --frozen-lockfile
    if errorlevel 1 exit /b 1
)
call pnpm desktop:dev
exit /b %ERRORLEVEL%

:run_build
echo [MovieDesign] Rebuilding release desktop bundle (pnpm desktop:build)...
cd /d "%ROOT%\desktop"
if not exist "node_modules" (
    echo [MovieDesign] Installing frontend dependencies...
    call pnpm install --frozen-lockfile
    if errorlevel 1 exit /b 1
)
call pnpm desktop:build
if errorlevel 1 (
    echo [MovieDesign] Build failed.
    exit /b 1
)
powershell -NoProfile -ExecutionPolicy Bypass -Command "git -C '%ROOT%' checkout -- gateway/internal/agent/media_prompt.go 2>$null"
goto :launch_release

:run_release
if not exist "%ROOT%\desktop\src-tauri\target\release\moviedesign.exe" (
    echo [MovieDesign] Release binary not found; building first...
    goto :run_build
)

:launch_release
set "RELEASE_DIR=%ROOT%\desktop\src-tauri\target\release"
set "RELEASE_EXE=%RELEASE_DIR%\moviedesign.exe"
echo [MovieDesign] Launching %RELEASE_EXE% ...
start "" /D "%RELEASE_DIR%" "%RELEASE_EXE%"
echo [MovieDesign] Started successfully.
exit /b 0

:usage
echo Usage:
echo   restart.bat [release^|build^|dev^|stop]
echo.
echo Modes:
echo   release  (Default) Stop running project processes and launch release moviedesign.exe
echo            (auto-builds if desktop\src-tauri\target\release\moviedesign.exe is missing)
echo   build    Stop running project processes, rebuild release binary, and launch it
echo   dev      Stop running project processes and start Tauri + Vite development mode
echo   stop     Stop running project processes (moviedesign / gateway / opencode) and exit
exit /b 0
