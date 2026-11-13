#!/usr/bin/env python3
"""Native disposable proof of local maintenance plus exact SCRAM peer handoff.

Uses throwaway DB/roles/peers in the existing guarded application fixture only.
No owner activation and no claim that Docker application operators use this yet.
"""
import dataclasses
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

hostmod=load('secure-postgres-hba-docker'); fence=load('secure-postgres-fence'); execution=load('secure-execution')

class Loss(BaseException): pass


def prove(container,fixture_root,project):
    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0
            or fixture_root.parent!=pathlib.Path('/tmp') or not fixture_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):
        raise ValueError('Only fresh native disposable application fixture permitted.')
    db='hba_native_fixture'; other='hba_native_other'; peers=[]; created_db=False;created_role=False
    catalog=fence.DockerCatalog(container,'ci',db); host=hostmod.DockerHost(catalog,project); path=host.bind()
    baseline=path.read_bytes(); metadata=path.stat(); network=project+'_default'
    def call(args,check=True):return subprocess.run(list(map(str,args)),capture_output=True,text=True,timeout=20,check=check)
    def local(statement):
        return call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d',db,'-Atc',statement]).stdout.strip()
    def network_connection(peer,user='ci'):
        return call(['docker','exec','-e','PGPASSWORD=synthetic-ci-only',peer,'psql','-X','-v','ON_ERROR_STOP=1',
                     '-h','postgres','-U',user,'-d',db,'-Atc','SELECT 7'],False)
    try:
        catalog.sql('CREATE ROLE '+fence.identifier(other)+" LOGIN PASSWORD 'synthetic-ci-only'; SELECT json_build_object('created',true)")
        created_role=True
        call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','postgres','-c','CREATE DATABASE '+fence.identifier(db)])
        created_db=True
        local('CREATE TABLE retained(n integer); INSERT INTO retained VALUES(89)')
        for suffix in ['accepted','outside']:
            peer=project+'-hba-'+suffix;peers.append(peer)
            call(['docker','run','-d','--name',peer,'--network',network,'--label','com.docker.compose.project='+project,
                  '--label','com.docker.compose.service=api','postgres:16-alpine','sleep','180'])
        accepted=json.loads(call(['docker','inspect',peers[0]]).stdout)[0]
        address=accepted['NetworkSettings']['Networks'][network]['IPAddress']
        reviewed_peer={'container':accepted['Id'],'network':network,'address':address,'user':'ci','ssl':False}
        hba_peer={key:reviewed_peer[key]for key in ['address','user','ssl']}
        for peer in peers:
            if network_connection(peer).returncode!=0:raise ValueError('Synthetic initial network connection failed.')
        with tempfile.TemporaryDirectory(prefix='hexthings-hba-docker-ci-')as td:
            root=pathlib.Path(td)
            with fence.state.Journal(root)as journal:
                target=execution.Target(str(root),project,'application_bootstrap','activate',str(root/'previous'),str(root/'candidate'),'a'*64)
                fence.state.atomic(journal.directory/'maintenance.json',fence.DatabaseFence.binding(target))
                def inventory():
                    host.check()
                    # Do not allow an unknown address/container into a handoff.
                    host.verify_peers([reviewed_peer])
                def prove_connections(expected):
                    if local('SELECT 7')!='7':raise ValueError('Controlled local maintenance channel failed.')
                    for peer,user,wanted in [(peers[1],'ci',False),(peers[0],other,False),
                                             (peers[0],'ci',b'hostnossl '+db.encode()+b' ci ' in expected)]:
                        result=network_connection(peer,user)
                        if wanted:
                            if result.returncode or result.stdout.strip()!='7':raise ValueError('Reviewed SCRAM handoff peer refused.')
                        elif result.returncode==0 or 'pg_hba.conf rejects connection' not in result.stderr:
                            raise ValueError('Unlisted network identity was not refused by HBA.')
                def reload(expected):host.reload_and_verify(expected,prove_connections)
                barrier=hostmod.hba.Barrier(journal,path,db,'ci',inventory,reload)
                barrier.enter();barrier.check()
                # The closed HBA gate remains active while exclusive database
                # admission drains all pre-existing sessions and then reopens.
                exclusive=fence.DatabaseFence(journal,catalog,lambda _:None)
                ticket=exclusive.enter(target);exclusive.release(ticket,target)
                barrier.check()
                local('INSERT INTO retained VALUES(93)')
                barrier.handoff([hba_peer]);barrier.check()
                original_reload=barrier.reload
                def lost(expected):original_reload(expected);raise Loss('lost actual HBA reload acknowledgement')
                barrier.reload=lost
                try:barrier.handoff([hba_peer])
                except Loss:pass
                else:raise ValueError('Expected native reload interruption.')
                if fence.state.read_private(barrier.record_path)['stage']!='staged':raise ValueError('Native HBA interruption not durably recorded.')
            with fence.state.Journal(root)as journal:
                fresh=hostmod.hba.Barrier(journal,path,db,'ci',inventory,reload)
                fresh.enter(recovery=True);fresh.check()
                if network_connection(peers[0]).returncode==0:raise ValueError('Fresh HBA recovery left application access open.')
                if local('SELECT string_agg(n::text,\',\' ORDER BY n) FROM retained')!='89,93':raise ValueError('HBA lifecycle changed retained rows.')
                def restore_proof(expected):
                    if expected==baseline:
                        def originals(_):
                            for peer in peers:
                                if network_connection(peer).returncode:raise ValueError('Original network authentication not restored.')
                        host.reload_and_verify(expected,originals)
                    else:reload(expected)
                fresh.reload=restore_proof;fresh.release()
                if path.read_bytes()!=baseline or path.stat().st_mode!=metadata.st_mode or path.stat().st_uid!=metadata.st_uid:
                    raise ValueError('Exact original host-auth bytes/mode/owner not restored.')
        print('PASS actual Docker HBA controlled channel: immutable volume/server-byte binding; local admin authentication preserved; exclusive drain/reopen behind closed HBA; unlisted IP/user denied; exact SCRAM peer handoff; actual reload-loss fresh recovery recloses; retained89,93 and original HBA restored. Internal component proof, not application-adapter routing or complete host provider.',flush=True)
    finally:
        # This fallback is only disposable fixture hygiene, not product recovery.
        # Never erase independently changed production HBA as exception cleanup.
        if path.read_bytes()!=baseline:
            path.write_bytes(baseline);os.chown(path,metadata.st_uid,metadata.st_gid);os.chmod(path,metadata.st_mode&0o777)
            catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        for peer in peers:call(['docker','rm','-f',peer],False)
        if created_db:call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','postgres','-c','DROP DATABASE '+fence.identifier(db)+' WITH (FORCE)'])
        if created_role:catalog.sql('DROP ROLE '+fence.identifier(other)+"; SELECT json_build_object('removed',true)")
