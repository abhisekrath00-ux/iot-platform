# HexThings Windows server removal. Run in PowerShell, not CMD.
# Deletes only the verified installation and its Docker project data.
param([string]$InstallDir = '', [switch]$Check)
$ErrorActionPreference = 'Stop'
function Read-DockerJson([string[]]$Arguments) {
  $text = (& docker @Arguments) -join "`n"
  if ($LASTEXITCODE -ne 0) { throw ('Docker check failed: ' + ($Arguments -join ' ')) }
  if (-not $text.Trim()) { return @() }
  return ($text | ConvertFrom-Json)
}
function Normalize-Path([string]$Value) { return [IO.Path]::GetFullPath($Value).TrimEnd('\','/').ToLowerInvariant() }
function Assert-Root([string]$Value) {
  $p = Normalize-Path $Value
  $blocked = @([IO.Path]::GetPathRoot($p), $HOME, $env:USERPROFILE, $env:LOCALAPPDATA, $env:APPDATA, $env:ProgramData, $env:ProgramFiles, $env:windir)
  foreach ($b in $blocked) { if ($b -and $p -eq (Normalize-Path $b)) { throw 'Refusing a drive, user, Windows or shared application folder.' } }
  foreach ($f in @('docker-compose.yml','scripts\hexthings.ps1','scripts\install.ps1','server','web')) {
    if (-not (Test-Path -LiteralPath (Join-Path $p $f))) { throw "Not a recognized HexThings server source folder: missing $f" }
  }
  $item = Get-Item -LiteralPath $p -Force
  while ($item) {
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Installation path contains a link/junction. Refusing removal.' }
    $item = $item.Parent
  }
  foreach ($item in Get-ChildItem -LiteralPath $p -Force -Recurse) {
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked file/folder found: $($item.FullName). Refusing removal." }
  }
  return $p
}
function Get-Plan([string]$Root) {
  $ids = @(& docker ps -aq)
  if ($LASTEXITCODE -ne 0) { throw 'Docker Desktop must be running in Linux-container mode. No files removed.' }
  $all = @(); if ($ids.Count) { $all = @(Read-DockerJson (@('inspect') + $ids)) }
  $matching = @($all | Where-Object {
    $wd = $_.Config.Labels.'com.docker.compose.project.working_dir'
    $wd -and (Normalize-Path $wd) -eq $Root
  })
  $projects = @($matching | ForEach-Object { $_.Config.Labels.'com.docker.compose.project' } | Where-Object { $_ } | Sort-Object -Unique)
  if ($projects.Count -ne 1) { throw 'Cannot identify one Docker project for this folder. No deletion attempted. Keep the folder and request help with Docker project detection.' }
  $project = $projects[0]
  $containers = @($all | Where-Object { $_.Config.Labels.'com.docker.compose.project' -eq $project })
  $services = @($containers | ForEach-Object { $_.Config.Labels.'com.docker.compose.service' })
  foreach ($required in @('api','web','postgres')) { if ($required -notin $services) { throw 'Expected HexThings API/web/database containers are missing. Refusing guessed cleanup.' } }
  foreach ($service in $services) { if ($service -notin @('postgres','mosquitto','api','ingest','elasticsearch','mcp','web','ai-runtime','redis')) { throw "Unexpected service in project: $service. Refusing removal." } }
  foreach ($c in $containers) {
    $wd = $c.Config.Labels.'com.docker.compose.project.working_dir'
    if (-not $wd -or (Normalize-Path $wd) -ne $Root) { throw "Project name is shared by another folder: $project. Refusing removal." }
    $cf = $c.Config.Labels.'com.docker.compose.project.config_files'
    if (-not $cf -or @($cf -split ',' | Where-Object { (Normalize-Path $_) -ne (Join-Path $Root 'docker-compose.yml') }).Count) { throw 'Custom/override Compose configuration detected. Refusing automatic removal.' }
  }
  $vn = @(& docker volume ls -q --filter "label=com.docker.compose.project=$project")
  if ($LASTEXITCODE -ne 0) { throw 'Cannot list project volumes.' }
  $volumes = @(); if ($vn.Count) { $volumes = @(Read-DockerJson (@('volume','inspect') + $vn)) }
  $nn = @(& docker network ls -q --filter "label=com.docker.compose.project=$project")
  if ($LASTEXITCODE -ne 0) { throw 'Cannot list project networks.' }
  $networks = @(); if ($nn.Count) { $networks = @(Read-DockerJson (@('network','inspect') + $nn)) }
  $ownedIds = @($containers | ForEach-Object { $_.Id })
  foreach ($v in $volumes) {
    foreach ($c in $all) {
      if (@($c.Mounts | Where-Object { $_.Type -eq 'volume' -and $_.Name -eq $v.Name }).Count -and $c.Id -notin $ownedIds) { throw "Volume used by another container: $($v.Name). Refusing removal." }
    }
  }
  foreach ($n in $networks) {
    foreach ($id in @($n.Containers.PSObject.Properties.Name)) { if ($id -and $id -notin $ownedIds) { throw "Network used by another container: $($n.Name). Refusing removal." } }
  }
  foreach ($c in $containers) {
    foreach ($m in $c.Mounts) {
      if ($m.Type -eq 'volume' -and $m.Name -notin @($volumes | ForEach-Object { $_.Name })) { throw "External/unowned volume attached: $($m.Name). Refusing removal." }
      if ($m.Type -eq 'bind') {
        # Docker Desktop can translate a Windows bind source into a Linux path.
        $source = [string]$m.Source
        if ($source -match '^/(run/desktop/mnt/host|host_mnt)/([a-z])/(.*)$') { $source = $Matches[2] + ':\' + $Matches[3].Replace('/','\') }
        $src = Normalize-Path $source
        if ($src -ne $Root -and -not $src.StartsWith($Root + '\')) { throw "Bind-mounted data outside the install folder: $source. Refusing removal." }
      }
    }
  }
  return [pscustomobject]@{ Project=$project; Containers=$containers; Volumes=$volumes; Networks=$networks }
}
function Invoke-Removal {
  if ($env:OS -ne 'Windows_NT') { throw 'Run this script on the Windows server PC.' }
  if (-not $InstallDir) { $script:InstallDir = Read-Host "HexThings install folder (normally $env:LOCALAPPDATA\HexThings)" }
  $root = Assert-Root $InstallDir
  $engine = (& docker info --format '{{.OSType}}') -join ''
  if ($LASTEXITCODE -ne 0 -or $engine.Trim() -ne 'linux') { throw 'Start Docker Desktop with Linux containers first. Nothing removed.' }
  $plan = Get-Plan $root
  Write-Host "Installation: $root"
  Write-Host "Docker project: $($plan.Project)"
  Write-Host "Containers: $((@($plan.Containers | ForEach-Object { $_.Name })) -join ', ')"
  Write-Host "DELETE volumes/database/history: $((@($plan.Volumes | ForEach-Object { $_.Name })) -join ', ')"
  Write-Host "DELETE folder including .env, local backups, logs, models and source: $root"
  Write-Host 'Project networks and this folder\scripts user-PATH entry are removed too.'
  Write-Host 'Docker Desktop, WSL, shared images/build cache, external backups/downloads and other apps are kept.'
  Write-Host 'This is deletion, not secure disk erasure. Back up outside this folder first if needed.'
  if ($Check) { Write-Host 'Check only. Nothing removed.'; return }
  if ((Read-Host 'Type REMOVE HEXTHINGS to permanently delete the items above') -cne 'REMOVE HEXTHINGS') { throw 'Cancelled. Nothing removed.' }
  # Recheck ownership and sharing immediately before the first mutation.
  $root = Assert-Root $root
  $again = Get-Plan $root
  if ($again.Project -ne $plan.Project) { throw 'Docker project changed. Nothing removed.' }
  $plan = $again
  $ports = @($plan.Containers | ForEach-Object { $_.NetworkSettings.Ports.PSObject.Properties | ForEach-Object { $_.Value | ForEach-Object { $_.HostPort } } } | Where-Object { $_ } | Sort-Object -Unique)
  foreach ($c in $plan.Containers) { & docker rm -f $c.Id; if ($LASTEXITCODE -ne 0) { throw 'Container removal failed. Folder and remaining data retained; cleanup is partial.' } }
  foreach ($v in $plan.Volumes) { & docker volume rm $v.Name; if ($LASTEXITCODE -ne 0) { throw 'Volume removal failed. Folder retained; cleanup is partial.' } }
  foreach ($n in $plan.Networks) { & docker network rm $n.Id; if ($LASTEXITCODE -ne 0) { throw 'Network removal failed. Folder retained; cleanup is partial.' } }
  $remaining = @(& docker ps -aq --filter "label=com.docker.compose.project=$($plan.Project)")
  if ($LASTEXITCODE -ne 0 -or $remaining.Count) { throw 'Project containers still present or verification failed. Folder retained.' }
  foreach ($kind in @('volume','network')) {
    $remaining = @(& docker $kind ls -q --filter "label=com.docker.compose.project=$($plan.Project)")
    if ($LASTEXITCODE -ne 0 -or $remaining.Count) { throw "Project $kind resources still present or verification failed. Folder retained." }
  }
  $bin = Normalize-Path (Join-Path $root 'scripts')
  $path = [string][Environment]::GetEnvironmentVariable('Path','User')
  $kept = @($path -split ';' | Where-Object { $_ -and (Normalize-Path ([Environment]::ExpandEnvironmentVariables($_))) -ne $bin })
  [Environment]::SetEnvironmentVariable('Path',($kept -join ';'),'User')
  Set-Location $env:TEMP
  $root = Assert-Root $root
  Remove-Item -LiteralPath $root -Recurse -Force
  if (Test-Path -LiteralPath $root) { throw 'Installation folder remains. Cleanup is partial.' }
  Write-Host 'Verified: selected HexThings folder, project containers, data volumes and networks removed. Open a new terminal for the PATH change.'
  $busy = @()
  try { $busy = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { [string]$_.LocalPort -in $ports } | Select-Object LocalAddress,LocalPort,OwningProcess -Unique) }
  catch { Write-Warning 'Could not verify remaining TCP listeners. Port release is unverified; no process was killed.'; return }
  if ($busy.Count) { Write-Warning 'These formerly published ports still have listeners. They may belong to Docker forwarding or another app; no unrelated process was killed.'; $busy | Format-Table | Out-Host }
  else { Write-Host 'Verified: no TCP listener remains on the ports published by the removed containers.' }
}
if ($MyInvocation.InvocationName -ne '.') { try { Invoke-Removal } catch { Write-Error $_; exit 1 } }
