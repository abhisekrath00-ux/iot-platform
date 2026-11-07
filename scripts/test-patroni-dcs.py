#!/usr/bin/env python3
"""Internal helper for disposable TLS fixture only. No daemon or PostgreSQL is started."""
import copy
from datetime import datetime, timezone
from concurrent.futures import ThreadPoolExecutor
import importlib.metadata
import json
import sys
import time
from threading import Barrier

from patroni.dcs import get_dcs

assert importlib.metadata.version('patroni') == '4.1.5'
with open(sys.argv[1]) as stream:
    config = json.load(stream)
assert config['scope'] == 'disposable-patroni-fixture'
assert config['namespace'] == '/fixture/' and config['patronictl'] is True
assert config['etcd3']['protocol'] == 'https'
assert all(host.startswith('127.0.0.1:') and host.split(':')[1].isdigit()
           for host in config['etcd3']['hosts'].split(','))
def compete(first_write, second_write):
    # Release independent clients from one gate, not sequential fake winners.
    gate = Barrier(3, timeout=5)

    def write(action):
        gate.wait()
        return action()

    with ThreadPoolExecutor(max_workers=2) as pool:
        jobs = [pool.submit(write, action) for action in (first_write, second_write)]
        gate.wait()
        return [job.result(timeout=10) for job in jobs]


