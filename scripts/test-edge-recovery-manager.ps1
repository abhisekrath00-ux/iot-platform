# Exercise actual recovery action functions, with confirmation/installer stand-ins.
# No WinForms pixels/clicks, SCM, enrollment, real service or owner files.
$ErrorActionPreference='Stop'
$old=$env:ProgramData
$root=Join-Path ([IO.Path]::GetTempPath()) ('edge-recovery-manager-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root|Out-Null
$env:ProgramData=$root
try {
 . (Join-Path $PSScriptRoot '../edge/packaging/setup-wizard.ps1') -ContractOnly
 $here=$root;New-Item -ItemType Directory -Force $dir|Out-Null
 $script:accepted=$false;$script:confirmations=0
 function Confirm-SetupRecovery([string]$Title,[string]$Message){$script:confirmations++;return $script:accepted}
 @'
param([switch]$ReviewInstall,[switch]$ResumeInstall,[switch]$RecoverUpgrade)
$global:LASTEXITCODE=0
if($ReviewInstall){Get-Content (Join-Path $PSScriptRoot 'review.json') -Raw;return}
if($env:EDGE_RECOVERY_FAIL -eq 'yes'){throw 'fixture backend failure'}
if($ResumeInstall){Add-Content (Join-Path $PSScriptRoot 'effects') 'resume';return}
if($RecoverUpgrade){Add-Content (Join-Path $PSScriptRoot 'effects') 'restore';return}
throw 'Unexpected installer action'
'@ |Set-Content (Join-Path $root 'install-windows.ps1')
 $review=@{version=1;review_only=$true;binary='exact_candidate';config_exact=$true;service='Stopped';enrollment_was_requested=$false;saved_identity_files=@('identity.key');can_start=$false;can_retry_enrollment=$false;can_clear_pending=$false}
 function SaveReview {$review|ConvertTo-Json|Set-Content (Join-Path $root 'review.json')}
 SaveReview
 $text=Invoke-SetupRecoveryAction review
 if($text -notmatch 'Review only' -or $script:confirmations -ne 0 -or (Test-Path (Join-Path $root effects))){throw 'Review had side effects or confirmation'}
 $text=Invoke-SetupRecoveryAction resume
 if($text -notmatch 'cancelled' -or (Test-Path (Join-Path $root effects))){throw 'Cancelled resume mutated'}
 $review.enrollment_was_requested=$true;SaveReview;$before=$script:confirmations;$blocked=$false
 try{Invoke-SetupRecoveryAction resume}catch{$blocked=$true}
 if(-not $blocked -or $script:confirmations -ne $before -or (Test-Path (Join-Path $root effects))){throw 'Uncertain enrollment reached confirmation/backend'}
 $review.enrollment_was_requested=$false;$review.config_exact=$false;SaveReview;$blocked=$false
 try{Invoke-SetupRecoveryAction resume}catch{$blocked=$true};if(-not $blocked){throw 'Config drift allowed'}
 $review.config_exact=$true;SaveReview;$script:accepted=$true
 $text=Invoke-SetupRecoveryAction resume
 if($text -notmatch 'stopped/manual' -or (Get-Content (Join-Path $root effects)) -ne 'resume'){throw 'Explicit resume routing failed'}
 Remove-Item (Join-Path $root effects)
 $env:EDGE_RECOVERY_FAIL='yes';$blocked=$false;try{Invoke-SetupRecoveryAction resume}catch{$blocked=$true};if(-not $blocked){throw 'Backend failure reported success'};Remove-Item Env:EDGE_RECOVERY_FAIL
 $blocked=$false;try{Invoke-SetupRecoveryAction restore}catch{$blocked=$true};if(-not $blocked){throw 'No-pending restore reached installer'}
 Set-Content (Join-Path $dir 'upgrade-pending.json') '{}'
 $script:accepted=$false;$text=Invoke-SetupRecoveryAction restore
 if($text -notmatch 'cancelled' -or (Test-Path (Join-Path $root effects))){throw 'Cancelled restore mutated'}
 $script:accepted=$true;$text=Invoke-SetupRecoveryAction restore
 if($text -notmatch 'Service is stopped' -or (Get-Content (Join-Path $root effects)) -ne 'restore'){throw 'Restore routing failed'}
 Set-Content (Join-Path $dir 'install-pending.json') '{}';$blocked=$false
 try{Invoke-SetupRecoveryAction restore}catch{$blocked=$true};if(-not $blocked){throw 'First install routed to upgrade restore'}
 Write-Host 'PASS real recovery action functions: read-only review, cancel/no-effects, explicit resume/restore flags, uncertain enrollment and drift refusal, backend failure propagation. Confirmation/installer stand-ins; no native WinForms clicks or pixels.'
}finally{$env:ProgramData=$old;Remove-Item $root -Recurse -Force}
$global:LASTEXITCODE=0
