# Disposable Windows runner only. Real service operations, no enrollment/hardware/reboot.
$ErrorActionPreference='Stop'
if (-not $env:GITHUB_ACTIONS -or $env:RUNNER_OS -ne 'Windows') { throw 'Only run on a disposable Windows Actions runner' }
if (Get-Service HexmonEdge -ErrorAction SilentlyContinue) { throw 'Refusing an existing service' }
$stage=Join-Path $env:RUNNER_TEMP 'edge-native-lifecycle'
New-Item -ItemType Directory -Force $stage|Out-Null
Copy-Item "$PSScriptRoot/../edge/packaging/*" $stage -Recurse -Force
Push-Location "$PSScriptRoot/../edge"
try { go build -o "$stage/edge-agent.exe" ./cmd/edge-agent;if($LASTEXITCODE -ne 0){throw 'Native agent build failed'} } finally {Pop-Location}
$dir=Join-Path $env:ProgramData 'Hexmon/edge'
try {
 & "$stage/install-windows.ps1"
 if($LASTEXITCODE -ne 0){throw 'Native installation failed'}
 $service=Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
 if($service.StartMode -ne 'Manual' -or $service.State -ne 'Stopped'){throw 'New install must be stopped/manual'}
 . "$stage/setup-wizard.ps1" -ContractOnly
 $blocked=$false;try{Set-SetupBoot $true}catch{$blocked=$true};if(-not $blocked){throw 'Example config must not enable boot startup'}
 @'
gateway_id: ci-fixture
tenant_id: ci-fixture
serial: ci-fixture
mqtt:
  host: 127.0.0.1
  port: 18883
  tls: false
autodetect:
  enabled: false
ui:
  listen: 127.0.0.1:8088
devices: []
'@ | Set-Content "$dir/edge-agent.yaml"
 Set-Content "$dir/data/ci-retain-marker" 'retain'
 $hash=(Get-FileHash "$dir/edge-agent.yaml").Hash
 Set-SetupBoot $true
 if((Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'").StartMode -ne 'Auto'){throw 'Automatic service choice failed'}
 Set-SetupBoot $false
 if((Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'").StartMode -ne 'Manual'){throw 'Manual service choice failed'}
 Start-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 $ready=$false
 for($i=0;$i -lt 20;$i++) {
  & "$dir/edge-agent.exe" -health -config "$dir/edge-agent.yaml" |Out-Host
  if($LASTEXITCODE -in @(0,2)){$ready=$true;break};Start-Sleep -Seconds 1
 }
 if(-not $ready){throw 'No local health response'}
 Stop-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
 & "$stage/install-windows.ps1"
 if($LASTEXITCODE -ne 0){throw 'Stopped-service upgrade failed'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or -not(Test-Path "$dir/data/ci-retain-marker")){throw 'Upgrade changed config/identity fixture'}
 & "$stage/uninstall-windows.ps1"
 if(Get-Service HexmonEdge -ErrorAction SilentlyContinue){throw 'Service remains after uninstall'}
 if(Test-Path "$dir/edge-agent.exe"){throw 'Agent binary remains'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or -not(Test-Path "$dir/data/ci-retain-marker")){throw 'Uninstall did not retain config/identity fixture'}
 Write-Host 'PASS real native service install, manual/automatic settings, start/health/stop, stopped upgrade and retained-config uninstall. No reboot/UAC/GUI-click/telemetry proof.'
} finally {
 if(Get-Service HexmonEdge -ErrorAction SilentlyContinue){Stop-Service HexmonEdge -Force -ErrorAction SilentlyContinue;sc.exe delete HexmonEdge|Out-Null}
}