# Independent DCS clients share the same isolated fixture namespace.
a = get_dcs(copy.deepcopy(config))
b = get_dcs(copy.deepcopy(config))
try:
    for round_number in range(3):
        ids = [f'fixture-bootstrap-a-{round_number}', f'fixture-bootstrap-b-{round_number}']
        outcomes = compete(lambda: a.initialize(sysid=ids[0]), lambda: b.initialize(sysid=ids[1]))
        assert all(isinstance(item, dict) for item in outcomes), f'Unexpected initialization result: {outcomes}'
        assert [bool(item) for item in outcomes] in ([True, False], [False, True]), f'Bootstrap race outcomes: {outcomes}'
        successful = next(item for item in outcomes if item)
        assert successful.get('succeeded') is True and successful.get('header', {}).get('revision')
        winner_id = ids[0] if outcomes[0] else ids[1]
        assert a.get_cluster().initialize == winner_id and b.get_cluster().initialize == winner_id
        assert not b.initialize(sysid='fixture-bootstrap-late'), 'late bootstrap overwrote existing system ID'
        assert a.get_cluster().initialize == winner_id and b.get_cluster().initialize == winner_id
        assert a.cancel_initialization()
        assert a.get_cluster().initialize is None and b.get_cluster().initialize is None
    print('PASS: three real Patroni competing create-only bootstrap markers select one system ID and reject overwrite', flush=True)
    assert a.initialize(sysid='fixture-system-id')
    assert a.set_config_value(json.dumps({'ttl': 30, 'synchronous_mode': True}))
    cluster = b.get_cluster()
    assert cluster.initialize == 'fixture-system-id'
    assert cluster.config.data['synchronous_mode'] is True
    assert cluster.config.data['ttl'] == 30
    assert b.set_config_value(json.dumps({'ttl': 30, 'synchronous_mode': True, 'synchronous_node_count': 1}))
    assert a.get_cluster().config.data['synchronous_node_count'] == 1
    # Read two actual etcd revisions, then reject a stale writer rather than
    # allowing an old observer to overwrite the newer configuration.
    first = a.get_cluster().config
    assert first and first.version
    second_value = {'ttl': 30, 'synchronous_mode': True, 'fixture_revision': 'second'}
    assert b.set_config_value(json.dumps(second_value), version=first.version)
    second = a.get_cluster().config
    assert second.version != first.version and second.data == second_value
    assert a.set_config_value(json.dumps({'fixture_revision': 'stale'}), version=first.version) is False
    unchanged = b.get_cluster().config
    assert unchanged.version == second.version and unchanged.data == second_value
    assert a.set_config_value(json.dumps({'ttl': 30, 'fixture_revision': 'third'}), version=second.version)
    assert b.get_cluster().config.data['fixture_revision'] == 'third'

    # /sync is DCS metadata, not evidence of actual PostgreSQL replication.
    initial_sync = a.write_sync_state('fixture-a', ['fixture-b'], 0, version='0')
    assert initial_sync and initial_sync.version
    assert b.write_sync_state('fixture-x', ['fixture-y'], 0, version='0') is None
    seen = b.get_cluster().sync
    assert seen == initial_sync
    updated_sync = b.write_sync_state('fixture-a', ['fixture-c'], 0, version=seen.version)
    assert updated_sync and updated_sync.version != seen.version
    assert a.write_sync_state('fixture-old', ['fixture-b'], 0, version=seen.version) is None
    assert a.delete_sync_state(version=seen.version) == {}
    assert a.get_cluster().sync == updated_sync
    assert a.delete_sync_state(version=updated_sync.version)
    assert b.get_cluster().sync.leader is None
    # Competing writers use the SAME observed revision but distinct payloads.
    # Repeat both keys to avoid accepting a single accidental happy path.
    for round_number in range(3):
        current = a.get_cluster().config
        values = [{'ttl': 30, 'fixture_race': f'{round_number}-{name}'} for name in ('a', 'b')]
        outcomes = compete(lambda: a.set_config_value(json.dumps(values[0]), version=current.version),
                           lambda: b.set_config_value(json.dumps(values[1]), version=current.version))
        assert outcomes in ([True, False], [False, True]), f'Config CAS race outcomes: {outcomes}'
        winner = outcomes.index(True)
        for observer in (a, b):
            committed = observer.get_cluster().config
            assert committed.version != current.version and committed.data == values[winner]

        initial = a.write_sync_state('fixture-leader', ['fixture-initial'], 0, version='0')
        assert initial and initial.version
        outcomes = compete(lambda: a.write_sync_state('fixture-leader', ['fixture-a'], 0, version=initial.version),
                           lambda: b.write_sync_state('fixture-leader', ['fixture-b'], 0, version=initial.version))
        successes = [item for item in outcomes if item is not None]
        assert len(successes) == 1, f'Sync CAS race outcomes: {outcomes}'
        winner = successes[0]
        assert winner.version != initial.version
        assert winner.sync_standby in ('fixture-a', 'fixture-b')
        assert a.get_cluster().sync == winner and b.get_cluster().sync == winner
        assert a.delete_sync_state(version=winner.version)
        assert b.get_cluster().sync.leader is None
    # Synthetic metadata only: no HA loop consumes these requests.
    scheduled = datetime(2030, 1, 2, 3, 4, 5, tzinfo=timezone.utc)
    assert a.manual_failover('fixture-leader', 'fixture-initial', scheduled)
    initial_request = b.get_cluster().failover
    assert initial_request and initial_request.version
    assert (initial_request.leader, initial_request.candidate, initial_request.scheduled_at) == (
        'fixture-leader', 'fixture-initial', scheduled)
    for round_number in range(3):
        current = a.get_cluster().failover
        candidates = [f'fixture-request-{round_number}-{name}' for name in ('a', 'b')]
        outcomes = compete(lambda: a.manual_failover('fixture-leader', candidates[0], scheduled, current.version),
                           lambda: b.manual_failover('fixture-leader', candidates[1], scheduled, current.version))
        assert outcomes in ([True, False], [False, True]), f'Failover metadata CAS outcomes: {outcomes}'
        winning = b.get_cluster().failover
        assert winning.version != current.version
        assert (winning.leader, winning.candidate, winning.scheduled_at) == (
            'fixture-leader', candidates[outcomes.index(True)], scheduled)
        assert a.get_cluster().failover == winning
        assert a.manual_failover(None, None, version=current.version) is False
        assert b.manual_failover('fixture-stale', 'fixture-stale', scheduled, current.version) is False
        assert a.get_cluster().failover == winning and b.get_cluster().failover == winning
    assert a.manual_failover(None, None, version=winning.version)
    cleared = b.get_cluster().failover
    assert cleared.version != winning.version
    assert cleared.leader is None and cleared.candidate is None and cleared.scheduled_at is None
    assert a.get_cluster().failover == cleared
    print('PASS: real Patroni scheduled failover metadata serializes, races one winner, rejects stale update/clear, and conditionally clears', flush=True)
    print('PASS: three real concurrent config and sync-state CAS rounds each commit exactly one writer payload', flush=True)
    print('PASS: real Patroni config and sync-state revision guards reject stale mutation/deletion and accept current revision', flush=True)
    print('PASS: Patroni4.1.5 real etcd3 DCS discovery and cross-client config read/write over rendered TLS keys', flush=True)
finally:
    assert a.delete_cluster()
empty = b.get_cluster()
assert empty.initialize is None and empty.config is None
assert empty.is_unlocked()

