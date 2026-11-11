#!/usr/bin/env python3
"""Actual Bash installer PTY input with stub Docker/network, not a Docker installation."""
import os,pty,select,subprocess,tempfile,time,secrets
from pathlib import Path
r=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as td:
 t=Path(td);w=t/'w';(w/'scripts').mkdir(parents=True);b=t/'bin';b.mkdir()
 for f in ['install.sh','model-choose.sh','model-catalog.txt']:(w/'scripts'/f).write_bytes((r/'scripts'/f).read_bytes())
 for f in ['.env.example','docker-compose.yml']:(w/f).write_bytes((r/f).read_bytes())
 (b/'docker').write_text('''#!/usr/bin/env python3
import sys,os,pathlib
args=sys.argv[1:];p=pathlib.Path(os.environ['CHECK_DIR'])
with (p/'argv').open('a') as f:f.write(repr(args)+'\\n')
if args[:2]==['compose','ps'] and os.environ.get('EXISTING'):print('abc')
if 'list' in args:
 if os.environ.get('LIST_FAIL'):sys.exit(9)
 if os.environ.get('EXISTING'):print('my-plant my-plant users=1 devices=0')
if 'create' in args:(p/'secret-input').write_bytes(sys.stdin.buffer.read())
''')
 (b/'curl').write_text('#!/bin/sh\nexit 0\n')
 for f in b.iterdir():f.chmod(0o755)
 def run(mode):
  for x in ['argv','secret-input']:(t/x).unlink(missing_ok=True)
  pw=secrets.token_urlsafe(24)
  env=os.environ.copy();env.update(PATH=str(b)+':'+env['PATH'],CHECK_DIR=str(t),HEXTHINGS_FAKE_MEM_MB='8192',HEXTHINGS_FAKE_DISK_MB='100000',NO_COLOR='1')
  if mode=='existing':env['EXISTING']='1'
  if mode=='listfail':env['EXISTING']='1';env['LIST_FAIL']='1'
  pid,fd=pty.fork()
  if pid==0:os.execvpe('bash',['bash',str(w/'scripts/install.sh'),'--web-port','18431','--workspace','my-plant','--admin-email','owner@example.invalid','--no-ai'],env)
  data=b'';state=0;deadline=time.monotonic()+15
  while time.monotonic()<deadline:
   ready,_,_=select.select([fd],[],[],0.05)
   if ready:
    try:chunk=os.read(fd,65536)
    except OSError:break
    if not chunk:break
    data+=chunk
   if state==0 and b'Administrator password' in data:os.write(fd,((pw if mode!='weak' else 'short')+'\n').encode());state=1
   if state==1 and b'Confirm administrator password' in data:os.write(fd,((pw if mode not in ['mismatch','weak'] else 'different' if mode=='mismatch' else 'short')+'\n').encode());state=2
  else:os.kill(pid,9);raise AssertionError('hung')
  _,status=os.waitpid(pid,0);os.close(fd);rc=os.waitstatus_to_exitcode(status)
  assert pw.encode() not in data and pw not in (t/'argv').read_text()
  assert pw not in (w/'install.log').read_text() and pw not in (w/'.env').read_text()
  if mode=='success':assert rc==0 and (t/'secret-input').read_bytes()==(pw+'\n').encode()
  elif mode=='existing':assert rc==0 and state==0 and not(t/'secret-input').exists()
  else:assert rc!=0 and not(t/'secret-input').exists()
 for mode in ['success','mismatch','weak','existing','listfail']:run(mode)
 # Unattended setup cannot silently seed a default.
 env=os.environ.copy();env.update(PATH=str(b)+':'+env['PATH'],CHECK_DIR=str(t))
 x=subprocess.run(['bash',str(w/'scripts/install.sh'),'--yes','--web-port','18431','--no-ai'],input=b'',capture_output=True,env=env)
 assert x.returncode!=0 and b'no default password' in x.stdout
print('PASS actual hidden/confirmed installer input, weak/mismatch/list-failure refusal, unchanged existing admin, unattended no-default refusal; Docker stubbed.')
