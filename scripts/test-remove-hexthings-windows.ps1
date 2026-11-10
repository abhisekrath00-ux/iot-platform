# Pure mocked Docker ownership tests. Never deletes an installation.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'remove-hexthings-windows.ps1')
$root = Join-Path ([IO.Path]::GetTempPath()) ('hx-uninstall-test-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path (Join-Path $root 'scripts'),(Join-Path $root 'server'),(Join-Path $root 'web') -Force | Out-Null
foreach ($f in @('docker-compose.yml','scripts/hexthings.ps1','scripts/install.ps1')) { Set-Content (Join-Path $root $f) 'fixture' }
$root = Normalize-Path $root
function Reset-Fixture {
  $script:containers = @()
  foreach ($service in @('api','web','postgres')) {
    $labels = @{'com.docker.compose.project'='fixture';'com.docker.compose.project.working_dir'=$root;'com.docker.compose.project.config_files'=(Join-Path $root 'docker-compose.yml');'com.docker.compose.service'=$service}
    $script:containers += [pscustomobject]@{Id=$service;Name=$service;Config=[pscustomobject]@{Labels=$labels};Mounts=@()}
  }
  $script:volumes = @([pscustomobject]@{Name='fixture_pgdata'})
  $script:networks = @([pscustomobject]@{Id='network';Name='fixture_default';Containers=[pscustomobject]@{api=@{};web=@{};postgres=@{}}})
  $script:containers[2].Mounts = @([pscustomobject]@{Type='volume';Name='fixture_pgdata'})
}
function docker {
  $a = @($args); $global:LASTEXITCODE = 0
  if ($a[0] -eq 'ps') { return @($script:containers | ForEach-Object { $_.Id }) }
  if ($a[0] -eq 'inspect') { return ConvertTo-Json -InputObject @($script:containers) -Depth 15 }
  if ($a[0] -eq 'volume' -and $a[1] -eq 'ls') { return @($script:volumes | ForEach-Object { $_.Name }) }
  if ($a[0] -eq 'volume' -and $a[1] -eq 'inspect') { return ConvertTo-Json -InputObject @($script:volumes) -Depth 15 }
  if ($a[0] -eq 'network' -and $a[1] -eq 'ls') { return @($script:networks | ForEach-Object { $_.Id }) }
  if ($a[0] -eq 'network' -and $a[1] -eq 'inspect') { return ConvertTo-Json -InputObject @($script:networks) -Depth 15 }
  throw 'Unexpected command, no mutation allowed in this fixture'
}
function Must-Refuse([string]$Name,[scriptblock]$Change) {
  Reset-Fixture; & $Change
  $refused=$false
  try { $null=Get-Plan $root } catch { $refused=$true }
  if (-not $refused) { throw "FAILED refusal: $Name" }
  Write-Host "PASS refusal: $Name"
}
try {
  foreach ($file in @('remove-hexthings-windows.ps1','test-remove-hexthings-windows.ps1')) {
    $t=$null;$e=$null;[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $file),[ref]$t,[ref]$e)|Out-Null
    if ($e.Count) { throw 'Parse failed' }
  }
  Reset-Fixture; $null=Assert-Root $root; $p=Get-Plan $root
  if ($p.Project -ne 'fixture' -or $p.Containers.Count -ne 3 -or $p.Volumes.Count -ne 1) { throw 'Bad valid plan' }
  Write-Host 'PASS exact installation/project plan; no Docker mutation'
  Must-Refuse 'missing containers' { $script:containers=@() }
  Must-Refuse 'same project other folder' { $script:containers[0].Config.Labels['com.docker.compose.project.working_dir']=([IO.Path]::GetTempPath()) }
  Must-Refuse 'custom override file' { $script:containers[0].Config.Labels['com.docker.compose.project.config_files'] += ',other.yml' }
  Must-Refuse 'unexpected service' { $script:containers[0].Config.Labels['com.docker.compose.service']='unrelated' }
  Must-Refuse 'shared volume' { $script:containers += [pscustomobject]@{Id='other';Config=[pscustomobject]@{Labels=@{}};Mounts=@([pscustomobject]@{Type='volume';Name='fixture_pgdata'})} }
  Must-Refuse 'external volume' { $script:containers[0].Mounts=@([pscustomobject]@{Type='volume';Name='external'}) }
  Must-Refuse 'shared network' { $script:networks[0].Containers | Add-Member -NotePropertyName other -NotePropertyValue @{} }
  Must-Refuse 'outside bind mount' { $script:containers[0].Mounts=@([pscustomobject]@{Type='bind';Source=([IO.Path]::GetTempPath())}) }
  $refused=$false;try{$null=Assert-Root $HOME}catch{$refused=$true};if(-not $refused){throw 'HOME not refused'}
  Write-Host 'PASS protected user folder refused'
  Write-Host 'Contract checks only: no live Docker, Windows filesystem deletion or port-release proof.'
} finally { Remove-Item -LiteralPath $root -Recurse -Force }
