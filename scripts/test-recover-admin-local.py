#!/usr/bin/env python3
"""PTY wrapper contracts with fake Docker/network, not a real container lifecycle."""
import os,pty,select,subprocess,tempfile,time,secrets,hashlib
from pathlib import Path
root=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as td:
 t=Path(td);bin=t/'bin';bin.mkdir();source=t/'source';source.mkdir();(source/'docker-compose.yml').write_text('services: {}')
 script=t/'recover.sh';script.write_text((root/'scripts/recover-admin-local.sh').read_text().replace('cd /opt/hexthings',f'cd {source}'))
 (bin/'id').write_text('#!/bin/sh\necho 0\n')
 (bin/'curl').write_text('#!/bin/sh\nwhile [ "$1" != -o ];do shift;done;touch "$2"\n')
 (bin/'tar').write_text('#!/bin/sh\nexit 0\n')
 (bin/'docker').write_text('''#!/usr/bin/env python3
import sys,os,pathlib,hashlib
args=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f:f.write(repr(args)+'\\n')
if args[0]=='run':
 if os.environ.get('BUILD_FAIL'):sys.exit(3)
 out=next(x.split(',')[1][4:] for x in args if x.startswith('type=bind,src=') and 'dst=/out' in x)
 pathlib.Path(out,'recover').write_text('fixture')
if 'recover-admin' in args:
 data=sys.stdin.buffer.read();assert hashlib.sha256(data).hexdigest()==os.environ['EXPECTED'];pathlib.Path(os.environ['RAN']).touch()
''')
 for f in bin.iterdir():f.chmod(0o755)
 def run(mode):
  calls=t/'calls';calls.write_text('');ran=t/'ran';ran.unlink(missing_ok=True)
  password=secrets.token_urlsafe(20);env=os.environ.copy();env.update(PATH=str(bin)+':'+env['PATH'],CALLS=str(calls),RAN=str(ran),EXPECTED=hashlib.sha256((password+'\n').encode()).hexdigest())
  if mode=='buildfail':env['BUILD_FAIL']='1'
  pid,fd=pty.fork()
  if pid==0:os.execvpe('bash',['bash',str(script),'hexthings','fixture@example.invalid'],env)
  data=b'';state=0;end=time.monotonic()+15
  while time.monotonic()<end:
   ready,_,_=select.select([fd],[],[],0.1)
   if ready:
    try:b=os.read(fd,65536)
    except OSError:break
    if not b:break
    data+=b
   if state==0 and b'Type RESET' in data:os.write(fd,b'RESET\n');state=1
   if state==1 and b'New password' in data:os.write(fd,(password+'\n').encode());state=2
   if state==2 and b'Confirm new password' in data:os.write(fd,((password if mode!='mismatch' else password+'x')+'\n').encode());state=3
  else:os.kill(pid,9);raise AssertionError('PTY timeout')
  _,status=os.waitpid(pid,0);os.close(fd);rc=os.waitstatus_to_exitcode(status)
  assert password.encode() not in data,'password echoed'
  assert password not in calls.read_text(),'password on argv'
  assert 'rm' in calls.read_text(),'cleanup not attempted'
  if mode=='success':assert rc==0 and ran.exists()
  else:assert rc!=0 and not ran.exists()
  if mode=='buildfail':assert state==0,'password asked before build succeeded'
 for mode in ['success','mismatch','buildfail']:run(mode)
print('PASS hidden-input PTY, no secret in argv/output, mismatch/build refusal and cleanup. Docker/network stubbed.')
