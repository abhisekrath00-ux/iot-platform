# Remove only the HexThings edge service and binary. Identity/config retained unless explicit -Purge.
param([switch]$Purge)
$ErrorActionPreference = 'Stop'
$dir = Join-Path $env:ProgramData 'Hexmon\edge'
if (Test-Path -LiteralPath (Join-Path $dir 'upgrade-pending.json')) { throw 'Unresolved executable upgrade. Preserve state and explicitly recover it before uninstall or purge.' }
if (Test-Path -LiteralPath (Join-Path $dir 'install-pending.json')) { throw 'Unresolved first installation. Preserve its files and identity for review before uninstall or purge.' }
$svc = Get-Service HexmonEdge -ErrorAction SilentlyContinue
if ($svc) {
 $installed = Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
 if (-not $installed -or $installed.PathName -notlike ('"' + $dir + '\edge-agent.exe" *')) { throw 'HexmonEdge service binary is not this installation. Refusing removal.' }
 if ($svc.Status -ne 'Stopped') { Stop-Service HexmonEdge -Force; $svc.WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30)) }
 sc.exe delete HexmonEdge | Out-Null
 if ($LASTEXITCODE -ne 0) { throw "Service deletion failed (exit $LASTEXITCODE)." }
}
foreach ($file in @('edge-agent.exe','edge-agent.prev.exe')) { $path=Join-Path $dir $file;if (Test-Path $path) { Remove-Item $path -Force } }
$shortcut=Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'HexThings Edge.lnk'
if (Test-Path $shortcut) { Remove-Item $shortcut -Force }
if ($Purge) { Remove-Item $dir -Recurse -Force;Write-Host 'Removed including config/identity/queue/logs.' }
else { Write-Host "Service and agent executable removed. Kept $dir (config, identity, queue, logs and local manager). No purge performed." }
