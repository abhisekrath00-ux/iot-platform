# Installs the Hexmon edge agent as a Windows service (run in elevated PowerShell).
# Usage: .\install-windows.ps1 [-ClaimApi URL -ClaimCode CODE -ClaimSerial SERIAL]
param([string]$ClaimApi = "", [string]$ClaimCode = "", [string]$ClaimSerial = "")
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$src = Join-Path $here "edge-agent-windows-$arch.exe"
if (-not (Test-Path $src)) { $src = Join-Path $here "edge-agent.exe" }
$dir = Join-Path $env:ProgramData "Hexmon\edge"
New-Item -ItemType Directory -Force -Path $dir, "$dir\data" | Out-Null
Copy-Item $src "$dir\edge-agent.exe" -Force
if (-not (Test-Path "$dir\edge-agent.yaml")) { Copy-Item (Join-Path $here "edge-agent.example.yaml") "$dir\edge-agent.yaml" }
# Restrict the folder (identity + keys live here) to SYSTEM and Administrators.
icacls $dir /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F" | Out-Null
if ($ClaimCode) {
  & "$dir\edge-agent.exe" -claim-api $ClaimApi -claim-code $ClaimCode -claim-serial $ClaimSerial
}
if (Get-Service HexmonEdge -ErrorAction SilentlyContinue) { Stop-Service HexmonEdge; sc.exe delete HexmonEdge | Out-Null; Start-Sleep 2 }
New-Service -Name HexmonEdge -DisplayName "Hexmon Edge Agent" -BinaryPathName "`"$dir\edge-agent.exe`"" -StartupType Automatic | Out-Null
sc.exe failure HexmonEdge reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
Write-Host "Installed. Edit $dir\edge-agent.yaml (or claim), then: Start-Service HexmonEdge"
