# Runs chooser logic with fake curl on PowerShell 7; not a real Windows install or inference test.
$ErrorActionPreference='Stop'
$t=Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory (Join-Path $t 'scripts') -Force | Out-Null
Copy-Item (Join-Path $PSScriptRoot 'model-choose.ps1'),(Join-Path $PSScriptRoot 'model-catalog.txt') (Join-Path $t 'scripts')
$env:HEXTHINGS_FAKE_MEM_MB='5120'; $env:HEXTHINGS_FAKE_CPU='2'; $env:HEXTHINGS_FAKE_DISK_MB='100000'
$chooser=Join-Path $t 'scripts/model-choose.ps1'; $checks=0
function Check($label,$ok) { if (-not $ok) { throw "FAIL: $label" }; $script:checks++; Write-Host "ok: $label" }
function Refused($id,[switch]$Force) { try { & $chooser -Size $id -Force:$Force; return $false } catch { return $true } }
try {
  Check 'recommend measured model on 5 GiB' ((& $chooser -Recommended) -eq 'qwen3-1.7b')
  $env:HEXTHINGS_FAKE_MEM_MB='4096'; Check '4 GiB recommends skip' ((& $chooser -Recommended) -eq 'skip')
  Check 'tight selection blocked' (Refused 'qwen3-1.7b')
  $env:HEXTHINGS_FAKE_MEM_MB='5120'
  Check 'unknown blocked' (Refused 'unknown')
  Check 'manual-only blocked with force' (Refused 'gpt-oss-120b' -Force)
  Check 'large model blocked' (Refused 'qwen3-8b')
  $env:HEXTHINGS_FAKE_DISK_MB='1'; Check 'low disk not bypassed' (Refused 'qwen3-1.7b' -Force); $env:HEXTHINGS_FAKE_DISK_MB='100000'
  $env:HEXTHINGS_SKIP_DOWNLOAD='1'; Check 'offline switch blocks' (Refused 'qwen3-1.7b'); Remove-Item Env:HEXTHINGS_SKIP_DOWNLOAD
  New-Item (Join-Path $t 'images.tar.gz') -ItemType File | Out-Null
  Check 'airgap blocks' (Refused 'qwen3-1.7b'); Remove-Item (Join-Path $t 'images.tar.gz')
  & $chooser -Size skip
  Check 'skip creates no model' (-not (Test-Path (Join-Path $t 'models/model.gguf')))
  # Mock the native curl command at PowerShell command resolution, with real file hash/size checks.
  function global:curl.exe {
    $i=[array]::IndexOf($args,'-o'); [IO.File]::WriteAllText($args[$i+1],'fake-model')
    $global:LASTEXITCODE=0
  }
  $env:HEXTHINGS_MODEL_BYTES='10'
  $sample=Join-Path $t 'sample'; [IO.File]::WriteAllText($sample,'fake-model')
  $env:HEXTHINGS_MODEL_SHA256=(Get-FileHash $sample -Algorithm SHA256).Hash.ToLower()
  [IO.File]::WriteAllText((Join-Path $t '.env'),'JWT_SIGNING_SECRET=keep-me')
  & $chooser -Size qwen3-1.7b
  Check 'verified download records id' ((Get-Content (Join-Path $t 'models/model.id')) -eq 'qwen3-1.7b')
  Check 'runtime config set, secrets kept' ((Get-Content (Join-Path $t '.env') -Raw) -match 'JWT_SIGNING_SECRET=keep-me' -and (Get-Content (Join-Path $t '.env') -Raw) -match 'AI_MODEL_NAME=qwen3-1.7b')
  $env:HEXTHINGS_MODEL_SHA256='bad'; Check 'bad hash blocked' (Refused 'qwen3-1.7b')
  Check 'old verified model kept' (([IO.File]::ReadAllText((Join-Path $t 'models/model.gguf'))) -eq 'fake-model')
  Write-Host "$checks PowerShell chooser checks passed (Linux mocks only)"
} finally {
  Remove-Item $t -Recurse -Force
  Remove-Item Function:curl.exe -ErrorAction SilentlyContinue
  foreach ($key in @('HEXTHINGS_FAKE_MEM_MB','HEXTHINGS_FAKE_CPU','HEXTHINGS_FAKE_DISK_MB','HEXTHINGS_MODEL_SHA256','HEXTHINGS_MODEL_BYTES','HEXTHINGS_SKIP_DOWNLOAD')) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue }
}
