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
} finally {$env:ProgramData=$old;Remove-Item $tmp -Recurse -Force}
