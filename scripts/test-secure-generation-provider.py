#!/usr/bin/env python3
"""Provider generation resource isolation with fake host/catalog only."""
import copy
import dataclasses
import importlib.util
import pathlib
import tempfile
import unittest
from types import SimpleNamespace
HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

f = load('test-secure-application-provider')
g = load('test-secure-generation-execution')
m = f.m

class Tests(unittest.TestCase):
    def test_components_use_generation_resources_and_global_admission(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            g.Tests().initial(root)
            # fixture initial needs its own lock, so set up remaining plan here.
            receipt = g.Tests().next_plan(root)
            with m.state.Journal(root) as journal:
                generation = m.execution.generations.Generation(journal)
                value = generation.prepare('fixture', receipt)
                p = object.__new__(m.Provider)
                p.journal = journal
                p.target = m.execution.Target(str(root), 'fixture', 'application_bootstrap', 'activate',
                                             str(receipt.parent / 'previous-runtime.json'),
                                             str(receipt.parent / 'candidate-runtime.json'), m.state.digest(receipt))
                p.review = lambda _: None
                p.generation_handoff(journal, p.target, value)
                resources = pathlib.Path(value['archive']) / 'resources'
                self.assertEqual(p.path, resources / 'application-provider.json')
                catalog = SimpleNamespace(container='a' * 64, user='ci', database='app')
                fence = m.fence.DatabaseFence(journal, catalog, lambda _: None)
                barrier = m.hostmod.hba.Barrier(journal, root / 'hba', 'app', 'ci', lambda: None, lambda _: None)
                operator = m.operators.LocalOperator(journal, catalog, 'sha256:' + 'a' * 64, lambda: None)
                hosttools = load('secure-postgres-hosttool')
                tool = hosttools.HostTool(journal, 'sha256:' + 'a' * 64, lambda: None)
                gate = m.groups.gates.StartGate(journal, 'fixture', 'api',
                                               {'image': 'sha256:' + 'a' * 64, 'command': ['/bin/api']}, lambda: None)
                for path in [fence.path, barrier.record_path, operator.path, tool.path, gate.path]:
                    self.assertEqual(path.parent, resources)
                self.assertIsNone(p.transaction())
                self.assertEqual(journal.read()['phase'], 'committed')
                m.state.atomic(journal.directory / 'maintenance.json', {'root': str(root), 'scope': 'application_bootstrap'})
                self.assertEqual(tool.check()['root'], str(root))
                gate.prepare()
                self.assertEqual(pathlib.Path(gate.record()['directory']).parent, resources)
                self.assertFalse((resources / 'maintenance.json').exists())

    def test_real_provider_enter_does_not_mistake_committed_predecessor_for_settled_successor(self):
        # Exercise the actual enter implementation with real fake-host HBA and
        # catalog components; transaction classification supplies no successor.
        with tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
            root = pathlib.Path(td)
            p, target, catalog, calls, pg, peer = f.Tests().fixture(root, journal)
            resources = journal.directory / 'test-generation'
            resources.mkdir(mode=0o700)
            journal.resource_directory = resources
            p.path = resources / 'application-provider.json'
            p.transaction = lambda: None
            ticket = p.enter(target)
            p.check(ticket, target)
            self.assertEqual(p.record()['stage'], 'local')
            self.assertEqual(p.barrier.record_path.parent, resources)
            self.assertEqual(catalog.calls, [('allow', False), ('drain',), ('allow', True)])
            self.assertIn(('exclusive', []), calls)
            self.assertFalse((journal.directory / 'postgres-hba.json').exists())

    def test_fresh_bind_recovers_durable_resource_namespace_after_intent_removed(self):
        for damage in [None, 'name', 'symlink', 'successor']:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                root = pathlib.Path(td)
                p, target, catalog, calls, pg, peer = f.Tests().fixture(root, journal)
                name = 'generation-' + 'a' * 64 + '-' + target.receipt_sha256
                archive = journal.directory / name
                archive.mkdir(mode=0o700)
                resources = archive / 'resources'
                resources.mkdir(mode=0o700)
                active = {'version': 1, 'phase': 'committed', 'generation': name,
                          **{key: getattr(target, key) for key in ['project', 'previous', 'candidate', 'receipt_sha256']}}
                m.state.atomic(archive / 'successor.json', active)
                m.state.atomic(journal.directory / 'journal.json', active)
                # Same target/host, entirely new provider, no generation intent.
                journal.resource_directory = journal.directory
                if damage == 'name':
                    active['generation'] = '../elsewhere'
                    m.state.atomic(journal.directory / 'journal.json', active)
                elif damage == 'symlink':
                    resources.rmdir()
                    resources.symlink_to(root, target_is_directory=True)
                elif damage == 'successor':
                    value = dict(active, project='wrong')
                    m.state.atomic(archive / 'successor.json', value)
                fresh = m.Provider(catalog, lambda _: None, lambda *args: None, p.host.run)
                if damage is None:
                    fresh.bind_journal(journal, dataclasses.replace(target, action='recover'))
                    self.assertEqual(fresh.path, resources / 'application-provider.json')
                    self.assertEqual(journal.resource_directory, resources)
                else:
                    with self.assertRaises(ValueError):
                        fresh.bind_journal(journal, dataclasses.replace(target, action='recover'))

    def test_predecessor_provider_must_be_released_original_and_same_identity(self):
        for damage in [None, 'provider', 'hba', 'allow', 'identity', 'tools', 'missing']:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                root = pathlib.Path(td)
                p, target, catalog, calls, pg, peer = f.Tests().fixture(root, journal)
                ticket = p.enter(target)
                p.barrier.release()
                p.save(p.record(), 'released', [])
                predecessor = {'phase': 'committed', **{key: getattr(target, key) for key in
                               ['project', 'previous', 'candidate', 'receipt_sha256']}}
                archive = journal.directory / 'archive'
                archive.mkdir(mode=0o700)
                m.state.atomic(archive / 'journal.json', predecessor)
                # Next target has no effects or provider record yet.
                resources = journal.directory / 'new-resources'
                resources.mkdir(mode=0o700)
                old_path = p.path
                p.path = resources / 'application-provider.json'
                journal.resource_directory = resources
                p.target = dataclasses.replace(target, previous=str(root / 'next-previous'),
                                               candidate=str(root / 'next-candidate'), receipt_sha256='f' * 64)
                if damage == 'provider':
                    record = m.state.read_private(old_path)
                    record['stage'] = 'peers'
                    m.state.atomic(old_path, record)
                elif damage == 'hba':
                    p.host.path.write_bytes(p.host.path.read_bytes() + b'#changed')
                elif damage == 'allow':
                    catalog.value['allow'] = False
                elif damage == 'identity':
                    catalog.value['oid'] = 'different'
                elif damage == 'tools':
                    m.state.atomic(journal.directory / 'postgres-hosttool.json', {'stage': 'ready'})
                elif damage == 'missing':
                    old_path.unlink()
                if damage is None:
                    p.verify_predecessor_provider(archive)
                else:
                    with self.assertRaises(ValueError):
                        p.verify_predecessor_provider(archive)

    def test_prebegin_interruption_accepts_only_matching_new_barrier_original_baseline(self):
        for damage in [False, True]:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                root = pathlib.Path(td)
                p, target, catalog, calls, pg, peer = f.Tests().fixture(root, journal)
                p.enter(target)
                p.barrier.release()
                p.save(p.record(), 'released', [])
                prior_provider = p.record()
                prior_hba = p.barrier.read()
                archive = journal.directory / 'archive'
                archive.mkdir(mode=0o700)
                m.state.atomic(archive / 'journal.json', {'phase': 'committed', **{key: getattr(target, key) for key in
                               ['project', 'previous', 'candidate', 'receipt_sha256']}})
                resources = journal.directory / 'new-resources'
                resources.mkdir(mode=0o700)
                journal.resource_directory = resources
                p.path = resources / 'application-provider.json'
                p.target = dataclasses.replace(target, previous=str(root / 'next-previous'),
                                               candidate=str(root / 'next-candidate'), receipt_sha256='f' * 64)
                current = copy.deepcopy(prior_provider)
                current['binding'] = p.binding()
                current['stage'] = 'closing'
                m.state.atomic(p.path, current)
                new_hba = copy.deepcopy(prior_hba)
                new_hba['binding'] = {'version': 1, **p.binding()}
                new_hba['stage'] = 'held'
                if damage:
                    new_hba['original'] = b'different'.hex()
                m.state.atomic(resources / 'postgres-hba.json', new_hba)
                p.host.path.write_bytes(b'new held restricted HBA')
                catalog.value['allow'] = False
                if damage:
                    with self.assertRaises(ValueError):
                        p.verify_predecessor_provider(archive)
                else:
                    p.verify_predecessor_provider(archive)

if __name__ == '__main__':
    unittest.main()
