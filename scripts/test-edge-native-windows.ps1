# Disposable Windows runner only. Real service operations, no enrollment/hardware/reboot.
$ErrorActionPreference='Stop'
if (-not $env:GITHUB_ACTIONS -or $env:RUNNER_OS -ne 'Windows') { throw 'Only run on a disposable Windows Actions runner' }
if (Get-Service HexmonEdge -ErrorAction SilentlyContinue) { throw 'Refusing an existing service' }
$stage=Join-Path $env:RUNNER_TEMP 'edge-native-lifecycle'
New-Item -ItemType Directory -Force $stage|Out-Null
Copy-Item "$PSScriptRoot/../edge/packaging/*" $stage -Recurse -Force
Push-Location "$PSScriptRoot/../edge"
try {
 # Real Windows file locking/recovery and synthetic HTTPS contracts. No physical broker/meter.
 go test -v ./internal/setup -count=1
 if($LASTEXITCODE -ne 0){throw 'Native secure pairing setup tests failed'}
 go build -o "$stage/edge-agent.exe" ./cmd/edge-agent
 if($LASTEXITCODE -ne 0){throw 'Native agent build failed'}
} finally {Pop-Location}
$dir=Join-Path $env:ProgramData 'Hexmon/edge'
try {
 # Actual new buttons -> real installer backend -> real SCM. Only the modal
 # Yes/No answer is supplied by this disposable test, never auto-confirmed in product.
 . "$stage/setup-wizard.ps1" -NoShow
 $here=$stage
 function Confirm-SetupRecovery([string]$Title,[string]$Message){return $true}
 $form.Show();[System.Windows.Forms.Application]::DoEvents()
 $script:recoveryFrames=New-Object System.Collections.ArrayList
 function Invoke-NativeRecoveryClick([string]$Label,[string]$Expected) {
  $buttons=@($form.Controls|Where-Object {$_.Text -eq $Label -and $_ -is [System.Windows.Forms.Button]})
  if($buttons.Count -ne 1){throw 'Missing actual recovery control'}
  $form.ScrollControlIntoView($buttons[0]);$buttons[0].PerformClick();[System.Windows.Forms.Application]::DoEvents()
  if($output.Text -notmatch $Expected -or $output.Text.StartsWith('FAILED:')){throw ('Actual recovery button failed: '+$output.Text)}
  $form.ScrollControlIntoView($output);$form.Refresh();[System.Windows.Forms.Application]::DoEvents()
  $image=New-Object System.Drawing.Bitmap($form.Width,$form.Height)
  $form.DrawToBitmap($image,(New-Object System.Drawing.Rectangle(0,0,$form.Width,$form.Height)))
  [void]$script:recoveryFrames.Add($image)
 }
 # Actual install-only service-registration acknowledgement loss. No enrollment.
 $realNewService=(Get-Command New-Service -CommandType Cmdlet)
 function New-Service {
  param($Name,$DisplayName,$BinaryPathName,$StartupType,$Description)
  & $realNewService -Name $Name -DisplayName $DisplayName -BinaryPathName $BinaryPathName -StartupType $StartupType -Description $Description |Out-Null
  throw 'fixture actual service creation acknowledgement loss'
 }
 $blocked=$false;try{& "$stage/install-windows.ps1"}catch{$blocked=$true}
 if(-not $blocked -or -not(Test-Path "$dir/install-pending.json")){throw 'First-install acknowledgement loss lost pending state'}
 Remove-Item Function:New-Service
 $review=(& "$stage/install-windows.ps1" -ReviewInstall)|ConvertFrom-Json
 if(-not $review.review_only -or $review.binary -ne 'exact_candidate' -or -not $review.config_exact -or $review.service -ne 'Stopped' -or $review.can_start -or $review.can_retry_enrollment){throw 'Actual first-install review lost exact stopped state'}
 $blocked=$false;try{& "$stage/install-windows.ps1"}catch{$blocked=$true};if(-not $blocked){throw 'Pending first-install rerun was not refused'}
 Invoke-NativeRecoveryClick 'Review install' 'Review only'
 Invoke-NativeRecoveryClick 'Resume install only' 'stopped/manual'
 if(Test-Path "$dir/install-pending.json"){throw 'Explicit native install-only resume did not settle'}
 if($LASTEXITCODE -ne 0){throw 'Native install-only recovery failed'}
 Write-Host 'PASS actual first-install SCM acknowledgement loss, read-only review, refused rerun and explicit stopped/manual no-enrollment resume.'

 $service=Get-CimInstance Win32_Service -Filter "Name='HexmonEdge'"
 if($service.StartMode -ne 'Manual' -or $service.State -ne 'Stopped'){throw 'New install must be stopped/manual'}
 . "$stage/setup-wizard.ps1" -ContractOnly
 function Confirm-SetupRecovery([string]$Title,[string]$Message){return $true}
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
'@ | Set-Content "$stage/connection-only.yaml"
 # Mirror an actual lab enrollment: generated key saved, no signed cert returned.
 Set-Content "$dir/data/identity.key" 'fixture-only-key-not-a-real-secret'
 $keyHash=(Get-FileHash "$dir/data/identity.key").Hash
 Import-SetupConfig "$stage/connection-only.yaml" $false
 $blocked=$false;try{Assert-ReadyConfig}catch{$blocked=$true};if($blocked){throw 'Manager rejected connection-only config with saved lab key'}
 & "$dir/edge-agent.exe" -check-config -config "$dir/edge-agent.yaml" -identity-dir "$dir/data"
 if($LASTEXITCODE -ne 0){throw 'Installed native configuration check rejected saved key-only lab identity'}
 if((Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Configuration check changed identity key'}
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
 # Actual running-service local control. No device is configured or read.
 $receipt=Invoke-SetupExplorer 'open'
 $page=Invoke-WebRequest -UseBasicParsing -Uri $receipt.url -TimeoutSec 5
 if($page.Content -notmatch 'Modbus explorer'){throw 'Explorer receipt did not identify the actual handler'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or (Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Explorer changed saved config or identity'}
 $null=Invoke-SetupExplorer 'close'
 $closed=$false;try{$null=Invoke-WebRequest -UseBasicParsing -Uri $receipt.url -TimeoutSec 2}catch{$closed=$true};if(-not $closed){throw 'Explorer listener survived Close'}
 if((Get-Service HexmonEdge).Status -ne 'Running'){throw 'Explorer stopped telemetry service'}
 Write-Host 'PASS native Open/Close explorer receipt, local handler, no configured device or service restart. GUI pixels/clicks are separate.'

 # Native SCM fault path: failure after actual running-service replacement.
 # Same built binary only, not historical-binary compatibility or power loss.
 . "$stage/upgrade-transaction.ps1"
 $realHealth=${function:Test-EdgeUpgradeHealth}
 function Test-EdgeUpgradeHealth {param($Executable,$Config);throw 'fixture health acknowledgement loss'}
 $blocked=$false;try{Invoke-EdgeUpgrade -Directory $dir -Candidate "$stage/edge-agent.exe"}catch{$blocked=$true}
 if(-not $blocked -or -not(Test-Path "$dir/upgrade-pending.json")){throw 'Actual service interruption lost upgrade intent'}
 $blocked=$false;try{& "$stage/install-windows.ps1"}catch{$blocked=$true};if(-not $blocked){throw 'Installer repeated an unresolved native upgrade'}
 $blocked=$false;try{Assert-ReadyConfig}catch{$blocked=$true};if(-not $blocked){throw 'Manager permitted Start during unresolved native upgrade'}
 $historicalSource='bfe5d89b46324a566fd089b6c83ab9c111dc9231'
 $historicalTree=Join-Path $env:RUNNER_TEMP 'edge-fixed-historical'
 git -C (Join-Path $PSScriptRoot '..') worktree add --detach $historicalTree $historicalSource
 if($LASTEXITCODE -ne 0){throw 'Fixed historical source is unavailable; historical native proof cannot be claimed'}
 try {
  Push-Location (Join-Path $historicalTree edge)
  try {
   go build -trimpath -o "$stage/edge-agent-historical.exe" ./cmd/edge-agent
   if($LASTEXITCODE -ne 0){throw 'Fixed historical Windows agent build failed'}
  } finally {Pop-Location}
 } finally {
  git -C (Join-Path $PSScriptRoot '..') worktree remove --force $historicalTree
  if($LASTEXITCODE -ne 0){throw 'Historical source cleanup failed'}
 }
 $oldHash=(Get-FileHash "$stage/edge-agent-historical.exe").Hash
 $newHash=(Get-FileHash "$stage/edge-agent.exe").Hash
 if($oldHash -eq $newHash){throw 'Historical/current binaries must be distinct'}
 Write-Host ('WINDOWS_HISTORICAL_CURRENT='+(@{historical_source=$historicalSource;historical_sha256=$oldHash;current_sha256=$newHash}|ConvertTo-Json -Compress))
 Invoke-NativeRecoveryClick 'Restore previous executable' 'Service is stopped'
 if(Test-Path "$dir/upgrade-pending.json"){throw 'Recovery did not settle intent'}
 if((Get-Service HexmonEdge).Status -ne 'Stopped'){throw 'Recovery must not restart automatically'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or (Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Upgrade recovery changed retained config/key'}
 Set-Item Function:Test-EdgeUpgradeHealth $realHealth
 # Establish the fixed historical binary in the same stopped installation,
 # preserving the actual saved key/config. This is a fixture step, not an old installer.
 Copy-Item "$stage/edge-agent-historical.exe" "$dir/edge-agent.exe" -Force
 Start-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 $historicalReady=$false
 for($i=0;$i -lt 20;$i++) {
  & "$dir/edge-agent.exe" -health -config "$dir/edge-agent.yaml" |Out-Host
  if($LASTEXITCODE -in @(0,2)){$historicalReady=$true;break};Start-Sleep -Seconds 1
 }
 if(-not $historicalReady){throw 'Fixed historical service is not locally healthy'}
 # Explicit production binary transaction, with a failure after candidate starts.
 function Test-EdgeUpgradeHealth {param($Executable,$Config);throw 'fixture distinct candidate health acknowledgement loss'}
 $blocked=$false;try{Invoke-EdgeUpgrade -Directory $dir -Candidate "$stage/edge-agent.exe"}catch{$blocked=$true}
 if(-not $blocked -or (Get-FileHash "$dir/edge-agent.exe").Hash -ne $newHash){throw 'Distinct candidate replacement failure was not exercised'}
 Invoke-NativeRecoveryClick 'Restore previous executable' 'Service is stopped'
 if((Get-FileHash "$dir/edge-agent.exe").Hash -ne $oldHash -or (Get-Service HexmonEdge).Status -ne 'Stopped'){throw 'Explicit recovery did not restore distinct historical binary stopped'}
 Set-Item Function:Test-EdgeUpgradeHealth $realHealth
 Start-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 Invoke-EdgeUpgrade -Directory $dir -Candidate "$stage/edge-agent.exe"
 if((Get-FileHash "$dir/edge-agent.exe").Hash -ne $newHash -or (Get-Service HexmonEdge).Status -ne 'Running'){throw 'Second historical/current update failed'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or (Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Historical/current binary transitions changed config or identity'}
 Write-Host 'PASS distinct fixed historical/current running service -> candidate failure -> explicit historical stopped restore -> separate historical restart -> second current update; retained key/config. Initial historical binary fixture-created; no old-installer, remote enrollment, actual meter, reboot or power-cut proof.'
 Stop-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
 Write-Host 'PASS native service upgrade acknowledgement-loss, blocked retry/Start, explicit stopped prior-binary recovery. Same built binary; no reboot/power-loss/historical compatibility proof.'
 Start-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30))
 Stop-Service HexmonEdge
 (Get-Service HexmonEdge).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30))
 & "$stage/install-windows.ps1"
 if($LASTEXITCODE -ne 0){throw 'Stopped-service upgrade failed'}
 if((Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Identity key changed'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or -not(Test-Path "$dir/data/ci-retain-marker")){throw 'Upgrade changed config/identity fixture'}
 & "$stage/uninstall-windows.ps1"
 if(Get-Service HexmonEdge -ErrorAction SilentlyContinue){throw 'Service remains after uninstall'}
 if(Test-Path "$dir/edge-agent.exe"){throw 'Agent binary remains'}
 if((Get-FileHash "$dir/data/identity.key").Hash -ne $keyHash){throw 'Identity key changed'}
 if((Get-FileHash "$dir/edge-agent.yaml").Hash -ne $hash -or -not(Test-Path "$dir/data/ci-retain-marker")){throw 'Uninstall did not retain config/identity fixture'}
 $combined=New-Object System.Drawing.Bitmap($form.Width,($form.Height*$script:recoveryFrames.Count))
 $graphics=[System.Drawing.Graphics]::FromImage($combined)
 for($i=0;$i -lt $script:recoveryFrames.Count;$i++){$graphics.DrawImageUnscaled($script:recoveryFrames[$i],0,($i*$form.Height))}
 $combined.Save((Join-Path $env:RUNNER_TEMP 'hexthings-edge-setup.png'),[System.Drawing.Imaging.ImageFormat]::Png)
 $graphics.Dispose();$combined.Dispose();foreach($image in $script:recoveryFrames){$image.Dispose()}
 Write-Host 'PASS integrated actual new recovery button PerformClick -> actual reviewed installer -> real SCM first-install resume and historical/current restore, with captured state frames. Modal answer supplied by fixture; no physical pointer/UAC/reboot/meter proof.'
 Write-Host 'PASS real native service install, manual/automatic settings, start/health/stop, stopped upgrade and retained-config uninstall. No reboot/UAC/GUI-click/telemetry proof.'
} finally {
 if($form){$form.Close();$form.Dispose()}
 if(Get-Service HexmonEdge -ErrorAction SilentlyContinue){Stop-Service HexmonEdge -Force -ErrorAction SilentlyContinue;sc.exe delete HexmonEdge|Out-Null}
}
