# Installs or upgrades the Hexmon edge agent as a Windows service (elevated PowerShell).
#   .\install-windows.ps1 [-ClaimApi URL -ClaimCode CODE -ClaimSerial SERIAL]
# Re-running is the upgrade path: the old exe is kept as edge-agent.prev.exe, config and
# identity are not touched, and if the new version is not healthy within 60 s the old exe is restored.
param([string]$ClaimApi = "", [string]$ClaimCode = "", [string]$ClaimSerial = "")
$ErrorActionPreference = "Stop"
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw "Run this in an elevated (Administrator) PowerShell." }

$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$src = Join-Path $here "edge-agent-windows-$arch.exe"
if (-not (Test-Path $src)) { $src = Join-Path $here "edge-agent.exe" }
if (-not (Test-Path $src)) { throw "edge-agent executable not found next to this script." }
$newVer = & $src -version
if ($LASTEXITCODE -ne 0) { throw "This executable does not run on this machine." }

$dir = Join-Path $env:ProgramData "Hexmon\edge"
$exe = "$dir\edge-agent.exe"; $prev = "$dir\edge-agent.prev.exe"
New-Item -ItemType Directory -Force -Path $dir, "$dir\data", "$dir\data\logs" | Out-Null
$svc = Get-Service HexmonEdge -ErrorAction SilentlyContinue
$upgrading = [bool]$svc -and (Test-Path $exe)
$wasRunning = $upgrading -and $svc.Status -eq "Running"
if ($upgrading) { Write-Host "upgrading $(& $exe -version) -> $newVer" }

if ($svc -and $svc.Status -ne "Stopped") { Stop-Service HexmonEdge -Force; $svc.WaitForStatus("Stopped", [TimeSpan]::FromSeconds(30)) }
if (Test-Path $exe) { Copy-Item $exe $prev -Force }
Copy-Item $src $exe -Force
if (-not (Test-Path "$dir\edge-agent.yaml")) { Copy-Item (Join-Path $here "edge-agent.example.yaml") "$dir\edge-agent.yaml" }
# Identity and keys live here: restrict to SYSTEM and Administrators.
icacls $dir /inheritance:r /grant:r "SYSTEM:(OI)(CI)F" "Administrators:(OI)(CI)F" | Out-Null

if ($ClaimCode) { & $exe -claim-api $ClaimApi -claim-code $ClaimCode -claim-serial $ClaimSerial }

if (-not $svc) {
  New-Service -Name HexmonEdge -DisplayName "Hexmon Edge Agent" -BinaryPathName "`"$exe`" -config `"$dir\edge-agent.yaml`" -identity-dir `"$dir\data`"" -StartupType Automatic -Description "Hexmon edge gateway agent" | Out-Null
  sc.exe failure HexmonEdge reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
}

if ($upgrading) {
  if ($wasRunning) {
    Start-Service HexmonEdge
    $ok = $false
    for ($i = 0; $i -lt 30 -and -not $ok; $i++) {
      Start-Sleep 2
      & $exe -health -config "$dir\edge-agent.yaml" | Out-Null
      # 0 healthy, 2 running but broker unreachable (not the upgrade's fault)
      if ($LASTEXITCODE -eq 0 -or $LASTEXITCODE -eq 2) { $ok = $true }
    }
    if (-not $ok) {
      Write-Warning "new version is not healthy; restoring the previous executable"
      Stop-Service HexmonEdge -Force -ErrorAction SilentlyContinue
      Copy-Item $prev $exe -Force
      Start-Service HexmonEdge
      exit 1
    }
    Write-Host "upgrade complete; previous executable kept as $prev"
  } else { Write-Host "upgrade installed; the service was not running. Start it with: Start-Service HexmonEdge" }
} else {
  Write-Host "Installed. Check the config:  & `"$exe`" -check-config -config `"$dir\edge-agent.yaml`""
  Write-Host "Serial ports on this PC:      & `"$exe`" -list-ports"
  Write-Host "Then: Start-Service HexmonEdge   (logs: $dir\data\logs\edge-agent.log)"
}
