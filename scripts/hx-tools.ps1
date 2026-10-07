# HexThings diagnostics, maintenance, dev tools and the live terminal dashboard.
# Dot-sourced by hexthings.ps1 (it provides Dc, Ok, Warn, Bad, Step, Paint, NeedDocker, $WebPort, $PgUser, $PgDb, $Dir).
# Windows PowerShell 5.1 safe. Everything here reads real state from docker, the database and the web port.

function Hx-TcpOpen([string]$h, [int]$port, [int]$ms = 1500) {
  try {
    $c = New-Object System.Net.Sockets.TcpClient
    $iar = $c.BeginConnect($h, $port, $null, $null)
    $ok = $iar.AsyncWaitHandle.WaitOne($ms, $false) -and $c.Connected
    $c.Close(); return [bool]$ok
  } catch { return $false }
}
function Hx-Psql([string]$sql) {
  $out = & docker compose exec -T postgres psql -U $PgUser -d $PgDb -At -c $sql 2>$null
  if ($LASTEXITCODE -ne 0) { return $null }
  return (($out | Out-String).Trim())
}
function Hx-Running { return @(docker compose --profile ai ps --status running --format "{{.Service}}" 2>$null) }

# Hide anything that looks like a secret. Used for .env and for log text in the support bundle.
function Hx-Redact([string]$text) {
  if (-not $text) { return $text }
  $t = [regex]::Replace($text, '(?im)^([A-Z0-9_]*(PASS|PASSWORD|SECRET|KEY|TOKEN|DSN)[A-Z0-9_]*)\s*=.*$', '$1=<redacted>')
  $t = [regex]::Replace($t, '(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._\-]+', '$1<redacted>')
  $t = [regex]::Replace($t, '(?i)(postgres(ql)?://[^:/\s]+:)[^@\s]+@', '$1<redacted>@')
  $t = [regex]::Replace($t, '(?i)((password|passwd|secret|token|api[_-]?key)["''\s:=]+)[^\s"'',;]{6,}', '$1<redacted>')
  $t = [regex]::Replace($t, '\bsk-[A-Za-z0-9_\-]{12,}', 'sk-<redacted>')
  return $t
}

function Do-Diagnose {
  NeedDocker; $script:stepTotal = 5; Banner "diagnostics"
  $bad = 0; $warn = 0
  Step "Docker and services"
  $v = (& docker version --format "{{.Server.Version}}" 2>$null); if ($v) { Ok "docker engine $v" } else { Bad "docker engine not answering"; $bad++ }
  $expected = @("postgres", "mosquitto", "api", "ingest", "web")
  $run = Hx-Running
  foreach ($s in $expected) { if ($run -contains $s) { Ok "$s is running" } else { Bad "$s is NOT running"; $bad++ } }
  foreach ($s in @("redis", "elasticsearch", "mcp", "ai-runtime")) { if ($run -contains $s) { Ok "$s is running" } else { Warn "$s is not running (optional)"; $warn++ } }
  $restarts = @(docker compose ps --format "{{.Service}} {{.Status}}" 2>$null | Where-Object { $_ -match "Restarting" })
  foreach ($r in $restarts) { Bad "restart loop: $r"; $bad++ }
  Step "Ports"
  foreach ($p in @(@("web", $WebPort), @("mqtt broker", 1883), @("postgres", 5432), @("api", 8000))) {
    if (Hx-TcpOpen "127.0.0.1" ([int]$p[1])) { Ok "$($p[0]) answers on port $($p[1])" } else { Warn "$($p[0]) does not answer on port $($p[1])"; $warn++ }
  }
  Step "Database and web health"
  $r = Hx-Psql "select 1"; if ($r -eq "1") { Ok "database answers" } else { Bad "database does not answer"; $bad++ }
  $sz = Hx-Psql "select pg_size_pretty(pg_database_size(current_database()))"; if ($sz) { Ok "database size $sz" }
  $cn = Hx-Psql "select count(*) from pg_stat_activity where datname = current_database()"; if ($cn) { Ok "$cn database connections" }
  if (Healthy) { Ok "web health check passes" } else { Bad "web health check fails at http://localhost:$WebPort/healthz"; $bad++ }
  Step "Machine"
  try { $d = Get-PSDrive -Name ((Get-Location).Path.Substring(0, 1)) -ErrorAction Stop; $free = [math]::Round($d.Free / 1GB, 1); if ($free -lt 5) { Bad "only $free GB free on this drive"; $bad++ } elseif ($free -lt 15) { Warn "$free GB free on this drive"; $warn++ } else { Ok "$free GB free on this drive" } } catch { Warn "could not read free disk space" }
  try { $resp = Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 "http://localhost:$WebPort/healthz"; $hd = $resp.Headers["Date"]; if ($hd) { $skew = [math]::Abs(([DateTime]::Parse($hd).ToUniversalTime() - [DateTime]::UtcNow).TotalSeconds); if ($skew -gt 120) { Warn "clock differs from the server by $([int]$skew) s"; $warn++ } else { Ok "clock agrees with the server (within $([int]$skew) s)" } } } catch { }
  if (Test-Path "models\model.gguf") { if ($run -contains "ai-runtime") { Ok "local AI runtime running" } else { Warn "local AI model installed but the runtime is stopped (hexthings ai start)"; $warn++ } }
  Step "Recent errors in the logs (last 300 lines per service)"
  foreach ($s in $run) {
    $lines = @(docker compose --profile ai logs --no-color --tail 300 $s 2>$null)
    $n = @($lines | Where-Object { $_ -match "(?i)\b(error|panic|fatal)\b" }).Count
    if ($n -gt 0) { Warn "$s : $n error lines. See: hexthings logs $s"; $warn++ } else { Ok "$s : no error lines" }
  }
  Write-Host ""
  if ($bad -gt 0) { Bad "$bad problem(s), $warn warning(s). A support bundle helps: hexthings support" } elseif ($warn -gt 0) { Warn "no problems, $warn warning(s)" } else { Ok "everything checks out" }
}

