# Local binary-upgrade transaction. Caller verifies elevation and exact service ownership.
# No enrollment, config replacement, network request or restart on uncertain recovery.
function Write-EdgeUpgradeRecord([string]$Path, $Value) {
  $temp=$Path+'.'+[guid]::NewGuid().ToString('N')+'.tmp'
  try {
    $bytes=[Text.Encoding]::UTF8.GetBytes(($Value | ConvertTo-Json -Depth 8 -Compress))
    $stream=[IO.File]::Open($temp,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None)
    try {$stream.Write($bytes,0,$bytes.Length);$stream.Flush($true)} finally {$stream.Dispose()}
    if(Test-Path -LiteralPath $Path) {[IO.File]::Replace($temp,$Path,[System.Management.Automation.Language.NullString]::Value)} else {[IO.File]::Move($temp,$Path)}
  } finally {if(Test-Path -LiteralPath $temp){Remove-Item -LiteralPath $temp -Force}}
}
function Get-EdgeUpgradeHash([string]$Path) { (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash }
function Assert-EdgeUpgradePlain([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  while($null -ne $item) {
    if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Linked upgrade state, ancestor or executable refused.'}
    if($item -is [IO.DirectoryInfo]){$item=$item.Parent}else{$item=$item.Directory}
  }
}

function Assert-EdgeUpgradeAcl([string]$Directory) {
  # An elevated path check alone does not make an existing directory trustworthy.
  # Reject effective write grants outside built-in SYSTEM/Administrators. Never
  # change an existing ACL silently while upgrading retained identity material.
  $acl=Get-Acl -LiteralPath $Directory
  $allowed=@('S-1-5-18','S-1-5-32-544')
  $owner=([Security.Principal.NTAccount]$acl.Owner).Translate([Security.Principal.SecurityIdentifier]).Value
  if($owner -notin $allowed){throw 'Existing installation owner needs review; upgrade refused.'}
  $writes=[Security.AccessControl.FileSystemRights]::Write -bor [Security.AccessControl.FileSystemRights]::Delete -bor [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor [Security.AccessControl.FileSystemRights]::ChangePermissions -bor [Security.AccessControl.FileSystemRights]::TakeOwnership
  foreach($rule in $acl.Access) {
    $sid=$rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
    if($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $sid -notin $allowed -and ($rule.FileSystemRights -band $writes)) {throw 'Existing installation permits another identity to change files. ACL review required before upgrade.'}
  }
}
function Set-EdgeUpgradeExecutable([string]$Source,[string]$Destination,[string]$Expected) {
  $temp=$Destination+'.'+[guid]::NewGuid().ToString('N')+'.staged'
  try {
    Copy-Item -LiteralPath $Source -Destination $temp
    $stream=[IO.File]::Open($temp,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    try {$stream.Flush($true)} finally {$stream.Dispose()}
    if((Get-EdgeUpgradeHash $temp) -ne $Expected){throw 'Staged executable digest mismatch.'}
    if(Test-Path -LiteralPath $Destination){[IO.File]::Replace($temp,$Destination,[System.Management.Automation.Language.NullString]::Value)}else{[IO.File]::Move($temp,$Destination)}
  } finally {if(Test-Path -LiteralPath $temp){Remove-Item -LiteralPath $temp -Force}}
}
function Test-EdgeUpgradeHealth([string]$Executable,[string]$Config) {
  & $Executable -health -config $Config | Out-Null
  return ($LASTEXITCODE -in @(0,2) -and (Get-Service HexmonEdge).Status -eq 'Running')
}
function Invoke-EdgeUpgrade([string]$Directory,[string]$Candidate,[switch]$Recover) {
  $exe=Join-Path $Directory 'edge-agent.exe'
  $pointer=Join-Path $Directory 'upgrade-pending.json'
  $lockPath=Join-Path $Directory 'upgrade.lock'
  Assert-EdgeUpgradePlain $Directory
  Assert-EdgeUpgradeAcl $Directory
  if(Test-Path -LiteralPath $pointer){Assert-EdgeUpgradePlain $pointer}
  if(Test-Path -LiteralPath $lockPath){Assert-EdgeUpgradePlain $lockPath}
  $lock=[IO.File]::Open($lockPath,[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
  try {
    if($Recover) {
      if(-not(Test-Path -LiteralPath $pointer)){throw 'No interrupted upgrade is recorded.'}
      Assert-EdgeUpgradePlain $pointer
      $record=Get-Content -LiteralPath $pointer -Raw | ConvertFrom-Json
      if($record.version -ne 1 -or $record.id -notmatch '^[a-f0-9]{32}$' -or $record.root -ne [IO.Path]::GetFullPath($Directory) -or $record.old -notmatch '^[A-F0-9]{64}$' -or $record.new -notmatch '^[A-F0-9]{64}$' -or $record.running -isnot [bool]){throw 'Upgrade record needs review; no recovery attempted.'}
      $archive=Join-Path $Directory ('upgrade-'+$record.id)
      Assert-EdgeUpgradePlain $archive
      $intent=Join-Path $archive 'intent.json';Assert-EdgeUpgradePlain $intent
      $original=Get-Content -LiteralPath $intent -Raw | ConvertFrom-Json
      foreach($field in @('version','id','root','old','new','running')) {if($record.$field -cne $original.$field){throw 'Pending record differs from immutable intent; recovery refused.'}}
      $backup=Join-Path $archive 'previous.exe';Assert-EdgeUpgradePlain $backup
      if((Get-EdgeUpgradeHash $backup) -ne $record.old){throw 'Retained executable changed; recovery refused.'}
      # Missing executable is an expected interrupted copy. An unknown executable is not.
      if(Test-Path -LiteralPath $exe){Assert-EdgeUpgradePlain $exe;if((Get-EdgeUpgradeHash $exe) -notin @($record.old,$record.new)){throw 'Installed executable drifted; recovery refused.'}}
      $svc=Get-Service HexmonEdge
      if($svc.Status -ne 'Stopped'){Stop-Service HexmonEdge -Force;$svc.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))}
      Set-EdgeUpgradeExecutable $backup $exe $record.old
      if((Get-EdgeUpgradeHash $exe) -ne $record.old){throw 'Restored executable could not be verified.'}
      # Recovery always stops here. Restart needs the operator's separate Start action.
      Write-EdgeUpgradeRecord (Join-Path $archive 'restored.json') $record
      Remove-Item -LiteralPath $pointer -Force
      Write-Host 'Previous executable verified and restored. Service remains stopped. Identity, config and startup choice were not changed. Review health/config before choosing Start.'
      $global:LASTEXITCODE=0
      return
    }
    if(Test-Path -LiteralPath $pointer){throw 'An earlier upgrade is unresolved. Preserve its files and use explicit upgrade recovery. No replacement requested.'}
    Assert-EdgeUpgradePlain $exe;Assert-EdgeUpgradePlain $Candidate
    $svc=Get-Service HexmonEdge
    if($svc.Status -notin @('Running','Stopped')){throw 'Wait for a stable running or stopped service before upgrading.'}
    $record=@{version=1;id=[guid]::NewGuid().ToString('N');root=[IO.Path]::GetFullPath($Directory);old=(Get-EdgeUpgradeHash $exe);new=(Get-EdgeUpgradeHash $Candidate);running=($svc.Status -eq 'Running')}
    $archive=Join-Path $Directory ('upgrade-'+$record.id)
    New-Item -ItemType Directory -Path $archive | Out-Null
    $backup=Join-Path $archive 'previous.exe'
    Set-EdgeUpgradeExecutable $exe $backup $record.old
    if((Get-EdgeUpgradeHash $backup) -ne $record.old){throw 'Previous executable snapshot failed. Service unchanged.'}
    Write-EdgeUpgradeRecord (Join-Path $archive 'intent.json') $record
    # Flush the intent before stopping or replacing anything. Keep it on every failure.
    Write-EdgeUpgradeRecord $pointer $record
    if($record.running){Stop-Service HexmonEdge -Force;$svc.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))}
    if((Get-EdgeUpgradeHash $exe) -ne $record.old -or (Get-EdgeUpgradeHash $Candidate) -ne $record.new){throw 'Executable changed after review. Upgrade remains blocked for explicit recovery.'}
    Set-EdgeUpgradeExecutable $Candidate $exe $record.new
    if((Get-EdgeUpgradeHash $exe) -ne $record.new){throw 'Installed executable could not be verified. Use explicit upgrade recovery.'}
    if($record.running) {
      Start-Service HexmonEdge
      $ok=$false
      for($i=0;$i -lt 30 -and -not $ok;$i++) {
        Start-Sleep -Seconds 2
        if(Test-EdgeUpgradeHealth $exe (Join-Path $Directory 'edge-agent.yaml')){$ok=$true}
      }
      if(-not $ok){throw 'New executable health unconfirmed. Upgrade intent retained; use explicit recovery. Do not rerun upgrade.'}
    }
    Write-EdgeUpgradeRecord (Join-Path $archive 'completed.json') $record
    Remove-Item -LiteralPath $pointer -Force
    $global:LASTEXITCODE=0
    Write-Host 'Upgrade verified. Previous executable retained in a unique private archive. Config, identity and startup choice unchanged. Service returned to its earlier running/stopped state. Verify actual readings separately.'
  } finally {$lock.Dispose()}
}
