# Pure contract tests. No WSL, Docker Desktop or real product installation is performed.
$ErrorActionPreference = 'Stop'
. "$PSScriptRoot/windows-desktop.ps1"
function Assert($ok,$name) { if (-not $ok) { throw "FAIL: $name" }; Write-Host "PASS: $name" }
function Refuses($work) { try { & $work; return $false } catch { return $true } }
$f = [pscustomobject]@{ProductType=1;Build=22631;Arch='AMD64';MemMB=8192;DiskMB=30000}
Assert-WindowsHost $f
Assert $true 'Windows11 x64/resource boundary accepted'
foreach ($field in @('ProductType','Build','Arch','MemMB','DiskMB')) {
  $g = $f.PSObject.Copy()
  switch ($field) { ProductType {$g.ProductType=3}; Build {$g.Build=19045}; Arch {$g.Arch='ARM64'}; MemMB {$g.MemMB=8191}; DiskMB {$g.DiskMB=20479} }
  Assert (Refuses { Assert-WindowsHost $g }) "unsafe/unsupported $field refused"
}
$cfg = '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1","target":80}]}}}' | ConvertFrom-Json
Assert-LoopbackCompose $cfg
Assert $true 'loopback Compose accepted'
$cfg.services.web.ports[0].host_ip='0.0.0.0'
Assert (Refuses { Assert-LoopbackCompose $cfg }) 'public Compose binding refused'
$cfg = '{"services":{"web":{"ports":[{"host_ip":"127.0.0.1"}],"network_mode":"host"}}}' | ConvertFrom-Json
Assert (Refuses { Assert-LoopbackCompose $cfg }) 'host networking refused'
Complete-WindowsSetup 'C:\Example' $true
Assert $true 'unattended completion is nonblocking'
$tokens=$null;$errors=$null
foreach ($file in @('windows-desktop.ps1','prereq.ps1','install.ps1','hexthings.ps1','hx-ui.ps1','hx-tools.ps1')) {
  [void][Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $file),[ref]$tokens,[ref]$errors)
  Assert ($errors.Count -eq 0) "$file parses"
}
Write-Host 'Contract-only tests passed. This does not prove Windows11, WSL2, Docker Desktop or live services.'
