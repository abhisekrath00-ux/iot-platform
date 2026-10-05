# Removes the HexThings edge agent service and executable. Config, identity and logs are kept unless -Purge.
param([switch]$Purge)
$ErrorActionPreference = "Stop"
$dir = Join-Path $env:ProgramData "Hexmon\edge"
if (Get-Service HexmonEdge -ErrorAction SilentlyContinue) { Stop-Service HexmonEdge -Force -ErrorAction SilentlyContinue; sc.exe delete HexmonEdge | Out-Null }
Remove-Item "$dir\edge-agent.exe", "$dir\edge-agent.prev.exe" -Force -ErrorAction SilentlyContinue
if ($Purge) { Remove-Item $dir -Recurse -Force; Write-Host "removed, including config, identity and the buffered queue" }
else { Write-Host "removed. Kept $dir (config, identity, queue, logs). Use -Purge to delete it." }
