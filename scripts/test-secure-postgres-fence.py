#!/usr/bin/env python3
"""Database-admission contracts. Optional live test uses disposable databases only."""
import copy
import dataclasses
import importlib.util
import os
import pathlib
import subprocess
import tempfile
import unittest
import uuid

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value

m = load('secure-postgres-fence')
execution = load('secure-execution')


class Catalog:
    def __init__(self):
        self.value = {'cluster': '123', 'database': 'application', 'oid': '22', 'owner': '10',
                      'allow': True, 'template': False, 'admin': True, 'recovery': False,
                      'sessions': 3, 'prepared': 0, 'slots': 0, 'subscriptions': 0}
        self.calls = []
        self.crash = None
        self.keep_sessions = False
    def inspect(self):
        return copy.deepcopy(self.value)
    def allow(self, enabled, expected):
        if m.DatabaseFence.identity(self.value) != expected:
            raise ValueError('changed identity')
        self.calls.append(('allow', enabled))
        self.value['allow'] = enabled
        if self.crash == enabled:
            raise SystemExit('lost acknowledgement')
        return {'changed': True}
    def drain(self, expected):
        self.calls.append(('drain',))
        if not self.keep_sessions:
            self.value['sessions'] = 0
        return {'terminated': [True]}


class Tests(unittest.TestCase):
    def target(self, root, action='activate'):
        target = execution.Target(str(root), 'fixture', 'application_bootstrap', action,
                                  str(root / 'previous.json'), str(root / 'candidate.json'), 'a' * 64)
        m.state.atomic(root / '.secure-deployment' / 'maintenance.json', m.DatabaseFence.binding(target))
        return target
    def test_closes_drains_checks_then_restores_only_original_admission(self):
        with tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
            target = self.target(pathlib.Path(td)); catalog = Catalog()
            fence = m.DatabaseFence(journal, catalog, lambda _: None)
            ticket = fence.enter(target)
            self.assertEqual(catalog.calls, [('allow', False), ('drain',)])
            self.assertEqual(m.state.read_private(fence.path)['stage'], 'held')
            self.assertEqual(fence.path.stat().st_mode & 0o777, 0o600)
            fence.check(ticket, target)
            with self.assertRaises(ValueError): m.state.selection(td)
            fence.release(ticket, target)
            self.assertTrue(catalog.value['allow'])
            self.assertEqual(m.state.read_private(fence.path)['stage'], 'released')
            with self.assertRaises(ValueError): m.state.selection(td)
            with self.assertRaises(ValueError): fence.enter(dataclasses.replace(target, action='recover'))
    def test_admission_loss_recovers_without_replacing_baseline(self):
        for lost in [False, True]:
            with self.subTest(lost=lost), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td); catalog = Catalog()
                with m.state.Journal(root) as journal:
                    target = self.target(root); fence = m.DatabaseFence(journal, catalog, lambda _: None)
                    catalog.crash = lost
                    if lost:
                        ticket = fence.enter(target)
                        with self.assertRaises(SystemExit): fence.release(ticket, target)
                    else:
                        with self.assertRaises(SystemExit): fence.enter(target)
                    fence.retain(None, target)
                    with self.assertRaises(ValueError): m.state.selection(root)
                with m.state.Journal(root) as recovered:
                    catalog.crash = None
                    recovery = dataclasses.replace(target, action='recover')
                    fresh = m.DatabaseFence(recovered, catalog, lambda _: None)
                    ticket = fresh.enter(recovery)
                    self.assertFalse(catalog.value['allow'])
                    self.assertTrue(m.state.read_private(fresh.path)['original_allow'])
                    fresh.release(ticket, recovery)
                    self.assertTrue(catalog.value['allow'])
    def test_refuses_preclosed_database_and_unreviewed_scope(self):
        for problem in ['preclosed', 'boolean-review', 'missing-admission', 'unlocked']:
            with self.subTest(problem=problem), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                root = pathlib.Path(td); target = self.target(root); catalog = Catalog()
                if problem == 'preclosed': catalog.value['allow'] = False
                if problem == 'missing-admission': (journal.directory / 'maintenance.json').unlink()
                reviewer = (lambda _: True) if problem == 'boolean-review' else (lambda _: None)
                fence = m.DatabaseFence(journal, catalog, reviewer)
                if problem == 'unlocked': journal.__exit__()
                with self.assertRaises((ValueError, FileNotFoundError)): fence.enter(target)
                self.assertFalse(catalog.calls)
                self.assertFalse(fence.path.exists())
    def test_unsupported_database_writers_refused_before_admission_changes(self):
        for key, value in [('admin', False), ('recovery', True), ('template', True),
                           ('prepared', 1), ('slots', 1), ('subscriptions', 1), ('database', 'postgres')]:
            with self.subTest(key=key), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                target = self.target(pathlib.Path(td)); catalog = Catalog(); catalog.value[key] = value
                fence = m.DatabaseFence(journal, catalog, lambda _: None)
                with self.assertRaises(ValueError): fence.enter(target)
                self.assertFalse(catalog.calls)
    def test_existing_sessions_or_fence_drift_stop_effects_and_release(self):
        for changed in ['sessions', 'allow', 'cluster', 'oid', 'owner']:
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                target = self.target(pathlib.Path(td)); catalog = Catalog()
                fence = m.DatabaseFence(journal, catalog, lambda _: None); ticket = fence.enter(target)
                catalog.value[changed] = {'sessions': 1, 'allow': True, 'cluster': 'wrong',
                                          'oid': 'wrong', 'owner': 'wrong'}[changed]
                calls = len(catalog.calls)
                with self.assertRaises(ValueError): fence.check(ticket, target)
                with self.assertRaises(ValueError): fence.release(ticket, target)
                self.assertEqual(calls, len(catalog.calls))
                fence.retain(None, target)
                self.assertEqual(m.state.read_private(fence.path)['stage'], 'held')
    def test_drain_failure_keeps_durable_closing_intent(self):
        with tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
            target = self.target(pathlib.Path(td)); catalog = Catalog(); catalog.keep_sessions = True
            fence = m.DatabaseFence(journal, catalog, lambda _: None)
            with self.assertRaises(ValueError): fence.enter(target)
            self.assertFalse(catalog.value['allow'])
            self.assertEqual(m.state.read_private(fence.path)['stage'], 'closing')
    def test_changed_receipt_opaque_session_symlink_and_corruption_refused(self):
        for changed in ['receipt', 'ticket', 'symlink', 'corrupt']:
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                target = self.target(pathlib.Path(td)); catalog = Catalog()
                fence = m.DatabaseFence(journal, catalog, lambda _: None); ticket = fence.enter(target)
                if changed == 'receipt': target = dataclasses.replace(target, receipt_sha256='b' * 64)
                if changed == 'ticket': ticket = m.Ticket(ticket.binding, ticket.token)
                if changed == 'symlink':
                    saved = journal.directory / 'saved.json'; fence.path.rename(saved); fence.path.symlink_to(saved)
                if changed == 'corrupt': fence.path.write_text('{}')
                with self.assertRaises((ValueError, KeyError)): fence.check(ticket, target)
                self.assertFalse(catalog.value['allow'])
    def test_already_exited_backend_needs_fresh_closed_exact_zero_session_proof(self):
        identity={'cluster':'123','database':'application','oid':'22','owner':'10'}
        for damage in [None,'sessions','allow','oid','owner','cluster','database','admin','recovery','template','prepared','slots','subscriptions']:
            with self.subTest(damage=damage):
                backend=m.DockerCatalog('a'*64,'admin','application')
                live=Catalog().value;live.update(allow=False,sessions=0)
                if damage in ['sessions','prepared','slots','subscriptions']:live[damage]=1
                elif damage in ['oid','owner','cluster','database']:live[damage]='changed'
                elif damage=='admin':live[damage]=False
                elif damage:live[damage]=True
                seen=[]
                backend.mutate=lambda statement,expected: {'terminated':[True,False]}
                backend.inspect=lambda: seen.append('fresh')or live
                if damage:
                    with self.assertRaises(ValueError):backend.drain(identity)
                else:self.assertEqual(backend.drain(identity),{'terminated':[]})
                self.assertEqual(seen,['fresh'])
    def test_drain_malformed_response_never_becomes_empty_success(self):
        for response in [{},{'terminated':None},{'terminated':[0]},{'terminated':[1]}, {'terminated':[False],'extra':True}]:
            backend=m.DockerCatalog('a'*64,'admin','application')
            backend.mutate=lambda statement,expected:response
            backend.inspect=lambda: self.fail('Malformed response must refuse before further proof')
            with self.assertRaises(ValueError):backend.drain({})

    def test_sql_channel_uses_exact_container_catalog_and_guarded_mutation(self):
        calls = []
        def run(command, **kwargs):
            calls.append((command, kwargs))
            return subprocess.CompletedProcess(command, 0, '{"changed":true}', '')
        db = "a'\\\"$hexthings_fence$"
        backend = m.DockerCatalog('a' * 64, 'admin', db, run)
        identity = {'database': db, 'cluster': '1', 'oid': '2', 'owner': '3'}
        backend.allow(False, identity)
        command, opts = calls[0]
        self.assertEqual(command[:4], ['docker', 'exec', '-i', 'a' * 64])
        self.assertEqual(command[-2:], ['-d', 'postgres'])
        self.assertNotIn('shell', opts)
        self.assertIn('LOCK TABLE pg_catalog.pg_database', opts['input'])
        self.assertIn('d.oid::text=', opts['input'])
        self.assertIn('pg_control_system()', opts['input'])
        self.assertIn('ALLOW_CONNECTIONS false', opts['input'])
        self.assertIn('$hexthings_fencex$', opts['input'])
        for value in ['name', '', 'a' * 63]:
            with self.assertRaises(ValueError): m.DockerCatalog(value, 'admin', 'app')
        for database in ['postgres', 'template0', 'template1']:
            with self.assertRaises(ValueError): m.DockerCatalog('a' * 64, 'admin', database)


