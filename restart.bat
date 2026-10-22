@echo off
setlocal EnableExtensions
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\restart.ps1" "%~1"
exit /b %ERRORLEVEL%
