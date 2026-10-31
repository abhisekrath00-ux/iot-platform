#!/usr/bin/env python3
"""Actual ZIP+PowerShell7 preflight tests. Not native Windows or Docker proof."""
import os,pathlib,subprocess,tempfile,zipfile
root=pathlib.Path(__file__).resolve().parent.parent
pwsh=os.environ.get('PWSH','pwsh')
base={'repo/docker-compose.yml':'services: {}','repo/.env.example':'fixture','repo/scripts/install.ps1':'# fixture','repo/scripts/hexthings.ps1':'# fixture'}
with tempfile.TemporaryDirectory(prefix='patch guard ') as tmp:
 t=pathlib.Path(tmp);dest=t/'destination';dest.mkdir();(dest/'.env').write_text('private fixture');(dest/'marker').write_text('unchanged')
 runner=t/'run.ps1';runner.write_text('''. $env:HELPER
 $p=Prepare-PatchSource $env:ZIP $env:DEST
 try { if(-not (Test-Path (Join-Path $p.Source '.env.example'))){throw 'missing extracted file'}; Write-Output 'VALIDATED' }
 finally {Remove-Item -LiteralPath $p.Stage -Recurse -Force}
 ''')
 def run(items,ok=False):
  z=t/'source.zip'
  with zipfile.ZipFile(z,'w') as f:
   for k,v in items:f.writestr(k,v)
  r=subprocess.run([pwsh,'-NoProfile','-File',str(runner)],env=dict(os.environ,HELPER=str(root/'scripts/patch-source.ps1'),ZIP=str(z),DEST=str(dest)),capture_output=True,text=True,timeout=20)
  assert (r.returncode==0)==ok,(r.stdout,r.stderr)
  assert (dest/'.env').read_text()=='private fixture' and (dest/'marker').read_text()=='unchanged'
 run(list(base.items()),True)
 for name in ['repo/../outside','/outside','repo/x\\evil','repo/file:stream','repo/CON.txt','repo/a./b','other/file','repo/.env','repo/.env.prod','repo/backups/a','repo/models/a','repo/cache-tls/key','repo/.git/config','repo/scripts/INSTALL.ps1']:
  run([*base.items(),(name,'bad')])
 run([*base.items(),('repo/scripts/install.ps1','duplicate')])
 run([*base.items(),('repo/conflict','file'),('repo/conflict/child','bad')])
 run([('repo/docker-compose.yml','missing other required')])
 link=zipfile.ZipInfo('repo/link');link.create_system=3;link.external_attr=(0o120777<<16)
 run([*base.items(),(link,'/outside')])
 (dest/'scripts').symlink_to(t,target_is_directory=True);run(list(base.items()));(dest/'scripts').unlink()
 (dest/'scripts').symlink_to(t/'nonexistent',target_is_directory=True);run(list(base.items()));(dest/'scripts').unlink()
 (dest/'scripts').mkdir();(dest/'scripts/install.ps1').mkdir();run(list(base.items()))
 # Execute real Do-Update with rejecting archive. No Docker/backup stub may run.
 guard=t/'guard.ps1';guard.write_text("""$tok=$null;$err=$null
 $ast=[System.Management.Automation.Language.Parser]::ParseFile($env:SOURCE,[ref]$tok,[ref]$err)
 if($err.Count){throw 'parse failure'}
 $ast.FindAll({param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Do-Update'},$true)|ForEach-Object {Invoke-Expression ($_.Extent.Text.Replace('$PSScriptRoot', '(Split-Path -Parent $env:SOURCE)'))}
 function Bad($x){Write-Output $x}
 function NeedDocker{Add-Content $env:CALLS 'DOCKER';throw 'unexpected'}
 function BackupCore{Add-Content $env:CALLS 'BACKUP';throw 'unexpected'}
 $PSScriptRoot=Split-Path -Parent $env:SOURCE
 $Root=$env:DEST
 Do-Update $env:ZIP
 """)
 z=t/'unsafe.zip'
 with zipfile.ZipFile(z,'w') as f:
  for k,v in {**base,'repo/.env':'bad'}.items():f.writestr(k,v)
 calls=t/'calls';r=subprocess.run([pwsh,'-NoProfile','-File',str(guard)],env=dict(os.environ,SOURCE=str(root/'scripts/hexthings.ps1'),ZIP=str(z),DEST=str(dest),CALLS=str(calls)),capture_output=True,text=True,timeout=20)
 assert r.returncode!=0 and not calls.exists(),(r.stdout,r.stderr)
 # Successful command-copy path with stub Docker/DB/health, real archive and
 # real destination copying. Does not establish Docker/database correctness.
 import shutil
 shutil.rmtree(dest/'scripts')
 apply=t/'apply.ps1';apply.write_text("""$tok=$null;$err=$null
 $ast=[System.Management.Automation.Language.Parser]::ParseFile($env:SOURCE,[ref]$tok,[ref]$err)
 if($err.Count){throw 'parse failure'}
 $ast.FindAll({param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Do-Update'},$true)|ForEach-Object {Invoke-Expression ($_.Extent.Text.Replace('$PSScriptRoot', '(Split-Path -Parent $env:SOURCE)'))}
 function Bad($x){Write-Output $x}
 function NeedDocker{Add-Content $env:CALLS 'DOCKER'}
 function BackupCore{Add-Content $env:CALLS 'BACKUP'}
 function Step($x){}
 function Banner($x){}
 function Ok($x){}
 function Installed{return 'fixture'}
 function Dc{Add-Content $env:CALLS 'APPLY';return ([int]$env:BUILD_RC)}
 function WaitHealthy($x){return $true}
 $PSScriptRoot=Split-Path -Parent $env:SOURCE
 $Root=$env:DEST;$NoBackup=$false;$VersionFile=Join-Path $Root '.hexthings-version'
 Do-Update $env:ZIP
 """)
 with zipfile.ZipFile(z,'w') as f:
  for k,v in base.items():f.writestr(k,v)
 for rc in [0,8]:
  calls.write_text('')
  r=subprocess.run([pwsh,'-NoProfile','-File',str(apply)],env=dict(os.environ,SOURCE=str(root/'scripts/hexthings.ps1'),ZIP=str(z),DEST=str(dest),CALLS=str(calls),BUILD_RC=str(rc)),capture_output=True,text=True,timeout=20)
  assert r.returncode==(0 if rc==0 else 1),(r.stdout,r.stderr)
  assert calls.read_text().splitlines()==['DOCKER','BACKUP','APPLY']
  assert (dest/'.env').read_text()=='private fixture'
  assert (dest/'.env.example').read_text()=='fixture'
  assert (dest/'scripts/install.ps1').read_text()=='# fixture'
 print('PASS actual ZIP preflight: valid extraction, traversal/case/duplicate/runtime/link/collision/missing-source refusals, destination unchanged (PowerShell7 Linux only)')
