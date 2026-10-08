# Guided installer for the HexThings on Windows (Docker Desktop with WSL2).
# Double-click install.bat, or: powershell -ExecutionPolicy Bypass -File install.ps1 [-Yes]
# Same steps as install.sh. NOT TESTED: no Windows machine was available when this was written.
param([switch]$Yes, [string]$AdminEmail = "", [string]$Workspace = "", [int]$WebPort = 0, [string]$AiModel = "", [switch]$AiDownload, [switch]$NoAi, [string]$AiModelSize = "", [switch]$AiForce)
$ErrorActionPreference = "Continue"   # Windows PowerShell 5.1 turns native stderr (docker progress) into errors under Stop; every docker call checks $LASTEXITCODE
Set-Location -Path $PSScriptRoot
if (-not (Test-Path docker-compose.yml)) { Set-Location -Path (Split-Path $PSScriptRoot -Parent) }
$Log = "install.log"; Set-Content -Path $Log -Value ""
function Say($m) { Write-Host $m; Add-Content -Path $Log -Value $m }
function Ok($m) { Say "  ok    $m" }
function Warn($m) { Say "  warn  $m"; $script:Warnings++ }
function Fail($m) { Say "  FAIL  $m"; Say ""; Say "Nothing was started. Fix the line above and run the installer again; it is safe to re-run."; exit 1 }
$script:Warnings = 0
function Ask($q, $d) { if ($Yes) { return $d }; $a = Read-Host "$q [$d]"; if ($a) { return $a } else { return $d } }
# Windows PowerShell 5.1 (.NET Framework) has no RandomNumberGenerator.GetInt32/Fill: use the older provider.
$script:Rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
function RandBytes($n) { $b = New-Object byte[] $n; $script:Rng.GetBytes($b); return ,$b }
function RandAlnum($n) {
  $chars = [char[]]"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
  $sb = New-Object System.Text.StringBuilder
  while ($sb.Length -lt $n) { foreach ($x in (RandBytes 64)) { if ($x -lt 248 -and $sb.Length -lt $n) { [void]$sb.Append($chars[$x % 62]) } } }
  return $sb.ToString()
}
function RandB64 { return [Convert]::ToBase64String((RandBytes 32)) }
function Dc { & docker compose @args 2>&1 | Add-Content -Path $Log; if ($LASTEXITCODE -ne 0) { throw "docker compose $args failed" } }

