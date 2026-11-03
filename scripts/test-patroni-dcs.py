#!/usr/bin/env python3
"""Internal helper for disposable TLS fixture only. No daemon or PostgreSQL is started."""
import copy
import importlib.metadata
import json
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
# Independent DCS clients share the same isolated fixture namespace.
a = get_dcs(copy.deepcopy(config))
b = get_dcs(copy.deepcopy(config))
try:
    assert a.initialize(sysid='fixture-system-id')
    assert a.set_config_value(json.dumps({'ttl': 30, 'synchronous_mode': True}))
    cluster = b.get_cluster()
    assert cluster.initialize == 'fixture-system-id'
    assert cluster.config.data['synchronous_mode'] is True
    assert cluster.config.data['ttl'] == 30
    assert b.set_config_value(json.dumps({'ttl': 30, 'synchronous_mode': True, 'synchronous_node_count': 1}))
    assert a.get_cluster().config.data['synchronous_node_count'] == 1
    print('PASS: Patroni4.1.5 real etcd3 DCS discovery and cross-client config read/write over rendered TLS keys', flush=True)
finally:
    assert a.delete_cluster()
empty = b.get_cluster()
assert empty.initialize is None and empty.config is None
assert empty.is_unlocked()

# Non-patronictl mode starts the real DCS watch cache and grants etcd leases.
# This is still only the DCS component, not a Patroni daemon or PostgreSQL test.

def eventually(client, predicate, seconds=10):
    deadline = time.monotonic() + seconds
    while True:
        cluster = client.get_cluster()
        if predicate(cluster):
            return cluster
        if time.monotonic() >= deadline:
            raise AssertionError('DCS fixture state did not converge before deadline')
        time.sleep(0.1)


runtime = copy.deepcopy(config)
runtime.update(scope='disposable-patroni-fixture-runtime', patronictl=False, name='fixture-a')
a = get_dcs(copy.deepcopy(runtime))
runtime['name'] = 'fixture-b'
b = get_dcs(copy.deepcopy(runtime))
assert a._client._kv_cache is not None and b._client._kv_cache is not None
try:
    assert a.touch_member({'state': 'running', 'role': 'replica'})
    assert b.touch_member({'state': 'running', 'role': 'replica'})
    eventually(a, lambda c: c.has_member('fixture-a') and c.has_member('fixture-b'))
    assert a.attempt_to_acquire_leader()
    first = eventually(b, lambda c: c.leader_name == 'fixture-a').leader
    assert first.session
    assert not b.attempt_to_acquire_leader(), 'second DCS client acquired an occupied leader key'
    assert a.update_leader(a.get_cluster(), None)
    assert not b.delete_leader(first), 'non-owner deleted the leader key'
    assert a.delete_leader(first)
    eventually(b, lambda c: c.is_unlocked())
    assert b.attempt_to_acquire_leader()
    second = eventually(a, lambda c: c.leader_name == 'fixture-b').leader
    assert second.session and second.session != first.session
    assert not a.delete_leader(first), 'old owner deleted a new owner leader key'
    assert b.update_leader(b.get_cluster(), None)
    # Stop refreshing b. The actual etcd lease must expire, removing both its
    # member entry and leader key; a sees this through its watch-backed cache.
    eventually(a, lambda c: c.is_unlocked() and not c.has_member('fixture-b'), seconds=45)
    assert a.attempt_to_acquire_leader()
    eventually(b, lambda c: c.leader_name == 'fixture-a')
    print('PASS: real Patroni DCS watch visibility, exclusive leader key, keepalive, ownership guards and lease expiry/reacquisition', flush=True)
finally:
    assert a.delete_cluster()
eventually(b, lambda c: c.is_unlocked() and not c.members)
