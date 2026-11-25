# Read-only interrupted-install review. Never runs agent code, enrollment,
# service changes, file repair or deletion. Call under installation.lock.
function Get-EdgeFirstInstallReview([string]$Directory) {
  Assert-EdgeUpgradePlain $Directory
  Assert-EdgeUpgradeAcl $Directory
  $pending=Join-Path $Directory 'install-pending.json';Assert-EdgeUpgradePlain $pending
  $record=Get-Content -LiteralPath $pending -Raw|ConvertFrom-Json
  if($record.version -ne 1 -or $record.root -cne [IO.Path]::GetFullPath($Directory) -or $record.candidate -notmatch '^[A-F0-9]{64}$' -or $record.config_sha256 -notmatch '^[A-F0-9]{64}$' -or $record.enrollment_requested -isnot [bool]){throw 'Unrecognized first-install record. Preserve all files; no recovery proposed.'}
  $archive=[IO.Path]::GetFullPath([string]$record.archive)
  if([IO.Path]::GetDirectoryName($archive) -cne [IO.Path]::GetFullPath($Directory) -or [IO.Path]::GetFileName($archive) -notmatch '^install-[a-f0-9]{32}$'){throw 'Installation archive is not local and exact.'}
  Assert-EdgeUpgradePlain $archive
  $intent=Join-Path $archive 'intent.json';Assert-EdgeUpgradePlain $intent
  $original=Get-Content -LiteralPath $intent -Raw|ConvertFrom-Json
  foreach($field in @('version','root','candidate','archive','enrollment_requested','config_sha256')) {if($record.$field -cne $original.$field){throw 'First-install intent differs; preserve for review.'}}
  $exe=Join-Path $Directory 'edge-agent.exe';$binary='missing'
  if(Test-Path -LiteralPath $exe){Assert-EdgeUpgradePlain $exe;$binary=if((Get-EdgeUpgradeHash $exe) -eq $record.candidate){'exact_candidate'}else{'changed'}}
  $svc=Get-Service HexmonEdge -ErrorAction SilentlyContinue
  $service='absent'
  if($svc) {
    $entry=Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
    if(-not $entry -or $entry.PathName -notlike ('"'+$exe+'" *')){throw 'Service belongs to another path. No recovery proposed.'}
    $service=[string]$svc.Status
  }
  $savedIdentity=@()
  foreach($file in @('identity.json','identity.key','identity.crt','pairing-pending.json')) {
    $path=Join-Path (Join-Path $Directory data) $file
    if(Test-Path -LiteralPath $path){Assert-EdgeUpgradePlain $path;$savedIdentity+=$file}
  }
  $configPath=Join-Path $Directory 'edge-agent.yaml'
  $configExact=$false
  if(Test-Path -LiteralPath $configPath){Assert-EdgeUpgradePlain $configPath;$configExact=((Get-EdgeUpgradeHash $configPath) -eq $record.config_sha256)}
  [pscustomobject]@{config_exact=$configExact;version=1;review_only=$true;binary=$binary;service=$service;enrollment_was_requested=$record.enrollment_requested;saved_identity_files=@($savedIdentity);config_present=(Test-Path -LiteralPath (Join-Path $Directory 'edge-agent.yaml'));can_retry_enrollment=$false;can_start=$false;can_clear_pending=$false;next_step='Preserve the pending installation and saved identity. No automatic repair or enrollment replay is allowed. Review the exact failure before choosing a recovery action.'}
}

# Explicit install-only resume. Enrollment attempts, even apparently successful
# ones, need separate server reconciliation and are never replayed here.
function Resume-EdgeFirstInstall([string]$Directory) {
  $review=Get-EdgeFirstInstallReview $Directory
  if($review.enrollment_was_requested){throw 'Enrollment outcome must be reconciled with the server first. Install-only resume cannot repeat or accept enrollment.'}
  if($review.binary -ne 'exact_candidate' -or -not $review.config_exact -or $review.service -notin @('absent','Stopped')){throw 'Install-only resume requires exact retained executable/config and an absent or stopped owned service. Nothing changed.'}
  $pending=Join-Path $Directory 'install-pending.json'
  $record=Get-Content -LiteralPath $pending -Raw|ConvertFrom-Json
  $exe=Join-Path $Directory 'edge-agent.exe'
  if($review.service -eq 'absent') {
    New-Service -Name HexmonEdge -DisplayName 'HexThings Edge Agent' -BinaryPathName ('"'+$exe+'" -config "'+(Join-Path $Directory 'edge-agent.yaml')+'" -identity-dir "'+(Join-Path $Directory data)+'"') -StartupType Manual -Description 'Hexmon edge gateway agent'|Out-Null
  } else {
    $entry=Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
    if($entry.StartMode -ne 'Manual'){throw 'Existing service startup choice differs. Review before resuming.'}
  }
  sc.exe failure HexmonEdge reset= 86400 actions= restart/5000/restart/5000/restart/30000 |Out-Null
  if($LASTEXITCODE -ne 0){throw 'Service recovery configuration failed. Pending installation retained.'}
  $final=Get-EdgeFirstInstallReview $Directory
  if($final.service -ne 'Stopped' -or $final.binary -ne 'exact_candidate'){throw 'Resumed service could not be verified stopped. Pending installation retained.'}
  $entry=Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
  if($entry.StartMode -ne 'Manual'){throw 'Resumed service is not manual. Pending installation retained.'}
  Write-EdgeUpgradeRecord (Join-Path $record.archive 'resumed-install-only.json') $record
  Remove-Item -LiteralPath $pending -Force
  $global:LASTEXITCODE=0
  Write-Host 'Install-only recovery verified. Service is stopped/manual. Configuration and saved identity were retained. No enrollment, configuration download or Start was requested.'
}
