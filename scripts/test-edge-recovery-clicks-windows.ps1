# Native WinForms PerformClick contract in a disposable runner. No real product
# service/enrollment: only the actual form/action handler and fake backend fixture.
$ErrorActionPreference='Stop'
if(-not $env:GITHUB_ACTIONS -or $env:RUNNER_OS -ne 'Windows'){throw 'Disposable Windows Actions runner required'}
$old=$env:ProgramData;$root=Join-Path $env:RUNNER_TEMP ('recovery-clicks-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory $root|Out-Null
$env:ProgramData=$root
try {
 . (Join-Path $PSScriptRoot '../edge/packaging/setup-wizard.ps1') -NoShow
 $here=$root;New-Item -ItemType Directory -Force $dir|Out-Null
 $script:confirmed=$false;$script:confirmCount=0
 function Confirm-SetupRecovery([string]$Title,[string]$Message){$script:confirmCount++;return $script:confirmed}
 @'
param([switch]$ReviewInstall,[switch]$ResumeInstall,[switch]$RecoverUpgrade)
$global:LASTEXITCODE=0
if($ReviewInstall){Get-Content (Join-Path $PSScriptRoot 'review.json') -Raw;return}
if($ResumeInstall){Add-Content (Join-Path $PSScriptRoot effects) resume;return}
if($RecoverUpgrade){Add-Content (Join-Path $PSScriptRoot effects) restore;return}
throw 'Unexpected backend action'
'@ |Set-Content (Join-Path $root 'install-windows.ps1')
 $review=@{version=1;review_only=$true;binary='exact_candidate';config_exact=$true;service='Stopped';enrollment_was_requested=$false;saved_identity_files=@('identity.key');can_start=$false;can_retry_enrollment=$false;can_clear_pending=$false}
 function SaveReview {$review|ConvertTo-Json|Set-Content (Join-Path $root review.json)}
 function Click([string]$Label){$button=@($form.Controls|Where-Object {$_.Text -eq $Label -and $_ -is [System.Windows.Forms.Button]});if($button.Count -ne 1){throw 'Expected actual single recovery control'};$form.ScrollControlIntoView($button[0]);$button[0].PerformClick();[System.Windows.Forms.Application]::DoEvents()}
 $frames=New-Object System.Collections.ArrayList
 function Capture {
  $form.ScrollControlIntoView($output);$form.Refresh();[System.Windows.Forms.Application]::DoEvents()
  $bitmap=New-Object System.Drawing.Bitmap($form.Width,$form.Height)
  $form.DrawToBitmap($bitmap,(New-Object System.Drawing.Rectangle(0,0,$form.Width,$form.Height)))
  [void]$frames.Add($bitmap)
 }
 SaveReview;$form.Show();[System.Windows.Forms.Application]::DoEvents()
 Click 'Review install'
 if($output.Text -notmatch 'Review only' -or (Test-Path (Join-Path $root effects))){throw 'Review click failed or changed state'};Capture
 Click 'Resume install only'
 if($output.Text -notmatch 'cancelled' -or (Test-Path (Join-Path $root effects))){throw 'Cancel click changed state'};Capture
 $review.enrollment_was_requested=$true;SaveReview;$before=$script:confirmCount
 Click 'Resume install only'
 if($output.Text -notmatch 'Pairing outcome is uncertain' -or $script:confirmCount -ne $before -or (Test-Path (Join-Path $root effects))){throw 'Uncertain enrollment click reached mutation'};Capture
 $review.enrollment_was_requested=$false;SaveReview;$script:confirmed=$true
 Click 'Resume install only'
 if($output.Text -notmatch 'stopped/manual' -or (Get-Content (Join-Path $root effects)) -ne 'resume'){throw 'Accepted resume click misrouted'};Capture
 Remove-Item (Join-Path $root effects);Set-Content (Join-Path $dir upgrade-pending.json) '{}'
 Click 'Restore previous executable'
 if($output.Text -notmatch 'Service is stopped' -or (Get-Content (Join-Path $root effects)) -ne 'restore'){throw 'Accepted restore click misrouted'};Capture
 $combined=New-Object System.Drawing.Bitmap($form.Width,($form.Height*$frames.Count));$graphics=[System.Drawing.Graphics]::FromImage($combined)
 for($i=0;$i -lt $frames.Count;$i++){$graphics.DrawImageUnscaled($frames[$i],0,($i*$form.Height))}
 $combined.Save((Join-Path $env:RUNNER_TEMP 'hexthings-edge-setup.png'),[System.Drawing.Imaging.ImageFormat]::Png)
 $graphics.Dispose();$combined.Dispose();foreach($frame in $frames){$frame.Dispose()}
 Write-Host 'PASS actual WinForms PerformClick review/cancel/enrollment-refusal/resume/restore states and screenshot capture. Backend/confirmation stand-ins, no native user pointer, real modal confirmation or real service effects in this test.'
}finally{if($form){$form.Close();$form.Dispose()};$env:ProgramData=$old;Remove-Item $root -Recurse -Force}
$global:LASTEXITCODE=0
