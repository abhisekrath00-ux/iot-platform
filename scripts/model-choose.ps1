# Shared local-AI chooser, Windows PowerShell 5.1-compatible. No network unless -Size is set.
param([string]$Size = "", [switch]$List, [switch]$Recommended, [switch]$Force)
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
if (Test-Path (Join-Path $PSScriptRoot "docker-compose.yml")) { $root = $PSScriptRoot }
$catalog = Get-Content (Join-Path $PSScriptRoot "model-catalog.txt") | Where-Object { $_ -and -not $_.StartsWith("#") } | ForEach-Object {
  $v = $_ -split '\|'
  [pscustomobject]@{ Id=$v[0]; Label=$v[1]; Quant=$v[2]; Bytes=[long]$v[3]; Sha=$v[4]; Url=$v[5]; Min=[long]$v[6]; Rec=[long]$v[7]; License=$v[8]; Tested=$v[9]; Note=$v[10] }
}
$ram=0L; $cpu=0; $disk=0L
try {
  $hostInfo = Get-CimInstance Win32_ComputerSystem
  $ram = [long]($hostInfo.TotalPhysicalMemory / 1MB); $cpu=$hostInfo.NumberOfLogicalProcessors
  $drive = [IO.Path]::GetPathRoot($root)
  $disk = [long]((New-Object IO.DriveInfo($drive)).AvailableFreeSpace / 1MB)
} catch { Write-Warning 'Could not detect Windows resources. Check RAM/CPU/disk manually; automatic download will be refused.' }
if ($env:HEXTHINGS_FAKE_MEM_MB) { $ram=[long]$env:HEXTHINGS_FAKE_MEM_MB }
if ($env:HEXTHINGS_FAKE_CPU) { $cpu=[int]$env:HEXTHINGS_FAKE_CPU }
if ($env:HEXTHINGS_FAKE_DISK_MB) { $disk=[long]$env:HEXTHINGS_FAKE_DISK_MB }
function Fit($m) {
  if ($disk -lt ([math]::Ceiling($m.Bytes / 1MB)+10240)) { return 'DISK-LOW' }
  if ($ram -lt $m.Min) { return 'TOO-BIG' }
  if ($ram -lt $m.Rec) { return 'TIGHT' }
  return 'FIT'
}
$recommend = 'skip'
foreach ($m in $catalog) { if ($m.Tested -eq 'tested' -and (Fit $m) -eq 'FIT') { $recommend=$m.Id } }
if ($Recommended) { return $recommend }
if ($List -or -not $Size) {
  Write-Host "Detected host: RAM $ram MiB | CPU $cpu logical cores | free disk $disk MiB"
  Write-Host 'Check Docker Desktop/WSL memory limits separately. CPU speed is not benchmarked.'
  foreach ($m in $catalog) {
    Write-Host ("{0,-16} {1,6:N2} GB | RAM min {2} MiB | {3} | {4} | {5}" -f $m.Id,($m.Bytes/1e9),$m.Min,(Fit $m),$m.Tested,$m.License)
    Write-Host "  $($m.Label) / $($m.Quant). $($m.Note)"
  }
  Write-Host "skip: no local AI. Recommendation: $recommend (only the measured model)."
  Write-Host 'RAM verdicts are whole-stack estimates, not guarantees. Disk includes 10 GiB reserve.'
  if (-not $Size) { return }
}
if ($Size -eq 'skip') { Write-Host 'AI skipped; existing model kept. Stop ai-runtime to disable a running model.'; return }
$m = $catalog | Where-Object Id -eq $Size | Select-Object -First 1
if (-not $m) { throw "Unknown model id: $Size" }
if ($m.Sha -eq 'manual') { throw "Manual-only split GGUF, untested runtime: $($m.Url). Nothing downloaded." }
$verdict=Fit $m
if ($verdict -eq 'DISK-LOW') { throw 'Not enough disk for model plus 10 GiB platform reserve' }
if ($verdict -ne 'FIT') { Write-Warning "$verdict on this host. Minimum RAM estimate $($m.Min), comfortable $($m.Rec) MiB"; if (-not $Force) { throw 'Choose smaller/skip, or -Force to accept RAM risk' } }
if ($env:HEXTHINGS_SKIP_DOWNLOAD -eq '1') { throw 'Download disabled by HEXTHINGS_SKIP_DOWNLOAD=1' }
if (Test-Path (Join-Path $root 'images.tar.gz')) { throw 'Air-gapped bundle: network downloads disabled. Copy a verified GGUF instead.' }
$url=$m.Url; $sha=$m.Sha; $bytes=$m.Bytes
if ($env:HEXTHINGS_MODEL_URL) { $url=$env:HEXTHINGS_MODEL_URL }
if ($env:HEXTHINGS_MODEL_SHA256) { $sha=$env:HEXTHINGS_MODEL_SHA256 }
if ($env:HEXTHINGS_MODEL_BYTES) { $bytes=[long]$env:HEXTHINGS_MODEL_BYTES }
$models=Join-Path $root 'models'; New-Item -ItemType Directory -Force $models | Out-Null
$part=Join-Path $models "$Size.gguf.part"
Write-Host "Downloading $($m.Label), $bytes bytes, $($m.License), $($m.Tested). Old model kept until verification."
if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
  & curl.exe -fL --retry 3 --retry-delay 3 -C - -o $part $url
  if ($LASTEXITCODE -ne 0) { throw 'Download failed; same-model partial kept for resume' }
} else {
  [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
  Write-Host 'curl.exe unavailable: this download restarts instead of resuming.'
  Invoke-WebRequest -UseBasicParsing $url -OutFile $part
}
$got=(Get-FileHash $part -Algorithm SHA256).Hash.ToLower()
if ($got -ne $sha.ToLower() -or (Get-Item $part).Length -ne $bytes) { Remove-Item $part -Force; throw 'Model checksum mismatch or wrong size; old model unchanged' }
Move-Item $part (Join-Path $models 'model.gguf') -Force
Set-Content (Join-Path $models 'model.id') $Size -Encoding ASCII
$limit=[math]::Max(3072,($m.Min-2560))
$envFile=Join-Path $root '.env'
if (Test-Path $envFile) {
  $lines=@(Get-Content $envFile | Where-Object { $_ -notmatch '^AI_(MODEL_NAME|MODEL_SHA256|MEM_LIMIT)=' })
  $lines += @("AI_MODEL_NAME=$Size", "AI_MODEL_SHA256=$sha", "AI_MEM_LIMIT=$($limit)m")
  [IO.File]::WriteAllLines($envFile,$lines)
}
Write-Host 'Verified model installed. Runtime not started. Restart ai-runtime to load it.'
