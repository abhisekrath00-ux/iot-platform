# HexThings prerequisite check and installer for Windows 10/11 (single node). Works in Windows PowerShell 5.1 and PowerShell 7.
#   powershell -ExecutionPolicy Bypass -File scripts\prereq.ps1 [-Yes] [-Check]
# Checks: Windows build, memory, disk, CPU virtualization, WSL2, Docker Desktop, a running Docker engine.
# Whatever is already present is skipped. If something is missing it shows the exact plan, asks ONCE, then installs only that.
# Installing needs administrator rights: it opens ONE elevated window (a Windows UAC prompt) for just the install steps.
# Exit codes: 0 ready, 1 failed (reason printed), 2 declined / cannot ask, 10 Windows must restart, then run the same command again.
# Docker Desktop has its own license terms (larger companies may need a paid subscription): check docker.com/pricing. Say no to skip it.
# NOT TESTED on real Windows. The decision logic (Get-Plan) is unit-tested with PowerShell 7 on Linux (scripts/test-prereq.ps1).
param([switch]$Yes, [switch]$Check, [switch]$Elevated, [string]$ResultFile = "")
$ErrorActionPreference = "Continue"; $ProgressPreference = "SilentlyContinue"

# Pure decision logic: facts in, list of steps out. No side effects, so it can be tested anywhere.
function Get-Plan($f) {
  $plan = @(); $fail = @()
  if ($f.Build -lt 19041) { $fail += "This Windows version (build $($f.Build)) is too old for WSL2 and Docker Desktop. Windows 10 version 2004 (build 19041) or newer is needed." }
  if ($f.DiskMB -lt 5000) { $fail += "Only $($f.DiskMB) MB free on the system drive; at least 5 GB is needed." }
  if (-not $f.Virtualization -and -not $f.DockerOk) { $fail += "CPU virtualization is turned off. Turn on Intel VT-x / AMD SVM in the BIOS/UEFI, then run this again. (This cannot be done from software.)" }
  if ($fail.Count -gt 0) { return @{ Fail = $fail; Plan = @() } }
  if (-not $f.DockerOk) {
    if (-not $f.Wsl -and -not $f.DockerInstalled) { $plan += "wsl" }
    if (-not $f.DockerInstalled) { $plan += "docker" }
    elseif (-not $f.EngineUp) { $plan += "start" }
    if ($f.DockerInstalled -and -not $f.EngineUp -and -not $f.Wsl) { $plan = @("wsl") + $plan }
  }
  return @{ Fail = @(); Plan = $plan }
}
$stepText = @{
  wsl    = "Turn on WSL2 (Windows Subsystem for Linux): wsl --install --no-distribution. Windows may need a restart afterwards."
  docker = "Install Docker Desktop (with winget if available, otherwise Docker's official installer from desktop.docker.com)."
  start  = "Start Docker Desktop and wait for its engine."
}
if ($env:HEXTHINGS_PS_LIBRARY_ONLY) { return }   # test hook: load the functions only

function Say($m) { Write-Host $m }
function Ok($m) { Write-Host "  ok    $m" -ForegroundColor Green }
function Warn($m) { Write-Host "  warn  $m" -ForegroundColor Yellow }
function Bad($m) { Write-Host "  FAIL  $m" -ForegroundColor Red }
function Finish($code, $msg) { if ($ResultFile) { Set-Content -Path $ResultFile -Value "$code|$msg" }; if ($Elevated) { Read-Host "Press Enter to close this window" | Out-Null }; exit $code }
function IsAdmin { ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator) }
function DockerCmd { $env:Path = $env:Path + ";$env:ProgramFiles\Docker\Docker\resources\bin"; [bool](Get-Command docker -ErrorAction SilentlyContinue) }
function EngineUp { if (-not (DockerCmd)) { return $false }; & docker info *> $null; return ($LASTEXITCODE -eq 0) }
function WslOk { if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) { return $false }; & wsl.exe --status *> $null; return ($LASTEXITCODE -eq 0) }

function Get-Facts {
  $os = Get-CimInstance Win32_OperatingSystem; $cs = Get-CimInstance Win32_ComputerSystem; $cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
  $drive = (Get-PSDrive -Name ($env:SystemDrive.Substring(0, 1)))
  [pscustomobject]@{
    Build = [int]$os.BuildNumber; MemMB = [int]($cs.TotalPhysicalMemory / 1MB); DiskMB = [int]($drive.Free / 1MB)
    Virtualization = ([bool]$cpu.VirtualizationFirmwareEnabled -or [bool]$cs.HypervisorPresent)
    Wsl = (WslOk); DockerInstalled = (DockerCmd); EngineUp = (EngineUp); DockerOk = ((DockerCmd) -and (EngineUp))
  }
}

