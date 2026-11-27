#!/usr/bin/env python3
"""Selected multi-generation Coordinator tests. Fake runtime, no Docker."""
import copy
import pathlib
import tempfile
import unittest
import importlib.util
HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

fixture = load('test-secure-execution')
m = fixture.m
BASELINE = fixture.BASELINE

class Lost(BaseException):
    pass

class Provider(fixture.Provider):
    def generation_handoff(self, journal, target, value):
        journal.locked()
        self.calls.append(('generation', target))
        if value['receipt_sha256'] != target.receipt_sha256:
            raise ValueError('Wrong generation target')

class Tests(unittest.TestCase):
    def initial(self, root, restored=False):
        caller, effects, provider, receipt = fixture.Tests().fixture(root, fail='accept-candidate' if restored else None)
        if restored:
            with self.assertRaises(RuntimeError):
                caller.run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            effects.fail = None
            provider = fixture.Provider()
            self.coordinator(effects, provider).run(root, 'fixture', receipt, 'recover')
        else:
            caller.run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
        return effects

    def next_plan(self, root, name='second', image='c'):
        selected = m.state.selection(root)
        directory = root / name
        directory.mkdir(mode=0o700)
        old = m.state.read_private(selected['config'])
        new = copy.deepcopy(old)
        for service in ['api', 'ingest', 'mcp']:
            new['services'][service]['image'] = 'sha256:' + image * 64
        previous, candidate, receipt = [directory / n for n in ['previous-runtime.json', 'candidate-runtime.json', 'preflight.json']]
        for path, value in [(previous, old), (candidate, new)]:
            m.state.atomic(path, value)
        plan = {'version': 1, 'status': 'preflight_only', 'activation_allowed': False,
                'root': str(root), 'project': 'fixture', 'scope': 'application_bootstrap',
                'current_services': {name: {'image': service['image']} for name, service in old['services'].items()},
                'files': {str(path): m.state.digest(path) for path in [previous, candidate]},
                'previous_files': {str(previous): m.state.digest(previous)}}
        m.state.atomic(receipt, plan)
        return receipt

    def coordinator(self, effects, provider=None):
        def factory(target, receipt, authorize, accept, compatible):
            effects.authorize = authorize
            return effects
        return m.Coordinator(provider or Provider(), lambda *a: None, lambda *a: None, factory)

    def test_committed_and_restored_predecessors_support_two_more_generations_without_rename(self):
        for restored in [False, True]:
            with self.subTest(restored=restored), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                effects = self.initial(root, restored)
                inode = (root / '.secure-deployment').stat().st_ino
                for name, image in [('second', 'c'), ('third', 'd')]:
                    receipt = self.next_plan(root, name, image)
                    result = self.coordinator(effects).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
                    self.assertEqual(result['state'], 'committed')
                    self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'candidate-runtime.json'))
                    self.assertIn('retained-identity', effects.rows)
                    self.assertIn('post-backup-telemetry', effects.rows)
                    self.assertEqual((root / '.secure-deployment').stat().st_ino, inode)
                self.assertEqual(len(list((root / '.secure-deployment').glob('generation-*/successor.json'))), 2)

    def test_prechange_inspection_gets_exact_predecessor_only_until_begin(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = self.initial(root)
            selected = m.state.selection(root)
            receipt = self.next_plan(root)
            effects.revalidate = lambda *args: self.assertEqual(effects.selection_reader(root), selected)
            def after_begin(*args):
                with self.assertRaises(ValueError):
                    effects.selection_reader(root)
            effects.stop_writers = lambda *args: (setattr(effects, 'stopped', True), after_begin())[-1]
            self.coordinator(effects).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)

    def test_generation_requires_explicit_provider_support_and_review_before_intent(self):
        for unsupported in [False, True]:
            with self.subTest(unsupported=unsupported), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                effects = self.initial(root)
                selected = m.state.selection(root)
                receipt = self.next_plan(root)
                provider = fixture.Provider() if unsupported else Provider()
                provider.review_fail = not unsupported
                with self.assertRaises(ValueError):
                    self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
                self.assertFalse((root / '.secure-deployment/generation-intent.json').exists())
                self.assertEqual(m.state.selection(root), selected)

    def test_admission_loss_keeps_predecessor_and_recovery_never_replays_schema(self):
        for where in ['after-intent', 'enter']:
            with self.subTest(where=where), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                effects = self.initial(root)
                selected = m.state.selection(root)
                receipt = self.next_plan(root)
                effects.calls.clear()
                provider = Provider()
                def lost(*args):
                    raise Lost()
                if where == 'after-intent':
                    provider.generation_handoff = lost
                else:
                    provider.enter = lost
                with self.assertRaises(Lost):
                    self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
                self.assertEqual(effects.calls, [])
                with self.assertRaises(ValueError):
                    m.state.selection(root)
                result = self.coordinator(effects).run(root, 'fixture', receipt, 'recover')
                self.assertEqual(result['state'], 'maintenance_settled_before_changes')
                self.assertNotIn('apply-reviewed-schema', effects.calls)
                self.assertNotIn('restore-previous', effects.calls)
                self.assertIn('accept-previous', effects.calls)
                self.assertEqual(m.state.selection(root), selected)

    def test_schema_loss_requires_compatibility_and_preserves_postbackup_rows(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = self.initial(root)
            receipt = self.next_plan(root)
            effects.crash = 'apply-reviewed-schema'
            effects.calls.clear()
            with self.assertRaises(fixture.fixture.Interrupted):
                self.coordinator(effects).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            with self.assertRaises(ValueError):
                m.state.selection(root)
            effects.crash = None
            effects.fail = 'verify-prior-schema-compatibility'
            with self.assertRaises(ValueError):
                self.coordinator(effects).run(root, 'fixture', receipt, 'recover')
            with self.assertRaises(ValueError):
                m.state.selection(root)
            effects.fail = None
            effects.calls.clear()
            result = self.coordinator(effects).run(root, 'fixture', receipt, 'recover')
            self.assertEqual(result['state'], 'restored')
            self.assertIn('verify-prior-schema-compatibility', effects.calls)
            self.assertIn('post-backup-telemetry', effects.rows)
            self.assertNotIn('apply-reviewed-schema', effects.calls)
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'previous-runtime.json'))

    def test_release_loss_fresh_acceptance_without_schema_or_rollback_replay(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = self.initial(root)
            receipt = self.next_plan(root)
            provider = Provider()
            provider.release_fail = True
            with self.assertRaises(ValueError):
                self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            with self.assertRaises(ValueError):
                m.state.selection(root)
            effects.calls.clear()
            result = self.coordinator(effects).run(root, 'fixture', receipt, 'recover')
            self.assertEqual(result['state'], 'committed')
            self.assertEqual(effects.calls, ['verify-recovery-inputs', 'accept-candidate'])
            self.assertEqual(m.state.selection(root)['config'], str(receipt.parent / 'candidate-runtime.json'))

    def test_settled_prebegin_recovery_requires_fresh_plan_not_same_intent_reuse(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = self.initial(root)
            receipt = self.next_plan(root)
            provider = Provider()
            provider.enter = lambda *args: (_ for _ in ()).throw(Lost())
            with self.assertRaises(Lost):
                self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            self.coordinator(effects).run(root, 'fixture', receipt, 'recover')
            selected = m.state.selection(root)
            effects.calls.clear()
            with self.assertRaises(ValueError):
                self.coordinator(effects).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            self.assertEqual(effects.calls, [])
            self.assertEqual(m.state.selection(root), selected)
            fresh = self.next_plan(root, 'fresh-second')
            self.assertEqual(self.coordinator(effects).run(root, 'fixture', fresh, 'activate', baseline=BASELINE)['state'], 'committed')

    def test_recovery_review_handles_intent_before_marker_and_active_successor_without_writes(self):
        review = fixture.load('secure-recovery-review')
        for where, expected in [('entry', 'generation_predecessor_held'), ('schema', 'generation_successor_active')]:
            with self.subTest(where=where), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                effects = self.initial(root)
                receipt = self.next_plan(root)
                provider = Provider()
                if where == 'entry':
                    provider.generation_handoff = lambda *args: (_ for _ in ()).throw(Lost())
                    error = Lost
                else:
                    effects.crash = 'apply-reviewed-schema'
                    error = fixture.fixture.Interrupted
                with self.assertRaises(error):
                    self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
                before = {str(path): path.read_bytes() for path in root.rglob('*') if path.is_file()}
                result = review.inspect(root)
                self.assertEqual(result['state'], expected)
                self.assertTrue(result['normal_actions_blocked'])
                self.assertFalse(result['changes_allowed'])
                after = {str(path): path.read_bytes() for path in root.rglob('*') if path.is_file()}
                self.assertEqual(before, after)

    def test_wrong_receipt_recovery_does_not_touch_provider(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = self.initial(root)
            receipt = self.next_plan(root)
            provider = Provider()
            provider.enter = lambda *args: (_ for _ in ()).throw(Lost())
            with self.assertRaises(Lost):
                self.coordinator(effects, provider).run(root, 'fixture', receipt, 'activate', baseline=BASELINE)
            plan = m.state.read_private(receipt)
            plan['changed'] = True
            m.state.atomic(receipt, plan)
            provider = Provider()
            with self.assertRaises(ValueError):
                self.coordinator(effects, provider).run(root, 'fixture', receipt, 'recover')
            self.assertEqual(provider.calls, [])

if __name__ == '__main__':
    unittest.main()
