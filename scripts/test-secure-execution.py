#!/usr/bin/env python3
"""Execution caller contracts with fake effects, never owner Docker/authority."""
import copy
import importlib.util
import pathlib
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

m = load('secure-execution')
fixture = load('test-secure-application')

class Session:
    pass

class Provider:
    def __init__(self):
        self.calls = []
        self.ticket = Session()
        self.valid = True
        self.release_fail = False
        self.review_fail = False
    def review(self, target):
        self.calls.append(('review', target))
        if self.review_fail: raise ValueError('No reviewed maintenance authority')
    def enter(self, target):
        self.calls.append(('enter', target))
        return self.ticket
    def check(self, ticket, target):
        self.calls.append(('check', target))
        if ticket is not self.ticket or not self.valid: raise ValueError('Fence no longer held')
    def release(self, ticket, target):
        self.check(ticket, target)
        self.calls.append(('release', target))
        if self.release_fail: raise ValueError('Release not verified')
    def retain(self, ticket, target):
        self.calls.append(('retain', target))

class Tests(unittest.TestCase):
    def fixture(self, root, provider=None, crash=None, fail=None):
        provider = provider or Provider()
        bundle = root / 'plan'
        bundle.mkdir(mode=0o700)
        old = fixture.Tests().config()
        new = copy.deepcopy(old)
        for name in ['api', 'ingest', 'mcp']: new['services'][name]['image'] = fixture.NEW
        previous, candidate, receipt = [bundle / name for name in ['previous-runtime.json', 'candidate-runtime.json', 'preflight.json']]
        for path, data in [(previous, old), (candidate, new)]: m.state.atomic(path, data)
        images = {name: item['image'] for name, item in old['services'].items()}
        plan = {'version': 1, 'status': 'preflight_only', 'activation_allowed': False,
                'root': str(root), 'project': 'fixture', 'scope': 'application_bootstrap',
                'current_services': {name: {'image': image} for name, image in images.items()},
                'files': {str(path): m.state.digest(path) for path in [previous, candidate]},
                'previous_files': {str(previous): m.state.digest(previous)}}
        m.state.atomic(receipt, plan)
        effects = fixture.Effects(root, fail=fail, crash=crash)
        def factory(target, path, authorize, accept, compatible):
            self.assertEqual(target.scope, 'application_bootstrap')
            self.assertEqual(path, receipt)
            original = effects.authorize
            def approved(*args):
                authorize(*args)
                original(*args)
            effects.authorize = approved
            return effects
        coordinator = m.Coordinator(provider, lambda *a: None, lambda *a: None, factory)
        return coordinator, effects, provider, receipt

    def test_explicit_provider_binds_same_live_journal_before_entry(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            provider = Provider()
            held = []
            def bind(journal, target):
                journal.locked()
                self.assertEqual(journal.root, root)
                held.append(journal)
            provider.bind_journal = bind
            caller, effects, provider, receipt = self.fixture(root, provider)
            original_enter = provider.enter
            def enter(target):
                self.assertEqual(len(held), 1)
                held[0].locked()
                self.assertTrue((held[0].directory / 'maintenance.json').exists())
                return original_enter(target)
            provider.enter = enter
            caller.run(root, 'fixture', receipt, 'activate')
            with self.assertRaises(ValueError): held[0].locked()

    def test_activation_uses_provider_through_commit_and_releases_last(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root)
            result = caller.run(root, 'fixture', receipt, 'activate')
            self.assertEqual(result['state'], 'committed')
            self.assertGreater(sum(name == 'check' for name, _ in provider.calls), 15)
            self.assertEqual(provider.calls[-1][0], 'release')
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'candidate-runtime.json'))
            with m.state.Journal(root) as journal: self.assertEqual(journal.read()['maintenance_state'], 'released')

    def test_missing_provider_verifiers_flags_and_wrong_target_refuse(self):
        with self.assertRaises(ValueError): m.Coordinator(object(), lambda *a: None, lambda *a: None)
        for damage in ['review', 'ticket', 'flag', 'target', 'action']:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                caller, effects, provider, receipt = self.fixture(root)
                if damage == 'review': provider.review_fail = True
                if damage == 'ticket': provider.ticket = {'approved': True}
                if damage == 'flag': provider.review = lambda target: False
                project = 'wrong' if damage == 'target' else 'fixture'
                action = 'force' if damage == 'action' else 'activate'
                with self.assertRaises(ValueError): caller.run(root, project, receipt, action)
                self.assertNotIn('stop-all-writers', effects.calls)
                self.assertFalse((root / '.secure-deployment/journal.json').exists())

    def test_fence_loss_after_schema_never_runs_candidate_or_unguarded_rollback(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root)
            original = effects.apply_reviewed_schema
            def lose(*args):
                original(*args)
                provider.valid = False
            effects.apply_reviewed_schema = lose
            with self.assertRaises(RuntimeError): caller.run(root, 'fixture', receipt, 'activate')
            self.assertNotIn('start-candidate', effects.calls)
            self.assertNotIn('stop-all-candidate-writers', effects.calls)
            self.assertNotIn('restore-previous', effects.calls)
            self.assertEqual(provider.calls[-1][0], 'retain')
            with self.assertRaises(ValueError): m.state.selection(root)
            with m.state.Journal(root) as journal:
                self.assertEqual(journal.read()['phase'], 'rollback_required')
                self.assertEqual(journal.read()['maintenance_state'], 'held')

    def test_process_loss_requires_new_reviewed_recovery_and_preserves_rows(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root, crash='apply-reviewed-schema')
            with self.assertRaises(fixture.Interrupted): caller.run(root, 'fixture', receipt, 'activate')
            self.assertEqual(provider.calls[-1][0], 'retain')
            with self.assertRaises(ValueError): m.state.selection(root)
            effects.crash = None
            # Replace provider/session and authorization adapter, as a new process.
            recovery_provider = Provider()
            def factory(target, path, authorize, accept, compatible):
                effects.authorize = authorize
                return effects
            recovery = m.Coordinator(recovery_provider, lambda *a: None, lambda *a: None, factory)
            result = recovery.run(root, 'fixture', receipt, 'recover')
            self.assertEqual(result['state'], 'restored')
            self.assertIn('post-backup-telemetry', effects.rows)
            self.assertIn('verify-prior-schema-compatibility', effects.calls)
            self.assertEqual(recovery_provider.calls[-1][0], 'release')
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'previous-runtime.json'))

    def test_release_failure_keeps_committed_selector_blocked_until_fresh_acceptance(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root)
            provider.release_fail = True
            with self.assertRaises(ValueError): caller.run(root, 'fixture', receipt, 'activate')
            with self.assertRaises(ValueError): m.state.selection(root)
            with m.state.Journal(root) as journal:
                self.assertEqual(journal.read()['phase'], 'committed')
                self.assertEqual(journal.read()['maintenance_state'], 'held')
            provider.release_fail = False
            effects.calls.clear()
            caller.run(root, 'fixture', receipt, 'recover')
            self.assertIn('verify-recovery-inputs', effects.calls)
            self.assertIn('accept-candidate', effects.calls)
            self.assertNotIn('restore-previous', effects.calls)
            self.assertNotIn('apply-reviewed-schema', effects.calls)
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'candidate-runtime.json'))

    def test_receipt_change_on_recovery_refuses_before_maintenance(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root, crash='apply-reviewed-schema')
            with self.assertRaises(fixture.Interrupted): caller.run(root, 'fixture', receipt, 'activate')
            plan = m.state.read_private(receipt)
            plan['changed'] = True
            m.state.atomic(receipt, plan)
            provider.calls.clear()
            with self.assertRaises(ValueError): caller.run(root, 'fixture', receipt, 'recover')
            self.assertEqual(provider.calls, [])

    def test_transport_routes_separately_and_retains_on_process_loss(self):
        transport_fixture = load('test-secure-transaction')
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            args = transport_fixture.Tests().fixture(root)
            effects = transport_fixture.Effects(root, crash='start-candidate')
            provider = Provider()
            def factory(target, path, authorize, accept, compatible):
                self.assertEqual(target.scope, 'transport_only')
                effects.authorize = authorize
                return effects
            caller = m.Coordinator(provider, lambda *a: None, lambda *a: None, factory)
            with self.assertRaises(transport_fixture.Interrupted): caller.run(root, 'fixture', args[4], 'activate')
            with self.assertRaises(ValueError): m.state.selection(root)
            effects.crash = None
            result = caller.run(root, 'fixture', args[4], 'recover')
            self.assertEqual(result['state'], 'rolled_back')
            self.assertIn('restore-previous', effects.calls)
            self.assertEqual(provider.calls[-1][0], 'release')
            self.assertEqual(m.state.selection(root)['config'], str(args[1]))

    def test_admission_crash_before_transaction_is_durable_and_recovers_without_changes(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            caller, effects, provider, receipt = self.fixture(root)
            def interrupted_enter(target):
                self.assertTrue((root / '.secure-deployment/maintenance.json').is_file())
                raise fixture.Interrupted('Fence admission lost before journal.begin')
            provider.enter = interrupted_enter
            with self.assertRaises(fixture.Interrupted): caller.run(root, 'fixture', receipt, 'activate')
            self.assertFalse((root / '.secure-deployment/journal.json').exists())
            self.assertEqual(effects.calls, [])
            with self.assertRaises(ValueError): m.state.selection(root)
            review = load('secure-recovery-review')
            self.assertEqual(review.inspect(root)['state'], 'maintenance_admission_pending')
            provider.enter = lambda target: provider.ticket
            result = caller.run(root, 'fixture', receipt, 'recover')
            self.assertEqual(result['state'], 'maintenance_settled_before_changes')
            self.assertIn('accept-previous', effects.calls)
            self.assertNotIn('apply-reviewed-schema', effects.calls)
            self.assertNotIn('stop-all-writers', effects.calls)
            self.assertFalse((root / '.secure-deployment/maintenance.json').exists())
            self.assertIsNone(m.state.selection(root))

    def test_all_adapter_calls_have_pre_and_post_checks(self):
        checks, effects = [], []
        class Effect:
            def call(self): effects.append('called'); return 'result'
        wrapped = m.GuardedEffects(Effect(), lambda: checks.append(len(effects)))
        self.assertEqual(wrapped.call(), 'result')
        self.assertEqual(checks, [0, 1])

if __name__ == '__main__':
    unittest.main()
