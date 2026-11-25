# Actual filesystem/lock/record tests with service calls mocked. Not Windows SCM proof.
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot '../edge/packaging/upgrade-transaction.ps1')
function Assert-EdgeUpgradeAcl {param($Directory);if($script:fault -eq 'acl'){throw 'fixture untrusted ACL'}}
$originalWrite=${function:Write-EdgeUpgradeRecord}
$originalSet=${function:Set-EdgeUpgradeExecutable}
function Set-EdgeUpgradeExecutable([string]$Source,[string]$Destination,[string]$Expected) {
 & $script:originalSet $Source $Destination $Expected
 if($script:fault -eq 'published' -and $Destination.EndsWith('edge-agent.exe')){throw 'injected replacement publication acknowledgement loss'}
}
function Get-Service {param($Name);[pscustomobject]@{Status=$script:status} | Add-Member -MemberType ScriptMethod -Name WaitForStatus -Value {param($state,$timeout);if($script:status -ne $state){throw 'Wrong mocked service state'}} -PassThru}
function Stop-Service {param($Name,[switch]$Force);$script:status='Stopped';if($script:fault -eq 'stop'){throw 'injected stop acknowledgement loss'}}
function Start-Sleep {param($Seconds)}
function Test-EdgeUpgradeHealth {param($Executable,$Config);$global:LASTEXITCODE=2;return ($script:fault -ne 'health' -and $script:status -eq 'Running')}
function Start-Service {param($Name);$script:status='Running';if($script:fault -eq 'start'){throw 'injected start acknowledgement loss'}}
function Write-EdgeUpgradeRecord([string]$Path,$Value) {
 & $script:originalWrite $Path $Value
 if($script:fault -eq 'intent' -and $Path.EndsWith('upgrade-pending.json')){throw 'injected intent acknowledgement loss'}
 if($script:fault -eq 'complete' -and $Path.EndsWith('completed.json')){throw 'injected completion acknowledgement loss'}
 if($script:fault -eq 'restore' -and $Path.EndsWith('restored.json')){throw 'injected restore acknowledgement loss'}
}
function Copy-Item {param($LiteralPath,$Destination,[switch]$Force);Microsoft.PowerShell.Management\Copy-Item -LiteralPath $LiteralPath -Destination $Destination -Force:$Force;if($script:fault -eq 'copy' -and $Destination.Contains('edge-agent.exe.')){throw 'injected replacement acknowledgement loss'}}
$base=Join-Path ([IO.Path]::GetTempPath()) ('edge-upgrade-test-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $base|Out-Null
$cases=0
try {
 foreach($point in @('intent','stop','copy','published','start','complete','health','running','none')) {
  $root=Join-Path $base $point;New-Item -ItemType Directory $root|Out-Null
  $exe=Join-Path $root 'edge-agent.exe';$src=Join-Path $root 'candidate.exe'
  Set-Content $exe 'old-binary';Set-Content $src 'new-binary';Set-Content (Join-Path $root 'edge-agent.yaml') 'saved-config'
  New-Item -ItemType Directory (Join-Path $root data)|Out-Null;Set-Content (Join-Path $root 'data/identity.key') 'synthetic-retained-key'
  $old=Get-EdgeUpgradeHash $exe;$config=Get-EdgeUpgradeHash (Join-Path $root 'edge-agent.yaml');$key=Get-EdgeUpgradeHash (Join-Path $root 'data/identity.key')
  $script:status=if($point -in @('stop','start','health','running')){'Running'}else{'Stopped'};$script:fault=$point
  $failed=$false;try{Invoke-EdgeUpgrade $root $src}catch{$failed=$true}
  $pending=Join-Path $root 'upgrade-pending.json'
  if($point -in @('none','running')) {
   if($failed -or (Test-Path $pending) -or $script:status -ne $(if($point -eq 'running'){'Running'}else{'Stopped'}) -or (Get-EdgeUpgradeHash $exe) -ne (Get-EdgeUpgradeHash $src)){throw 'Stopped success failed'}
   if($global:LASTEXITCODE -ne 0){throw 'Successful upgrade leaked broker-unreachable exit2'}
   # A further distinct update never overwrites predecessor snapshots.
   Set-Content $src 'third-binary';Invoke-EdgeUpgrade $root $src
   if(@(Get-ChildItem $root -Directory -Filter 'upgrade-*').Count -ne 2){throw 'Archive reuse'}
  } else {
   if(-not $failed -or -not(Test-Path $pending)){throw "Lost durable pending state at $point"}
   $before=Get-Content $pending -Raw;$script:fault='none'
   $blocked=$false;try{Invoke-EdgeUpgrade $root $src}catch{$blocked=$true}
   if(-not $blocked -or (Get-Content $pending -Raw) -ne $before){throw 'Unsafe retry changed pending transaction'}
   # Fail after restoration evidence, then resume exactly without starting.
   $script:fault='restore';$blocked=$false;try{Invoke-EdgeUpgrade -Directory $root -Recover}catch{$blocked=$true}
   if(-not $blocked -or -not(Test-Path $pending) -or (Get-EdgeUpgradeHash $exe) -ne $old){throw 'Restore interruption did not retain evidence'}
   $script:fault='none';Invoke-EdgeUpgrade -Directory $root -Recover
   if((Test-Path $pending) -or $script:status -ne 'Stopped' -or (Get-EdgeUpgradeHash $exe) -ne $old){throw 'Explicit recovery failed'}
  }
  if((Get-EdgeUpgradeHash (Join-Path $root 'edge-agent.yaml')) -ne $config -or (Get-EdgeUpgradeHash (Join-Path $root 'data/identity.key')) -ne $key){throw 'Config/identity modified'}
  $cases++;Write-Host "PASS filesystem transaction $point, retained config/key, explicit stopped recovery or successful update"
 }
 # Existing unknown bytes must not be overwritten by recovery.
 $root=Join-Path $base drift;New-Item -ItemType Directory $root|Out-Null;$exe=Join-Path $root 'edge-agent.exe';$src=Join-Path $root 'candidate.exe';Set-Content $exe old;Set-Content $src new
 $script:status='Stopped';$script:fault='intent';try{Invoke-EdgeUpgrade $root $src}catch{}
 $script:fault='none';Set-Content $exe unknown;$blocked=$false;try{Invoke-EdgeUpgrade -Directory $root -Recover}catch{$blocked=$true};if(-not $blocked -or (Get-Content $exe).Trim() -ne 'unknown'){throw 'Drift was overwritten'}
 Set-Content $exe old
 $record=Get-Content (Join-Path $root 'upgrade-pending.json') -Raw|ConvertFrom-Json;$record.running=$true
 Write-EdgeUpgradeRecord (Join-Path $root 'upgrade-pending.json') $record
 $blocked=$false;try{Invoke-EdgeUpgrade -Directory $root -Recover}catch{$blocked=$true};if(-not $blocked){throw 'Altered pending record accepted'}
 # A symlinked ancestor must be refused before taking the lock or writing.
 $link=Join-Path $base linked
 New-Item -ItemType SymbolicLink -Path $link -Target $root|Out-Null
 $blocked=$false;try{Invoke-EdgeUpgrade -Directory $link -Recover}catch{$blocked=$true};if(-not $blocked){throw 'Linked ancestor accepted'}
 $script:fault='acl';$blocked=$false;try{Invoke-EdgeUpgrade -Directory $root -Recover}catch{$blocked=$true};if(-not $blocked){throw 'Untrusted ACL allowed'};$script:fault='none'
 $held=[IO.File]::Open((Join-Path $root 'upgrade.lock'),[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
 try{$blocked=$false;try{Invoke-EdgeUpgrade -Directory $root -Recover}catch{$blocked=$true};if(-not $blocked){throw 'Concurrent installer entered'}}finally{$held.Dispose()}
 Write-Host "PASS $cases cases plus unknown-binary refusal and actual exclusive file lock. Service transitions mocked; native Windows running/health/UAC/GUI acceptance not established."
} finally {Microsoft.PowerShell.Management\Remove-Item -LiteralPath $base -Recurse -Force}
