# Pure startup/config contracts, not service installation or boot execution.
$ErrorActionPreference='Stop'
$old=$env:ProgramData
$tmp=Join-Path ([IO.Path]::GetTempPath()) ('edge-manager-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp|Out-Null
try {
 $env:ProgramData=$tmp
 . (Join-Path $PSScriptRoot '../edge/packaging/setup-wizard.ps1') -ContractOnly
 function Set-Service {param($Name,$StartupType);$script:lastStartup=$StartupType}
 $script:lastStartup='unset';Set-SetupBoot $false
 if($script:lastStartup -ne 'Manual'){throw 'Manual choice was not applied'}
 $blocked=$false;try{Set-SetupBoot $true}catch{$blocked=$true}
 if(-not $blocked -or $script:lastStartup -ne 'Manual'){throw 'Missing config must not enable startup'}
 New-Item -ItemType Directory -Force -Path (Split-Path $config)|Out-Null
 Copy-Item (Join-Path $here 'edge-agent.example.yaml') $config
 $blocked=$false;try{Set-SetupBoot $true}catch{$blocked=$true}
 if(-not $blocked -or $script:lastStartup -ne 'Manual'){throw 'Unchanged sample must not enable startup'}
 function Assert-ReadyConfig {$script:checked=$true}
 $script:checked=$false;Set-SetupBoot $true
 if(-not $script:checked -or $script:lastStartup -ne 'Automatic'){throw 'Automatic startup must follow config gate'}
 $blocked=$false;try{Assert-SetupExit 23 'native fixture'}catch{$blocked=$true};if(-not $blocked){throw 'Nonzero exit must fail'}
 $actualHere=$here;$here=$tmp
 Set-Content (Join-Path $tmp 'install-windows.ps1') 'exit 23'
 $blocked=$false;try{Invoke-SetupInstall @{}}catch{$blocked=$true};if(-not $blocked){throw 'Native installer nonzero must fail manager action'}
 Set-Content (Join-Path $tmp 'install-windows.ps1') 'exit 0'
 Invoke-SetupInstall @{}
 $here=$actualHere
 Write-Host 'PASS Manual/Automatic choice, missing/sample config refusal, gate ordering and native nonzero failure. No real service/boot execution.'
 # Fetch/import contracts exercise only a fake agent and a stopped fake service.
 # Existing real identity/config files, services and network are not used.
 $exe=Join-Path $tmp 'fake-agent.ps1'
 $script:serviceState='Stopped';$script:imported=0
 function Get-Service {param($Name);return @{Status=$script:serviceState}}
 function Import-SetupConfig([string]$candidate,[bool]$StartOnBoot) {
   if (-not (Test-Path $candidate) -or (Get-Content $candidate -Raw) -notmatch 'fixture-config') {throw 'Staging config was not passed to checked import'}
   $script:imported++;$script:importBoot=$StartOnBoot
 }
 @'
$record=@{args=@($args)}|ConvertTo-Json -Depth 3
Set-Content ($MyInvocation.MyCommand.Path+'.args') $record
$outIndex=[array]::IndexOf($args,'-setup-output')
if($outIndex -lt 0){throw 'Missing staged output'}
Set-Content $args[$outIndex+1] 'fixture-config'
$global:LASTEXITCODE=0
'@ | Set-Content $exe
 Get-SetupConfig 'https://server.example' 'SAVED-001' $false $true
 if($script:imported -ne 1 -or $script:importBoot){throw 'Fetch did not import with unchanged manual startup choice'}
 $argv=(Get-Content ($exe+'.args') -Raw|ConvertFrom-Json).args
 if($argv -notcontains '-fetch-setup' -or $argv -notcontains '-allow-managed-devices' -or $argv -contains '-claim-code'){throw 'Fetch attempted enrollment or lost explicit source opt-in'}
 $savedConnection=Get-Content (Join-Path $dir 'setup-connection.json') -Raw
 if($savedConnection -notmatch 'SAVED-001'){throw 'Connection recovery metadata not saved'}
 if(@(Get-ChildItem $dir -Filter 'setup-*.yaml').Count){throw 'Staging file leaked after checked import'}
 $script:serviceState='Running';$blocked=$false
 try{Get-SetupConfig 'https://server.example' 'SAVED-001' $false $true}catch{$blocked=$true}
 if(-not $blocked -or $script:imported -ne 1){throw 'Running service config was replaced'}
 $script:serviceState='Stopped';Set-Content $exe 'exit 23';$blocked=$false
 try{Get-SetupConfig 'https://server.example' 'SAVED-001' $false $true}catch{$blocked=$true}
 if(-not $blocked -or $script:imported -ne 1 -or (Get-Content (Join-Path $dir 'setup-connection.json') -Raw) -ne $savedConnection){throw 'Failed fetch changed recovery/import state'}
 if(@(Get-ChildItem $dir -Filter 'setup-*.yaml').Count){throw 'Staging file leaked after failure'}
 Write-Host 'PASS staged fetch -> checked import, saved server/serial, explicit opt-in, no claim, running-service/fetch-failure refusal. Fake agent only.'

 # Exercise the three pairing paths against a recorder, never a real device.
 @'
@{args=@($args)}|ConvertTo-Json -Depth 3|Set-Content ($MyInvocation.MyCommand.Path+'.args')
$global:LASTEXITCODE=0
'@ | Set-Content $exe
 $savedConfig=Get-FileHash $config
 foreach($action in @('request','finish','recover')) {
  $null=Invoke-SetupPairing 'https://server.example' 'SAVED-001' $action
  $argv=(Get-Content ($exe+'.args') -Raw|ConvertFrom-Json).args
  $want='-'+@{request='request';finish='finish';recover='recover'}[$action]+'-secure-pairing'
  if($argv[0] -ne $want -or $argv -notcontains '-identity-dir' -or $argv -notcontains 'SAVED-001' -or $argv -contains '-claim-code') {throw "Wrong pairing action or identity: $action"}
  if((Get-FileHash $config).Hash -ne $savedConfig.Hash) {throw 'Pairing manager rewrote config'}
 }
 $before=Get-Content ($exe+'.args') -Raw
 $script:serviceState='Running';$blocked=$false
 try {$null=Invoke-SetupPairing 'https://server.example' 'SAVED-001' 'request'}catch{$blocked=$true}
 if(-not $blocked -or (Get-Content ($exe+'.args') -Raw) -ne $before){throw 'Pairing called agent while service running'}
 $script:serviceState='Stopped';Set-Content $exe 'exit 23';$blocked=$false
 try {$null=Invoke-SetupPairing 'https://server.example' 'SAVED-001' 'finish'}catch{$blocked=$true}
 if(-not $blocked){throw 'Pairing agent failure reported success'}
 Write-Host 'PASS pairing request/finish/recover flags, same saved identity/config, running-service refusal and agent nonzero failure. Fake agent only.'


} finally {$env:ProgramData=$old;Remove-Item $tmp -Recurse -Force}
foreach ($address in @('https://server.example','http://127.0.0.1:8000')) { Assert-SecureSetupServer $address }
foreach ($address in @('http://server.example','https://user:pass@server.example','https://server.example/path','https://server.example?q=1','ftp://localhost')) {
 $blocked=$false;try { Assert-SecureSetupServer $address } catch { $blocked=$true };if (-not $blocked) { throw "Unsafe pairing address accepted: $address" }
}
Write-Host 'PASS secure pairing URL gate; this does not prove server connectivity or physical enrollment.'
