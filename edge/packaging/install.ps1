# Guided installer for the HexThings edge agent on Windows 10/11 and Server 2019+ (elevated PowerShell).
#   .\install.ps1                                   asks for the server address and claim code
#   .\install.ps1 -Server https://api.example -Code CODE -Serial SERIAL -Yes
# Checks the machine, tests that your server answers and the clock is close, then runs install-windows.ps1.
# Works air-gapped: the agent .exe is in this folder and only your own HexThings server is contacted.
# NOT TESTED: no Windows machine was available when this was written.
param([string]$Server = "", [string]$Code = "", [string]$Serial = "", [switch]$Yes, [switch]$SkipNetCheck)
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
function Ok($m) { Write-Host "  [ok] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "  [!] $m" -ForegroundColor Yellow }
function Bad($m) { Write-Host "  [x] $m" -ForegroundColor Red }
function Step($n, $t) { Write-Host ""; Write-Host "$n  $t" -ForegroundColor Cyan }
$art = @("  #   #  #####","  #   #    #    HexThings","  #####    #    edge agent installer","  #   #    #","  #   #    #")
$col = @("Cyan","Cyan","Blue","DarkBlue","Magenta")
Write-Host ""; for ($i = 0; $i -lt $art.Count; $i++) { Write-Host $art[$i] -ForegroundColor $col[$i] }

Step "1/4" "Checking this machine"
$p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { Bad "Run this in an elevated (Administrator) PowerShell."; exit 1 }
Ok "running as Administrator"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
if (-not ((Test-Path (Join-Path $here "edge-agent-windows-$arch.exe")) -or (Test-Path (Join-Path $here "edge-agent.exe")))) { Bad "edge-agent-windows-$arch.exe not found next to this script"; exit 1 }
if (-not (Test-Path (Join-Path $here "install-windows.ps1"))) { Bad "install-windows.ps1 not found next to this script"; exit 1 }
Ok "agent binary is bundled (no internet needed)"

Step "2/4" "Your HexThings server"
if (-not $Server -and -not $Yes) { $Server = Read-Host "  Server address (https://...)" }
if ($Server -and -not $Code -and -not $Yes) { $Code = Read-Host "  Claim code (Devices > Add gateway)" }
if ($Server -and -not $Serial -and -not $Yes) { $Serial = Read-Host "  Gateway serial" }
if ($Server) {
  # -Server may list several addresses separated by commas (primary first, then fallbacks).
  $addrs = @($Server -split ',' | ForEach-Object { $_.Trim() } | Where-Object { $_ })
  foreach ($x in $addrs) {
    if ($x -notmatch '^https?://') { Bad "server address must start with http:// or https:// ($x)"; exit 1 }
    if ($x -match '^https?://(localhost|127\.)') { Warn "$x is a loopback address; it only works if the server runs on this machine" }
  }
  if ($SkipNetCheck) { Warn "server reachability check skipped" }
  else {
    $reach = $null; $r = $null
    foreach ($x in $addrs) {
      try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 8 ($x.TrimEnd('/') + "/healthz"); $reach = $x; Ok "server answers at $x"; break }
      catch { Warn "no answer from $x ($($_.Exception.Message))" }
    }
    if (-not $reach) { Bad "cannot reach any server address ($Server); check the address, network and firewall"; exit 1 }
    if ($r.Headers["Date"]) { $d = [DateTimeOffset]::Parse($r.Headers["Date"]); $diff = [math]::Abs(([DateTimeOffset]::UtcNow - $d).TotalSeconds)
      if ($diff -gt 300) { Warn "clock differs from the server by $([int]$diff)s; fix the time or certificates and tokens may fail" } else { Ok "clock is in sync with the server" } }
  }
} else { Warn "no server given: installing only; place the config yourself" }

Step "3/4" "Installing the service"
$a = @{}; if ($Server -and $Code) { $a.ClaimApi = $Server; $a.ClaimCode = $Code; if ($Serial) { $a.ClaimSerial = $Serial } }
& (Join-Path $here "install-windows.ps1") @a
Ok "service installed"

Step "4/4" "Done"
Ok "HexThings edge agent is installed"
Write-Host "  Start:   Start-Service HexmonEdge"
Write-Host "  Health:  & `"$env:ProgramData\Hexmon\edge\edge-agent.exe`" -health"
