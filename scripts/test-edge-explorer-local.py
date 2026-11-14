#!/usr/bin/env python3
"""Local-only explorer launch acceptance. Empty sources, disposable state.

Builds the actual edge binary, runs it with an unreachable loopback broker,
uses its real Open/Close CLI and checks the returned local HTTP page. It never
installs a service, opens a serial port, contacts an owner server or enrolls.
"""
import json
import os
import socket
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix='hexthings-explorer-cli-') as tmp, socket.socket() as broker_guard:
        # Reserve a loopback port without listening, so it cannot be another
        # local program's broker and every connection is refused.
        broker_guard.bind(('127.0.0.1', 0))
        broker_port = broker_guard.getsockname()[1]
        directory = Path(tmp)
        binary = directory / ('edge-agent.exe' if os.name == 'nt' else 'edge-agent')
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/edge-agent'],
                       cwd=ROOT / 'edge', check=True)
        data = directory / 'data'
        data.mkdir()
        config = directory / 'edge.yaml'
        config.write_text('gateway_id: synthetic-explorer\ntenant_id: synthetic-explorer\n'
                          'serial: synthetic-explorer\nmqtt:\n  host: 127.0.0.1\n'
                          f'  port: {broker_port}\n  tls: false\nautodetect:\n  enabled: false\n'
                          'ui:\n  listen: 127.0.0.1:0\ndevices: []\nqueue_path: '
                          + json.dumps(str(data / 'queue.db')) + '\n')
        before = config.read_bytes()
        marker = data / 'retain-fixture'
        marker.write_text('synthetic, no real identity')
        env = os.environ.copy()
        env['HEXMON_DATA_DIR'] = str(data)
        with (directory / 'agent.log').open('w+') as log:
            process = subprocess.Popen([str(binary), '-config', str(config),
                                        '-identity-dir', str(data)], env=env,
                                       stdout=log, stderr=log)
            try:
                # The CLI itself has a bounded eight-second receipt wait. There
                # is one attempt per explicit action, not an automatic retry.
                time.sleep(0.8)

                def control(action):
                    result = subprocess.run([str(binary), '-' + action + '-explorer',
                                             '-identity-dir', str(data)], env=env,
                                            text=True, capture_output=True, timeout=12)
                    if result.returncode:
                        raise RuntimeError(action + ': ' + result.stderr)
                    receipt = json.loads(result.stdout)
                    assert receipt['state'] == 'complete'
                    return receipt

                opened = control('open')
                url = opened['url']
                with urllib.request.urlopen(url, timeout=2) as page:
                    assert b'Modbus explorer' in page.read()
                control('close')
                try:
                    with urllib.request.urlopen(url, timeout=1):
                        raise AssertionError('listener survived Close')
                except urllib.error.URLError:
                    pass
                assert process.poll() is None
                assert config.read_bytes() == before
                assert marker.read_text() == 'synthetic, no real identity'
                opened = control('open')
                with urllib.request.urlopen(opened['url'], timeout=2) as page:
                    assert page.status == 200
                control('close')
                print('PASS actual edge binary + real CLI Open/Close/reopen + matching '
                      'receipts + actual local HTTP page. Empty devices, unreachable '
                      'synthetic loopback broker; process/config/fixture marker retained. '
                      'No installed service, owner account, serial port or hardware.')
            finally:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                if process.returncode not in (0, -15):
                    log.seek(0)
                    print(log.read()[-3000:])


if __name__ == '__main__':
    main()