# Support bundle: versions, service state, recent logs and redacted configuration in one zip. No secrets.
function Do-Support {
  NeedDocker; $script:stepTotal = 2; Banner "support bundle"
  Step "Collecting"
  $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
  $tmp = Join-Path ([IO.Path]::GetTempPath()) "hexthings-support-$stamp"
  New-Item -ItemType Directory -Force $tmp | Out-Null
  Set-Content -Path (Join-Path $tmp "versions.txt") -Value ("hexthings: " + (Installed) + "`r`n" + ((& docker version 2>&1) | Out-String) + ((& docker compose version 2>&1) | Out-String))
  Set-Content -Path (Join-Path $tmp "services.txt") -Value ((& docker compose --profile ai ps -a 2>&1) | Out-String)
  Set-Content -Path (Join-Path $tmp "docker-info.txt") -Value (Hx-Redact (((& docker info 2>&1) | Out-String)))
  if (Test-Path ".env") { Set-Content -Path (Join-Path $tmp "env-redacted.txt") -Value (Hx-Redact ((Get-Content .env -Raw))) }
  $logdir = Join-Path $tmp "logs"; New-Item -ItemType Directory -Force $logdir | Out-Null
  foreach ($s in (docker compose --profile ai ps -a --format "{{.Service}}" 2>$null)) {
    $txt = ((& docker compose --profile ai logs --no-color --tail 1000 $s 2>&1) | Out-String)
    Set-Content -Path (Join-Path $logdir "$s.log") -Value (Hx-Redact $txt)
  }
  Ok "collected versions, service state, redacted .env and the last 1000 log lines per service"
  Step "Zipping"
  $zip = Join-Path (Get-Location).Path "hexthings-support-$stamp.zip"
  Compress-Archive -Path (Join-Path $tmp "*") -DestinationPath $zip -Force
  Remove-Item -Recurse -Force $tmp
  Ok "wrote $zip ($([math]::Round((Get-Item $zip).Length / 1KB)) KB)"
  Warn "secrets in .env and common secret patterns in logs are redacted, but read the files before sharing them"
}

