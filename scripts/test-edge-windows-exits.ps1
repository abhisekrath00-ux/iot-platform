# Native process exit regression only, not a service installation or enrollment.
$ErrorActionPreference='Stop'
$source=Join-Path (Split-Path $PSScriptRoot -Parent) 'edge/packaging/install-windows.ps1'
$t=$null;$e=$null;$ast=[Management.Automation.Language.Parser]::ParseFile($source,[ref]$t,[ref]$e)
if($e.Count){throw 'Installer parse failed'}
$functions=@($ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Assert-EdgeNativeSuccess'},$true))
if($functions.Count -ne 1){throw 'Native exit guard missing'}
Invoke-Expression $functions[0].Extent.Text
$hostExe=(Get-Process -Id $PID).Path
& $hostExe -NoProfile -Command 'exit 19'
$code=$LASTEXITCODE
$refused=$false
try { Assert-EdgeNativeSuccess $code 'Gateway enrollment' } catch { $refused=$true; if($_.Exception.Message -notmatch 'exit 19'){throw} }
if(-not $refused){throw 'Nonzero native enrollment exit was ignored'}
Write-Host 'PASS real child-process exit19 blocks enrollment success'
& $hostExe -NoProfile -Command 'exit 0'
Assert-EdgeNativeSuccess $LASTEXITCODE 'Gateway enrollment'
Write-Host 'PASS child-process exit0 accepted'
# Execute the actual enrollment branch with a failing native-command stand-in.
$branch=@($ast.FindAll({param($n) $n -is [Management.Automation.Language.IfStatementAst] -and $n.Extent.Text.StartsWith('if ($ClaimCode)')},$true))
if($branch.Count -ne 1){throw 'Enrollment branch missing'}
$tmp=Join-Path ([IO.Path]::GetTempPath()) ('edge-exit-'+[guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $script:seenArgs=@()
  function Invoke-FailingEdge { $script:seenArgs=@($args); & $hostExe -NoProfile -Command 'exit 23' }
  $exe='Invoke-FailingEdge';$dir=$tmp;$ClaimCode='synthetic-code';$ClaimApi='https://synthetic.invalid';$ClaimSerial='synthetic-serial'
  $refused=$false
  try { Invoke-Expression $branch[0].Extent.Text; throw 'success marker reached' } catch {
    if($_.Exception.Message -notmatch 'Gateway enrollment failed \(exit 23\)'){throw};$refused=$true
  }
  if(-not $refused -or '-identity-dir' -notin $script:seenArgs -or ($dir + '\data') -notin $script:seenArgs){throw 'Enrollment identity/exit binding failed'}
  Write-Host 'PASS actual enrollment branch refuses child exit23 and uses service data identity directory'
  $guide=Join-Path (Split-Path $PSScriptRoot -Parent) 'edge/packaging/install.ps1'
  $text=Get-Content -LiteralPath $guide -Raw
  if($text.IndexOf('& (Join-Path $here "install-windows.ps1") @a') -gt $text.IndexOf('Ok "service installed"')){throw 'Success message precedes installer'}
  Write-Host 'PASS guided installer success follows checked low-level installer'
} finally { Remove-Item -LiteralPath $tmp -Recurse -Force }
Write-Host 'Native process/branch contract only: no Docker, live enrollment, service install or sensor test.'
