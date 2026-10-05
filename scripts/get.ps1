# One-command HexThings setup for Windows 10/11 (needs Docker Desktop with WSL2). In PowerShell:
#   irm https://raw.githubusercontent.com/abhisekrath00-ux/iot-platform/main/scripts/get.ps1 | iex
# Downloads the source to .\hexthings, then runs the guided installer with defaults.
# NOT TESTED: no Windows machine was available when this was written.
$ErrorActionPreference = "Stop"
$repo = if ($env:HEXTHINGS_REPO) { $env:HEXTHINGS_REPO } else { "abhisekrath00-ux/iot-platform" }
$dir = if ($env:HEXTHINGS_DIR) { $env:HEXTHINGS_DIR } else { "hexthings" }
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host "Docker is not installed. Install Docker Desktop (WSL2 backend), start it, then run this again."; return }
if (-not (Test-Path "$dir\docker-compose.yml")) {
  $zip = Join-Path $env:TEMP "hexthings.zip"
  Invoke-WebRequest -UseBasicParsing "https://codeload.github.com/$repo/zip/refs/heads/main" -OutFile $zip
  $tmp = Join-Path $env:TEMP "hexthings-src"; if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
  Expand-Archive $zip -DestinationPath $tmp
  Move-Item (Get-ChildItem $tmp | Select-Object -First 1).FullName $dir
}
Set-Location $dir
powershell -ExecutionPolicy Bypass -File scripts\install.ps1 -Yes
