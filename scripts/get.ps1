# One-command HexThings setup for Windows 10/11. Checks what is needed (WSL2, Docker Desktop, a running engine, RAM, disk),
# installs ONLY what is missing after one yes/no question (one Windows admin prompt), then installs and starts HexThings. In PowerShell:
#   powershell -ExecutionPolicy Bypass -File scripts/get.ps1 -LocalSource .
# Uses an explicit authorized checkout/extracted source without downloading or updating files.
# Prerequisites and the installer may still need network access for Docker/images/optional AI.
param([string]$LocalSource = "")
$ErrorActionPreference = "Stop"
$hostExecutable = (Get-Process -Id $PID).Path
if ($LocalSource) {
  $dir = (Resolve-Path -LiteralPath $LocalSource).Path
  foreach ($file in @('docker-compose.yml','.env.example','scripts/prereq.ps1','scripts/install.ps1')) {
    if (-not (Test-Path -LiteralPath (Join-Path $dir $file) -PathType Leaf)) { throw "Local source is incomplete (missing $file); no download or install attempted" }
  }
} else {
  throw 'This repository is private. Sign in to GitHub and download/extract its source ZIP, or use an authorized Git checkout. Then run: powershell -ExecutionPolicy Bypass -File scripts/get.ps1 -LocalSource . . No token is requested by this script.'
}
Set-Location $dir
# Prerequisites: skips whatever is present, asks once before installing anything missing.
$pre = @(); if ($env:HEXTHINGS_YES) { $pre += "-Yes" }
& $hostExecutable -NoProfile -ExecutionPolicy Bypass -File (Join-Path $dir "scripts/prereq.ps1") @pre
if ($LASTEXITCODE -ne 0) { Write-Host "Stopped before installing HexThings (see above)."; exit $LASTEXITCODE }
# Local-source mode never contacts GitHub. Keep any existing recorded version;
# this command does not fetch, merge, overwrite source or establish its provenance.
$extra = @(); if ($env:HEXTHINGS_NO_AI) { $extra += "-NoAi" }
if ($env:HEXTHINGS_AI_MODEL_SIZE) { $extra += @("-AiModelSize", $env:HEXTHINGS_AI_MODEL_SIZE) }
# Guided install asks before any AI download.
& $hostExecutable -NoProfile -ExecutionPolicy Bypass -File (Join-Path $dir "scripts/install.ps1") @extra
exit $LASTEXITCODE
