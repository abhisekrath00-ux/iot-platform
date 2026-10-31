#!/usr/bin/env python3
"""PowerShell7 on Linux command contracts, not Windows/Docker acceptance."""
import os,pathlib,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parent.parent
pwsh=os.environ.get('PWSH','pwsh')
with tempfile.TemporaryDirectory(prefix='private bootstrap ') as tmp:
 t=pathlib.Path(tmp); src=t/'source with spaces';(src/'scripts').mkdir(parents=True)
 (src/'docker-compose.yml').write_text('services: {}');(src/'.env.example').write_text('fixture')
 pre=src/'scripts/prereq.ps1';install=src/'scripts/install.ps1';calls=t/'calls'
 pre.write_text('Add-Content $env:CALLS ("PREREQ " + ($args -join " ")); exit ([int]$env:PRE_RC)')
 install.write_text('Add-Content $env:CALLS ("INSTALL " + ($args -join " ")); exit ([int]$env:INSTALL_RC)')
 env=dict(os.environ,CALLS=str(calls),HEXTHINGS_YES='1',HEXTHINGS_NO_AI='1',HEXTHINGS_AI_MODEL_SIZE='skip')
 for pre_rc,install_rc in [(0,0),(3,0),(1,0),(0,7)]:
  calls.write_text('')
  r=subprocess.run([pwsh,'-NoProfile','-File',str(root/'scripts/get.ps1'),'-LocalSource',str(src)],env=dict(env,PRE_RC=str(pre_rc),INSTALL_RC=str(install_rc)),capture_output=True,text=True,timeout=20)
  assert r.returncode==(pre_rc or install_rc),(r.returncode,r.stdout,r.stderr)
  text=calls.read_text();assert 'PREREQ -Yes' in text,text
  assert ('INSTALL' in text)==(pre_rc==0),text
  if pre_rc==0:assert '-NoAi -AiModelSize skip' in text,text
 # Without explicit local source, and with incomplete local source: no child scripts.
 for args in [[],['-LocalSource',str(t)],['-LocalSource',str(t/'absent')]]:
  calls.write_text('');r=subprocess.run([pwsh,'-NoProfile','-File',str(root/'scripts/get.ps1'),*args],env=env,capture_output=True,text=True,timeout=20)
  assert r.returncode!=0 and not calls.read_text(),(r.stdout,r.stderr)
 # Static AST extraction executes the actual update function; anonymous mode
 # must refuse before Docker/backup/download/mutation even when NoBackup is set.
 runner=t/'guard.ps1'
 runner.write_text('''$tok=$null;$err=$null
 $ast=[System.Management.Automation.Language.Parser]::ParseFile($env:SOURCE,[ref]$tok,[ref]$err)
 if($err.Count){throw 'parse failure'}
 $ast.FindAll({param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq 'Do-Update'},$true)|ForEach-Object {Invoke-Expression $_.Extent.Text}
 function Bad($x){Write-Output $x}
 function NeedDocker{Add-Content $env:CALLS 'DOCKER';throw 'unexpected Docker'}
 $NoBackup=$true
 Do-Update ''
 ''')
 calls.write_text('');r=subprocess.run([pwsh,'-NoProfile','-File',str(runner)],env=dict(env,SOURCE=str(root/'scripts/hexthings.ps1')),capture_output=True,text=True,timeout=20)
 assert r.returncode==1 and not calls.read_text() and 'hexthings patch' in r.stdout,(r.stdout,r.stderr)
 print('PASS private local-source PowerShell bootstrap/exit forwarding/anonymous update guard: Linux PowerShell7, no real Windows/Docker/download')