$uiFile = Join-Path $PSScriptRoot "hx-ui.ps1"
$hasUi = Test-Path $uiFile
if ($hasUi) { . $uiFile }
function Banner {
  if ($hasUi) { Hx-Wordmark "guided installer" -Animate; Hx-Footer; Write-Host "" }
  else {
    $c = @("Cyan","Cyan","Blue","Blue","DarkBlue","Magenta")
    $art = @("  #   #  #####","  #   #    #    HexThings","  #####    #    industrial IoT platform","  #   #    #","  #   #    #    guided installer","")
    Write-Host ""
    for ($i = 0; $i -lt $art.Count; $i++) { Write-Host $art[$i] -ForegroundColor $c[$i] }
    Write-Host ""
    Write-Host "  Made with <3 by Hexmon Technology"
  }
  Add-Content -Path $Log -Value "HexThings guided installer"
}
$chooser = Join-Path $PSScriptRoot "model-choose.ps1"
if ($AiModelSize -eq "skip") { $NoAi=$true }
if ($AiModelSize -and $AiModel) { Fail "Choose -AiModel OR -AiModelSize, not both" }
if ($AiModelSize) { $AiDownload=$true }
Banner; Say "1. Checking this machine"
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Fail "Docker is not installed. Install Docker Desktop (WSL2 backend) and run this again." }
docker info *> $null; if ($LASTEXITCODE -ne 0) { Fail "Docker is installed but not running. Start Docker Desktop and wait until it says running." }
Ok "Docker is running"
docker compose version *> $null; if ($LASTEXITCODE -ne 0) { Fail "Docker Compose v2 is missing. Update Docker Desktop." }
Ok "Docker Compose v2 present"
$Bundle = Test-Path images.tar.gz
if ($Bundle) { Ok "air-gapped bundle found: images load from images.tar.gz, no internet needed" } else { Ok "source checkout: images are built here (needs internet for base images unless cached)" }
$memMb = [int]((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory / 1MB)
if ($memMb -lt 4096) { Warn "$memMb MB RAM: the platform needs about 4 GB" } else { Ok "$memMb MB RAM" }
$freeGb = [int]((Get-PSDrive -Name (Get-Location).Drive.Name).Free / 1GB)
if ($freeGb -lt 10) { Warn "only $freeGb GB free; plan for 10 GB or more" } else { Ok "$freeGb GB free disk" }
if ($WebPort -eq 0 -and (Test-Path .env)) { $m = Select-String -Path .env -Pattern '^WEB_PORT=(\d+)' | Select-Object -First 1; if ($m) { $WebPort = [int]$m.Matches[0].Groups[1].Value } }
if ($WebPort -eq 0) { $WebPort = 8080 }
$running = (docker compose ps --status running -q 2>$null)
if ($running) { Ok "an existing install is running; this will update it in place" }
else {
  foreach ($p in @($WebPort, 8000, 1883, 5432)) {
    if (Get-NetTCPConnection -State Listen -LocalPort $p -ErrorAction SilentlyContinue) {
      if ($p -eq $WebPort) { Fail "port $p (web) is already in use. Re-run with -WebPort N, or stop what uses it." }
      Warn "port $p is in use by something else; the service that needs it may fail to start"
    }
  }
  Ok "ports free"
}

Say ""; Say "2. Setup"
if (Test-Path .env) {
  $envLines = @(Get-Content .env); $fixed = $false
  for ($i = 0; $i -lt $envLines.Count; $i++) {
    foreach ($k in @("POSTGRES_PASSWORD", "JWT_SIGNING_SECRET", "SECRETS_KEY")) {
      if ($envLines[$i] -match "^$k=(.*)$" -and ($Matches[1].Trim() -eq "" -or $Matches[1] -like "change-me*")) {
        $v = if ($k -eq "SECRETS_KEY") { RandB64 } elseif ($k -eq "JWT_SIGNING_SECRET") { RandAlnum 64 } else { RandAlnum 32 }
        $envLines[$i] = "$k=$v"; $fixed = $true
      }
    }
  }
  if ($fixed) { [System.IO.File]::WriteAllLines((Join-Path (Get-Location) ".env"), $envLines); Ok ".env had empty or placeholder secrets from an earlier failed run: new ones generated" }
  else { Ok ".env exists and is kept as it is (secrets are not regenerated)" }
}
else {
  $lines = Get-Content .env.example | Where-Object { $_ -notmatch '^DATABASE_URL=' } | ForEach-Object {
    if ($_ -match '^POSTGRES_PASSWORD=') { "POSTGRES_PASSWORD=$(RandAlnum 32)" }
    elseif ($_ -match '^JWT_SIGNING_SECRET=') { "JWT_SIGNING_SECRET=$(RandAlnum 64)" }
    elseif ($_ -match '^SECRETS_KEY=') { "SECRETS_KEY=$(RandB64)" }
    elseif ($_ -match '^WEB_PORT=') { "WEB_PORT=$WebPort" }
    else { $_ } }
  $lines += "# Installer: the first administrator signs in with a password. Use SSO for production (docs/security.md)."
  $lines += "LOCAL_LOGIN=1"
  [System.IO.File]::WriteAllLines((Join-Path (Get-Location) ".env"), $lines)
  Ok ".env written with generated secrets"
}
if (-not $Workspace) { $Workspace = Ask "Name for your first workspace (lowercase letters, digits, dashes)" "my-plant" }
if (-not $AdminEmail) { $AdminEmail = Ask "Administrator email (you must change it at first sign-in if it stays the default)" "admin@hexthings.com" }
if ($Workspace -notmatch '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$') { Fail "workspace '$Workspace' must be 3-40 characters: a-z, 0-9, dashes" }
if ($AdminEmail -notmatch '^[^@ ]+@[^@ ]+\.[^@ ]+$') { Fail "'$AdminEmail' is not an email address" }
$aiProfile = @()
if (-not $NoAi) {
  if (-not $AiModel -and (Test-Path model.gguf)) { $AiModel = "model.gguf" }
  if (-not $AiModel -and (Test-Path "models\model.gguf")) { $AiModel = "models\model.gguf" }
  if (-not $AiModel -and -not $AiDownload -and -not $Yes) {
    & $chooser -List
    $a = Ask "Local AI: model ID, skip, or path to your .gguf" "skip"
    if ($a -match '\.gguf$') { $AiModel=$a } elseif ($a -notmatch '^(skip|n|no|)$') { $AiModelSize=$a; $AiDownload=$true }
  }
  if ($AiDownload) {
    if (-not $AiModelSize) { $AiModelSize='qwen3-1.7b' }
    try { & $chooser -List -Size $AiModelSize -Force:$AiForce } catch { Fail "Model selection/download failed: $_" }
    $AiModel='models\model.gguf'
  }
  if ($AiModel) {
    if (-not (Test-Path $AiModel)) { Fail "model file not found: $AiModel" }
    if ($memMb -lt 5120) { Warn "$memMb MB host RAM: model plus platform may be tight; check Docker/WSL limits" }
    New-Item -ItemType Directory -Force -Path models | Out-Null
    if ((Resolve-Path $AiModel).Path -ne (Join-Path (Get-Location) "models\model.gguf")) { Copy-Item $AiModel models\model.gguf -Force }
    $env:AI_MODEL_SHA256 = (Get-FileHash models\model.gguf -Algorithm SHA256).Hash.ToLower()
    if (-not $AiModelSize -and (Test-Path "models/model.id")) { $AiModelSize=(Get-Content "models/model.id" -TotalCount 1).Trim() }
    $aiProfile = @("--profile", "ai")
    Ok "local AI model ready; AI stays optional and off until enabled in Settings"
  } else { Ok "no local AI model (the platform works the same without it)" }
}

Say ""; Say "3. Installing (the first run takes several minutes)"
try {
  if ($Bundle) {
    if (Test-Path SHA256SUMS) {
      foreach ($l in Get-Content SHA256SUMS) { $h, $f = $l -split '\s+\*?', 2; if ((Test-Path $f) -and ((Get-FileHash $f -Algorithm SHA256).Hash.ToLower() -ne $h)) { Fail "checksum mismatch in ${f}: the bundle is damaged. Copy it again." } }
      Ok "bundle checksums verified"
    }
    docker load -i images.tar.gz 2>&1 | Add-Content -Path $Log   # docker load reads gzip directly
    if ($LASTEXITCODE -ne 0) { Fail "could not load images (see $Log)" }
    Ok "images loaded"
    Dc @aiProfile up -d --no-build
  } else { Dc @aiProfile up -d --build }
} catch { Say ""; Say "Last lines of $Log :"; Get-Content $Log -Tail 25 | ForEach-Object { Write-Host "  $_" }; Fail "$_ (see $Log)" }
Ok "services started"
Say "     waiting for the platform to answer ..."
$up = $false
foreach ($i in 1..90) { try { Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 "http://localhost:$WebPort/healthz" | Out-Null; $up = $true; break } catch { Start-Sleep 2 } }
if (-not $up) { Fail "the platform did not become healthy within 3 minutes. Run: docker compose logs api" }
Ok "platform is healthy"

Say ""; Say "4. First workspace"
if (Test-Path install-credentials.txt) { Warn "install-credentials.txt from an older install holds a password in plain text. Change that password in the app, then delete the file." }
$Pass = ""
$existing = (docker compose exec -T api /bin/tenantctl list 2>$null) -join "`n"
if ($existing -match "(?m)^$Workspace ") { Ok "workspace '$Workspace' already exists; the administrator was not changed" }
else {
  $out = docker compose exec -T api /bin/tenantctl create --id $Workspace --name $Workspace --admin-email $AdminEmail --default-credentials 2>&1
  Add-Content -Path $Log -Value $out
  if ($LASTEXITCODE -ne 0) { Fail "could not create the workspace (see $Log)" }
  $Pass = "Hex@2026"   # documented first-run password; hashed in the database, not saved to any file
  Ok "workspace '$Workspace' and administrator created"
}
if ($aiProfile.Count -gt 0) {
  & docker compose exec -T api /bin/tenantctl ai-connect --tenant $Workspace --model $(if ($AiModelSize) { $AiModelSize } else { "qwen3-1.7b" }) 2>&1 | Add-Content -Path $Log
  if ($LASTEXITCODE -eq 0) { Ok "AI assistant connected to the local model (the first answer is slow while the model loads)" }
  else { Warn "could not connect the assistant automatically; set it in Settings > AI (base URL http://ai-runtime:8090/v1, model qwen3-1.7b)" }
}
# `hexthings` management command: version stamp and a PATH entry for this user
try { Set-Content -Path ".hexthings-version" -Value ($(if ($env:HEXTHINGS_VERSION) { $env:HEXTHINGS_VERSION } else { "installed " + (Get-Date -Format s) })) } catch { }
try {
  $bin = Join-Path (Get-Location) "scripts"
  $up = [Environment]::GetEnvironmentVariable("Path", "User")
  if (-not $up -or ($up -split ";") -notcontains $bin) { [Environment]::SetEnvironmentVariable("Path", ($up.TrimEnd(";") + ";" + $bin).TrimStart(";"), "User"); Ok "added the hexthings command to your PATH (open a new terminal, then try: hexthings status)" }
  else { Ok "hexthings command is on your PATH" }
} catch { Warn "could not add hexthings to PATH; run scripts\hexthings.ps1 instead" }
Say ""; Say "Installed."; Say ""
Say "  Open:      http://localhost:$WebPort"; Say "  Workspace: $Workspace"; Say "  Email:     $AdminEmail"
if ($Pass) { Write-Host "  Password:  $Pass"; Say "             (first-run login: you are asked to change the email and password right after signing in; not saved anywhere)" }
Say ""; Say "  Manage: hexthings status | update | backup | restore | logs | version   (open a new terminal first)"
if ($script:Warnings -gt 0) { Say "  Note: $($script:Warnings) warning(s) above. The log is in $Log." }
Write-Host ""; if ($hasUi) { Hx-Footer } else { Write-Host "  Made with <3 by Hexmon Technology" }
