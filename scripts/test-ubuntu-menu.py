#!/usr/bin/env python3
"""Real PTY handoff checks. Uses a stub menu, not Docker or a product install."""
import os,pty,select,subprocess,tempfile,time
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as temp:
    root=Path(temp); (root/'scripts').mkdir()
    menu=root/'scripts/hexthings'
    def run(yes,fail=False):
        menu.write_text('#!/bin/bash\necho MENU-OPENED\n'+('exit 7\n' if fail else 'read -r answer\n[[ $answer == q ]]\n'))
        command=['bash','-c','source "$1"; finish_setup "$2" "$3"','test',str(ROOT/'scripts/ubuntu-server.sh'),str(root),str(yes)]
        pid,fd=pty.fork()
        if pid==0: os.execvp(command[0],command)
        data=b''; sent=False; deadline=time.monotonic()+8
        while time.monotonic()<deadline:
            ready,_,_=select.select([fd],[],[],0.1)
            if ready:
                try: chunk=os.read(fd,65536)
                except OSError: break
                if not chunk:break
                data+=chunk
                if b'MENU-OPENED' in data and not sent and not fail:
                    os.write(fd,b'q\n');sent=True
        else:
            os.kill(pid,9); raise AssertionError('menu handoff hung')
        _,status=os.waitpid(pid,0);os.close(fd)
        return data.decode(),os.waitstatus_to_exitcode(status)
    out,rc=run(0)
    assert rc==0 and 'Opening the HexThings terminal menu' in out and 'MENU-OPENED' in out
    out,rc=run(1)
    assert rc==0 and 'MENU-OPENED' not in out and 'menu later' in out
    out,rc=run(0,True)
    assert rc==0 and 'MENU-OPENED' in out and 'installed, but the terminal menu exited with an error' in out
    print('Real PTY: guided menu launch/q, unattended skip and post-success menu error semantics passed')
