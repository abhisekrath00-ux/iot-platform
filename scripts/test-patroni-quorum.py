#!/usr/bin/env python3
"""Disposable config-client quorum failure/recovery. No Patroni HA loop or PostgreSQL."""
import importlib.metadata
import json
from pathlib import Path
import sys
import time

from patroni.dcs import get_dcs

assert importlib.metadata.version('patroni') == '4.1.5'
with open(sys.argv[1]) as stream:
    config = json.load(stream)
assert config['scope'] == 'disposable-patroni-fixture'
assert config['namespace'] == '/fixture/' and config['patronictl'] is True
assert config['etcd3']['protocol'] == 'https'
assert all(host.startswith('127.0.0.1:') and host.split(':')[1].isdigit()
           for host in config['etcd3']['hosts'].split(','))
config['scope'] = 'disposable-patroni-fixture-quorum'
control = Path(sys.argv[2])
assert control.is_dir() and control.parent == Path(sys.argv[1]).parent


def wait_for(name):
    deadline = time.monotonic() + 60
    while not (control / name).exists():
        if time.monotonic() >= deadline:
            raise AssertionError('Quorum fixture handshake deadline: ' + name)
        time.sleep(0.1)


client = get_dcs(config)
assert client.set_config_value(json.dumps({'fixture_phase': 'before'}))
assert client.get_cluster().config.data['fixture_phase'] == 'before'
(control / 'ready').touch()
wait_for('lost')
# This API catches expected etcd exceptions and returns False; don't accept
# an arbitrary Python exception or forced process timeout as a passed test.
assert client.set_config_value(json.dumps({'fixture_phase': 'uncertain'})) is False
(control / 'rejected').touch()
wait_for('restored')
try:
    deadline = time.monotonic() + 20
    while True:
        if client.set_config_value(json.dumps({'fixture_phase': 'after'})):
            assert client.get_cluster().config.data['fixture_phase'] == 'after'
            break
        if time.monotonic() >= deadline:
            raise AssertionError('Same Patroni config client failed write recovery')
        time.sleep(0.2)
    print('PASS: same real Patroni config DCS client returns False without quorum and writes/reads after restoration', flush=True)
finally:
    assert client.delete_cluster()
assert client.get_cluster().config is None
