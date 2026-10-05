@echo off
rem Double-click installer for Windows. Runs install.ps1 with PowerShell for this run only.
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" %*
echo.
pause
