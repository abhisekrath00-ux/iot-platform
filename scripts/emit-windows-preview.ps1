# Encode only the known synthetic Windows form screenshot into ordinary logs.
# No artifact/cache upload, external storage, secrets or arbitrary file selection.
$ErrorActionPreference='Stop'
$path=Join-Path $env:RUNNER_TEMP 'hexthings-edge-setup.png'
$bytes=[IO.File]::ReadAllBytes($path)
if($bytes.Length -lt 8 -or $bytes.Length -gt 2097152 -or [BitConverter]::ToString($bytes[0..7]) -ne '89-50-4E-47-0D-0A-1A-0A'){throw 'Expected bounded PNG preview only'}
$sha=[Security.Cryptography.SHA256]::Create()
try{$hash=([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace('-','').ToLower()}finally{$sha.Dispose()}
$encoded=[Convert]::ToBase64String($bytes)
Write-Host ('HEX_PREVIEW_BEGIN bytes='+$bytes.Length+' sha256='+$hash)
for($i=0;$i -lt $encoded.Length;$i+=4096){Write-Host ('HEX_PREVIEW_DATA '+$encoded.Substring($i,[Math]::Min(4096,$encoded.Length-$i)))}
Write-Host 'HEX_PREVIEW_END'