function Do-Prune {
  NeedDocker; $script:stepTotal = 1; Banner "prune"; Step "Unused Docker images and build cache"
  if (-not $Yes) { $a = Read-Host "  Remove dangling images and old build cache? Your data volumes are NOT touched. Type yes"; if ($a -ne "yes") { Warn "cancelled"; return } }
  & docker image prune -f | Out-Host
  & docker builder prune -f | Out-Host
  Ok "pruned (data volumes untouched)"
}
function Do-Vacuum {
  NeedDocker; $script:stepTotal = 1; Banner "vacuum"; Step "Database VACUUM (ANALYZE)"
  & docker compose exec -T postgres psql -U $PgUser -d $PgDb -c "VACUUM (ANALYZE)" 2>&1 | Out-Host
  if ($LASTEXITCODE -eq 0) { Ok "vacuum finished" } else { Bad "vacuum failed" }
}
function Do-Reindex {
  NeedDocker; $script:stepTotal = 1; Banner "reindex"; Step "Database REINDEX (can lock tables briefly)"
  if (-not $Yes) { $a = Read-Host "  Reindex the whole database now? Type yes"; if ($a -ne "yes") { Warn "cancelled"; return } }
  & docker compose exec -T postgres psql -U $PgUser -d $PgDb -c "REINDEX DATABASE $PgDb" 2>&1 | Out-Host
  if ($LASTEXITCODE -eq 0) { Ok "reindex finished" } else { Bad "reindex failed" }
}
function Do-Shell {
  NeedDocker; $svc = if ($Arg) { $Arg } else { "api" }
  Write-Host "  Opening a shell in '$svc'. Type exit to leave."
  & docker compose exec $svc sh
}
function Do-ApiTest {
  $script:stepTotal = 1; Banner "api test"; Step "Health checks"
  foreach ($u in @("http://localhost:$WebPort/healthz", "http://localhost:$WebPort/", "http://localhost:8000/healthz")) {
    $sw = [Diagnostics.Stopwatch]::StartNew()
    try { $r = Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 $u; Ok "$u -> $($r.StatusCode) in $([int]$sw.Elapsed.TotalMilliseconds) ms" } catch { Warn "$u -> no answer ($($_.Exception.Message))" }
  }
}
function Do-Info {
  NeedDocker; $script:stepTotal = 1; Banner "info"; Step "Versions"
  Ok "hexthings $(Installed)"
  Ok "docker $((& docker version --format '{{.Server.Version}}' 2>$null))"
  Ok "compose $((& docker compose version --short 2>$null))"
  Ok "powershell $($PSVersionTable.PSVersion)"
  Ok "folder $Root"
  $imgs = @(docker compose --profile ai images --format "{{.ContainerName}} {{.Repository}}:{{.Tag}} {{.Size}}" 2>$null); foreach ($i in $imgs) { Ok $i }
}

