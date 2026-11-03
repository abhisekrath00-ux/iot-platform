#!/usr/bin/env python3
"""Disposable config/watch/lease quorum checks. No Patroni HA loop or PostgreSQL."""
import copy
import importlib.metadata
import json
from pathlib import Path
import sys
import time

from patroni.dcs import get_dcs
from patroni.dcs.etcd3 import Etcd3Error

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
runtime = copy.deepcopy(config)
runtime.update(patronictl=False, name='fixture-quorum-leader')
watcher = get_dcs(runtime)
assert watcher._client._kv_cache is not None
assert watcher.touch_member({'state': 'running', 'role': 'replica'})
assert watcher.attempt_to_acquire_leader()
leader_cluster = watcher.get_cluster()
assert leader_cluster.leader_name == 'fixture-quorum-leader'
assert watcher.update_leader(leader_cluster, None)
(control / 'ready').touch()
wait_for('lost')
# This API catches expected etcd exceptions and returns False; don't accept
# an arbitrary Python exception or forced process timeout as a passed test.
assert client.set_config_value(json.dumps({'fixture_phase': 'uncertain'})) is False
deadline = time.monotonic() + 15
transient_successes = 0
while True:
    try:
        lease_result = watcher.update_leader(leader_cluster, None)
    except Etcd3Error:
        break
    if lease_result is False:
        break
    assert lease_result is True, 'unexpected leader lease result'
    transient_successes += 1
    if time.monotonic() >= deadline:
        raise AssertionError('Leader lease renewal still reports success beyond quorum-loss deadline')
    time.sleep(0.2)
print('DCS fixture transient lease-renewal successes after quorum loss:', transient_successes, flush=True)
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
    deadline = time.monotonic() + 20
    while True:
        try:
            visible = watcher.get_cluster()
            if visible.config and visible.config.data.get('fixture_phase') == 'after':
                break
        except Etcd3Error:
            pass
        if time.monotonic() >= deadline:
            raise AssertionError('Existing Patroni watch cache failed fresh post-recovery marker')
        time.sleep(0.2)
    assert watcher.update_leader(watcher.get_cluster(), None)
    print('PASS: real Patroni leader lease renewal eventually fails without quorum; existing watch cache sees fresh restored-quorum marker', flush=True)
    print('PASS: same real Patroni config DCS client returns False without quorum and writes/reads after restoration', flush=True)
finally:
    assert client.delete_cluster()
assert client.get_cluster().config is None