if len(sys.argv) > 2:
    assert sys.argv[2:] == ['--config-only']
    raise SystemExit(0)

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
    first_member = b.get_cluster().get_member('fixture-a', fallback_to_leader=False)
    other_member = a.get_cluster().get_member('fixture-b', fallback_to_leader=False)
    assert first_member and first_member.session and first_member.version
    assert other_member and other_member.session and other_member.version
    original_data = {'state': 'running', 'role': 'replica'}
    # Prime the local observer too: touch_member uses its cached cluster to
    # avoid rewriting unchanged metadata, while still refreshing the lease.
    eventually(a, lambda c: c.get_member('fixture-a', fallback_to_leader=False) == first_member)
    assert a.touch_member(original_data)
    for observer in (a, b):
        seen = observer.get_cluster().get_member('fixture-a', fallback_to_leader=False)
        assert seen == first_member, 'unchanged member heartbeat rewrote metadata or lease'
    changed_data = dict(original_data, tags={'nofailover': True}, fixture_revision='changed')
    assert a.touch_member(changed_data)
    for observer in (a, b):
        changed_cluster = eventually(observer, lambda c: c.get_member('fixture-a', fallback_to_leader=False)
                                     and c.get_member('fixture-a', fallback_to_leader=False).data == changed_data)
        changed = changed_cluster.get_member('fixture-a', fallback_to_leader=False)
        assert changed.session == first_member.session and changed.version != first_member.version
        assert changed_cluster.get_member('fixture-b', fallback_to_leader=False) == other_member
    assert a.touch_member(original_data)
    for observer in (a, b):
        restored = eventually(observer, lambda c: c.get_member('fixture-a', fallback_to_leader=False)
                              and c.get_member('fixture-a', fallback_to_leader=False).data == original_data)
        member = restored.get_member('fixture-a', fallback_to_leader=False)
        assert member.session == first_member.session and member.version != changed.version
        assert restored.get_member('fixture-b', fallback_to_leader=False) == other_member
    print('PASS: real Patroni unchanged member heartbeat retains revision; updates/restoration keep lease and other member', flush=True)
    for round_number in range(3):
        outcomes = compete(a.attempt_to_acquire_leader, b.attempt_to_acquire_leader)
        assert all(isinstance(item, dict) for item in outcomes), f'Unexpected leader result type: {outcomes}'
        assert [bool(item) for item in outcomes] in ([True, False], [False, True]), f'Leader race outcomes: {outcomes}'
        successful = next(item for item in outcomes if item)
        assert successful.get('succeeded') is True and successful.get('header', {}).get('revision')
        winner, loser = (a, b) if outcomes[0] else (b, a)
        name = 'fixture-a' if outcomes[0] else 'fixture-b'
        leader = eventually(loser, lambda c: c.leader_name == name).leader
        assert leader.session
        assert eventually(winner, lambda c: c.leader_name == name).leader.session == leader.session
        assert not loser.attempt_to_acquire_leader(), 'loser replaced occupied leader key'
        assert not loser.delete_leader(leader), 'loser deleted race winner leader key'
        assert winner.update_leader(winner.get_cluster(), None)
        assert eventually(loser, lambda c: c.leader_name == name).leader.session == leader.session
        assert winner.delete_leader(leader)
        eventually(a, lambda c: c.is_unlocked())
        eventually(b, lambda c: c.is_unlocked())
    print('PASS: three competing real Patroni leader acquisitions each have one lease owner with loser deletion rejected', flush=True)
    assert a.attempt_to_acquire_leader()
    first = eventually(b, lambda c: c.leader_name == 'fixture-a').leader
    assert first.session
    assert not b.attempt_to_acquire_leader(), 'second DCS client acquired an occupied leader key'
    assert a.update_leader(a.get_cluster(), None)
    assert not b.delete_leader(first), 'non-owner deleted the leader key'
    assert a.delete_leader(first)
    eventually(b, lambda c: c.is_unlocked())
    assert b.attempt_to_acquire_leader()
    prior_owner_cluster = eventually(a, lambda c: c.leader_name == 'fixture-b')
    second = prior_owner_cluster.leader
    assert second.session and second.session != first.session
    assert not a.delete_leader(first), 'old owner deleted a new owner leader key'
    assert b.update_leader(b.get_cluster(), None)
    # Stop refreshing b. The actual etcd lease must expire, removing both its
    # member entry and leader key; a sees this through its watch-backed cache.
    eventually(a, lambda c: c.is_unlocked() and not c.has_member('fixture-b'), seconds=45)
    assert a.attempt_to_acquire_leader()
    replacement = eventually(b, lambda c: c.leader_name == 'fixture-a').leader
    assert replacement.session and replacement.session != second.session
    # b still holds its captured prior-owner Leader, but its actual lease expired.
    # Force the public renewal path to discover expiry and attempt recovery while
    # a owns the key. No database daemon or fencing action is involved.
    assert b.update_leader(prior_owner_cluster, None) is False, 'expired owner renewed over replacement leader'
    assert not b.delete_leader(second), 'expired owner deleted replacement leader'
    for observer in (a, b):
        retained = eventually(observer, lambda c: c.leader_name == 'fixture-a').leader
        assert retained.session == replacement.session and retained.version == replacement.version
    assert a.update_leader(a.get_cluster(), None)
    retained = eventually(b, lambda c: c.leader_name == 'fixture-a').leader
    assert retained.session == replacement.session and retained.version == replacement.version
    print('PASS: expired real Patroni lease owner cannot renew/delete replacement leader; owner session/revision retained', flush=True)
    print('PASS: real Patroni DCS watch visibility, exclusive leader key, keepalive, ownership guards and lease expiry/reacquisition', flush=True)
finally:
    assert a.delete_cluster()
eventually(b, lambda c: c.is_unlocked() and not c.members)