# ---- live terminal dashboard (docker stats + database + web health), q to quit ---------------------
function Hx-Spark($vals, [double]$max) {
  $chars = if ($global:HxUtf) { @(" ", [char]0x2581, [char]0x2582, [char]0x2583, [char]0x2584, [char]0x2585, [char]0x2586, [char]0x2587, [char]0x2588) } else { @(" ", ".", ".", ":", ":", "-", "=", "+", "#") }
  if ($max -le 0) { $max = 1 }
  $s = ""
  foreach ($x in $vals) { $i = [int][math]::Round(8 * [math]::Min([double]$x, $max) / $max); $s += $chars[$i] }
  return $s
}
function Hx-Gauge([double]$pct, [int]$w = 14) {
  $f = [int][math]::Round($w * [math]::Min([math]::Max($pct, 0), 100) / 100)
  return (($global:HxBlock * $f) + ("-" * ($w - $f)))
}
function Do-Dash {
  NeedDocker
  if (-not $global:HxColor -or [Console]::IsInputRedirected) { Warn "the live dashboard needs an interactive terminal. Use: hexthings status"; return }
  $hist = @{}; $n = 0; $dbq = $null; $alerts = $null
  [Console]::CursorVisible = $false; Clear-Host
  try {
    while ($true) {
      $lines = @(& docker stats --no-stream --format "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}" 2>$null)
      if (($n % 5) -eq 0) {
        $dbq = Hx-Psql "select pg_size_pretty(pg_database_size(current_database()))"
        $alerts = Hx-Psql "select status || ':' || count(*) from alerts group by status order by status"
      }
      $n++
      [Console]::SetCursorPosition(0, 0)
      Hx-Wordmark "live dashboard" -Compact:((Hx-Height) -lt 36)
      Write-Host ("  {0}   web {1}   database {2}   alerts {3}" -f (Get-Date -Format "HH:mm:ss"), $(if (Healthy) { "up" } else { "DOWN" }), $(if ($dbq) { $dbq } else { "n/a" }), $(if ($alerts) { ($alerts -replace "`r?`n", " ") } else { "none" })) -ForegroundColor White
      Write-Host ""
      Write-Host ("  {0} {1} {2} {3}" -f "service".PadRight(24), "cpu".PadRight(22), "memory".PadRight(26), "network") -ForegroundColor DarkGray
      foreach ($l in $lines) {
        $p = $l.Split("|"); if ($p.Count -lt 5) { continue }
        $name = $p[0] -replace "^[^-]+-", "" -replace "-\d+$", ""
        $cpu = 0.0; [void][double]::TryParse(($p[1] -replace "%", ""), [ref]$cpu)
        $mem = 0.0; [void][double]::TryParse(($p[3] -replace "%", ""), [ref]$mem)
        if (-not $hist.ContainsKey($name)) { $hist[$name] = New-Object System.Collections.ArrayList }
        [void]$hist[$name].Add($cpu); if ($hist[$name].Count -gt 12) { $hist[$name].RemoveAt(0) }
        $col = if ($cpu -gt 80) { "Red" } elseif ($cpu -gt 40) { "Yellow" } else { "Green" }
        Write-Host ("  {0} " -f $name.PadRight(24)) -NoNewline
        Write-Host ("{0} {1,5:N1}% " -f (Hx-Gauge $cpu 10), $cpu) -ForegroundColor $col -NoNewline
        Write-Host ((Hx-Spark $hist[$name] 100).PadRight(13)) -ForegroundColor DarkCyan -NoNewline
        Write-Host ("{0} {1}" -f (Hx-Gauge $mem 8), ($p[2].Split("/")[0].Trim()).PadRight(10)) -NoNewline
        Write-Host (" " + $p[4]) -ForegroundColor DarkGray
      }
      Write-Host ""
      Write-Host "  CPU and memory come from docker stats; database size and alert counts from the database. q quits." -ForegroundColor DarkGray
      Write-Host (" " * 100)
      for ($t = 0; $t -lt 20; $t++) { if ([Console]::KeyAvailable) { $k = [Console]::ReadKey($true); if ($k.Key -eq "Q" -or $k.Key -eq "Escape") { return } }; Start-Sleep -Milliseconds 100 }
    }
  } finally { [Console]::CursorVisible = $true }
}

