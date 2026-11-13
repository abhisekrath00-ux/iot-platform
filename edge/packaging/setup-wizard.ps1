# Bundled Windows desktop setup/manager. No internet download, embedded secrets or public networking.
param([switch]$ContractOnly, [string]$RenderPreview = "")
$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$dir = Join-Path $env:ProgramData 'Hexmon\edge'
$exe = Join-Path $dir 'edge-agent.exe'
$config = Join-Path $dir 'edge-agent.yaml'
function Assert-SetupExit([int]$Code, [string]$Step) { if ($Code -ne 0) { throw "$Step failed (exit $Code)" } }
function Assert-ReadyConfig {
  if (-not (Test-Path $config)) { throw 'Import your generated gateway YAML first.' }
  $sample = Join-Path $here 'edge-agent.example.yaml'
  if ((Test-Path $sample) -and (Get-FileHash $sample).Hash -eq (Get-FileHash $config).Hash) { throw 'The example YAML is not a commissioned gateway config. Import your generated YAML first.' }
  & $exe -check-config -config $config -identity-dir "$dir\data"
  Assert-SetupExit $LASTEXITCODE 'Configuration check'
}
function Invoke-SetupInstall([hashtable]$Parameters) {
  & (Join-Path $here 'install-windows.ps1') @Parameters
  Assert-SetupExit $LASTEXITCODE 'Agent installation'
}
function Set-SetupBoot([bool]$Enabled) {
  if ($Enabled) { Assert-ReadyConfig }
  if ($Enabled) { Set-Content -Path "$dir\start-on-boot" -Value 'yes' } elseif (Test-Path "$dir\start-on-boot") { Remove-Item "$dir\start-on-boot" -Force }
  $mode = if ($Enabled) { 'Automatic' } else { 'Manual' }
  Set-Service -Name HexmonEdge -StartupType $mode
}
function Import-SetupConfig([string]$candidate, [bool]$StartOnBoot) {
  & $exe -check-config -config $candidate -identity-dir "$dir\data";Assert-SetupExit $LASTEXITCODE 'New configuration check'
  $sample=Join-Path $here 'edge-agent.example.yaml';if ((Get-FileHash $candidate).Hash -eq (Get-FileHash $sample).Hash) { throw 'Do not use the unchanged example config.' }
  if ((Get-Service HexmonEdge).Status -eq 'Running') { throw 'Stop the service before replacing its config.' }
  if (Test-Path $config) { Copy-Item $config "$config.previous" -Force };Copy-Item $candidate $config -Force
  Set-SetupBoot $StartOnBoot
}
function Assert-SecureSetupServer([string]$Address) {
  $uri=$null
  if (-not [Uri]::TryCreate($Address,[UriKind]::Absolute,[ref]$uri) -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or $uri.AbsolutePath -notin @('','/')) { throw 'Enter the server HTTPS address only, without a login or path.' }
  if ($uri.Scheme -ne 'https' -and -not ($uri.Scheme -eq 'http' -and $uri.IsLoopback)) { throw 'Use HTTPS for server pairing. HTTP is only available for a local loopback test.' }
}
function Get-SetupConfig([string]$ServerAddress,[string]$GatewaySerial,[bool]$StartOnBoot,[bool]$AllowSignedSources) {
  Assert-SecureSetupServer $ServerAddress
  if (-not $GatewaySerial.Trim()) { throw 'Enter the saved gateway serial.' }
  if (-not (Test-Path $exe)) { throw 'Install the edge application first.' }
  if ((Get-Service HexmonEdge).Status -eq 'Running') { throw 'Stop the service before refreshing its saved configuration.' }
  $candidate=Join-Path $dir ('setup-'+[guid]::NewGuid().ToString('N')+'.yaml')
  try {
    $flags=@('-fetch-setup','-setup-server',$ServerAddress.Trim(),'-setup-serial',$GatewaySerial.Trim(),'-identity-dir',"$dir\data",'-setup-output',$candidate)
    if ($AllowSignedSources) { $flags+='-allow-managed-devices' }
    & $exe @flags
    Assert-SetupExit $LASTEXITCODE 'Secure server configuration download'
    Import-SetupConfig $candidate $StartOnBoot
    @{ server=$ServerAddress.Trim(); serial=$GatewaySerial.Trim() } | ConvertTo-Json | Set-Content (Join-Path $dir 'setup-connection.json')
  } finally { if (Test-Path $candidate) { Remove-Item $candidate -Force } }
}
function Get-SetupSerialPorts {
  # OS inventory only. This never opens a port or sends Modbus traffic.
  Add-Type -AssemblyName System.IO.Ports
  @([System.IO.Ports.SerialPort]::GetPortNames() | Sort-Object -Unique)
}
function Invoke-SetupPairing([string]$ServerAddress,[string]$GatewaySerial,[string]$Action) {
  Assert-SecureSetupServer $ServerAddress
  if (-not $GatewaySerial.Trim()) { throw 'Enter the saved gateway serial.' }
  if (-not (Test-Path $exe)) { throw 'Install the edge application first.' }
  if ((Get-Service HexmonEdge).Status -eq 'Running') { throw 'Stop the service before certificate pairing or recovery.' }
  $flag=switch ($Action) { 'request' {'-request-secure-pairing'} 'finish' {'-finish-secure-pairing'} 'recover' {'-recover-secure-pairing'} default {throw 'Unknown pairing action'} }
  $text=& $exe $flag -setup-server $ServerAddress.Trim() -setup-serial $GatewaySerial.Trim() -identity-dir "$dir\data" 2>&1
  Assert-SetupExit $LASTEXITCODE "Secure pairing: $text"
  return ($text -join "`r`n")
}
if ($ContractOnly) { return }
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object System.Windows.Forms.Form
$form.Text = 'HexThings Edge - Install and manage'
$workArea=[System.Windows.Forms.Screen]::PrimaryScreen.WorkingArea
$form.Size=New-Object System.Drawing.Size(700,([Math]::Min(800,$workArea.Height-40)))
$form.MinimumSize=New-Object System.Drawing.Size(700,500)
$form.AutoScroll=$true;$form.AutoScrollMinSize=New-Object System.Drawing.Size(0,805)
$form.StartPosition = 'CenterScreen'; $form.AutoScaleMode = 'Dpi'
$font = New-Object System.Drawing.Font('Segoe UI',10); $form.Font = $font
function Label($text,$y) { $x=New-Object System.Windows.Forms.Label; $x.Text=$text;$x.Location=New-Object System.Drawing.Point(22,$y);$x.Size=New-Object System.Drawing.Size(635,30);$form.Controls.Add($x);return $x }
function Field($y) { $x=New-Object System.Windows.Forms.TextBox;$x.Location=New-Object System.Drawing.Point(22,$y);$x.Size=New-Object System.Drawing.Size(635,28);$form.Controls.Add($x);return $x }
$null=Label 'HexThings Edge agent: install, configure, connect, then start' 18
$null=Label 'Server API address (blank installs only; HTTPS for a secure network)' 57
$server=Field 87
$null=Label 'One-time claim code (keep private)' 123
$code=Field 153;$code.UseSystemPasswordChar=$true
$null=Label 'Gateway serial/name from your dashboard claim' 189
$serial=Field 219
if (Test-Path (Join-Path $dir 'setup-connection.json')) { try { $saved=Get-Content (Join-Path $dir 'setup-connection.json') -Raw | ConvertFrom-Json; $server.Text=$saved.server; $serial.Text=$saved.serial } catch {} }
$managed=New-Object System.Windows.Forms.CheckBox;$managed.Text='Allow signed source settings from this server (reloads source polling)';$managed.Location=New-Object System.Drawing.Point(22,294);$managed.Size=New-Object System.Drawing.Size(635,30);$form.Controls.Add($managed)
if (Test-Path $config) { $managed.Checked=[bool](Select-String -Path $config -Pattern '^managed_devices:\s*true\s*$' -Quiet) }
$boot=New-Object System.Windows.Forms.CheckBox;$boot.Text='Start automatically when this PC boots';$boot.Location=New-Object System.Drawing.Point(22,260);$boot.Size=New-Object System.Drawing.Size(630,30);$form.Controls.Add($boot)
$svc=Get-Service HexmonEdge -ErrorAction SilentlyContinue
if ($svc) { $boot.Checked = $svc.StartType -eq 'Automatic' };if (Test-Path "$dir\start-on-boot") { $boot.Checked=$true }
$null=Label 'No automatic discovery or commands. Config is checked before startup.' 330
$output=New-Object System.Windows.Forms.TextBox;$output.Multiline=$true;$output.ReadOnly=$true;$output.ScrollBars='Vertical';$output.Location=New-Object System.Drawing.Point(22,587);$output.Size=New-Object System.Drawing.Size(635,160);$form.Controls.Add($output)
$output.Text="Install requires Administrator access. The agent runs as a Windows service. This build is unsigned.`r`nInstall does not prove a sensor is connected. Existing identity/config are retained."
function Run-Action([scriptblock]$Action) { try { $output.Text='Working...';$form.Refresh(); & $Action; } catch { $output.Text="FAILED: $($_.Exception.Message)`r`nNo claim of complete setup. Check the existing service/config before retrying." } }
function Button($text,$x,$y,$width,[scriptblock]$action) { $b=New-Object System.Windows.Forms.Button;$b.Text=$text;$b.Location=New-Object System.Drawing.Point($x,$y);$b.Size=New-Object System.Drawing.Size($width,38);$b.Add_Click($action);$form.Controls.Add($b) }
Button 'Install / upgrade' 22 372 190 { Run-Action {
  if ($code.Text -and (Test-Path "$dir\data\identity.json")) { throw 'This PC already has a saved gateway identity. Leave the claim code blank and use Connect / get config.' }
  if ($code.Text -and -not ($server.Text -and $serial.Text)) { throw 'Provide server, code and serial together, or leave all blank for install-only.' }
  if ($code.Text) { Assert-SecureSetupServer $server.Text }
  $args=@{};if ($code.Text) { if ($server.Text -notmatch '^https?://') { throw 'Server address must start with http:// or https://' };$args.ClaimApi=$server.Text.Trim();$args.ClaimCode=$code.Text.Trim();$args.ClaimSerial=$serial.Text.Trim() }
  Invoke-SetupInstall $args
  # Keep a durable local manager. On a new install startup remains Manual until configuration.
  Copy-Item (Join-Path $here 'uninstall-windows.ps1') $dir -Force
  Copy-Item (Join-Path $here 'setup-wizard.ps1') $dir -Force
  if ($env:HEXTHINGS_SETUP_EXE -and (Test-Path $env:HEXTHINGS_SETUP_EXE)) { if ([IO.Path]::GetFullPath($env:HEXTHINGS_SETUP_EXE) -ne [IO.Path]::GetFullPath("$dir\HexThings-Edge-Setup.exe")) { Copy-Item $env:HEXTHINGS_SETUP_EXE "$dir\HexThings-Edge-Setup.exe" -Force }
    $shell=New-Object -ComObject WScript.Shell;$shortcut=$shell.CreateShortcut((Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'HexThings Edge.lnk'));$shortcut.TargetPath="$dir\HexThings-Edge-Setup.exe";$shortcut.Save() }
  if ($boot.Checked) { Set-Content -Path "$dir\start-on-boot" -Value 'yes' } else { Set-SetupBoot $false }
  $code.Clear();if ($server.Text -and $serial.Text) { Get-SetupConfig $server.Text $serial.Text $boot.Checked $managed.Checked; $output.Text='Installed and server config checked. Press Start, then Health. A running service is not proof of a sensor reading.' } else { $output.Text='Installed only. Enter your server and gateway serial, then Connect / get config. Keep an existing claim code blank. Your identity and config were retained.' }
} }
Button 'Import YAML (advanced)' 227 470 205 { Run-Action {
  if (-not (Test-Path $exe)) { throw 'Install the agent first.' }
  $dialog=New-Object System.Windows.Forms.OpenFileDialog;$dialog.Filter='YAML files (*.yaml;*.yml)|*.yaml;*.yml';if ($dialog.ShowDialog() -ne 'OK') { $output.Text='Import cancelled.';return }
  $candidate=$dialog.FileName
  Import-SetupConfig $candidate $boot.Checked
  $output.Text='Configuration imported and checked. Startup choice applied. Press Start, then check health and first reading in the dashboard.'
} }
Button 'Show local USB ports' 22 470 190 { Run-Action {
  $ports=@(Get-SetupSerialPorts)
  if ($ports.Count -eq 0) { $output.Text='Windows reports no serial ports. Check that the USB adapter is connected and its driver is installed. No ports were opened or probed.' }
  else { $output.Text="Serial ports Windows currently sees: $($ports -join ', ').`r`nThese are not verified meters. Unplug/replug your adapter and compare this list to identify its port, then choose that exact port in the dashboard. No port was opened, probed or automatically selected." }
} }
Button 'Connect / get config' 227 372 205 { Run-Action { Get-SetupConfig $server.Text $serial.Text $boot.Checked $managed.Checked;$output.Text='Server config imported and checked. Press Start, then Health. Confirm your source reading in the dashboard.' } }
Button 'Apply startup choice' 447 372 210 { Run-Action { Set-SetupBoot $boot.Checked;$output.Text='Startup choice saved. This does not start or stop the service now.' } }
Button 'Start' 22 421 110 { Run-Action { Assert-ReadyConfig;Set-SetupBoot $boot.Checked;Start-Service HexmonEdge;(Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30));$output.Text='Service running. Check health and first sensor reading; running is not proof of telemetry.' } }
Button 'Stop' 142 421 110 { Run-Action { Stop-Service HexmonEdge;(Get-Service HexmonEdge).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30));$output.Text='Service stopped. Boot startup setting is unchanged.' } }
Button 'Health' 262 421 110 { Run-Action { $text=& $exe -health -config $config 2>&1;$rc=$LASTEXITCODE;if ($rc -notin @(0,2)) { throw "Not healthy/running (exit $rc): $text" };$output.Text=if ($rc -eq 2) { "Running, but MQTT broker unreachable. Readings may be buffered. $text" } else { "Local health OK. Still verify sensor readings in HexThings. $text" } } }
Button 'Open logs' 382 421 125 { Run-Action { $log="$dir\data\logs\edge-agent.log";if (-not (Test-Path $log)) { throw 'No agent log yet.' };Start-Process notepad.exe -ArgumentList ('"'+$log+'"');$output.Text='Opened the local agent log.' } }
Button 'Uninstall' 517 421 140 { Run-Action { if ([System.Windows.Forms.MessageBox]::Show('Remove the HexThings Edge service and executable? Config and identity will be retained.','Uninstall edge','YesNo','Warning') -ne 'Yes') { $output.Text='Uninstall cancelled.';return }; & (Join-Path $here 'uninstall-windows.ps1');$output.Text='Uninstall script finished. Config/identity retained. This does not remove the separate HexThings server.' } }
Button 'Send USB ports to dashboard' 447 470 210 { Run-Action {
 Assert-SecureSetupServer $server.Text
 if (-not (Test-Path $exe)) { throw 'Install the edge application first.' }
 $text=& $exe -report-serial-ports -setup-server $server.Text.Trim() -identity-dir "$dir\data" 2>&1
 Assert-SetupExit $LASTEXITCODE "Serial inventory report: $text";$output.Text=($text -join "`r`n")
} }
Button 'Request secure pairing'  22 519 190 { Run-Action { $output.Text=Invoke-SetupPairing $server.Text $serial.Text 'request' } }
Button 'Finish secure pairing' 227 519 205 { Run-Action { $output.Text=Invoke-SetupPairing $server.Text $serial.Text 'finish' } }
Button 'Recover pairing' 447 519 210 { Run-Action { $output.Text=Invoke-SetupPairing $server.Text $serial.Text 'recover' } }
$null=Label 'Local status when running: http://127.0.0.1:8088 (on this PC only)' 759
if ($RenderPreview) {
  $form.Show();$form.Refresh();[System.Windows.Forms.Application]::DoEvents()
  $bitmap=New-Object System.Drawing.Bitmap($form.Width,$form.Height)
  $form.DrawToBitmap($bitmap,(New-Object System.Drawing.Rectangle(0,0,$form.Width,$form.Height)))
  # Preserve top and scrolled-bottom pixels in one artifact on short screens.
  # This verifies hidden controls without pretending the clipped log is visible.
  $form.AutoScrollPosition=New-Object System.Drawing.Point(0,400)
  $form.Refresh();[System.Windows.Forms.Application]::DoEvents()
  $bottom=New-Object System.Drawing.Bitmap($form.Width,$form.Height)
  $form.DrawToBitmap($bottom,(New-Object System.Drawing.Rectangle(0,0,$form.Width,$form.Height)))
  $combined=New-Object System.Drawing.Bitmap($form.Width,($form.Height*2))
  $graphics=[System.Drawing.Graphics]::FromImage($combined)
  $graphics.DrawImageUnscaled($bitmap,0,0);$graphics.DrawImageUnscaled($bottom,0,$form.Height)
  $combined.Save($RenderPreview,[System.Drawing.Imaging.ImageFormat]::Png)
  $graphics.Dispose();$combined.Dispose();$bottom.Dispose();$bitmap.Dispose();$form.Close();return
}
[void]$form.ShowDialog()
