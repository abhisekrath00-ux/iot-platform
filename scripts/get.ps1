# One-command HexThings setup for Windows 10/11. Checks what is needed (WSL2, Docker Desktop, a running engine, RAM, disk),
# installs ONLY what is missing after one yes/no question (one Windows admin prompt), then installs and starts HexThings. In PowerShell:
#   irm https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.ps1 | iex
# Downloads the source to .\hexthings (updating the files in place if the folder already exists; your
# .env, secrets and install.log are kept), then runs the guided installer with defaults.
$ErrorActionPreference = "Stop"
Set-Location $env:USERPROFILE   # never sit inside the folder we may replace or remove
$repo = if ($env:HEXTHINGS_REPO) { $env:HEXTHINGS_REPO } else { "abhisekrath00-ux/iot-platform" }
$dir = if ($env:HEXTHINGS_DIR) { $env:HEXTHINGS_DIR } else { Join-Path $env:USERPROFILE "hexthings" }   # fixed location, never relative to where you run this
$zip = Join-Path $env:TEMP "hexthings.zip"
$tmp = Join-Path $env:TEMP "hexthings-src"
if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -UseBasicParsing "https://codeload.github.com/$repo/zip/refs/heads/main" -OutFile $zip
Expand-Archive $zip -DestinationPath $tmp -Force
$src = (Get-ChildItem $tmp | Select-Object -First 1).FullName
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Copy-Item -Path (Join-Path $src "*") -Destination $dir -Recurse -Force
# an earlier version could nest a copy as hexthings\hexthings: remove that stale copy
$nested = Join-Path $dir "hexthings"
if (Test-Path (Join-Path $nested "docker-compose.yml")) { try { Remove-Item -Recurse -Force $nested -ErrorAction Stop } catch { Write-Host "  note: could not remove the old nested copy $nested (in use?); it is harmless, delete it later." } }
Set-Location $dir
# Prerequisites: skips whatever is present, asks once before installing anything missing.
$pre = if ($env:HEXTHINGS_YES) { @("-Yes") } else { @() }
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\prereq.ps1 @pre
if ($LASTEXITCODE -ne 0) { Write-Host "Stopped before installing HexThings (see above)."; return }
# Update: record the version of the files we just downloaded
try { $env:HEXTHINGS_VERSION = (Invoke-RestMethod -UseBasicParsing -TimeoutSec 8 "https://api.github.com/repos/$repo/commits/main").sha } catch { }
$extra = @(); if ($env:HEXTHINGS_NO_AI) { $extra += "-NoAi" }
if ($env:HEXTHINGS_AI_MODEL_SIZE) { $extra += @("-AiModelSize", $env:HEXTHINGS_AI_MODEL_SIZE) }
# Guided install asks before any AI download.
powershell -ExecutionPolicy Bypass -File scripts\install.ps1 @extra
