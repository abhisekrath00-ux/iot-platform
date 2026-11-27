#!/usr/bin/env python3
"""Apply-caller wiring contracts with synthetic seams; never owner Docker/authority.

Proves the menu apply caller's refusals, action detection, provider input
binding, baseline ordering and full Coordinator apply/interrupted-apply/recover
flow through injected synthetic provider/client/runner seams. No real Docker,
real session, real database or owner machine is touched.
"""
import copy
import hashlib
import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import types
import unittest

HERE = pathlib.Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


m = load('secure-update-apply')
fixture = load('test-secure-application')

PASSWORD = 'synthetic-fixture-only'


def cid(name):
    return hashlib.sha256(name.encode()).hexdigest()


def inspect_doc(name, network, address):
    return {'Id': cid(name), 'Image': 'sha256:' + hashlib.sha256(('image-' + name).encode()).hexdigest(),
            'State': {'StartedAt': '2026-11-27T00:00:00.000000000Z', 'Status': 'running'},
            'Config': {'Labels': {'com.docker.compose.project': 'fixture', 'com.docker.compose.service': name}},
            'HostConfig': {'Privileged': False, 'NetworkMode': 'fixture_default'},
            'NetworkSettings': {'Networks': {network: {'NetworkID': cid('net-' + network), 'IPAddress': address}}}}


def fake_runner(args, **kwargs):
    docs = {'web': inspect_doc('web', 'fixture_default', '172.18.0.4'),
            'postgres': inspect_doc('postgres', 'fixture_default', '172.18.0.2')}
    if args[:2] == ['docker', 'inspect'] and args[2] in docs.values() or args[:2] == ['docker', 'inspect']:
        name = {'web': 'web', 'postgres': 'postgres'}.get({'web': cid('web'), 'postgres': cid('postgres')}.get(args[2], ''), '')
        found = {'web': docs['web'], 'postgres': docs['postgres']}
        if args[2] in (cid('web'), cid('postgres')):
            key = 'web' if args[2] == cid('web') else 'postgres'
            return types.SimpleNamespace(returncode=0, stdout=json.dumps([found[key]]), stderr='')
    return types.SimpleNamespace(returncode=1, stdout='', stderr='unknown')


class Events:
    def __init__(self):
        self.items = []


class FakeClient:
    def __init__(self, origin, session, tenant, user, **kwargs):
        self.origin, self.session, self.tenant, self.user, self.kwargs = origin, session, tenant, user, kwargs

    def snapshot(self):
        self.events.items.append('snapshot')
        return {'version': 1, 'scope': 'limited_retained_lists',
                'records': {'/v1/devices': {'d1': {'id': 'd1'}}},
                'full_compatibility': False, 'runtime_accepted': False}

    def compare(self, baseline):
        return {'matched': True, 'full_compatibility': False, 'runtime_accepted': False}

    def identity(self):
        return {'tenant_id': self.tenant, 'user_id': self.user, 'role': 'admin', 'via_key': False}


class Effects(fixture.Effects):
    def __init__(self, root, accept, compatible, **kwargs):
        super().__init__(root, **kwargs)
        self.accept_cb, self.compatible_cb = accept, compatible

    def accept(self, value, target):
        self.accept_cb(value, target)
        super().accept(value, target)

    def verify_previous_schema_compatibility(self, value):
        self.compatible_cb(value)
        super().verify_previous_schema_compatibility(value)


class FakeProvider:
    def __init__(self, catalog, reviewer, endpoint, request, runner, events, root, fail=None):
        self.catalog, self.reviewer, self.endpoint, self.request = catalog, reviewer, endpoint, request
        self.events, self.root, self.fail = events, root, fail
        self.adapter = types.SimpleNamespace(marker='adapter')
        self.group = types.SimpleNamespace(marker='group')
        self.ticket = object()
        self.calls = []

    def review(self, target):
        self.calls.append(('review', target))
        self.reviewer(target)

    def enter(self, target):
        self.calls.append(('enter', target))
        self.events.items.append('enter')
        return self.ticket

    def check(self, ticket, target):
        if ticket is not self.ticket:
            raise ValueError('Fence ticket changed.')

    def release(self, ticket, target):
        self.calls.append(('release', target))

    def retain(self, ticket, target):
        self.calls.append(('retain', target))

    def factory(self, target, receipt, authorize, accept, compatible):
        effects = Effects(self.root, accept, compatible, fail=self.fail)
        original = effects.authorize

        def approved(*args):
            authorize(*args)
            original(*args)
        effects.authorize = approved
        return effects


