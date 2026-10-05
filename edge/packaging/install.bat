@echo off
rem Double-click to run the guided HexThings edge installer (asks for Administrator rights).
net session >nul 2>&1 || (powershell -NoProfile -Command "Start-Process cmd -ArgumentList '/c \"%~f0\"' -Verb RunAs" & exit /b)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1"
pause
