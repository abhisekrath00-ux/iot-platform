# Installs or upgrades the HexThings edge agent as a Windows service (elevated PowerShell).
#   .\install-windows.ps1 [-ClaimApi URL -ClaimCode CODE -ClaimSerial SERIAL]
# Upgrades retain a unique executable snapshot and durable intent. Interrupted attempts
# block replacement until explicit -RecoverUpgrade restores the prior binary, stopped.
param([string]$ClaimApi = "", [string]$ClaimCode = "", [string]$ClaimSerial = "", [switch]$RecoverUpgrade, [switch]$ReviewInstall, [switch]$ResumeInstall)
$ErrorActionPreference = "Stop"
# Windows PowerShell5.1 does not turn native nonzero exits into exceptions.
function Assert-EdgeNativeSuccess([int]$Code, [string]$Step) {
  if ($Code -ne 0) { throw "$Step failed (exit $Code). Setup did not complete; do not start the service." }
}
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw "Run this in an elevated (Administrator) PowerShell." }

$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$src = Join-Path $here "edge-agent-windows-$arch.exe"
if (-not (Test-Path $src)) { $src = Join-Path $here "edge-agent.exe" }
$dir = Join-Path $env:ProgramData "Hexmon\edge"
$exe = Join-Path $dir 'edge-agent.exe'
. (Join-Path $here 'upgrade-transaction.ps1')
# Check the nearest existing ancestor before creating anything below it.
$existing=[IO.Path]::GetFullPath($dir)
while(-not(Test-Path -LiteralPath $existing)){$existing=[IO.Path]::GetDirectoryName($existing)}
Assert-EdgeUpgradePlain $existing
if($ReviewInstall -and -not(Test-Path -LiteralPath $dir)){throw 'No installation exists to review.'}
New-Item -ItemType Directory -Force -Path $dir, "$dir\data", "$dir\data\logs" | Out-Null
# One installer process, including fresh installs and recovery. The lower-level
# binary transaction has its own lock for callers using that explicit API.
$installationLockPath=Join-Path $dir 'installation.lock'
. (Join-Path $here 'upgrade-transaction.ps1')
Assert-EdgeUpgradePlain $dir
if(Test-Path -LiteralPath $exe){Assert-EdgeUpgradeAcl $dir}
if(Test-Path -LiteralPath $installationLockPath){Assert-EdgeUpgradePlain $installationLockPath}
$installationLock=[IO.File]::Open($installationLockPath,[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
try {
$svc = Get-Service HexmonEdge -ErrorAction SilentlyContinue
if ($svc) {
  $installed = Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
  if (-not $installed -or $installed.PathName -notlike ('"' + $exe + '" *')) { throw 'HexmonEdge is owned by another service path. Refusing replacement.' }
}
if ($ResumeInstall) {
  if($ReviewInstall -or $RecoverUpgrade -or $ClaimCode -or $ClaimApi -or $ClaimSerial){throw 'Install-only recovery cannot be combined with another action or enrollment.'}
  . (Join-Path $here 'first-install-review.ps1')
  Resume-EdgeFirstInstall $dir
  return
}
if ($ReviewInstall) {
  if($RecoverUpgrade -or $ClaimCode -or $ClaimApi -or $ClaimSerial){throw 'Read-only install review cannot enroll or recover an upgrade.'}
  . (Join-Path $here 'first-install-review.ps1')
  Get-EdgeFirstInstallReview $dir | ConvertTo-Json -Depth 4
  $global:LASTEXITCODE=0
  return
}
$freshPending=Join-Path $dir 'install-pending.json'
if(Test-Path -LiteralPath $freshPending){throw 'An earlier first installation is unresolved. Files and any saved pairing identity were preserved. Do not retry enrollment, upgrade, Start, uninstall or purge. Review the interrupted installation before continuing.'}
if($ClaimCode) {
  foreach($identityFile in @('identity.json','identity.key','identity.crt','pairing-pending.json')) {
    if(Test-Path -LiteralPath (Join-Path (Join-Path $dir data) $identityFile)){throw 'Saved gateway identity or pairing evidence exists. Leave the claim code blank; enrollment will not be repeated.'}
  }
}
if ($RecoverUpgrade) {
  if (-not $svc) { throw 'Recovery requires this installation service; preserve files for review.' }
  if ($ClaimCode -or $ClaimApi -or $ClaimSerial) { throw 'Recovery cannot enroll or replace identity.' }
  . (Join-Path $here 'upgrade-transaction.ps1')
  Invoke-EdgeUpgrade -Directory $dir -Recover
  $global:LASTEXITCODE=0
  return
}
if (-not (Test-Path $src)) { throw "edge-agent executable not found next to this script." }
$newVer = & $src -version
if ($LASTEXITCODE -ne 0) { throw "This executable does not run on this machine." }

$pending = Join-Path $dir 'upgrade-pending.json'
if ((Test-Path -LiteralPath $pending) -or $svc) {
  if (-not $svc -or -not (Test-Path -LiteralPath $exe)) { throw 'Existing installation is incomplete. Preserve its state for review.' }
  if ($ClaimCode -or $ClaimApi -or $ClaimSerial) { throw 'An upgrade or recovery cannot enroll or replace an existing gateway identity. Leave claim fields blank.' }
  . (Join-Path $here 'upgrade-transaction.ps1')
  Invoke-EdgeUpgrade -Directory $dir -Candidate $src
  $global:LASTEXITCODE=0
  return
}
# Identity and keys live here: restrict to SYSTEM and Administrators.
icacls $dir /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
Assert-EdgeNativeSuccess $LASTEXITCODE 'Identity folder permission setup'
icacls $dir /setowner '*S-1-5-32-544' | Out-Null
Assert-EdgeNativeSuccess $LASTEXITCODE 'Installation owner setup'
Assert-EdgeUpgradeAcl $dir
if(Test-Path -LiteralPath $exe){throw 'An executable exists without its service. Preserve it and review the earlier installation before replacing it.'}
$freshArchive=Join-Path $dir ('install-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $freshArchive | Out-Null
$freshConfig=Join-Path $dir 'edge-agent.yaml'
$freshConfigHash=if(Test-Path -LiteralPath $freshConfig){Assert-EdgeUpgradePlain $freshConfig;Get-EdgeUpgradeHash $freshConfig}else{Get-EdgeUpgradeHash (Join-Path $here 'edge-agent.example.yaml')}
$freshRecord=@{version=1;root=[IO.Path]::GetFullPath($dir);candidate=(Get-EdgeUpgradeHash $src);archive=$freshArchive;enrollment_requested=[bool]$ClaimCode;config_sha256=$freshConfigHash}
Write-EdgeUpgradeRecord (Join-Path $freshArchive 'intent.json') $freshRecord
Write-EdgeUpgradeRecord $freshPending $freshRecord
Set-EdgeUpgradeExecutable $src $exe $freshRecord.candidate
if (-not (Test-Path "$dir\edge-agent.yaml")) { Copy-Item (Join-Path $here "edge-agent.example.yaml") "$dir\edge-agent.yaml" }

if ($ClaimCode) {
  & $exe -identity-dir (Join-Path $dir data) -claim-api $ClaimApi -claim-code $ClaimCode -claim-serial $ClaimSerial
  Assert-EdgeNativeSuccess $LASTEXITCODE 'Gateway enrollment'
}

if (-not $svc) {
  New-Service -Name HexmonEdge -DisplayName "HexThings Edge Agent" -BinaryPathName "`"$exe`" -config `"$dir\edge-agent.yaml`" -identity-dir `"$dir\data`"" -StartupType Manual -Description "Hexmon edge gateway agent" | Out-Null
  sc.exe failure HexmonEdge reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
  Assert-EdgeNativeSuccess $LASTEXITCODE 'Service recovery configuration'
}

Write-EdgeUpgradeRecord (Join-Path $freshArchive 'completed.json') $freshRecord
Remove-Item -LiteralPath $freshPending -Force
$global:LASTEXITCODE=0

Write-Host "Installed. Check the config:  & `"$exe`" -check-config -config `"$dir\edge-agent.yaml`""
Write-Host "Serial ports on this PC:      & `"$exe`" -list-ports"
Write-Host "Then: Start-Service HexmonEdge   (logs: $dir\data\logs\edge-agent.log)"

} finally {$installationLock.Dispose()}
