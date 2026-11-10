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
function Set-SetupBoot([bool]$Enabled) {
  if ($Enabled) { Assert-ReadyConfig }
  if ($Enabled) { Set-Content -Path "$dir\start-on-boot" -Value 'yes' } elseif (Test-Path "$dir\start-on-boot") { Remove-Item "$dir\start-on-boot" -Force }
  $mode = if ($Enabled) { 'Automatic' } else { 'Manual' }
  Set-Service -Name HexmonEdge -StartupType $mode
}
if ($ContractOnly) { return }
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$form = New-Object System.Windows.Forms.Form
$form.Text = 'HexThings Edge - Install and manage'; $form.Size = New-Object System.Drawing.Size(700,700); $form.MinimumSize = $form.Size
$form.StartPosition = 'CenterScreen'; $form.AutoScaleMode = 'Dpi'
$font = New-Object System.Drawing.Font('Segoe UI',10); $form.Font = $font
function Label($text,$y) { $x=New-Object System.Windows.Forms.Label; $x.Text=$text;$x.Location=New-Object System.Drawing.Point(22,$y);$x.Size=New-Object System.Drawing.Size(635,30);$form.Controls.Add($x);return $x }
function Field($y) { $x=New-Object System.Windows.Forms.TextBox;$x.Location=New-Object System.Drawing.Point(22,$y);$x.Size=New-Object System.Drawing.Size(635,28);$form.Controls.Add($x);return $x }
$null=Label 'HexThings Edge agent: install, configure, then start' 18
$null=Label 'Server API address (blank installs only; HTTPS for a secure network)' 57
$server=Field 87
$null=Label 'One-time claim code (keep private)' 123
$code=Field 153;$code.UseSystemPasswordChar=$true
$null=Label 'Gateway serial/name from your dashboard claim' 189
$serial=Field 219
$boot=New-Object System.Windows.Forms.CheckBox;$boot.Text='Start automatically when this PC boots';$boot.Location=New-Object System.Drawing.Point(22,260);$boot.Size=New-Object System.Drawing.Size(630,30);$form.Controls.Add($boot)
$svc=Get-Service HexmonEdge -ErrorAction SilentlyContinue
if ($svc) { $boot.Checked = $svc.StartType -eq 'Automatic' };if (Test-Path "$dir\start-on-boot") { $boot.Checked=$true }
$null=Label 'Boot startup is applied only after a real config passes checks. No port is opened.' 296
$output=New-Object System.Windows.Forms.TextBox;$output.Multiline=$true;$output.ReadOnly=$true;$output.ScrollBars='Vertical';$output.Location=New-Object System.Drawing.Point(22,440);$output.Size=New-Object System.Drawing.Size(635,160);$form.Controls.Add($output)
$output.Text="Install requires Administrator access. The agent runs as a Windows service. This build is unsigned.`r`nInstall does not prove a sensor is connected. Existing identity/config are retained."
function Run-Action([scriptblock]$Action) { try { $output.Text='Working...';$form.Refresh(); & $Action; } catch { $output.Text="FAILED: $($_.Exception.Message)`r`nNo claim of complete setup. Check the existing service/config before retrying." } }
function Button($text,$x,$y,$width,[scriptblock]$action) { $b=New-Object System.Windows.Forms.Button;$b.Text=$text;$b.Location=New-Object System.Drawing.Point($x,$y);$b.Size=New-Object System.Drawing.Size($width,38);$b.Add_Click($action);$form.Controls.Add($b) }
Button 'Install / upgrade' 22 338 190 { Run-Action {
  if (($server.Text -or $code.Text -or $serial.Text) -and -not ($server.Text -and $code.Text -and $serial.Text)) { throw 'Provide server, code and serial together, or leave all blank for install-only.' }
  $args=@{};if ($server.Text) { if ($server.Text -notmatch '^https?://') { throw 'Server address must start with http:// or https://' };$args.ClaimApi=$server.Text.Trim();$args.ClaimCode=$code.Text.Trim();$args.ClaimSerial=$serial.Text.Trim() }
  & (Join-Path $here 'install-windows.ps1') @args
  # Keep a durable local manager. On a new install startup remains Manual until configuration.
  Copy-Item (Join-Path $here 'uninstall-windows.ps1') $dir -Force
  Copy-Item (Join-Path $here 'setup-wizard.ps1') $dir -Force
  if ($env:HEXTHINGS_SETUP_EXE -and (Test-Path $env:HEXTHINGS_SETUP_EXE)) { if ([IO.Path]::GetFullPath($env:HEXTHINGS_SETUP_EXE) -ne [IO.Path]::GetFullPath("$dir\HexThings-Edge-Setup.exe")) { Copy-Item $env:HEXTHINGS_SETUP_EXE "$dir\HexThings-Edge-Setup.exe" -Force }
    $shell=New-Object -ComObject WScript.Shell;$shortcut=$shell.CreateShortcut((Join-Path ([Environment]::GetFolderPath('CommonPrograms')) 'HexThings Edge.lnk'));$shortcut.TargetPath="$dir\HexThings-Edge-Setup.exe";$shortcut.Save() }
  if ($boot.Checked) { Set-Content -Path "$dir\start-on-boot" -Value 'yes' } else { Set-SetupBoot $false }
  $code.Clear();$output.Text='Installed. Boot preference saved; enable occurs only after config validation. Import generated gateway YAML, then apply startup choice and Start. Existing config/identity were not replaced. No telemetry acceptance yet.'
} }
Button 'Import gateway YAML' 227 338 205 { Run-Action {
  if (-not (Test-Path $exe)) { throw 'Install the agent first.' }
  $dialog=New-Object System.Windows.Forms.OpenFileDialog;$dialog.Filter='YAML files (*.yaml;*.yml)|*.yaml;*.yml';if ($dialog.ShowDialog() -ne 'OK') { $output.Text='Import cancelled.';return }
  $candidate=$dialog.FileName
  & $exe -check-config -config $candidate -identity-dir "$dir\data";Assert-SetupExit $LASTEXITCODE 'New configuration check'
  $sample=Join-Path $here 'edge-agent.example.yaml';if ((Get-FileHash $candidate).Hash -eq (Get-FileHash $sample).Hash) { throw 'Do not use the unchanged example config.' }
  if ((Get-Service HexmonEdge).Status -eq 'Running') { throw 'Stop the service before replacing its config.' }
  if (Test-Path $config) { Copy-Item $config "$config.previous" -Force };Copy-Item $candidate $config -Force
  Set-SetupBoot $boot.Checked;$output.Text='Configuration imported and checked. Startup choice applied. Press Start, then check health and first reading in the dashboard.'
} }
Button 'Apply startup choice' 447 338 210 { Run-Action { Set-SetupBoot $boot.Checked;$output.Text='Startup choice saved. This does not start or stop the service now.' } }
Button 'Start' 22 387 110 { Run-Action { Assert-ReadyConfig;Set-SetupBoot $boot.Checked;Start-Service HexmonEdge;(Get-Service HexmonEdge).WaitForStatus('Running',[TimeSpan]::FromSeconds(30));$output.Text='Service running. Check health and first sensor reading; running is not proof of telemetry.' } }
Button 'Stop' 142 387 110 { Run-Action { Stop-Service HexmonEdge;(Get-Service HexmonEdge).WaitForStatus('Stopped',[TimeSpan]::FromSeconds(30));$output.Text='Service stopped. Boot startup setting is unchanged.' } }
Button 'Health' 262 387 110 { Run-Action { $text=& $exe -health -config $config 2>&1;$rc=$LASTEXITCODE;if ($rc -notin @(0,2)) { throw "Not healthy/running (exit $rc): $text" };$output.Text=if ($rc -eq 2) { "Running, but MQTT broker unreachable. Readings may be buffered. $text" } else { "Local health OK. Still verify sensor readings in HexThings. $text" } } }
Button 'Open logs' 382 387 125 { Run-Action { $log="$dir\data\logs\edge-agent.log";if (-not (Test-Path $log)) { throw 'No agent log yet.' };Start-Process notepad.exe -ArgumentList ('"'+$log+'"');$output.Text='Opened the local agent log.' } }
Button 'Uninstall' 517 387 140 { Run-Action { if ([System.Windows.Forms.MessageBox]::Show('Remove the HexThings Edge service and executable? Config and identity will be retained.','Uninstall edge','YesNo','Warning') -ne 'Yes') { $output.Text='Uninstall cancelled.';return }; & (Join-Path $here 'uninstall-windows.ps1');$output.Text='Uninstall script finished. Config/identity retained. This does not remove the separate HexThings server.' } }
$null=Label 'Local status when running: http://127.0.0.1:8088 (on this PC only)' 615
if ($RenderPreview) {
  $form.Show();$form.Refresh();[System.Windows.Forms.Application]::DoEvents()
  $bitmap=New-Object System.Drawing.Bitmap($form.Width,$form.Height)
  $form.DrawToBitmap($bitmap,(New-Object System.Drawing.Rectangle(0,0,$form.Width,$form.Height)))
  $bitmap.Save($RenderPreview,[System.Drawing.Imaging.ImageFormat]::Png);$bitmap.Dispose();$form.Close();return
}
[void]$form.ShowDialog()
