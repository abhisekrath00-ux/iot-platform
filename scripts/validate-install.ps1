# HexThings install validation for native Windows hosts. READ-ONLY preflight:
# inspects the host, never installs Docker, never starts or stops services,
# never touches ports, data or service accounts. No rehearsal mode here:
# the isolated rehearsal is scripts/validate-install.sh --rehearse on Linux/WSL.
#
# UNTESTED in the build environment (no PowerShell there). Treat its first
# run on a real Windows machine as its test and report problems back.
#
# Usage: powershell -ExecutionPolicy Bypass -File scripts\validate-install.ps1 [-Bundle DIR]
param([string]$Bundle = "")
$fails = 0; $warns = 0
function Ok($m)   { Write-Host "ok:   $m" }
function Warn($m) { Write-Host "WARN: $m"; $script:warns++ }
function Fail($m) { Write-Host "FAIL: $m"; $script:fails++ }

Write-Host "== host preflight (read-only) =="
$os = Get-CimInstance Win32_OperatingSystem
Write-Host ("host: {0} {1}" -f $os.Caption, $os.Version)

$docker = Get-Command docker -ErrorAction SilentlyContinue
if ($docker) {
  Ok "docker present ($((docker --version) -join ''))"
  docker info 2>$null | Out-Null
  if ($LASTEXITCODE -eq 0) { Ok "docker daemon reachable (Docker Desktop running)" } else { Fail "docker daemon not reachable (start Docker Desktop)" }
  docker compose version 2>$null | Out-Null
  if ($LASTEXITCODE -eq 0) { Ok "compose plugin present" } else { Fail "docker compose plugin missing" }
} else {
  Fail "docker not installed (this script never installs it; install Docker Desktop, see docs/install.md)"
}

$memMB = [int]($os.TotalVisibleMemorySize / 1024)
if ($memMB -ge 6144) { Ok "memory $memMB MB (>= 6 GB)" }
elseif ($memMB -ge 4096) { Warn "memory ${memMB} MB: 4-6 GB works for the CPU-only profile, 8 GB recommended" }
else { Fail "memory $memMB MB below the 4 GB minimum" }

$freeMB = [int]((Get-PSDrive -Name $env:SystemDrive.TrimEnd(':')).Free / 1MB)
if ($freeMB -ge 20480) { Ok "disk free $freeMB MB (>= 20 GB)" } else { Fail "disk free $freeMB MB below the 20 GB minimum" }

foreach ($p in 8000, 8080, 1883, 8883, 5432) {
  $busy = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
  if ($busy) { Warn "port $p already in use (the installer lets you pick others)" } else { Ok "port $p free" }
}

if ($Bundle -ne "") {
  Write-Host "== bundle verification (read-only) =="
  $sums = Join-Path $Bundle "SHA256SUMS"
  if (Test-Path $sums) {
    $bad = 0
    foreach ($line in Get-Content $sums) {
      if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+)$') {
        $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $Bundle $Matches[2])).Hash.ToLower()
        if ($actual -ne $Matches[1].ToLower()) { Fail "checksum mismatch: $($Matches[2])"; $bad++ }
      }
    }
    if ($bad -eq 0) { Ok "SHA256SUMS verify in $Bundle" }
  } else { Fail "no SHA256SUMS in $Bundle" }
}

Write-Host ""
if ($fails -gt 0) { Write-Host "RESULT: FAIL ($fails failures, $warns warnings)"; exit 1 }
Write-Host "RESULT: PASS ($warns warnings) - host preflight only; OS/service-level gates (real Docker boot, Windows service, hardware) remain open"
