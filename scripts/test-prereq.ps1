# Unit test for the decision logic in scripts/prereq.ps1 (needs PowerShell; no Windows, no Docker, nothing installed).
$env:HEXTHINGS_PS_LIBRARY_ONLY = "1"
. "$PSScriptRoot/prereq.ps1"
$fails = 0
function Check($name, $cond) { if ($cond) { Write-Host "ok:   $name" } else { Write-Host "FAIL: $name"; $script:fails++ } }
function F($o) { $b = @{ Build = 22631; MemMB = 8192; DiskMB = 100000; Virtualization = $true; Wsl = $true; DockerInstalled = $true; EngineUp = $true; DockerOk = $true }; foreach ($k in $o.Keys) { $b[$k] = $o[$k] }; [pscustomobject]$b }
$p = Get-Plan (F @{})
Check "everything present: empty plan" ($p.Plan.Count -eq 0 -and $p.Fail.Count -eq 0)
$p = Get-Plan (F @{ DockerInstalled = $false; EngineUp = $false; DockerOk = $false; Wsl = $false })
Check "nothing present: wsl then docker" (($p.Plan -join ",") -eq "wsl,docker")
$p = Get-Plan (F @{ DockerInstalled = $false; EngineUp = $false; DockerOk = $false })
Check "wsl present, docker missing: docker only" (($p.Plan -join ",") -eq "docker")
$p = Get-Plan (F @{ EngineUp = $false; DockerOk = $false })
Check "docker installed, engine stopped: start only" (($p.Plan -join ",") -eq "start")
$p = Get-Plan (F @{ EngineUp = $false; DockerOk = $false; Wsl = $false })
Check "docker installed, engine stopped, wsl off: wsl then start" (($p.Plan -join ",") -eq "wsl,start")
$p = Get-Plan (F @{ Build = 18363 })
Check "old Windows build fails with a reason" ($p.Fail.Count -eq 1 -and $p.Fail[0] -match "too old")
$p = Get-Plan (F @{ DiskMB = 3000 })
Check "low disk fails" ($p.Fail.Count -eq 1 -and $p.Fail[0] -match "5 GB")
$p = Get-Plan (F @{ Virtualization = $false; DockerInstalled = $false; EngineUp = $false; DockerOk = $false })
Check "virtualization off and docker missing fails with BIOS hint" ($p.Fail.Count -eq 1 -and $p.Fail[0] -match "BIOS")
$p = Get-Plan (F @{ Virtualization = $false })
Check "docker already working: virtualization flag ignored" ($p.Fail.Count -eq 0 -and $p.Plan.Count -eq 0)
if ($fails -eq 0) { Write-Host "all prereq.ps1 logic checks passed" } else { Write-Host "$fails failed"; exit 1 }
