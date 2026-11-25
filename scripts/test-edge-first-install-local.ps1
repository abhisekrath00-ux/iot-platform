# Execute actual installer body in a disposable directory with mocked elevation,
# Windows ACL/native service commands and a real shell child standing in for agent.
# Linux PowerShell only. Not Windows service/elevation/ACL acceptance.
$ErrorActionPreference='Stop'
if($IsWindows){throw 'This harness is for local Linux PowerShell only'}
$root=Join-Path ([IO.Path]::GetTempPath()) ('edge-fresh-'+[guid]::NewGuid().ToString('N'))
$oldData=$env:ProgramData
$source=Join-Path $PSScriptRoot '../edge/packaging'
New-Item -ItemType Directory $root|Out-Null
$global:EdgeFreshFault='none';$global:EdgeFreshService=$null
function Get-Service {param($Name,$ErrorAction);return $global:EdgeFreshService}
function Get-CimInstance {param($ClassName,$Filter);return @{StartMode='Manual';PathName=('"'+(Join-Path $env:ProgramData 'Hexmon/edge/edge-agent.exe')+'" -config fixture')}}
function New-Service {param($Name,$DisplayName,$BinaryPathName,$StartupType,$Description);$global:EdgeFreshService=@{Status='Stopped'};if($global:EdgeFreshFault -eq 'service'){throw 'injected service registration acknowledgement loss'}}
function icacls {param([Parameter(ValueFromRemainingArguments=$true)]$Values);$global:LASTEXITCODE=0}
function sc.exe {param([Parameter(ValueFromRemainingArguments=$true)]$Values);$global:LASTEXITCODE=0;if($global:EdgeFreshFault -eq 'recovery'){throw 'injected SCM recovery configuration failure'}}
try {
 $stage=Join-Path $root package;New-Item -ItemType Directory $stage|Out-Null
 Copy-Item (Join-Path $source '*') $stage -Recurse
 $installer=Join-Path $stage 'install-windows.ps1'
 $text=Get-Content $installer -Raw
 $t=$null;$e=$null;$ast=[Management.Automation.Language.Parser]::ParseInput($text,[ref]$t,[ref]$e)
 # Only replace the host-specific elevation check, not installer flow.
 $principal=@($ast.FindAll({param($n)$n -is [Management.Automation.Language.AssignmentStatementAst] -and $n.Left.Extent.Text -eq '$principal'},$true))[0]
 $text=$text.Replace($principal.Extent.Text,'$principal=$null')
 $guard=@($ast.FindAll({param($n)$n -is [Management.Automation.Language.IfStatementAst] -and $n.Extent.Text.StartsWith('if (-not $principal.')},$true))[0]
 $text=$text.Replace($guard.Extent.Text,'# elevation mocked by Linux-only harness')
 Set-Content $installer $text
 Add-Content (Join-Path $stage 'upgrade-transaction.ps1') "`nfunction Assert-EdgeUpgradeAcl {param(`$Directory)}"
 Add-Content (Join-Path $stage 'upgrade-transaction.ps1') @'
$script:realFreshWriter=${function:Write-EdgeUpgradeRecord}
function Write-EdgeUpgradeRecord([string]$Path,$Value) {
 & $script:realFreshWriter $Path $Value
 if($global:EdgeFreshFault -eq 'resume-ack' -and $Path.EndsWith('resumed-install-only.json')){throw 'injected install resume acknowledgement loss'}
}
'@

 $child=Join-Path $stage 'edge-agent.exe'
 [IO.File]::WriteAllText($child,"#!/bin/sh`nexit 0`n")
 & chmod +x $child
 foreach($point in @('service','recovery','enrollment','none')) {
  $env:ProgramData=Join-Path $root $point;$global:EdgeFreshService=$null;$global:EdgeFreshFault=$point
  $failed=$false;try{
    if($point -eq 'enrollment') {
      [IO.File]::WriteAllText($child,"#!/bin/sh`nif [ `"`$1`" = '-version' ]; then exit 0; fi`nprintf synthetic-retained-key > `"`$2/identity.key`"`nexit 23`n")
      & chmod +x $child
      & $installer -ClaimApi 'https://fixture.invalid' -ClaimCode 'synthetic-code-not-secret' -ClaimSerial 'synthetic-serial'
    } else {[IO.File]::WriteAllText($child,"#!/bin/sh`nexit 0`n");& chmod +x $child;& $installer}
  }catch{$failed=$true;Write-Host $_.Exception.Message}
  $dir=Join-Path $env:ProgramData 'Hexmon/edge';$pending=Join-Path $dir 'install-pending.json'
  if($point -eq 'none') {
   if($failed -or (Test-Path $pending) -or $global:EdgeFreshService.Status -ne 'Stopped'){throw 'Fresh install did not settle stopped/manual fixture'}
   if(@(Get-ChildItem $dir -Directory -Filter 'install-*').Count -ne 1){throw 'Missing retained installation archive'}
  } else {
   if(-not $failed -or -not(Test-Path $pending)){throw 'First-install failure lost pending intent'}
   $saved=Get-Content $pending -Raw;$global:EdgeFreshFault='none';$blocked=$false
   $review=(& $installer -ReviewInstall)|ConvertFrom-Json
   if(-not $review.review_only -or $review.binary -ne 'exact_candidate' -or $review.can_start -or $review.can_retry_enrollment -or $review.can_clear_pending){throw 'Unsafe or incomplete install review'}
   if((Get-Content $pending -Raw) -ne $saved){throw 'Read-only review changed pending evidence'}
   if($point -eq 'enrollment' -and 'identity.key' -notin $review.saved_identity_files){throw 'Review lost saved uncertain-enrollment key'}
   try{& $installer}catch{$blocked=$true}
   if(-not $blocked -or (Get-Content $pending -Raw) -ne $saved){throw 'First-install retry was not blocked'}
   if($point -eq 'enrollment') {
    if((Get-Content (Join-Path $dir 'data/identity.key') -Raw) -ne 'synthetic-retained-key'){throw 'Uncertain enrollment identity was not retained'}
    if($saved -match 'synthetic-code-not-secret'){throw 'Claim code leaked into install record'}
   }
   if(-not(Test-Path (Join-Path $dir 'edge-agent.exe'))){throw 'Failure destroyed installed evidence'}
   if($point -eq 'enrollment') {
    $blocked=$false;try{& $installer -ResumeInstall}catch{$blocked=$true}
    if(-not $blocked -or -not(Test-Path $pending)){throw 'Enrollment outcome was silently accepted or replayed'}
   } else {
    $configPath=Join-Path $dir 'edge-agent.yaml';$savedConfig=[IO.File]::ReadAllBytes($configPath)
    Set-Content $configPath drifted
    $blocked=$false;try{& $installer -ResumeInstall}catch{$blocked=$true};if(-not $blocked -or -not(Test-Path $pending)){throw 'Config drift was accepted by resume'}
    [IO.File]::WriteAllBytes($configPath,$savedConfig)
    $global:EdgeFreshFault='resume-ack';$blocked=$false
    try{& $installer -ResumeInstall}catch{$blocked=$true}
    if(-not $blocked -or -not(Test-Path $pending)){throw 'Resume acknowledgement loss dropped pending intent'}
    $global:EdgeFreshFault='none'
    & $installer -ResumeInstall
    if((Test-Path $pending) -or $global:EdgeFreshService.Status -ne 'Stopped'){throw 'Install-only recovery failed to settle stopped'}
   }

  }
  if($point -eq 'none') {
   $global:EdgeFreshService=$null
   Set-Content (Join-Path $dir 'data/identity.key') 'synthetic-retained-identity'
   $blocked=$false;try{& $installer -ClaimApi 'https://fixture.invalid' -ClaimCode 'synthetic-code' -ClaimSerial 'synthetic'}catch{$blocked=$true}
   if(-not $blocked -or (Get-Content (Join-Path $dir 'data/identity.key')) -ne 'synthetic-retained-identity'){throw 'Fresh entrypoint replaced saved identity'}
  }
  Write-Host "PASS actual installer body: $point, durable first-install intent or settled stopped service; Windows ACL/elevation/SCM mocked"
 }
} finally {$env:ProgramData=$oldData;Remove-Item $root -Recurse -Force}
$global:LASTEXITCODE=0