class FakeAcceptance:
    def __init__(self):
        self.constructed = []
        self.called = []

    def RuntimeAcceptance(self, adapter, client, baseline, behavior):
        self.constructed.append(('runtime', adapter, client, baseline, behavior))

        def run(value, slot):
            self.called.append(('runtime', value[slot], slot))
        return run

    def PreviousCompatibility(self, adapter, group, client, baseline, behavior):
        self.constructed.append(('previous', adapter, group, client, baseline, behavior))

        def run(value):
            self.called.append(('previous', value['previous']))
        return run


class Tests(unittest.TestCase):
    def plan(self, root, password=PASSWORD, private=True):
        bundle = root / 'plan'
        bundle.mkdir(mode=0o700 if private else 0o755)
        old = fixture.Tests().config()
        old['services']['postgres']['environment'] = {
            'POSTGRES_USER': 'fixture', 'POSTGRES_DB': 'fixturedb', 'POSTGRES_PASSWORD': password}
        new = copy.deepcopy(old)
        for name in ['api', 'ingest', 'mcp']:
            new['services'][name]['image'] = fixture.NEW
        previous, candidate, receipt = [bundle / name for name in
                                        ['previous-runtime.json', 'candidate-runtime.json', 'preflight.json']]
        for path, data in [(previous, old), (candidate, new)]:
            m.state.atomic(path, data)
        images = {name: item['image'] for name, item in old['services'].items()}
        current = {name: {'image': image, 'container_id': cid(name)} for name, image in images.items()}
        plan = {'version': 1, 'status': 'preflight_only', 'activation_allowed': False,
                'root': str(root), 'project': 'fixture', 'scope': 'application_bootstrap',
                'current_services': current, 'writers': ['api', 'ingest', 'mcp'],
                'database': {'user': 'fixture', 'name': 'fixturedb'},
                'files': {str(path): m.state.digest(path) for path in [previous, candidate]},
                'previous_files': {str(previous): m.state.digest(previous)}}
        m.state.atomic(receipt, plan)
        return receipt

    def session(self, root, mode=0o600):
        path = root / 'session'
        path.write_text('synthetic-session-token')
        path.chmod(mode)
        return path

    def seams(self, root, fail=None):
        events = Events()
        acceptance = FakeAcceptance()
        original_acceptance, original_review = m.acceptance, m.review
        m.acceptance = acceptance
        m.review = types.SimpleNamespace(inspect=lambda *a: {'receipt_sha256': 'checked', 'images': []})

        def provider_class(catalog, reviewer, endpoint, request, runner):
            return FakeProvider(catalog, reviewer, endpoint, request, runner, events, root, fail=fail)

        def client_class(origin, session, tenant, user, **kwargs):
            client = FakeClient(origin, session, tenant, user, **kwargs)
            client.events = events
            return client

        def restore():
            m.acceptance, m.review = original_acceptance, original_review
        return events, acceptance, provider_class, client_class, restore

    def test_construction_refusals_are_closed_and_never_leak(self):
        for case in ['project', 'bundle', 'review', 'session', 'password', 'networks']:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                receipt = self.plan(root, password='' if case == 'password' else PASSWORD,
                                    private=case != 'bundle')
                session = self.session(root, mode=0o644 if case == 'session' else 0o600)
                events, acceptance, provider_class, client_class, restore = self.seams(root)
                runner = fake_runner
                if case == 'networks':
                    def runner(args, **kwargs):
                        doc = inspect_doc('web', 'fixture_default', '172.18.0.4')
                        doc['NetworkSettings']['Networks']['other_default'] = {
                            'NetworkID': cid('net-other'), 'IPAddress': '172.19.0.4'}
                        pg = inspect_doc('postgres', 'fixture_default', '172.18.0.2')
                        pg['NetworkSettings']['Networks']['other_default'] = {
                            'NetworkID': cid('net-other'), 'IPAddress': '172.19.0.2'}
                        key = 'web' if args[2] == cid('web') else 'postgres'
                        return types.SimpleNamespace(returncode=0, stdout=json.dumps([doc if key == 'web' else pg]), stderr='')
                if case == 'review':
                    m.review = types.SimpleNamespace(
                        inspect=lambda *a: (_ for _ in ()).throw(ValueError('plan changed')))
                try:
                    project = 'Fixture' if case == 'project' else 'fixture'
                    with self.assertRaises((ValueError, OSError)) as caught:
                        m.execute(root, project, receipt, 'activate', str(session), 't1', 'u1',
                                  provider_class=provider_class, client_class=client_class, runner=runner)
                    self.assertNotIn(PASSWORD, str(caught.exception))
                    self.assertNotIn('enter', events.items)
                    self.assertEqual(acceptance.constructed, [])
                finally:
                    restore()

    def test_action_detection_maps_interrupted_state(self):
        original = m.recovery
        try:
            m.recovery = types.SimpleNamespace(inspect=lambda root: {'normal_actions_blocked': False, 'state': 'no_transaction'})
            self.assertEqual(m.detect('/x', 'fixture')[0], 'activate')
            m.recovery = types.SimpleNamespace(inspect=lambda root: {
                'normal_actions_blocked': True, 'scope': 'application_bootstrap', 'project': 'fixture', 'state': 'maintenance_release_pending'})
            self.assertEqual(m.detect('/x', 'fixture')[0], 'recover')
            m.recovery = types.SimpleNamespace(inspect=lambda root: {
                'normal_actions_blocked': True, 'scope': 'transport_only', 'project': 'fixture'})
            with self.assertRaises(ValueError):
                m.detect('/x', 'fixture')
            m.recovery = types.SimpleNamespace(inspect=lambda root: {
                'normal_actions_blocked': True, 'scope': 'application_bootstrap', 'project': 'other'})
            with self.assertRaises(ValueError):
                m.detect('/x', 'fixture')
        finally:
            m.recovery = original

    def test_full_apply_binds_exact_inputs_and_releases_last(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            receipt = self.plan(root)
            session = self.session(root)
            events, acceptance, provider_class, client_class, restore = self.seams(root)
            captured = {}
            original_class = provider_class

            def provider_class(catalog, reviewer, endpoint, request, runner):
                captured.update(catalog=catalog, reviewer=reviewer, endpoint=endpoint, request=request)
                return original_class(catalog, reviewer, endpoint, request, runner)
            try:
                result = m.execute(root, 'fixture', receipt, 'activate', str(session), 't1', 'u1',
                                   provider_class=provider_class, client_class=client_class, runner=fake_runner)
            finally:
                restore()
            self.assertEqual(result['state'], 'committed')
            # Baseline was captured live before any maintenance admission.
            self.assertLess(events.items.index('snapshot'), events.items.index('enter'))
            # Exact provider inputs from the reviewed plan, never discovered.
            self.assertEqual(captured['catalog'].database, 'fixturedb')
            self.assertEqual(captured['catalog'].user, 'fixture')
            self.assertEqual(set(captured['endpoint']), {'container', 'image', 'started', 'project',
                                                         'service', 'network', 'network_id', 'address'})
            self.assertEqual(captured['endpoint']['container'], cid('web'))
            self.assertEqual(captured['endpoint']['service'], 'web')
            self.assertEqual(captured['request']['host'], '172.18.0.2')
            self.assertEqual(captured['request']['password'], PASSWORD)
            self.assertEqual(captured['request']['transport'], 'private_tcp_plain')
            # Reviewer refuses anything but the exact reviewed target.
            target = m.execution.Target(str(root), 'fixture', 'application_bootstrap', 'activate',
                                        str(receipt.parent / 'previous-runtime.json'),
                                        str(receipt.parent / 'candidate-runtime.json'),
                                        m.state.digest(receipt))
            captured['reviewer'](target)
            bad = m.execution.Target(str(root), 'other', 'application_bootstrap', 'activate',
                                     str(receipt.parent / 'previous-runtime.json'),
                                     str(receipt.parent / 'candidate-runtime.json'),
                                     m.state.digest(receipt))
            with self.assertRaises(ValueError):
                captured['reviewer'](bad)
            # Acceptance wiring used the live provider adapter/group and baseline.
            kinds = [name for name, *_ in acceptance.constructed]
            self.assertIn('runtime', kinds)
            runtime = next(item for item in acceptance.constructed if item[0] == 'runtime')
            self.assertIs(runtime[1].marker, 'adapter')
            self.assertEqual(runtime[3], {'version': 1, 'scope': 'limited_retained_lists',
                                          'records': {'/v1/devices': {'d1': {'id': 'd1'}}},
                                          'full_compatibility': False, 'runtime_accepted': False})
            self.assertIn(('runtime', str(receipt.parent / 'candidate-runtime.json'), 'candidate'), acceptance.called)
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'candidate-runtime.json'))
            # The baseline stays with the settled transaction record, bound to
            # its receipt, so a later explicit recovery re-verifies against it.
            settled = m.state.read_private(root / '.secure-deployment' / 'baseline.json')
            self.assertEqual(settled['receipt_sha256'], m.state.digest(receipt))
            self.assertEqual(settled['baseline']['records'], {'/v1/devices': {'d1': {'id': 'd1'}}})

    def test_interrupted_apply_rolls_back_then_recovers_without_db_replay(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            receipt = self.plan(root)
            session = self.session(root)
            events, acceptance, provider_class, client_class, restore = self.seams(root, fail='accept-candidate')
            try:
                with self.assertRaises(RuntimeError):
                    m.execute(root, 'fixture', receipt, 'activate', str(session), 't1', 'u1',
                              provider_class=provider_class, client_class=client_class, runner=fake_runner)
            finally:
                restore()
            # Rollback proved previous-image compatibility on the 93 ledger and
            # accepted the restored previous runtime; no schema replay happened.
            self.assertIn(('previous', str(receipt.parent / 'previous-runtime.json')), acceptance.called)
            self.assertIn(('runtime', str(receipt.parent / 'previous-runtime.json'), 'previous'), acceptance.called)
            with m.state.Journal(root) as journal:
                value = journal.read()
                self.assertEqual(value['phase'], 'restored')
                self.assertEqual(value['maintenance_state'], 'held')
                self.assertEqual(value['schema_attempted'], m.execution.application.app.ADDED)
            with self.assertRaises(ValueError):
                m.state.selection(root)
            # The interrupted transaction holds the baseline persisted before admission.
            recorded = m.state.read_private(root / '.secure-deployment' / 'baseline.json')
            self.assertEqual(recorded['receipt_sha256'], m.state.digest(receipt))
            self.assertEqual(recorded['baseline']['records'], {'/v1/devices': {'d1': {'id': 'd1'}}})
            # A fresh explicit recovery settles the held maintenance session
            # using the durable baseline, never a live read.
            events2, acceptance2, provider_class2, client_class2, restore2 = self.seams(root)
            try:
                result = m.execute(root, 'fixture', receipt, 'recover', str(session), 't1', 'u1',
                                   provider_class=provider_class2, client_class=client_class2, runner=fake_runner)
            finally:
                restore2()
            self.assertEqual(result['state'], 'restored')
            self.assertFalse(result['changed'])
            self.assertNotIn('snapshot', events2.items)
            runtime2 = next(item for item in acceptance2.constructed if item[0] == 'runtime')
            self.assertEqual(runtime2[3], recorded['baseline'])
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'previous-runtime.json'))
            # The settled recovery retains the baseline with the transaction record.
            settled = m.state.read_private(root / '.secure-deployment' / 'baseline.json')
            self.assertEqual(settled['receipt_sha256'], m.state.digest(receipt))

    def test_recovery_without_durable_baseline_refuses_before_any_work(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            receipt = self.plan(root)
            session = self.session(root)
            events, acceptance, provider_class, client_class, restore = self.seams(root, fail='accept-candidate')
            try:
                with self.assertRaises(RuntimeError):
                    m.execute(root, 'fixture', receipt, 'activate', str(session), 't1', 'u1',
                              provider_class=provider_class, client_class=client_class, runner=fake_runner)
            finally:
                restore()
            baseline_path = root / '.secure-deployment' / 'baseline.json'
            self.assertTrue(baseline_path.exists())
            # Content tampering within a correctly bound file is out of this
            # layer's reach: it fails safe at the real acceptance, whose strict
            # live comparison (reads.compare) refuses any mismatch; it can
            # never manufacture an approval. Binding violations refuse here.
            original = m.state.read_private(baseline_path)
            for case in ['missing', 'tampered-binding']:
                with self.subTest(case=case):
                    try:
                        if case == 'missing':
                            baseline_path.unlink()
                        else:
                            recorded = copy.deepcopy(original)
                            recorded['receipt_sha256'] = '0' * 64
                            m.state.atomic(baseline_path, recorded)
                        events2, acceptance2, provider_class2, client_class2, restore2 = self.seams(root)
                        try:
                            with self.assertRaises(ValueError) as caught:
                                m.execute(root, 'fixture', receipt, 'recover', str(session), 't1', 'u1',
                                          provider_class=provider_class2, client_class=client_class2, runner=fake_runner)
                        finally:
                            restore2()
                        self.assertIn('baseline', str(caught.exception))
                        self.assertNotIn('enter', events2.items)
                        self.assertNotIn('snapshot', events2.items)
                        self.assertEqual(acceptance2.constructed, [])
                        # Maintenance protection is retained after the refusal.
                        self.assertTrue((root / '.secure-deployment' / 'maintenance.json').exists())
                    finally:
                        m.state.atomic(baseline_path, original)

    def test_durable_baseline_source_by_interruption_window(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            receipt = self.plan(root)
            directory = root / '.secure-deployment'
            baseline_path = directory / 'baseline.json'
            # No interruption state at all: pre-admission, live read allowed.
            self.assertIsNone(m.durable_baseline(root, receipt))
            directory.mkdir(mode=0o700, exist_ok=True)
            bound = {'version': 1, 'receipt_sha256': m.state.digest(receipt),
                     'baseline': {'version': 1, 'scope': 'limited_retained_lists', 'records': {}}}
            # A bound baseline is authoritative even without a session: this is
            # the settled-record and post-baseline-write pre-admission case.
            m.state.atomic(baseline_path, bound)
            self.assertEqual(m.durable_baseline(root, receipt), bound['baseline'])
            # A foreign binding refuses even without a session.
            foreign = dict(bound)
            foreign['receipt_sha256'] = '0' * 64
            m.state.atomic(baseline_path, foreign)
            with self.assertRaises(ValueError):
                m.durable_baseline(root, receipt)
            baseline_path.unlink()
            # A maintenance session without its baseline refuses: admission was
            # closed, so no live read is ever safe here.
            m.state.atomic(directory / 'maintenance.json', {'version': 1})
            with self.assertRaises(ValueError):
                m.durable_baseline(root, receipt)
            (directory / 'maintenance.json').unlink()
            # A transaction journal bound to THIS receipt without its baseline
            # refuses; a journal from another receipt is not a session here.
            m.state.atomic(directory / 'journal.json', {'version': 1, 'receipt_sha256': '0' * 64})
            self.assertIsNone(m.durable_baseline(root, receipt))
            m.state.atomic(directory / 'journal.json',
                           {'version': 1, 'receipt_sha256': m.state.digest(receipt)})
            with self.assertRaises(ValueError):
                m.durable_baseline(root, receipt)

    def test_limited_behavior_reverifies_identity_and_returns_nothing(self):
        client = FakeClient('http://127.0.0.1:8000', 's', 't1', 'u1')
        check = m.limited_behavior(client)
        self.assertIsNone(check({'previous': 'x'}, 'previous'))

    def test_non_tty_refuses_before_any_plan_or_docker_work(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            receipt = self.plan(root)
            result = subprocess.run([sys.executable, str(HERE / 'secure-update-apply.py'),
                                     '--root', str(root), '--project', 'fixture', '--receipt', str(receipt)],
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True)
            self.assertEqual(result.returncode, 1)
            self.assertIn('interactive terminal', result.stderr)
            self.assertNotIn(PASSWORD, result.stderr + result.stdout)


if __name__ == '__main__':
    unittest.main()
