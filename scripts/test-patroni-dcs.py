#!/usr/bin/env python3
"""Internal helper for disposable TLS fixture only. No daemon or PostgreSQL is started."""
import copy
import importlib.metadata
import json
import sys

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
