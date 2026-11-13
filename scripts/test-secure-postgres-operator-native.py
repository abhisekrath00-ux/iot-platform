#!/usr/bin/env python3
"""Actual immutable setupctl through local socket under closed target HBA.

Only a separate clone of the freshly seeded native application fixture. No owner
activation or complete host-provider claim. Simulates lost SQL acknowledgement.
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

operator=load('secure-postgres-operator'); hostmod=load('secure-postgres-hba-docker'); fence=load('secure-postgres-fence')
class Loss(BaseException):pass


def prove(container,fixture_root,project,image):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or fixture_root.parent!=pathlib.Path('/tmp')or not fixture_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):raise ValueError('Fresh native fixture required.')
    db='operator_native_fixture';created=False;peer=project+'-operator-outsider'
    catalog=fence.DockerCatalog(container,'ci',db);host=hostmod.DockerHost(catalog,project);path=host.bind();baseline=path.read_bytes()
    def call(args,binary=False):return subprocess.run(list(map(str,args)),capture_output=True,text=not binary,timeout=30,check=True).stdout
    def local(statement):return call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d',db,'-Atc',statement]).strip()
    def network():return subprocess.run(['docker','exec','-e','PGPASSWORD=synthetic-ci-only',peer,'psql','-X','-U','ci','-h','postgres','-d',db,'-Atc','SELECT 1'],capture_output=True,text=True,timeout=10)
    try:
        call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','postgres','-c',f'CREATE DATABASE {db} TEMPLATE application_fixture'])
        created=True
        call(['docker','run','-d','--name',peer,'--network',project+'_default','postgres:16-alpine','sleep','180'])
        if network().returncode:raise ValueError('Initial cloned DB connection failed.')
        with tempfile.TemporaryDirectory(prefix='hexthings-local-operator-ci-')as td:
            root=pathlib.Path(td)
            with fence.state.Journal(root)as journal:
                fence.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
                def connections(expected):
                    if local('SELECT 7')!='7':raise ValueError('Local operator maintenance channel unavailable.')
                    blocked=network()
                    if blocked.returncode==0 or 'pg_hba.conf rejects connection'not in blocked.stderr:
                        raise ValueError('Outside network writer not refused under HBA.')
                def reload(expected):host.reload_and_verify(expected,connections)
                barrier=hostmod.hba.Barrier(journal,path,db,'ci',host.check,reload);barrier.enter()
                # A named clone has no app writers; verify current target clients
                # directly as an extra check, not as reconnect prevention.
                if catalog.inspect()['sessions']:raise ValueError('Unexpected cloned DB client.')
                guard=barrier.check
                # Interrupt two actual pre-SQL staging boundaries independently.
                # Fresh scoped cleanup must accept only known private artifacts,
                # archive evidence and leave the HBA/admission block in place.
                for boundary in ['mkdir','copy']:
                    interrupted=operator.LocalOperator(journal,catalog,image,guard)
                    ordinary=interrupted.command
                    def stage_loss(args,**kwargs):
                        result=ordinary(args,**kwargs)
                        if ((boundary=='mkdir'and 'mkdir'in args)or
                                (boundary=='copy'and args[:2]==['cp','-'])):
                            raise Loss('Lost actual tool staging acknowledgement')
                        return result
                    interrupted.command=stage_loss
                    try:interrupted.prepare()
                    except Loss:pass
                    else:raise ValueError('Expected actual staging interruption.')
                    if interrupted.record()['stage']!='copying':raise ValueError('Partial staging was not recorded.')
                    recovered=operator.LocalOperator(journal,catalog,image,guard)
                    recovered.recover_staging()
                    if recovered.path.exists():raise ValueError('Checked partial-tool reset did not archive evidence.')
                    barrier.check()
                    if local("SELECT count(*) FROM schema_migrations")!='89':raise ValueError('Pre-SQL staging recovery changed schema.')
                if len(list(journal.directory.glob('*-discarded.json')))!=2:raise ValueError('Partial staging recovery evidence missing.')
                tool=operator.LocalOperator(journal,catalog,image,guard);tool.prepare()
                if tool.connection().get('status')!='connected':raise ValueError('Exact staged local candidate probe did not connect.')
                identity=fence.DatabaseFence.identity(fence.checked_database(catalog.inspect()))
                catalog.allow(False,identity)
                if catalog.drain(identity)!={'terminated':[]}:raise ValueError('Unexpected cloned database sessions.')
                # barrier.check uses a positive local callback and cannot be
                # recursively reused while datallowconn is deliberately closed.
                # Preserve exact held HBA and independent catalog instead.
                def closed_guard():
                    host.check();live=fence.checked_database(catalog.inspect(),identity)
                    barrier.checked();record=barrier.read()
                    if (live['allow']or live['sessions']or record['stage']!='held'
                            or path.read_bytes()!=bytes.fromhex(record['current'])):
                        raise ValueError('Exact closed local probe fixture changed.')
                tool.guard=closed_guard
                if tool.connection()!={'version':1,'status':'database_closed'}:raise ValueError('Staged static local probe did not prove SQLSTATE55000.')
                catalog.allow(True,identity);tool.guard=guard
                if tool.connection().get('status')!='connected':raise ValueError('Exact local candidate connection not restored.')
                print('PASS actual candidate static local-socket connected -> exact closed zero-session SQLSTATE55000 -> reopened connected, no psql text/auth/HBA inference or schema change.',flush=True)
                before=tool({},'inspect-reviewed')
                if before['status']!='exact_reviewed_baseline_89':raise ValueError('Local setupctl did not inspect baseline89.')
                raw=call(['docker','exec',container,'pg_dump','-U','ci','-Fc',db],binary=True)
                backup=root/'verified.dump';backup.write_bytes(raw);backup.chmod(0o600)
                subprocess.run(['docker','exec','-i',container,'pg_restore','-l'],input=raw,capture_output=True,check=True)
                value={'schema_attempted':operator.application.ADDED,'backup':{'path':str(backup),'sha256':fence.state.digest(backup),'verified_archive':True}}
                # Add a committed retained row after backup, then lose the
                # operator acknowledgement only after actual setupctl returned.
                local('CREATE TABLE operator_retained(n integer);INSERT INTO operator_retained VALUES(93)')
                original=tool.command
                def lose(args,**kwargs):
                    result=original(args,**kwargs)
                    if args[-1]=='apply-reviewed':raise Loss('Lost actual local setupctl SQL acknowledgement')
                    return result
                tool.command=lose
                try:tool(value,'apply-reviewed')
                except Loss:pass
                else:raise ValueError('Expected actual local SQL acknowledgement loss.')
                try:fence.state.selection(root)
                except ValueError:pass
                else:raise ValueError('Interrupted local operator allowed ordinary actions.')
            with fence.state.Journal(root)as journal:
                # Same immutable staged bytes, new invocation, fresh HBA proof.
                freshbarrier=hostmod.hba.Barrier(journal,path,db,'ci',host.check,reload)
                freshbarrier.enter(recovery=True)
                fresh=operator.LocalOperator(journal,catalog,image,freshbarrier.check)
                after=fresh({},'inspect-reviewed')
                if after['status']!='exact_reviewed_additive_93':raise ValueError('Fresh local setupctl did not see committed reviewed schema.')
                if local('SELECT n FROM operator_retained')!='93':raise ValueError('Post-backup retained row lost.')
                if local("SELECT serial FROM gateways WHERE id='retain-gw'")!='KEEP-SERIAL':raise ValueError('Cloned retained gateway identity lost.')
                fresh.cleanup()
                def restored(expected):
                    if expected!=baseline:reload(expected);return
                    def original_connections(_):
                        if network().returncode:raise ValueError('Original target network authentication not restored.')
                    host.reload_and_verify(expected,original_connections)
                freshbarrier.reload=restored;freshbarrier.release()
                if path.read_bytes()!=baseline:raise ValueError('Original HBA not restored after local tool proof.')
        print('PASS actual local candidate setupctl channel with both actual mkdir/copy lost-ack staging recoveries: never-started immutable image binary/catalog staged with hashes; target HBA denies outside network; inspect89/apply93 through real local PostgreSQL socket; lost SQL ack fresh inspection sees93 without replay; post-backup row and retained gateway preserved; checked tool cleanup and original HBA restored. Scoped component/callback proof, not complete provider or owner compatibility.',flush=True)
    finally:
        # Disposal only, never product recovery behavior.
        if path.read_bytes()!=baseline:
            path.write_bytes(baseline);catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        subprocess.run(['docker','rm','-f',peer],capture_output=True,check=False)
        if created:call(['docker','exec',container,'psql','-X','-U','ci','-d','postgres','-c',f'DROP DATABASE {db} WITH (FORCE)'])