@unittest.skipUnless(os.environ.get('HEXTHINGS_FENCE_TEST_DSN'), 'Explicit disposable PostgreSQL fixture required')
class LiveTests(unittest.TestCase):
    def test_real_database_denial_drain_reconnect_lost_ack_and_preserved_rows(self):
        import psycopg2
        from psycopg2 import sql
        dsn = os.environ['HEXTHINGS_FENCE_TEST_DSN']
        db = 'hx_fence_' + uuid.uuid4().hex[:12]
        other = db + '_other'
        role = db + '_role'
        admin = psycopg2.connect(dsn); admin.autocommit = True
        curs = admin.cursor()
        writer = survivor = None
        def conn(database, user=None):
            values = admin.get_dsn_parameters(); values.update(dbname=database)
            if user: values['user'] = user
            return psycopg2.connect(**values)
        def local_run(command, **kwargs):
            # Execute the production SQL without Docker against this disposable
            # local fixture. Native Docker/container identity is NOT proven here.
            return subprocess.run(['psql', '-X', '-q', '-A', '-t', '-v', 'ON_ERROR_STOP=1',
                                   '-d', dsn], **kwargs)
        backend = m.DockerCatalog('a' * 64, admin.get_dsn_parameters()['user'], db, local_run)
        original_allow = backend.allow
        try:
            curs.execute(sql.SQL('CREATE ROLE {} LOGIN').format(sql.Identifier(role)))
            for database in [db, other]:
                curs.execute(sql.SQL('CREATE DATABASE {}').format(sql.Identifier(database)))
            writer = conn(db); writer.autocommit = True
            writer.cursor().execute('CREATE TABLE retained (n integer); INSERT INTO retained VALUES (89)')
            survivor = conn(other); survivor.autocommit = True
            for lost in [False, True]:
                with self.subTest(lost_ack_on_reopen=lost), tempfile.TemporaryDirectory() as td:
                    root = pathlib.Path(td)
                    with m.state.Journal(root) as journal:
                        target = Tests().target(root); fence = m.DatabaseFence(journal, backend, lambda _: None)
                        def lost_ack(enabled, expected):
                            result = original_allow(enabled, expected)
                            if enabled is lost: raise SystemExit('lost actual ALTER acknowledgement')
                            return result
                        backend.allow = lost_ack
                        if lost:
                            ticket = fence.enter(target)
                            with self.assertRaises(SystemExit): fence.release(ticket, target)
                        else:
                            with self.assertRaises(SystemExit): fence.enter(target)
                        self.assertEqual(m.state.read_private(fence.path)['stage'], 'releasing' if lost else 'closing')
                    backend.allow = original_allow
                    with m.state.Journal(root) as recovered:
                        recovery = dataclasses.replace(target, action='recover')
                        fresh = m.DatabaseFence(recovered, backend, lambda _: None)
                        ticket = fresh.enter(recovery)
                        self.assertFalse(backend.inspect()['allow'])
                        self.assertEqual(backend.inspect()['sessions'], 0)
                        for user in [None, role]:
                            with self.assertRaises(psycopg2.OperationalError): conn(db, user)
                        check_other = survivor.cursor(); check_other.execute('SELECT 7')
                        self.assertEqual(check_other.fetchone(), (7,))
                        fresh.release(ticket, recovery)
                    with conn(db) as reopened:
                        c = reopened.cursor(); c.execute('SELECT n FROM retained ORDER BY n')
                        self.assertEqual(c.fetchall(), [(89,)] if not lost else [(89,), (93,)])
                        c.execute('INSERT INTO retained VALUES (93)')
                    reopened.close()
            # The original connected writer was terminated, not merely denied
            # on a future reconnect. Another database's active session survived.
            with self.assertRaises(psycopg2.Error): writer.cursor().execute('SELECT 1')
            # Real stale-PID false response via identity-guarded production SQL;
            # explicit diagnostic input, not an unchanged-query race claim.
            gone=conn(db);pid=gone.get_backend_pid();gone.close()
            live=m.checked_database(backend.inspect());expected=m.DatabaseFence.identity(live)
            backend.allow(False,expected);mutate=backend.mutate;seen=[]
            def stale(statement,checked):
                value=mutate("SELECT json_build_object('terminated',json_build_array(pg_terminate_backend("+str(pid)+",5000)))",checked)
                self.assertEqual(value,{'terminated':[False]});seen.append(value);return value
            backend.mutate=stale
            try:self.assertEqual(backend.drain(expected),{'terminated':[]})
            finally:backend.mutate=mutate
            self.assertEqual(len(seen),1);self.assertFalse(backend.inspect()['allow']);self.assertEqual(backend.inspect()['sessions'],0)
            backend.allow(True,expected)
            print('LIVE database fence: superuser+ordinary reconnect denied; old writer terminated; unrelated DB retained; both lost-ack recoveries; rows preserved')
        finally:
            backend.allow = original_allow
            if writer: writer.close()
            if survivor: survivor.close()
            for database in [db, other]:
                curs.execute(sql.SQL('DROP DATABASE IF EXISTS {} WITH (FORCE)').format(sql.Identifier(database)))
            curs.execute(sql.SQL('DROP ROLE IF EXISTS {}').format(sql.Identifier(role)))
            admin.close()


if __name__ == '__main__':
    unittest.main()
