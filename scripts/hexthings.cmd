@echo off
rem Lets you type: hexthings status   (from cmd or PowerShell once this folder is on PATH)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0hexthings.ps1" %*
