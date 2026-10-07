# HexThings management command for Windows PowerShell 5.1+ (and PowerShell 7).
#   hexthings status | start | stop | restart | logs [service] | version | update | patch FILE.zip
#            backup | restore FILE | ai [status|connect] | open | help
# Run from anywhere once the installer has put `hexthings` on your PATH, or: .\scripts\hexthings.ps1 status
# Every docker call checks its exit code. NOT TESTED on Windows: parsed and partly run under PowerShell 7 on Linux.
param([Parameter(Position = 0)][string]$Command = "", [Parameter(Position = 1)][string]$Arg = "",
      [string]$ModelSize = "", [switch]$Force, [switch]$Yes, [switch]$NoBackup, [switch]$Follow, [string]$Workspace = "", [string]$Dir = "backups")
$ErrorActionPreference = "Continue"; $ProgressPreference = "SilentlyContinue"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
if (-not (Test-Path (Join-Path $Root "docker-compose.yml"))) { Write-Host "Cannot find the HexThings folder (docker-compose.yml) above $PSScriptRoot"; exit 1 }
Set-Location $Root
$Repo = if ($env:HEXTHINGS_REPO) { $env:HEXTHINGS_REPO } else { "abhisekrath00-ux/iot-platform" }
$VersionFile = ".hexthings-version"

# ---- look and feel: colour and Unicode only on a real console that supports them -------------------
$Color = (-not $env:NO_COLOR) -and (-not [Console]::IsOutputRedirected)
$Uni = $Color -and ([Console]::OutputEncoding.WebName -match "utf") -and (-not $env:HEXTHINGS_ASCII)
$sym = if ($Uni) { @{ ok = [string][char]0x2714; warn = [string][char]0x25B2; bad = [string][char]0x2716; on = [string][char]0x2588; off = [string][char]0x2591 } } else { @{ ok = "ok"; warn = "!"; bad = "FAIL"; on = "#"; off = "-" } }
function Paint($t, $c) { if ($Color) { Write-Host $t -ForegroundColor $c } else { Write-Host $t } }
function Ok($m)   { if ($Color) { Write-Host "  $($sym.ok)  " -ForegroundColor Green -NoNewline; Write-Host $m } else { Write-Host "  $($sym.ok)  $m" } }
function Warn($m) { if ($Color) { Write-Host "  $($sym.warn)  " -ForegroundColor Yellow -NoNewline; Write-Host $m } else { Write-Host "  $($sym.warn)  $m" } }
function Bad($m)  { if ($Color) { Write-Host "  $($sym.bad)  " -ForegroundColor Red -NoNewline; Write-Host $m } else { Write-Host "  $($sym.bad)  $m" } }
function Bar($n, $t) { $w = 20; $f = [int]($w * $n / $t); ($sym.on * $f) + ($sym.off * ($w - $f)) }
$script:stepNo = 0; $script:stepTotal = 1
function Step($title) { $script:stepNo++; Write-Host ""; Paint "| $($script:stepNo)/$($script:stepTotal)  $title   $(Bar $script:stepNo $script:stepTotal)" Cyan }
function Banner($sub) { Hx-Wordmark $sub }
function Spin($msg, [scriptblock]$work) {  # runs the work, shows elapsed seconds on one line
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $job = Start-Job -ScriptBlock $work
  $frames = if ($Uni) { "|/-\".ToCharArray() } else { "|/-\".ToCharArray() }; $i = 0
  while ($job.State -eq "Running") { if ($Color) { Write-Host -NoNewline ("`r  {0}  {1} ({2}s)   " -f $frames[$i % 4], $msg, [int]$sw.Elapsed.TotalSeconds) }; $i++; Start-Sleep -Milliseconds 200 }
  if ($Color) { Write-Host -NoNewline "`r$(' ' * 78)`r" }
  $out = Receive-Job $job; Remove-Job $job; return $out
}

$uiFile = Join-Path $PSScriptRoot "hx-ui.ps1"
if (Test-Path $uiFile) { . $uiFile } else {
  function Hx-Wordmark($Sub = "", [switch]$Animate) { Write-Host ""; Write-Host "  HexThings  $Sub"; Write-Host "" }
  function Hx-Footer { Write-Host "  Made with <3 by Hexmon Technology" }
  function Hx-Startup($Sub = "") { Hx-Wordmark $Sub; Hx-Footer }
  function Hx-Menu($Title, $Items, $Hints) { return -2 }
}
# ---- helpers -------------------------------------------------------------------------------------
function EnvVal($k, $d) { if (Test-Path .env) { $m = Select-String -Path .env -Pattern "^$k=(.*)$" | Select-Object -First 1; if ($m -and $m.Matches[0].Groups[1].Value.Trim()) { return $m.Matches[0].Groups[1].Value.Trim() } }; return $d }
$WebPort = EnvVal "WEB_PORT" "8080"
$PgUser = EnvVal "POSTGRES_USER" "iot"; $PgDb = EnvVal "POSTGRES_DB" "iot"
function ProfArgs { if (Test-Path "models\model.gguf") { return @("--profile", "ai") } else { return @() } }
function Dc { & docker compose @(ProfArgs) @args | Out-Host; return $LASTEXITCODE }
function NeedDocker {
  if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Bad "Docker is not installed."; exit 1 }
  docker info *> $null; if ($LASTEXITCODE -ne 0) { Bad "Docker is not running. Start Docker Desktop and wait until it says running."; exit 1 }
}
function Installed { if (Test-Path $VersionFile) { return (Get-Content $VersionFile -Raw).Trim() } else { return "unknown (installed before version tracking)" } }
function Healthy { try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 "http://localhost:$WebPort/healthz" | Out-Null; return $true } catch { return $false } }
function WaitHealthy($secs) { foreach ($i in 1..($secs / 2)) { if (Healthy) { return $true }; Start-Sleep 2 }; return $false }
function LatestCommit { try { $r = Invoke-RestMethod -UseBasicParsing -TimeoutSec 8 "https://api.github.com/repos/$Repo/commits/main"; return $r.sha } catch { return $null } }
function DownloadSource($zipPath, $dest) {
  if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
  Expand-Archive $zipPath -DestinationPath $dest -Force
  return (Get-ChildItem $dest | Select-Object -First 1).FullName
}

