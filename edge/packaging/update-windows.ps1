# Verifies a downloaded edge package against SHA256SUMS, then upgrades in place with automatic rollback.
#   .\update-windows.ps1 -Package hexmon-edge-<ver>-windows-<arch>.zip [-Sums SHA256SUMS]
param([Parameter(Mandatory)][string]$Package, [string]$Sums = "")
$ErrorActionPreference = "Stop"
if (-not $Sums) { $Sums = Join-Path (Split-Path -Parent (Resolve-Path $Package)) "SHA256SUMS" }
if (-not (Test-Path $Sums)) { throw "No SHA256SUMS next to the package; pass -Sums." }
$name = Split-Path -Leaf $Package
$line = Get-Content $Sums | Where-Object { ($_ -split "\s+", 2)[1].TrimStart("*") -eq $name } | Select-Object -First 1
if (-not $line) { throw "$name is not listed in $Sums" }
$want = ($line -split "\s+")[0].ToLower()
$got = (Get-FileHash $Package -Algorithm SHA256).Hash.ToLower()
if ($want -ne $got) { throw "Checksum mismatch for ${name}: refusing to install." }
$tmp = Join-Path $env:TEMP ("hexmon-edge-" + [guid]::NewGuid())
Expand-Archive $Package $tmp
try { & (Join-Path (Get-ChildItem $tmp -Directory | Select-Object -First 1).FullName "install-windows.ps1") } finally { Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue }
