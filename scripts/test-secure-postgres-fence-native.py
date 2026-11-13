#!/usr/bin/env python3
"""Actual DockerCatalog proof inside an already fresh native application fixture.

Not an owner runner or a complete Coordinator provider. Called before app
writers start, with a separate throwaway database in the fixture container.
"""
import dataclasses
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import time

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module

m = load('secure-postgres-fence')
execution = load('secure-execution')

class ProcessLoss(BaseException):
    pass


def prove(container, fixture_root, project):
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_OS') != 'Linux'
            or os.geteuid() != 0 or fixture_root.parent != pathlib.Path('/tmp')
            or not fixture_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):
        raise ValueError('Only the fresh disposable native application fixture is supported.')
    db, plain = 'fence_native_fixture', 'fence_native_plain'
    backend = m.DockerCatalog(container, 'ci', db)
    def call(args):
        return subprocess.run(args, capture_output=True, text=True, timeout=20, check=True).stdout
    def sql(statement):
        return backend.sql(statement)
    original = backend.allow
    created = False
    created_role = False
    try:
        sql('CREATE ROLE ' + m.identifier(plain) + " LOGIN; SELECT json_build_object('created',true)")
        created_role = True
        call(['docker', 'exec', container, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'ci', '-d', 'postgres',
              '-c', 'CREATE DATABASE ' + m.identifier(db)])
        created = True
        call(['docker', 'exec', container, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'ci', '-d', db,
              '-c', 'CREATE TABLE retained (n integer); INSERT INTO retained VALUES (89)'])
        # A real previously connected writer and an unrelated active database
        # session, not a substituted count or mocked backend response.
        for database, name in [(db, 'fence-native-writer'), ('application_fixture', 'fence-native-unrelated')]:
            call(['docker', 'exec', '-d', '-e', 'PGAPPNAME=' + name, container, 'psql', '-X', '-U', 'ci',
                  '-d', database, '-c', 'SELECT pg_sleep(120)'])
        until = time.monotonic() + 10
        while sql("SELECT json_build_object('n',count(*)) FROM pg_stat_activity WHERE application_name IN ('fence-native-writer','fence-native-unrelated')")['n'] != 2:
            if time.monotonic() > until: raise ValueError('Native fixture sessions did not appear.')
            time.sleep(.1)
        for reopening in [False, True]:
            with tempfile.TemporaryDirectory(prefix='hexthings-db-fence-ci-') as td:
                root = pathlib.Path(td)
                with m.state.Journal(root) as journal:
                    target = execution.Target(str(root), project, 'application_bootstrap', 'activate',
                                              str(root / 'previous.json'), str(root / 'candidate.json'), 'a' * 64)
                    m.state.atomic(journal.directory / 'maintenance.json', m.DatabaseFence.binding(target))
                    def reviewed(requested):
                        if requested != target: raise ValueError('Synthetic native fence scope changed.')
                    fence = m.DatabaseFence(journal, backend, reviewed)
                    def lose_ack(enabled, expected):
                        result = original(enabled, expected)
                        if enabled is reopening: raise ProcessLoss('Synthetic lost real ALTER acknowledgement')
                        return result
                    backend.allow = lose_ack
                    try:
                        if reopening:
                            ticket = fence.enter(target); fence.release(ticket, target)
                        else:
                            fence.enter(target)
                    except ProcessLoss:
                        pass
                    else:
                        raise ValueError('Native lost acknowledgement did not occur.')
                    try: m.state.selection(root)
                    except ValueError: pass
                    else: raise ValueError('Native interrupted fence permitted ordinary selection.')
                backend.allow = original
                with m.state.Journal(root) as journal:
                    recovery = dataclasses.replace(target, action='recover')
                    def reviewed_recovery(requested):
                        if requested != recovery: raise ValueError('Synthetic native recovery scope changed.')
                    fresh = m.DatabaseFence(journal, backend, reviewed_recovery)
                    ticket = fresh.enter(recovery)
                    if backend.inspect()['allow'] or backend.inspect()['sessions']:
                        raise ValueError('Native PostgreSQL exclusive fence was not held.')
                    for user in ['ci', plain]:
                        denied = subprocess.run(['docker', 'exec', container, 'psql', '-X', '-U', user, '-d', db,
                                                 '-c', 'SELECT 1'], capture_output=True, text=True, timeout=10)
                        if denied.returncode == 0 or 'not currently accepting connections' not in denied.stderr:
                            raise ValueError('Native reconnect was not rejected by database admission.')
                    if sql("SELECT json_build_object('n',count(*)) FROM pg_stat_activity WHERE application_name='fence-native-writer'")['n']:
                        raise ValueError('Previously connected native writer survived drain.')
                    if sql("SELECT json_build_object('n',count(*)) FROM pg_stat_activity WHERE application_name='fence-native-unrelated'")['n'] != 1:
                        raise ValueError('Fence terminated another database session.')
                    fresh.release(ticket, recovery)
                rows = call(['docker', 'exec', container, 'psql', '-X', '-A', '-t', '-U', 'ci', '-d', db,
                             '-c', 'SELECT n FROM retained ORDER BY n']).strip().splitlines()
                if rows != (['89'] if not reopening else ['89', '93']):
                    raise ValueError('Native fence recovery changed retained data.')
                call(['docker', 'exec', container, 'psql', '-X', '-U', 'ci', '-d', db,
                      '-c', 'INSERT INTO retained VALUES (93)'])
        # Deterministic real already-exited PID branch. The stale PID is supplied
        # explicitly after termination, not claimed as an unchanged-query race.
        call(['docker','exec','-d','-e','PGAPPNAME=fence-native-race',container,'psql','-X','-U','ci','-d',db,'-c','SELECT pg_sleep(120)'])
        until=time.monotonic()+10
        while True:
            ids=sql("SELECT json_build_object('pids',coalesce(json_agg(pid),'[]'::json)) FROM pg_stat_activity WHERE application_name='fence-native-race'")['pids']
            if len(ids)==1:break
            if time.monotonic()>until:raise ValueError('Native race fixture session did not appear.')
            time.sleep(.1)
        identity=m.checked_database(backend.inspect())
        expected={key:identity[key]for key in ['cluster','database','oid','owner']}
        backend.allow(False,expected)
        sql("SELECT json_build_object('terminated',pg_terminate_backend("+str(ids[0])+",5000))")
        mutate=backend.mutate;seen=[]
        def stale(statement,checked):
            value=mutate("SELECT json_build_object('terminated',json_build_array(pg_terminate_backend("+str(ids[0])+",5000)))",checked)
            if value!={'terminated':[False]}:raise ValueError('Native already-exited backend did not return false.')
            seen.append(value);return value
        backend.mutate=stale
        try:
            if backend.drain(expected)!={'terminated':[]}or len(seen)!=1:raise ValueError('Native gone-backend fresh exclusivity proof failed.')
        finally:backend.mutate=mutate
        final=m.checked_database(backend.inspect(),expected)
        if final['allow']or final['sessions']:raise ValueError('Native gone-backend admission not closed/empty.')
        backend.allow(True,expected)
        print('PASS actual already-exited backend false response reconciled only by fresh exact closed zero-session catalog; explicit stale PID diagnostic, not unchanged-query reproduction.',flush=True)
        print('PASS actual DockerCatalog PostgreSQL admission: closed to superuser/ordinary reconnects, existing writer drained, unrelated DB session preserved, both real ALTER lost-ack recoveries and retained rows. Exclusive component only; no migration/application exception, full host provider or owner authority.', flush=True)
    finally:
        backend.allow = original
        sql("SELECT json_build_object('terminated',coalesce(json_agg(pg_terminate_backend(pid,5000)),'[]'::json)) FROM pg_stat_activity WHERE application_name IN ('fence-native-writer','fence-native-unrelated','fence-native-race')")
        if created:
            call(['docker', 'exec', container, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'ci', '-d', 'postgres',
                  '-c', 'DROP DATABASE ' + m.identifier(db) + ' WITH (FORCE)'])
        if created_role:
            sql('DROP ROLE ' + m.identifier(plain) + "; SELECT json_build_object('removed',true)")
