#!/usr/bin/env python3
"""Run the unmodified public Ubuntu bootstrap in a real desktop terminal."""
import pathlib,sys,time,pexpect
out=pathlib.Path('/home/hextest/acceptance');out.mkdir(exist_ok=True)
c=pexpect.spawn('sudo',['bash','/home/hextest/ubuntu-server.sh'],encoding='utf-8',timeout=2400,dimensions=(45,145),env=dict(__import__('os').environ,TERM='xterm-256color',LANG='en_US.UTF-8'))
c.logfile=sys.stdout
try:
    c.expect_exact('Proceed with the plan above? [y/N] ');c.sendline('y')
    c.expect(r'Name for your first workspace[^\r\n]*: ');c.sendline('desktop-ci')
    c.expect(r'Administrator email[^\r\n]*: ');c.sendline('desktop-ci@example.invalid')
    c.expect_exact('Opening the HexThings terminal menu (q to quit).')
    c.expect_exact('Health check')
    (out/'menu-ready').touch()
    # Host takes a QEMU framebuffer screenshot of the real desktop and terminal.
    deadline=time.monotonic()+120
    while not (out/'screenshot-taken').exists():
        if time.monotonic()>deadline:raise RuntimeError('host did not acknowledge visual capture')
        time.sleep(1)
    c.send('q');c.expect(pexpect.EOF);c.close()
    assert c.exitstatus==0, (c.exitstatus,c.signalstatus)
    (out/'success').touch()
    print('\nReal Ubuntu Desktop24.04 guided bootstrap and automatic menu handoff passed.')
except Exception:
    (out/'failed').touch()
    raise
