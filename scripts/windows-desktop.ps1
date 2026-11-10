# Public HexThings Windows 11 x64 bootstrap (PowerShell 5.1+).
# Run: powershell -NoProfile -ExecutionPolicy Bypass -File windows-desktop.ps1
# No purchases, tokens, automatic reboot or machine-wide execution-policy changes.
param([switch]$Yes, [switch]$Check, [string]$InstallDir = "", [switch]$NoMenu)
$ErrorActionPreference = 'Stop'
function Assert-WindowsHost($Facts) {
  if ($Facts.ProductType -ne 1 -or $Facts.Build -lt 22631) { throw 'Requires Windows 11 23H2 or newer client OS. Windows Server/older Windows are refused.' }
  if ($Facts.Arch -ne 'AMD64') { throw 'Requires Intel/AMD x64 Windows. ARM is not verified.' }
  if ($Facts.MemMB -lt 8192) { throw 'At least 8GiB system RAM is required for Docker Desktop.' }
  if ($Facts.DiskMB -lt 20480) { throw 'At least 20GiB free space is required on the installation drive; more may be needed for images.' }
}
function Assert-LoopbackCompose($Config) {
  if (-not $Config.services.web.ports) { throw 'Downloaded source is missing web port bindings.' }
  foreach ($service in $Config.services.PSObject.Properties) {
    if ($service.Value.network_mode -eq 'host') { throw "Unsafe host networking: $($service.Name)" }
    foreach ($port in $service.Value.ports) { if ($port.host_ip -ne '127.0.0.1') { throw "Unsafe public binding: $($service.Name)" } }
  }
}
function Complete-WindowsSetup($SourceDir, $Unattended) {
  Write-Host "Reopen the terminal menu: powershell -NoProfile -ExecutionPolicy Bypass -File `"$SourceDir\scripts\hexthings.ps1`""
  if (-not $Unattended -and -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected) {
    Write-Host 'Setup succeeded. Opening the animated HexThings menu. Select Quit to leave.'
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $SourceDir 'scripts/hexthings.ps1') | Out-Host
    if ($LASTEXITCODE -ne 0) { Write-Warning 'HexThings installed successfully, but its menu exited with an error. Use the command above to reopen it.' }
  }
}
function Main {
  if ($env:OS -ne 'Windows_NT') { throw 'Run this script from Windows PowerShell, not Linux/macOS.' }
  if (-not $InstallDir) { $script:InstallDir = Join-Path $env:LOCALAPPDATA 'HexThings' }
  $script:InstallDir = [IO.Path]::GetFullPath($InstallDir)
  $os = Get-CimInstance Win32_OperatingSystem
  $drive = Get-PSDrive -Name $InstallDir.Substring(0,1)
  Assert-WindowsHost ([pscustomobject]@{ProductType=[int]$os.ProductType;Build=[int]$os.BuildNumber;Arch=$env:PROCESSOR_ARCHITECTURE;MemMB=[int64]((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory/1MB);DiskMB=[int64]($drive.Free/1MB)})
  $marker = Join-Path $InstallDir '.hexthings-bootstrap-source'
  if (Test-Path $InstallDir) {
    if (-not (Test-Path $marker) -or (Get-Content $marker -Raw).Trim() -ne 'abhisekrath00-ux/iot-platform') { throw 'Destination already exists and is not owned by this bootstrap. No overwrite attempted. Choose -InstallDir NEWPATH.' }
  }
  Write-Host "Destination: $InstallDir"
  Write-Host 'Checks WSL2 and Docker Desktop, offers missing prerequisites, then builds HexThings. A reboot or sign-out may be required; rerun the same command afterwards.'
  Write-Host 'Docker Desktop has separate license terms. Paid subscriptions may be required for larger organizations/government. This script never buys a subscription or accepts terms for you.'
  Write-Host 'AI stays off; service ports stay on 127.0.0.1. No public TLS endpoint is configured.'
  if ($Check) { Write-Host 'Check only: OS/resources/destination checked; no download/install. Docker prerequisites are checked after source download during setup.'; return 0 }
  if (-not $Yes) {
    if ([Console]::IsInputRedirected) { throw 'Run in an interactive PowerShell terminal, or use -Yes for defaults (Docker terms still require your own approval).' }
    if ((Read-Host 'Download source and continue? [y/N]') -notmatch '^(y|yes)$') { return 2 }
  }
  if (-not (Test-Path $InstallDir)) {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('hexthings-' + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
      $zip = Join-Path $tmp 'source.zip'
      Invoke-WebRequest -UseBasicParsing -Uri 'https://codeload.github.com/abhisekrath00-ux/iot-platform/zip/refs/heads/main' -OutFile $zip
      Expand-Archive -LiteralPath $zip -DestinationPath (Join-Path $tmp 'source')
      $roots = @(Get-ChildItem (Join-Path $tmp 'source') -Directory)
      if ($roots.Count -ne 1) { throw 'Unexpected source archive layout.' }
      foreach ($file in @('docker-compose.yml','.env.example','scripts/prereq.ps1','scripts/install.ps1','scripts/hexthings.ps1')) {
        if (-not (Test-Path (Join-Path $roots[0].FullName $file) -PathType Leaf)) { throw "Incomplete source archive: $file" }
      }
      Move-Item -LiteralPath $roots[0].FullName -Destination $InstallDir
      Set-Content -LiteralPath $marker -Value 'abhisekrath00-ux/iot-platform'
    } finally { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
  }
  foreach ($file in @('docker-compose.yml','.env.example','scripts/prereq.ps1','scripts/install.ps1','scripts/hexthings.ps1')) { if (-not (Test-Path (Join-Path $InstallDir $file))) { throw "Existing source is incomplete: $file. No overwrite attempted." } }
  $flags = @(); if ($Yes) { $flags += '-Yes' }
  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $InstallDir 'scripts/prereq.ps1') @flags | Out-Host
  if ($LASTEXITCODE -ne 0) { Write-Host 'Prerequisite setup stopped. If a restart was requested, restart Windows and rerun this same command.'; return $LASTEXITCODE }
  Set-Location $InstallDir
  $engine = (& docker info --format '{{.OSType}}') -join ''
  if ($LASTEXITCODE -ne 0 -or $engine.Trim() -ne 'linux') { throw 'Docker must be running Linux containers (WSL2 backend). Switch Docker Desktop to Linux containers and rerun.' }
  $raw = (& docker compose --env-file .env.example config --format json) -join "`n"
  if ($LASTEXITCODE -ne 0) { throw 'Could not validate downloaded Compose configuration.' }
  Assert-LoopbackCompose ($raw | ConvertFrom-Json)
  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $InstallDir 'scripts/install.ps1') -NoAi @flags | Out-Host
  if ($LASTEXITCODE -ne 0) { Write-Host 'Installation stopped. Existing started services may still be running; inspect install.log and docker compose ps.'; return $LASTEXITCODE }
  Complete-WindowsSetup $InstallDir ($Yes -or $NoMenu)
  return 0
}
if ($MyInvocation.InvocationName -ne '.') { try { $code = Main; exit $code } catch { Write-Error $_; exit 1 } }