function Install-Wsl {
  Say "  Turning on WSL2..."
  $out = (& wsl.exe --install --no-distribution 2>&1 | Out-String) -replace "`0", ""
  $code = $LASTEXITCODE; Say ($out.Trim())
  if ($code -eq 3010 -or $out -match "(?i)restart|reboot") { return "reboot" }
  if ($code -ne 0) { return "fail" }
  return "ok"
}
function Install-Docker {
  $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
  if (Get-Command winget -ErrorAction SilentlyContinue) {
    Say "  Installing Docker Desktop with winget..."
    & winget install -e --id Docker.DockerDesktop --accept-package-agreements --accept-source-agreements --silent
    $code = $LASTEXITCODE
  } else {
    $exe = Join-Path $env:TEMP "DockerDesktopInstaller.exe"
    Say "  Downloading Docker Desktop installer..."
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    try { Invoke-WebRequest -UseBasicParsing "https://desktop.docker.com/win/main/$arch/Docker%20Desktop%20Installer.exe" -OutFile $exe } catch { Bad "download failed: $($_.Exception.Message). Offline? Install Docker Desktop by hand, then run this again."; return "fail" }
    $p = Start-Process -FilePath $exe -ArgumentList "install", "--quiet", "--accept-license" -Wait -PassThru; $code = $p.ExitCode
  }
  if ($code -eq 3010) { return "reboot" }
  if ($code -ne 0) { Bad "Docker Desktop installer exit code $code"; return "fail" }
  return "ok"
}
function Start-DockerDesktop {
  $exe = Join-Path $env:ProgramFiles "Docker\Docker\Docker Desktop.exe"
  if (-not (Test-Path $exe)) { return $false }
  Start-Process $exe | Out-Null
  Say "  Waiting for the Docker engine (up to 4 minutes)..."
  for ($i = 0; $i -lt 120; $i++) { if (EngineUp) { return $true }; Start-Sleep -Seconds 2 }
  return $false
}

function Run-Steps($steps) {   # runs in the (elevated) window; returns "ok" | "reboot" | "fail"
  foreach ($s in $steps) {
    if ($s -eq "wsl") { $r = Install-Wsl; if ($r -ne "ok") { return $r } }
    if ($s -eq "docker") { $r = Install-Docker; if ($r -ne "ok") { return $r } }
  }
  return "ok"
}

Say "HexThings prerequisite check (Windows)"
$f = Get-Facts
if ($f.MemMB -ge 4000) { Ok "memory $($f.MemMB) MB" } else { Warn "memory $($f.MemMB) MB; about 4 GB is recommended, it may be slow" }
Ok "Windows build $($f.Build), free disk $($f.DiskMB) MB"
if ($f.DockerOk) { Ok "Docker is installed and its engine is running" } else { Warn "Docker is not ready (installed: $($f.DockerInstalled), engine running: $($f.EngineUp), WSL2: $($f.Wsl))" }
$d = Get-Plan $f
if ($d.Fail.Count -gt 0) { foreach ($m in $d.Fail) { Bad $m }; Say ""; Say "Not ready. Fix the items marked FAIL, then run the same command again."; Finish 1 "not ready" }
if ($d.Plan.Count -eq 0) { Say ""; Say "Everything needed is present."; Finish 0 "ready" }
Say ""; Say "To continue, this will change the machine:"
foreach ($s in $d.Plan) { Say "  - $($stepText[$s])" }
Say "Nothing else is touched. It needs the internet and administrator rights (one Windows prompt)."
if ($Check) { Finish 2 "check only" }
if (-not $Yes) {
  if ([Console]::IsInputRedirected) { Say "Cannot ask here (no console). Run again with -Yes to accept the plan above."; Finish 2 "cannot ask" }
  $a = Read-Host "Go ahead? [y/N]"; if ($a -notmatch "^(y|yes)$") { Say "Declined. Nothing was changed."; Finish 2 "declined" }
}
$installSteps = @($d.Plan | Where-Object { $_ -ne "start" })
if ($installSteps.Count -gt 0) {
  if (IsAdmin) { $r = Run-Steps $installSteps }
  else {
    $res = Join-Path $env:TEMP "hexthings-prereq-result.txt"; Remove-Item $res -ErrorAction SilentlyContinue
    Say "Opening one administrator window for the install steps (approve the Windows prompt)..."
    $argList = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Yes -Elevated -ResultFile `"$res`""
    try { Start-Process powershell -Verb RunAs -Wait -ArgumentList $argList } catch { Bad "administrator approval was not given (or could not open). Right-click PowerShell, choose Run as administrator, and run the same command again."; Finish 1 "no admin" }
    $line = if (Test-Path $res) { (Get-Content $res -Raw).Trim() } else { "1|no result" }
    $code = [int]($line.Split("|")[0])
    if ($code -eq 10) { $r = "reboot" } elseif ($code -eq 0) { $r = "ok" } else { $r = "fail" }
    if ($r -eq "ok") { $f = Get-Facts }
  }
  if ($r -eq "reboot") { Say ""; Say "Windows must restart to finish turning on WSL2 / Docker. Restart the PC, then run the SAME command again; it continues where this stopped."; Finish 10 "reboot" }
  if ($r -eq "fail") { Bad "an install step failed (details above). Nothing else was changed. Install Docker Desktop by hand from https://www.docker.com/products/docker-desktop/ and run this again."; Finish 1 "install failed" }
}
if ($Elevated) { Finish 0 "installed" }   # the parent window starts the engine and continues as the normal user
if (-not (EngineUp)) {
  if (-not (Start-DockerDesktop)) {
    Bad "Docker Desktop is installed but its engine did not start. Open Docker Desktop once, accept its terms, wait until it says it is running, then run the same command again. If it says the user is not in the docker-users group, sign out of Windows and in again."
    Finish 1 "engine not started"
  }
}
Ok "Docker engine is running"
Finish 0 "ready"
