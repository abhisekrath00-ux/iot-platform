#!/usr/bin/env python3
"""Host-auth barrier contracts and optional fresh local PostgreSQL process proof."""
import importlib.util
import os
import pathlib
import shutil
import socket
import subprocess
import tempfile
import time
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('hba', HERE / 'secure-postgres-hba.py')
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
BASE = b'local all all trust\nhost all all 127.0.0.1/32 scram-sha-256\nhost all all ::1/128 scram-sha-256\nhost all all all scram-sha-256\n'
PEER = {'address': '172.28.0.10', 'user': 'application', 'ssl': False}

class Loss(BaseException): pass

class Tests(unittest.TestCase):
    def fixture(self, root, journal):
        path = root / 'pg_hba.conf'; path.write_bytes(BASE); path.chmod(0o600)
        m.state.atomic(journal.directory / 'maintenance.json', {'root': str(root), 'receipt_sha256': 'a' * 64})
        calls = []
        def inventory(): calls.append('inventory')
        def reload(data): calls.append(data)
        return m.Barrier(journal, path, 'application_db', 'admin', inventory, reload), calls
    def test_exact_first_match_auth_preserved_maintenance_and_handoff(self):
        data = m.render(BASE, 'application_db', 'admin', [PEER])
        self.assertTrue(data.endswith(BASE))
        self.assertIn(b'local application_db admin trust\nlocal', m.render(BASE, 'application_db', 'admin', []))
        self.assertIn(b'hostnossl application_db application 172.28.0.10/32 scram-sha-256', data)
        self.assertLess(data.index(b'172.28.0.10'), data.index(b'0.0.0.0/0 reject'))
        self.assertIn(b'local application_db all reject', data)
        with self.assertRaises(ValueError): m.render(BASE, 'all', 'admin', [])
        for bad in [b'host all all all trust\n', b'host all all all reject\n', b'host all all all md5\n']:
            with self.assertRaises(ValueError): m.render(b'local all all peer\n' + bad, 'application_db', 'admin', [PEER])
        # An earlier exact rejection must win over a later broad SCRAM rule.
        earlier = b'local all all peer\nhost all all 172.28.0.0/16 reject\nhost all all all scram-sha-256\n'
        with self.assertRaises(ValueError): m.render(earlier, 'application_db', 'admin', [PEER])
    def test_unsupported_or_weakening_shapes_and_nonliteral_peers_refused(self):
        for row in [b'include_dir conf.d', b'host all all samenet scram-sha-256',
                    b'host all +group all scram-sha-256', b'local "database" all trust',
                    b'host all all all scram-sha-256 clientcert=verify-full', b'host all all all scram-sha-256\\']:
            with self.subTest(row=row), self.assertRaises(ValueError): m.render(BASE + row + b'\n', 'application_db', 'admin', [])
        for address in ['127.0.0.1', '::1', '0.0.0.0', 'fe80::1', '224.0.0.1', 'app.internal', '172.28.0.0/16']:
            with self.subTest(address=address), self.assertRaises(ValueError):
                m.render(BASE, 'application_db', 'admin', [dict(PEER, address=address)])
        with self.assertRaises(ValueError): m.render(BASE, 'application_db', 'admin', [PEER, PEER])
        with self.assertRaises(ValueError): m.render(BASE, 'application_db', 'admin', [dict(PEER, ssl='false')])
    def test_durable_file_lifecycle_preserves_bytes_mode_owner_and_admission(self):
        with tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
            root = pathlib.Path(td); barrier, calls = self.fixture(root, journal)
            before = barrier.path.stat(); barrier.enter(); barrier.check()
            self.assertEqual(barrier.path.read_bytes(), m.render(BASE, 'application_db', 'admin', []))
            barrier.handoff([PEER]); barrier.check(); barrier.release()
            self.assertEqual(barrier.path.read_bytes(), BASE)
            self.assertEqual((barrier.path.stat().st_uid, barrier.path.stat().st_gid, barrier.path.stat().st_mode),
                             (before.st_uid, before.st_gid, before.st_mode))
            self.assertEqual(m.state.read_private(barrier.record_path)['stage'], 'released')
            with self.assertRaises(ValueError): m.state.selection(root)
            with self.assertRaises(ValueError): barrier.enter(recovery=True)
            self.assertIn(m.render(BASE, 'application_db', 'admin', [PEER]), calls)
    def test_lost_reload_ack_recovery_restricts_to_local_before_handoff(self):
        for stage in ['enter', 'handoff', 'release']:
            with self.subTest(stage=stage), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                with m.state.Journal(root) as journal:
                    barrier, _ = self.fixture(root, journal)
                    if stage != 'enter': barrier.enter()
                    if stage == 'release': barrier.handoff([PEER])
                    def loss(data):
                        if stage != 'release' or data == BASE: raise Loss('Reload acknowledgement lost')
                    barrier.reload = loss
                    with self.assertRaises(Loss):
                        if stage == 'enter': barrier.enter()
                        elif stage == 'handoff': barrier.handoff([PEER])
                        else: barrier.release()
                    self.assertEqual(m.state.read_private(barrier.record_path)['stage'], 'staged')
                with m.state.Journal(root) as recovered:
                    fresh = m.Barrier(recovered, root / 'pg_hba.conf', 'application_db', 'admin', lambda: None, lambda _: None)
                    fresh.enter(recovery=True)
                    self.assertEqual(fresh.path.read_bytes(), m.render(BASE, 'application_db', 'admin', []))
                    fresh.release(); self.assertEqual(fresh.path.read_bytes(), BASE)
    def test_changed_inventory_scope_file_permissions_links_and_reload_fail_closed(self):
        for problem in ['inventory', 'scope', 'outside', 'mode', 'symlink', 'hardlink', 'reload']:
            with self.subTest(problem=problem), tempfile.TemporaryDirectory() as td, m.state.Journal(td) as journal:
                root = pathlib.Path(td); barrier, _ = self.fixture(root, journal); barrier.enter()
                if problem == 'inventory': barrier.inventory = lambda: True
                if problem == 'scope': m.state.atomic(journal.directory / 'maintenance.json', {'root': str(root), 'receipt_sha256': 'b' * 64})
                if problem == 'outside': barrier.path.write_bytes(b'# independently changed\n' + BASE)
                if problem == 'mode': barrier.path.chmod(0o666)
                if problem == 'symlink':
                    saved = root / 'saved'; barrier.path.rename(saved); barrier.path.symlink_to(saved)
                if problem == 'hardlink': os.link(barrier.path, root / 'linked')
                if problem == 'reload': barrier.reload = lambda _: True
                before = barrier.path.read_bytes()
                with self.assertRaises(ValueError): barrier.release()
                self.assertEqual(barrier.path.read_bytes(), before)
                self.assertNotEqual(m.state.read_private(barrier.record_path)['stage'], 'released')


