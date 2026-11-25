#!/usr/bin/env python3
"""Durable-write process loss across selected generations, fake effects only."""
import importlib.util
import pathlib
import tempfile
import unittest
HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('generation_fixture', HERE / 'test-secure-generation-execution.py')
f = importlib.util.module_from_spec(spec)
spec.loader.exec_module(f)
m = f.m

class Loss(BaseException):
    pass

class Tests(unittest.TestCase):
    def test_every_atomic_publication_before_and_after_requires_recovery_or_safe_preintent_retry(self):
        # Trace a real Coordinator success, then reproduce loss at each durable
        # JSON publication. Ordinary selection must block once intent is durable.
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            effects = f.Tests().initial(root)
            receipt = f.Tests().next_plan(root)
            trace = []
            original = m.state.atomic
            original_generation = m.generations.state.atomic
            def record(path, value):
                trace.append((path.name, value.get('phase'), value.get('maintenance_state')))
                return original(path, value)
            m.state.atomic = record
            m.generations.state.atomic = record
            try:
                f.Tests().coordinator(effects).run(root, 'fixture', receipt, 'activate')
            finally:
                m.state.atomic = original
                m.generations.state.atomic = original_generation
        self.assertGreaterEqual(len(trace), 13)
        self.checkpoints = len(trace) * 2
        for index in range(len(trace)):
            for after in [False, True]:
                with self.subTest(write=index, step=trace[index], after=after), tempfile.TemporaryDirectory() as td:
                    root = pathlib.Path(td)
                    helper = f.Tests()
                    effects = helper.initial(root)
                    receipt = helper.next_plan(root)
                    effects.calls.clear()
                    count = [0]
                    def fail(path, value):
                        hit = count[0] == index
                        count[0] += 1
                        if hit and not after:
                            raise Loss()
                        result = original(path, value)
                        if hit:
                            raise Loss()
                        return result
                    m.state.atomic = fail
                    m.generations.state.atomic = fail
                    try:
                        with self.assertRaises(Loss):
                            helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
                    finally:
                        m.state.atomic = original
                        m.generations.state.atomic = original_generation
                    directory = root / '.secure-deployment'
                    intent = (directory / 'generation-intent.json').exists()
                    marker = (directory / 'maintenance.json').exists()
                    effects.calls.clear()
                    if intent or marker:
                        with self.assertRaises(ValueError):
                            m.state.selection(root)
                        helper.coordinator(effects).run(root, 'fixture', receipt, 'recover')
                        self.assertNotIn('apply-reviewed-schema', effects.calls)
                    else:
                        self.assertIsNotNone(m.state.selection(root))
                        helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
                    self.assertIsNotNone(m.state.selection(root))
                    self.assertFalse((directory / 'generation-intent.json').exists())
                    self.assertFalse((directory / 'maintenance.json').exists())
        print('Atomic generation publication loss checkpoints:', self.checkpoints)

    def test_intent_and_marker_unlink_loss_recovers_without_schema_replay(self):
        for name in ['generation-intent.json', 'maintenance.json']:
            for after in [False, True]:
                with self.subTest(name=name, after=after), tempfile.TemporaryDirectory() as td:
                    root = pathlib.Path(td)
                    helper = f.Tests()
                    effects = helper.initial(root)
                    receipt = helper.next_plan(root)
                    original = m.state.unlink_durable
                    generation_original = m.generations.state.unlink_durable
                    def fail(path):
                        if path.name != name:
                            return original(path)
                        if after:
                            original(path)
                        raise Loss()
                    m.state.unlink_durable = fail
                    m.generations.state.unlink_durable = fail
                    try:
                        with self.assertRaises(Loss):
                            helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
                    finally:
                        m.state.unlink_durable = original
                        m.generations.state.unlink_durable = generation_original
                    effects.calls.clear()
                    helper.coordinator(effects).run(root, 'fixture', receipt, 'recover')
                    self.assertEqual(effects.calls, ['verify-recovery-inputs', 'accept-candidate'])
                    self.assertIsNotNone(m.state.selection(root))

    def test_immutable_archive_loss_is_safe_preintent_retry_without_replacement(self):
        for filename in ['journal.json', 'selected.json', 'receipt.json']:
            for after in [False, True]:
                with self.subTest(filename=filename, after=after), tempfile.TemporaryDirectory() as td:
                    root = pathlib.Path(td)
                    helper = f.Tests()
                    effects = helper.initial(root)
                    receipt = helper.next_plan(root)
                    selected = m.state.selection(root)
                    original = m.generations.Generation.immutable
                    def fail(path, raw):
                        if path.name != filename:
                            return original(path, raw)
                        if after:
                            original(path, raw)
                        raise Loss()
                    m.generations.Generation.immutable = staticmethod(fail)
                    try:
                        with self.assertRaises(Loss):
                            helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
                    finally:
                        m.generations.Generation.immutable = staticmethod(original)
                    self.assertEqual(m.state.selection(root), selected)
                    effects.calls.clear()
                    helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
                    self.assertIn('apply-reviewed-schema', effects.calls)
                    self.assertIsNotNone(m.state.selection(root))

    def test_noncanonical_predecessor_and_receipt_bytes_are_archived_exactly(self):
        import json
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            helper = f.Tests()
            effects = helper.initial(root)
            receipt = helper.next_plan(root)
            for path in [root / '.secure-deployment/journal.json', root / '.secure-deployment/selected.json', receipt]:
                path.write_text(json.dumps(m.state.read_private(path), separators=(',', ':')) + '\n')
            journal_bytes = (root / '.secure-deployment/journal.json').read_bytes()
            receipt_bytes = receipt.read_bytes()
            helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
            archives = list((root / '.secure-deployment').glob('generation-*'))
            self.assertEqual(len(archives), 1)
            self.assertEqual((archives[0] / 'journal.json').read_bytes(), journal_bytes)
            self.assertEqual((archives[0] / 'receipt.json').read_bytes(), receipt_bytes)

    def test_os_lock_blocks_concurrent_generation_without_any_effect(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            helper = f.Tests()
            effects = helper.initial(root)
            receipt = helper.next_plan(root)
            effects.calls.clear()
            with m.state.Journal(root):
                with self.assertRaises(ValueError):
                    helper.coordinator(effects).run(root, 'fixture', receipt, 'activate')
            self.assertEqual(effects.calls, [])
            self.assertFalse((root / '.secure-deployment/generation-intent.json').exists())

if __name__ == '__main__':
    unittest.main()
