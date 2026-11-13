#!/usr/bin/env python3
"""Real shell process lifecycle with a fake broker, never a Docker/owner proof."""
import os
import pathlib
import signal
import subprocess
import tempfile
import time
import unittest

SCRIPT = pathlib.Path(__file__).with_name('broker-acl-supervisor.sh')


class Tests(unittest.TestCase):
    def test_reload_signal_and_missing_acl_fail_closed(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            acl, config = root / 'acl', root / 'broker.conf'
            acl.write_text('synthetic ACL one')
            config.write_text('synthetic config')
            helper = root / 'fake.py'
            helper.write_text('''import os,pathlib,signal,time
root=pathlib.Path(os.environ['FIXTURE'])
(root/'pid').write_text(str(os.getpid()))
signal.signal(signal.SIGHUP,lambda *_:(root/'hup').write_text('signal only'))
signal.signal(signal.SIGTERM,lambda *_:exit(0))
while True:time.sleep(.1)
''')
            broker = root / 'mosquitto'
            broker.write_text('#!/bin/sh\nexec python3 "$FIXTURE/fake.py"\n')
            broker.chmod(0o700)
            proc = subprocess.Popen(['sh', str(SCRIPT), str(config), str(acl)],
                                    env=dict(os.environ, PATH=str(root) + ':' + os.environ['PATH'], FIXTURE=str(root)),
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            def wait_for(path):
                until = time.monotonic() + 5
                while not path.exists():
                    if proc.poll() is not None or time.monotonic() >= until:
                        self.fail('Supervisor stopped or timed out before expected process signal')
                    time.sleep(.02)
            try:
                wait_for(root / 'pid')
                child = int((root / 'pid').read_text())
                staged = root / 'acl.next'
                staged.write_text('synthetic ACL two')
                staged.replace(acl)
                wait_for(root / 'hup')
                acl.unlink()
                out, err = proc.communicate(timeout=5)
                self.assertNotEqual(proc.returncode, 0)
                self.assertIn('reload signal sent', out)
                self.assertIn('ACL became unavailable', err)
                with self.assertRaises(ProcessLookupError):
                    os.kill(child, 0)
            finally:
                if proc.poll() is None:
                    proc.send_signal(signal.SIGTERM)
                    proc.communicate(timeout=5)


if __name__ == '__main__':
    unittest.main()
