# No real Docker/account mutations. SecureString/encoding and stdin contracts.
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'admin-password.ps1')
$script:prompts=0
$script:fixture='fixture-only-long-password'
function Read-Host {param($Prompt,[switch]$AsSecureString);$script:prompts++;return ConvertTo-SecureString $script:fixture -AsPlainText -Force}
# InputRedirected can be true under CI, so test secure conversion with a dedicated helper's interactive branch.
$source=Get-Content (Join-Path $PSScriptRoot 'admin-password.ps1') -Raw
$source=$source.Replace('if ($Unattended -or [Console]::IsInputRedirected)', 'if ($Unattended)')
Invoke-Expression $source
$p=Get-NewAdminPassword 'fixture@example.invalid' $false $false
if($p -cne $script:fixture -or $script:prompts -ne 2){throw 'Confirmed secure input failed'}
$blocked=$false;try{Get-NewAdminPassword 'fixture@example.invalid' $false $true}catch{$blocked=$true};if(-not $blocked){throw 'Unattended default allowed'}
$script:fixture='short';$blocked=$false;try{Get-NewAdminPassword 'fixture@example.invalid' $false $false}catch{$blocked=$true};if(-not $blocked){throw 'Weak password accepted'}
$script:fixture='fixture-only-long-password'
$script:prompts=0
function Read-Host {param($Prompt,[switch]$AsSecureString);$script:prompts++;return ConvertTo-SecureString ($script:fixture + $script:prompts) -AsPlainText -Force}
$blocked=$false;try{Get-NewAdminPassword 'fixture@example.invalid' $false $false}catch{$blocked=$true};if(-not $blocked){throw 'Mismatch accepted'}
function docker {
 $script:capturedArgs=$args -join ' '
 $script:captured=($input|Out-String).TrimEnd("`r","`n")
 $global:LASTEXITCODE=if($script:failNative){23}else{0}
}
$tmp=Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString('N')+'.log')
try {
 $secret='fixture-utf8-password-' + [char]0x00e9
 $script:failNative=$false;Invoke-NewAdminCreate 'fixture' 'fixture@example.invalid' $secret $tmp
 if($script:captured -cne $secret -or $script:capturedArgs.Contains($secret) -or -not $script:capturedArgs.Contains('--password-stdin')){throw 'Secret stdin/argv contract failed'}
 if((Test-Path $tmp) -and (Get-Content $tmp -Raw).Contains($secret)){throw 'Secret logged'}
 $script:failNative=$true;$blocked=$false;try{Invoke-NewAdminCreate 'fixture' 'fixture@example.invalid' $secret $tmp}catch{$blocked=$true};if(-not $blocked){throw 'Native failure not propagated'}
 Write-Host 'PASS secure-entry confirmation, weak/mismatch/unattended refusal, UTF8 stdin, no password argv/log, native failure propagation. Docker and interactive host stubbed.'
} finally {Remove-Item $tmp -Force -ErrorAction SilentlyContinue}

# The intentional failing native mock must not become the CI host's process exit.
$global:LASTEXITCODE=0
