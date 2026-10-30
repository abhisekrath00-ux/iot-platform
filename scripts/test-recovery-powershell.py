#!/usr/bin/env python3
"""PowerShell recovery command-contract tests on Linux with fake Docker.
Not native Windows, Docker Desktop, filesystem ACL or actual DB recovery proof.
Run: PWSH=/path/to/pwsh python3 scripts/test-recovery-powershell.py
"""
import os, tempfile, pathlib, subprocess, json, hashlib, shutil
root=pathlib.Path(__file__).resolve().parent.parent
pwsh=os.environ.get('PWSH','pwsh')
with tempfile.TemporaryDirectory() as tmp:
    t=pathlib.Path(tmp); (t/'bin').mkdir()
    docker=t/'bin'/'docker'
    docker.write_text('''#!/usr/bin/env python3
import sys,os,json,pathlib
args=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f:f.write(json.dumps(args)+'\\n')
mode=os.environ.get('CASE','')
if mode=='dumpfail' and 'pg_dump' in args:sys.exit(8)
if mode=='gzipfail' and 'gzip' in args:sys.exit(8)
if mode=='copyfail' and 'cp' in args:sys.exit(8)
if mode=='decompressfail' and any('gunzip' in x for x in args):sys.exit(8)
if mode=='listfail' and 'pg_restore' in args and '-l' in args:sys.exit(8)
if mode=='stopfail' and 'stop' in args:sys.exit(8)
if mode=='restorefail' and '--clean' in args:sys.exit(8)
if mode=='startfail' and 'start' in args:sys.exit(8)
if 'cp' in args and args[-2].startswith('postgres:'):pathlib.Path(args[-1]).write_bytes(b'synthetic dump')
'''); docker.chmod(0o755)
    runner=t/'runner.ps1'
    runner.write_text('''$ErrorActionPreference='Stop'
$tokens=$null;$errors=$null
$ast=[System.Management.Automation.Language.Parser]::ParseFile($env:SOURCE,[ref]$tokens,[ref]$errors)
if($errors.Count){throw 'parse failure'}
$ast.FindAll({param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -in @('BackupCore','Do-Restore')},$true)|ForEach-Object {Invoke-Expression $_.Extent.Text}
function NeedDocker {}
function Step($x) {}
function Banner($x) {}
function Ok($x) {Write-Output $x}
function Bad($x) {Write-Output $x}
function Warn($x) {Write-Output $x}
function WaitHealthy($n) {return $env:CASE -ne 'healthfail'}
$Dir=$env:BACKDIR;$Arg=$env:ARCHIVE;$PgUser='synthetic_user';$PgDb='synthetic_db';$Yes=$true
if($env:ACTION -eq 'backup'){BackupCore}else{Do-Restore}
''')
    env=dict(os.environ,PATH=str(t/'bin')+os.pathsep+os.environ['PATH'],SOURCE=str(root/'scripts/hexthings.ps1'),BACKDIR=str(t/'custom backups'),ARCHIVE=str(t/'archive.dump.gz'),CALLS=str(t/'calls'))
    archive=pathlib.Path(env['ARCHIVE']); side=pathlib.Path(str(archive)+'.sha256')
    def run(case,action='restore',want=1):
        pathlib.Path(env['CALLS']).write_text('')
        r=subprocess.run([pwsh,'-NoProfile','-File',str(runner)],env=dict(env,CASE=case,ACTION=action),capture_output=True,text=True,timeout=20)
        calls=[json.loads(x) for x in pathlib.Path(env['CALLS']).read_text().splitlines()]
        assert r.returncode==want,(case,r.returncode,r.stdout,r.stderr)
        return calls,r.stdout
    archive.write_bytes(b'synthetic archive')
    def checksum():side.write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
    calls,_=run('missing');assert not calls
    checksum();archive.write_bytes(b'corrupted');calls,_=run('corrupt');assert not calls
    checksum();side.write_text(side.read_text().replace(archive.name,'wrong.dump.gz'));calls,_=run('wrongname');assert not calls
    checksum()
    for case in ['copyfail','decompressfail','listfail']:
        calls,_=run(case);assert not any('stop' in c or '--clean' in c for c in calls),case
    calls,_=run('stopfail');assert not any('--clean' in c for c in calls)
    calls,text=run('restorefail');assert not any('start' in c for c in calls);assert 'remain stopped' in text
    run('startfail');run('healthfail')
    calls,_=run('good',want=0);assert any('--exit-on-error' in c for c in calls) and any('start' in c for c in calls)
    for case in ['dumpfail','gzipfail','copyfail']:
        calls,_=run(case,'backup');assert not list(pathlib.Path(env['BACKDIR']).glob('*.sha256'))
    calls,_=run('good','backup',0)
    out=list(pathlib.Path(env['BACKDIR']).glob('*.dump.gz'));assert len(out)==1
    f=out[0];assert pathlib.Path(str(f)+'.sha256').read_text()==hashlib.sha256(f.read_bytes()).hexdigest()+'  '+f.name+'\n'
    calls,_=run('good','backup',0);assert len(list(pathlib.Path(env['BACKDIR']).glob('*.dump.gz')))==2
    temps=[c[c.index('-f')+1] for c in calls if 'pg_dump' in c];assert len(temps)==1 and 'hexthings-backup-' in temps[0]
    print('PASS: PowerShell7 recovery command-contract cases; fake Docker on Linux, not Windows/actualDB')