# ---- menus -----------------------------------------------------------------------------------------
function Hx-Pause { Write-Host ""; Write-Host "  Press any key to go back..." -ForegroundColor DarkGray; [void][Console]::ReadKey($true) }
function Hx-PickService([string]$title) {
  $svcs = @(docker compose --profile ai ps -a --format "{{.Service}}" 2>$null)
  if ($svcs.Count -eq 0) { Warn "no services found"; return "" }
  $i = Hx-Menu $title $svcs (@($svcs | ForEach-Object { "" }))
  if ($i -lt 0) { return "" }
  return $svcs[$i]
}
function Hx-Submenu([string]$title, [string[]]$labels, [string[]]$hints, [scriptblock[]]$actions) {
  while ($true) {
    Clear-Host; Hx-Wordmark $title.ToLower() -Compact:((Hx-Height) -lt 36); Hx-Footer; Write-Host ""
    $i = Hx-Menu $title ($labels + "Back") ($hints + "")
    if ($i -lt 0 -or $i -ge $labels.Count) { return }
    Clear-Host; & $actions[$i]; Hx-Pause
  }
}
function Do-Menu {
  $labels = @("Status", "Live dashboard", "Open the app", "Start", "Stop", "Restart", "Local AI", "Update", "Backup and restore", "Diagnostics", "Maintenance", "Dev tools", "Version", "Edge install help")
  $hints = @("what is running, version, last backup", "CPU, memory, alerts, database, live", "open the web app in your browser", "start all services", "stop all services (data is kept)", "restart all services", "start, stop or check the AI model", "backup, get the latest, rebuild, check", "database backups and restores", "check docker, ports, database, disk, logs", "prune, vacuum, reindex, support bundle", "logs, shell, API test, versions", "installed version, newer one available?", "how to install the edge app")
  while ($true) {
    Clear-Host; Hx-Startup ""
    $i = Hx-Menu "What would you like to do?" ($labels + "Quit") ($hints + "")
    if ($i -eq -2) { Do-Help; return }
    if ($i -lt 0 -or $i -ge $labels.Count) { Clear-Host; Hx-Footer; return }
    Clear-Host
    switch ($labels[$i]) {
      "Status" { Do-Status; Hx-Pause }
      "Live dashboard" { Do-Dash }
      "Open the app" { Start-Process "http://localhost:$WebPort" }
      "Start" { Do-Start; Hx-Pause }
      "Stop" { Do-Stop; Hx-Pause }
      "Restart" { Do-Restart; Hx-Pause }
      "Local AI" {
        $j = Hx-Menu "Local AI" @("Status", "Start the model", "Stop the model", "Choose model size", "Back") @("is the model running", "load the selected model", "free the memory", "resources, five sizes, or skip", "")
        if ($j -ge 0 -and $j -le 3) { $script:Arg = @("status", "start", "stop", "choose")[$j]; Clear-Host; Do-Ai; Hx-Pause }
      }
      "Update" { Do-Update ""; Hx-Pause }
      "Backup and restore" {
        $j = Hx-Menu "Backup and restore" @("Back up now", "Restore a backup", "Back") @("database dump with checksum", "replaces current data, asks first", "")
        if ($j -eq 0) { Clear-Host; Do-Backup; Hx-Pause }
        elseif ($j -eq 1) { Clear-Host; $f = Read-Host "  Path of the backup file (.dump.gz)"; if ($f) { $script:Arg = $f; Do-Restore }; Hx-Pause }
      }
      "Diagnostics" {
        Hx-Submenu "Diagnostics" @("Run all checks", "Show recent errors", "Support bundle (zip)") @("docker, services, ports, database, disk, clock", "tail the log of one service", "versions, state, redacted logs") @(
          { Do-Diagnose }, { $s = Hx-PickService "Which service?"; if ($s) { $script:Arg = $s; Do-Logs } }, { Do-Support })
      }
      "Maintenance" {
        Hx-Submenu "Maintenance" @("Back up now", "Prune Docker images", "Database vacuum", "Database reindex", "Support bundle (zip)") @("database dump with checksum", "free disk, data volumes untouched", "reclaim space and refresh statistics", "rebuild indexes (brief locks)", "redacted zip for support") @(
          { Do-Backup }, { Do-Prune }, { Do-Vacuum }, { Do-Reindex }, { Do-Support })
      }
      "Dev tools" {
        Hx-Submenu "Dev tools" @("Logs of a service", "Follow logs of a service", "Shell in a container", "API and health test", "Versions and images") @("last 100 lines", "live, Ctrl+C to stop", "docker compose exec sh", "web and api health with timings", "hexthings, docker, images") @(
          { $s = Hx-PickService "Which service?"; if ($s) { $script:Arg = $s; Do-Logs } }, { $s = Hx-PickService "Which service?"; if ($s) { $script:Arg = $s; $script:Follow = [switch]$true; Do-Logs } }, { $s = Hx-PickService "Which service?"; if ($s) { $script:Arg = $s; Do-Shell } }, { Do-ApiTest }, { Do-Info })
      }
      "Version" { Do-Version; Hx-Pause }
      "Edge install help" { Write-Host ""; Write-Host "  Edge app: docs\edge-install.md, or in the web app open Add device > Commission a sensor"; Write-Host "  and copy the install command for Ubuntu or Windows."; Hx-Pause }
    }
  }
}