function Do-Status {
  NeedDocker; $script:stepTotal = 1; Banner "status"
  Step "Services"
  $running = @(docker compose @(ProfArgs) ps --status running --format "{{.Service}}" 2>$null)
  if ($running.Count -eq 0) { Warn "nothing is running. Start it with: hexthings start" } else { foreach ($s in $running) { Ok $s } }
  if (Healthy) { Ok "web answers at http://localhost:$WebPort" } else { Warn "web does not answer at http://localhost:$WebPort" }
  Ok "installed version: $(Installed)"
  if (Test-Path "models\model.gguf") { if ($running -contains "ai-runtime") { Ok "local AI model is running" } else { Warn "local AI model installed but not running" } } else { Warn "no local AI model (hexthings ai connect after adding models\model.gguf, or re-run the installer with AI)" }
  $b = @(Get-ChildItem $Dir -Filter "hexmon-pg-*.dump.gz" -ErrorAction SilentlyContinue | Sort-Object LastWriteTime -Descending)
  if ($b.Count -gt 0) { Ok "last backup: $($b[0].Name) ($($b[0].LastWriteTime))" } else { Warn "no backup yet. Run: hexthings backup" }
}
function Do-Start { NeedDocker; $script:stepTotal = 1; Banner "start"; Step "Starting"; if ((Dc start) -ne 0) { Bad "docker compose start failed"; exit 1 }; if (WaitHealthy 120) { Ok "HexThings is up at http://localhost:$WebPort" } else { Warn "started but the web does not answer yet; check: hexthings logs" } }
function Do-Stop { NeedDocker; $script:stepTotal = 1; Banner "stop"; Step "Stopping (data is kept)"; if ((Dc stop) -ne 0) { Bad "docker compose stop failed"; exit 1 }; Ok "stopped" }
function Do-Restart { NeedDocker; $script:stepTotal = 1; Banner "restart"; Step "Restarting"; if ((Dc restart) -ne 0) { Bad "docker compose restart failed"; exit 1 }; if (WaitHealthy 120) { Ok "HexThings is up" } else { Warn "restarted but the web does not answer yet" } }
function Do-Logs { NeedDocker; $svc = if ($Arg) { $Arg } else { "api" }; if ($Follow) { [void](Dc logs -f --tail 100 $svc) } else { [void](Dc logs --tail 100 $svc) } }
function Do-Version {
  $script:stepTotal = 1; Banner "version"; Step "Version"
  Ok "installed: $(Installed)"
  $l = LatestCommit
  if ($l) { if ((Installed) -like "$l*") { Ok "up to date with GitHub main ($($l.Substring(0,7)))" } else { Warn "newer code on GitHub main: $($l.Substring(0,7)). Run: hexthings update" } }
  else { Warn "could not check for updates (offline or GitHub unreachable). For an offline patch: hexthings patch FILE.zip" }
}
function Do-Backup { NeedDocker; $script:stepTotal = 2; Banner "backup"; BackupCore }
function BackupCore {
  Step "Dumping the database"
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  $stamp = (Get-Date).ToUniversalTime().ToString("yyyyMMddTHHmmssZ"); $name = "hexmon-pg-$stamp.dump.gz"; $out = Join-Path $Dir $name
  & docker compose exec -T postgres sh -c "pg_dump -U $PgUser -Fc $PgDb | gzip -9 > /tmp/hexthings-backup.dump.gz" 2>$null
  if ($LASTEXITCODE -ne 0) { Bad "pg_dump failed. Is HexThings running? (hexthings start)"; exit 1 }
  & docker compose cp postgres:/tmp/hexthings-backup.dump.gz $out 2>$null
  if ($LASTEXITCODE -ne 0 -or -not (Test-Path $out)) { Bad "could not copy the backup out of the container"; exit 1 }
  & docker compose exec -T postgres rm -f /tmp/hexthings-backup.dump.gz 2>$null
  $sha = (Get-FileHash $out -Algorithm SHA256).Hash.ToLower(); $size = (Get-Item $out).Length
  Add-Content -Path (Join-Path $Dir "MANIFEST") -Value "$stamp  $sha  $size bytes"
  Ok "$out ($([math]::Round($size / 1MB, 1)) MB, sha256 $($sha.Substring(0, 12))...)"
  Step "Keeping the last 14"
  Get-ChildItem $Dir -Filter "hexmon-pg-*.dump.gz" | Sort-Object LastWriteTime -Descending | Select-Object -Skip 14 | ForEach-Object { Remove-Item $_.FullName; Ok "rotated out $($_.Name)" }
  Ok "done. Restore with: hexthings restore $out"
}
function Do-Restore {
  NeedDocker; $script:stepTotal = 3; Banner "restore"
  if (-not $Arg -or -not (Test-Path $Arg)) { Bad "usage: hexthings restore <backup.dump.gz>   (file not found: $Arg)"; exit 1 }
  Step "Checking the backup"
  $sha = (Get-FileHash $Arg -Algorithm SHA256).Hash.ToLower()
  $man = Join-Path (Split-Path -Parent (Resolve-Path $Arg)) "MANIFEST"
  if ((Test-Path $man) -and (Select-String -Path $man -Pattern $sha -SimpleMatch -Quiet)) { Ok "checksum matches the MANIFEST" } else { Warn "checksum is not in a MANIFEST next to the file (backup from another machine?)" }
  if (-not $Yes) { $a = Read-Host "  This REPLACES the current database with this backup. Type RESTORE to continue"; if ($a -ne "RESTORE") { Warn "cancelled, nothing changed"; exit 1 } }
  Step "Restoring"
  & docker compose stop api ingest 2>$null | Out-Null
  & docker compose cp $Arg postgres:/tmp/hexthings-restore.dump.gz 2>$null
  if ($LASTEXITCODE -ne 0) { Bad "could not copy the backup into the database container"; exit 1 }
  & docker compose exec -T postgres sh -c "gunzip -c /tmp/hexthings-restore.dump.gz | pg_restore -U $PgUser -d $PgDb --clean --if-exists --no-owner; rc=`$?; rm -f /tmp/hexthings-restore.dump.gz; exit `$rc" 2>&1 | Out-Null
  $rc = $LASTEXITCODE
  if ($rc -ne 0) { Warn "pg_restore reported warnings (exit $rc); often harmless with --clean. Checking the data next" } else { Ok "database restored" }
  Step "Starting services"
  & docker compose start api ingest 2>$null | Out-Null
  if (WaitHealthy 120) { Ok "HexThings is up again" } else { Warn "services started but the web does not answer yet; check: hexthings logs" }
}
function Do-Update([string]$fromZip) {
  NeedDocker; $script:stepTotal = 5; Banner $(if ($fromZip) { "patch" } else { "update" })
  if ($NoBackup) { $script:stepTotal = 4; Step "Backup"; Warn "skipped (-NoBackup)" } else { BackupCore; $script:stepTotal = 5 }
  Step "Getting the new version"
  $newVer = $null
  if ($fromZip) { if (-not (Test-Path $fromZip)) { Bad "file not found: $fromZip"; exit 1 }; $zip = (Resolve-Path $fromZip).Path; $newVer = "patch " + (Get-FileHash $zip -Algorithm SHA256).Hash.Substring(0, 12).ToLower() + " " + (Get-Date -Format s) }
  else {
    $zip = Join-Path ([IO.Path]::GetTempPath()) "hexthings-update.zip"
    try { Invoke-WebRequest -UseBasicParsing "https://codeload.github.com/$Repo/zip/refs/heads/main" -OutFile $zip } catch { Bad "download failed: $($_.Exception.Message). Offline? Use: hexthings patch FILE.zip"; exit 1 }
    $newVer = LatestCommit; if (-not $newVer) { $newVer = "main " + (Get-Date -Format s) }
  }
  $src = DownloadSource $zip (Join-Path ([IO.Path]::GetTempPath()) "hexthings-update-src")
  if (-not $src -or -not (Test-Path (Join-Path $src "docker-compose.yml"))) { Bad "that archive does not look like HexThings (no docker-compose.yml)"; exit 1 }
  # Your data stays: .env, models, backups, install.log and credentials are not in the archive and are never deleted.
  Copy-Item -Path (Join-Path $src "*") -Destination $Root -Recurse -Force
  Ok "files updated (your .env, models and backups are untouched)"
  Step "Rebuilding and restarting"
  $code = Dc up -d --build
  if ($code -ne 0) { Bad "docker compose up failed. Your data is safe; see: hexthings logs. Previous version: $(Installed)"; exit 1 }
  Set-Content -Path $VersionFile -Value $newVer
  Step "Checking it came back"
  if (WaitHealthy 180) { Ok "HexThings answers at http://localhost:$WebPort, now at $newVer" } else { Bad "it did not become healthy in 3 minutes. Run: hexthings logs   (restore the backup with: hexthings restore <file>)"; exit 1 }
}
function Do-Ai {
  if ($Arg -eq "choose" -or $Arg -eq "models") {
    $chooser=Join-Path $PSScriptRoot "model-choose.ps1"
    & $chooser -List
    if ($Arg -eq "models") { return }
    $pick=$ModelSize
    if (-not $pick) { $pick=Read-Host "Model ID, or skip (keeps current model)" }
    if (-not $pick -or $pick -eq "skip") { return }
    try { & $chooser -Size $pick -Force:$Force; Ok "Model installed; use hexthings ai stop then ai start to reload it" } catch { Bad "Model unchanged or download failed: $_" }
    return
  }
  NeedDocker; $script:stepTotal = 1; Banner "local AI"; Step "Local AI assistant"
  if ($Arg -eq "stop") {
    if ((Dc stop ai-runtime) -ne 0) { Bad "could not stop the AI runtime"; exit 1 }
    $still = @(docker compose --profile ai ps --status running --format "{{.Service}}" 2>$null) -contains "ai-runtime"
    if ($still) { Bad "ai-runtime is STILL running after stop. Try: docker compose --profile ai stop ai-runtime"; exit 1 }
    Ok "AI model stopped (verified: ai-runtime is not running). The platform keeps running; the assistant answers 'no model' until: hexthings ai start"
  } elseif ($Arg -eq "start") {
    if (-not (Test-Path "models\model.gguf")) { Bad "no models\model.gguf, nothing to start"; exit 1 }
    $env:AI_MODEL_SHA256 = (Get-FileHash "models\model.gguf" -Algorithm SHA256).Hash.ToLower()
    if (Test-Path "models/model.id") { $env:AI_MODEL_NAME=(Get-Content "models/model.id" -TotalCount 1).Trim() }
    if ((Dc up -d --force-recreate ai-runtime) -ne 0) { Bad "could not start the AI runtime"; exit 1 }
    Start-Sleep -Seconds 3
    $upNow = @(docker compose --profile ai ps --status running --format "{{.Service}}" 2>$null) -contains "ai-runtime"
    if ($upNow) { Ok "AI model started (verified running). The first answer is slow while it loads, about 10-60 seconds" } else { Bad "ai-runtime did not stay running. See: hexthings logs ai-runtime"; exit 1 }
  } elseif ($Arg -eq "connect") {
    if (-not (Test-Path "models\model.gguf")) { Bad "no models\model.gguf. Re-run the installer with AI (get.ps1) or copy a .gguf there first."; exit 1 }
    if (-not $Workspace -and (Test-Path install-credentials.txt)) { $m = Select-String -Path install-credentials.txt -Pattern "^Workspace:\s*(\S+)" | Select-Object -First 1; if ($m) { $Workspace = $m.Matches[0].Groups[1].Value } }
    if (-not $Workspace) { Bad "tell me the workspace: hexthings ai connect -Workspace my-plant"; exit 1 }
    $env:AI_MODEL_SHA256 = (Get-FileHash "models\model.gguf" -Algorithm SHA256).Hash.ToLower()
    if (Test-Path "models/model.id") { $env:AI_MODEL_NAME=(Get-Content "models/model.id" -TotalCount 1).Trim() }
    if ((Dc up -d --force-recreate ai-runtime) -ne 0) { Bad "could not start the AI runtime"; exit 1 }
    & docker compose exec -T api /bin/tenantctl ai-connect --tenant $Workspace --model $(if (Test-Path "models/model.id") { (Get-Content "models/model.id" -TotalCount 1).Trim() } else { "qwen3-1.7b" }) 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Ok "assistant for '$Workspace' connected to the local model" } else { Bad "could not connect the assistant" }
  } elseif ($Arg -and $Arg -ne "status") {
    Bad "unknown ai command '$Arg'. Use: hexthings ai stop | start | status | connect"; exit 1
  } else {
    if (Test-Path "models\model.gguf") { Ok "model file present ($([math]::Round((Get-Item 'models\model.gguf').Length / 1GB, 2)) GB)" } else { Warn "no model file" }
    $up = @(docker compose --profile ai ps --status running --format "{{.Service}}" 2>$null) -contains "ai-runtime"
    if ($up) { Ok "ai-runtime is running" } else { Warn "ai-runtime is not running" }
  }
}
$toolsFile = Join-Path $PSScriptRoot "hx-tools.ps1"
if (Test-Path $toolsFile) { . $toolsFile } else { function Do-Menu { Do-Help } }
function Do-Help {
  Banner "management"
  Write-Host "  hexthings status               what is running, version, last backup"
  Write-Host "  hexthings start | stop | restart   (stop also stops the AI model)"
  Write-Host "  hexthings ai stop | start | status   (only the AI model; the platform keeps running)"
  Write-Host "  hexthings logs [service] [-Follow]"
  Write-Host "  hexthings version              installed version and whether a newer one exists"
  Write-Host "  hexthings update [-NoBackup]   backup, download the latest, rebuild, check health"
  Write-Host "  hexthings patch FILE.zip       same, from a file (air-gapped machines)"
  Write-Host "  hexthings backup [-Dir D]      database backup with checksum, keeps the last 14"
  Write-Host "  hexthings restore FILE [-Yes]  restore a backup (replaces current data)"
  Write-Host "  hexthings ai models | choose [-ModelSize ID] [-Force]  choose/change model; skip available"
  Write-Host "  hexthings open                 open the web app in your browser"
  Write-Host ""
  Write-Host "  hexthings                      interactive menu (arrows or numbers)"
  Write-Host "  hexthings dash                 live dashboard: CPU, memory, alerts, database"
  Write-Host "  hexthings diag                 diagnostics: docker, services, ports, database, disk, logs"
  Write-Host "  hexthings support              support zip with versions, state and redacted logs"
  Write-Host "  hexthings prune | vacuum | reindex   maintenance"
  Write-Host "  hexthings shell [service]      shell inside a container"
  Write-Host "  hexthings apitest | info       health checks with timings / versions"
  Write-Host ""
  Hx-Footer
}
switch ($Command.ToLower()) {
  "status" { Do-Status } "start" { Do-Start } "stop" { Do-Stop } "restart" { Do-Restart } "logs" { Do-Logs }
  "version" { Do-Version } "update" { Do-Update "" } "patch" { if (-not $Arg) { Bad "usage: hexthings patch FILE.zip"; exit 1 }; Do-Update $Arg }
  "backup" { Do-Backup } "restore" { Do-Restore } "ai" { Do-Ai } "open" { Start-Process "http://localhost:$WebPort" }
  "" { Do-Menu } "menu" { Do-Menu } "dash" { Do-Dash } "dashboard" { Do-Dash }
  "diag" { Do-Diagnose } "diagnose" { Do-Diagnose } "diagnostics" { Do-Diagnose } "support" { Do-Support }
  "prune" { Do-Prune } "vacuum" { Do-Vacuum } "reindex" { Do-Reindex } "shell" { Do-Shell } "apitest" { Do-ApiTest } "info" { Do-Info }
  default { Do-Help }
}