@unittest.skipUnless(os.environ.get('HEXTHINGS_HBA_TEST_POSTGRES_BIN'), 'Explicit fresh-process PostgreSQL fixture required')
class LiveTests(unittest.TestCase):
    def test_fresh_process_controlled_local_network_handoff_and_recovery(self):
        import psycopg2
        bindir = pathlib.Path(os.environ['HEXTHINGS_HBA_TEST_POSTGRES_BIN'])
        with tempfile.TemporaryDirectory(prefix='hexthings-hba-pg-') as td:
            root = pathlib.Path(td); data = root / 'data'; sockdir = root / 'socket'; sockdir.mkdir()
            with socket.socket() as sock: sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
            def run(*args): return subprocess.run(list(map(str,args)), capture_output=True, text=True, check=True)
            run(bindir / 'initdb', '-D', data, '-A', 'trust', '--no-locale', '-U', 'admin')
            hba = data / 'pg_hba.conf'; hba.write_bytes(BASE); hba.chmod(0o600)
            run(bindir / 'pg_ctl', '-D', data, '-l', root / 'postgres.log', '-o', f'-k {sockdir} -p {port} -h 0.0.0.0', 'start')
            admin = None
            try:
                admin = psycopg2.connect(host=str(sockdir), port=port, dbname='postgres', user='admin'); admin.autocommit=True
                c = admin.cursor(); c.execute("CREATE DATABASE application_db;" )
                c.execute("CREATE ROLE application LOGIN PASSWORD 'synthetic-fixture-only'; CREATE ROLE unrelated LOGIN PASSWORD 'synthetic-fixture-only';")
                # Use this machine's non-loopback address to exercise the same
                # literal-address rule the Docker provider will use for peers.
                with socket.socket(socket.AF_INET,socket.SOCK_DGRAM) as sock:
                    sock.connect(('192.0.2.1',9)); address = sock.getsockname()[0]
                def tcp(user='application'):
                    return psycopg2.connect(host=address,port=port,dbname='application_db',user=user,password='synthetic-fixture-only',sslmode='disable',connect_timeout=2)
                with tcp() as connection: connection.cursor().execute('SELECT 1')
                with m.state.Journal(root) as journal:
                    m.state.atomic(journal.directory / 'maintenance.json', {'root':str(root),'receipt_sha256':'a'*64})
                    expected_peers = []
                    def verify(contents):
                        c.execute('SELECT pg_reload_conf()'); self.assertEqual(c.fetchone(), (True,))
                        until = time.monotonic()+5
                        while True:
                            try:
                                with tcp('unrelated'): pass
                            except psycopg2.OperationalError: break
                            if time.monotonic()>until: self.fail('Unlisted peer still admitted after reload')
                            time.sleep(.02)
                        c.execute('SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL'); self.assertEqual(c.fetchone(),(0,))
                        with psycopg2.connect(host=str(sockdir),port=port,dbname='application_db',user='admin') as local:
                            local.cursor().execute('SELECT 7')
                        if contents == BASE:
                            with tcp(): pass
                        elif b'hostnossl application_db application' in contents:
                            with tcp(): pass
                        else:
                            with self.assertRaises(psycopg2.OperationalError): tcp()
                    barrier=m.Barrier(journal,hba,'application_db','admin',lambda:None,verify)
                    barrier.enter(); barrier.check()
                    barrier.handoff([{'address':address,'user':'application','ssl':False}]); barrier.check()
                    # Lose acknowledgement after writing/reloading application
                    # handoff. Fresh recovery must return to local-only access.
                    def lost(contents): verify(contents); raise Loss('actual reload ack lost')
                    barrier.reload=lost
                    with self.assertRaises(Loss): barrier.handoff([{'address':address,'user':'application','ssl':False}])
                with m.state.Journal(root) as journal:
                    fresh=m.Barrier(journal,hba,'application_db','admin',lambda:None,verify)
                    fresh.enter(recovery=True); fresh.check()
                    # Restore uses a different verifier because original rules
                    # legitimately permit the unrelated role again.
                    def restored(contents):
                        if contents != BASE:
                            verify(contents); return
                        c.execute('SELECT pg_reload_conf()'); time.sleep(.1)
                        with tcp(): pass
                        with tcp('unrelated'): pass
                    fresh.reload=restored; fresh.release()
                    self.assertEqual(hba.read_bytes(),BASE)
                print('LIVE HBA barrier: local maintenance preserved; unlisted network user denied; exact SCRAM peer admitted only at handoff; actual reload-loss fresh recovery closes it again; original bytes restored')
            finally:
                if admin: admin.close()
                run(bindir / 'pg_ctl', '-D', data, '-m', 'immediate', 'stop')

if __name__ == '__main__': unittest.main()
