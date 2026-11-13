#!/usr/bin/env python3
"""Actual prior API dynamic endpoint held before its first DB connection.

Fresh disposable clone only. Not owner runtime/complete provider acceptance.
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import time

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

gates=load('secure-application-startgate');hostmod=load('secure-postgres-hba-docker');fence=load('secure-postgres-fence')
class Loss(BaseException):pass


def prove(container,fixture_root,project,image):
    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0
            or fixture_root.parent!=pathlib.Path('/tmp') or not fixture_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):
        raise ValueError('Fresh native fixture only.')
    db='startgate_native_fixture';network=project+'_default';api=project+'-gated-api';outside=project+'-gate-outside'
    catalog=fence.DockerCatalog(container,'ci',db);host=hostmod.DockerHost(catalog,project);path=host.bind();baseline=path.read_bytes();created=False
    def call(args,check=True):return subprocess.run(list(map(str,args)),capture_output=True,text=True,timeout=25,check=check)
    def local(sql):return call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d',db,'-Atc',sql]).stdout.strip()
    def client(net,user='ci'):
        return call(['docker','run','--rm','--network',net,'-e','PGPASSWORD=synthetic-ci-only','postgres:16-alpine',
                     'psql','-X','-v','ON_ERROR_STOP=1','-h','postgres','-U',user,'-d',db,'-Atc','SELECT 7'],False)
    def denied():
        answer=call(['docker','exec','-e','PGPASSWORD=synthetic-ci-only',outside,'psql','-X','-h','postgres','-U','ci','-d',db,'-Atc','SELECT 7'],False)
        if answer.returncode==0 or 'pg_hba.conf rejects connection' not in answer.stderr:
            raise ValueError('Unlisted outside IP was not refused during application handoff.')
    try:
        call(['docker','exec',container,'psql','-X','-U','ci','-d','postgres','-c',f'CREATE DATABASE {db} TEMPLATE application_fixture']);created=True
        call(['docker','run','-d','--name',outside,'--network',network,'postgres:16-alpine','sleep','180'])
        with tempfile.TemporaryDirectory(prefix='hexthings-startgate-ci-')as td:
            root=pathlib.Path(td)
            with gates.state.Journal(root)as journal:
                gates.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
                def connections(_):
                    if local('SELECT 7')!='7':raise ValueError('Local maintenance channel unavailable.')
                    denied()
                def reload(expected):host.reload_and_verify(expected,connections)
                barrier=hostmod.hba.Barrier(journal,path,db,'ci',host.check,reload);barrier.enter()
                service={'image':image,'command':['/bin/api'],'environment':{
                    'DATABASE_URL':f'postgres://ci:synthetic-ci-only@postgres:5432/{db}?sslmode=disable',
                    'JWT_SIGNING_SECRET':'synthetic-application-only','MQTT_TLS':'false','MQTT_HOST':'mosquitto','MQTT_PORT':'1883'}}
                gate=gates.StartGate(journal,project,'api',service,barrier.check);definition=gate.prepare()
                args=['docker','run','-d','--name',api,'--network',network,
                      '--label','com.docker.compose.project='+project,'--label','com.docker.compose.service=api',
                      '--label',gates.LABEL+'='+definition['labels'][gates.LABEL],
                      '--mount','type=bind,source='+gate.record()['directory']+',target='+gates.TARGET+',readonly']
                for key,value in service['environment'].items():args+=['-e',key+'='+value]
                args += [image,*definition['command']]
                call(args)
                def inspect():
                    actual=json.loads(call(['docker','inspect',api]).stdout)[0]
                    metadata=json.loads(call(['docker','image','inspect',image]).stdout)[0]
                    return actual,metadata
                actual,metadata=inspect();gate.bind(actual,metadata)
                address=actual['NetworkSettings']['Networks'][network]['IPAddress']
                peer={'container':actual['Id'],'network':network,'address':address,'user':'ci','ssl':False}
                host.verify_peers([peer])
                if local("SELECT count(*) FROM pg_stat_activity WHERE datname='"+db+"' AND client_addr='"+address+"'")!='0':
                    raise ValueError('Application connected before exact-peer admission.')
                comm=call(['docker','exec',api,'cat','/proc/1/comm']).stdout.strip()
                if comm not in {'sh','ash'}:raise ValueError('Prior API was not held before its first connection.')
                refused=client('container:'+actual['Id'])
                if refused.returncode==0 or 'pg_hba.conf rejects connection'not in refused.stderr:
                    raise ValueError('Actual gated application address was not initially refused.')
                barrier.handoff([{key:peer[key]for key in ['address','user','ssl']}])
                def admit(item):
                    if item['Id']!=peer['container']:raise ValueError('Bound application changed before admission.')
                    host.verify_peers([peer]);barrier.check();denied()
                    answer=client('container:'+item['Id'])
                    if answer.returncode or answer.stdout.strip()!='7':raise ValueError('Exact actual application endpoint not admitted.')
                # Lose the durable marker acknowledgement after it was fsynced,
                # then use a fresh gate instance to re-prove the same peer.
                ordinary=gates.state.atomic
                def lost(where,value):
                    if where==gate.path and value.get('stage')=='released':raise Loss('Lost actual gate-marker acknowledgement')
                    ordinary(where,value)
                gates.state.atomic=lost
                try:
                    try:gate.release(inspect,admit)
                    except Loss:pass
                    else:raise ValueError('Expected marker acknowledgement interruption.')
                finally:gates.state.atomic=ordinary
                if gate.record()['stage']!='releasing':raise ValueError('Interrupted release was not retained.')
            with gates.state.Journal(root)as journal:
                freshbarrier=hostmod.hba.Barrier(journal,path,db,'ci',host.check,reload)
                # Retain exact current admitted bytes rather than closing under
                # a possibly already-running app. Full provider recovery owns
                # stopping/draining that app before any further schema change.
                freshbarrier.check();barrier=freshbarrier
                fresh=gates.StartGate(journal,project,'api',service,freshbarrier.check)
                fresh.release(inspect,admit)
                until=time.monotonic()+20
                while True:
                    current,_=inspect()
                    if current['State']['Status']!='running':raise ValueError('Prior API exited during gated startup.')
                    result=call(['docker','exec',api,'wget','-qO-','http://127.0.0.1:8000/healthz'],False)
                    if result.returncode==0:break
                    if time.monotonic()>=until:raise ValueError('Prior API never became healthy after exact admission.')
                    time.sleep(.15)
                if call(['docker','exec',api,'cat','/proc/1/comm']).stdout.strip()!='api':raise ValueError('Gate did not exec the actual prior application as PID1.')
                if local('SELECT count(*) FROM schema_migrations')!='89':raise ValueError('Gated prior API changed schema history.')
                if local("SELECT serial FROM gateways WHERE id='retain-gw'")!='KEEP-SERIAL':raise ValueError('Gated startup lost retained identity.')
                denied();call(['docker','rm','-f',api]);freshbarrier.handoff([])
                def originals(expected):
                    if expected!=baseline:reload(expected);return
                    def permitted(_):
                        if client(network).returncode:raise ValueError('Original network authentication not restored.')
                    host.reload_and_verify(expected,permitted)
                freshbarrier.reload=originals;freshbarrier.release()
                if path.read_bytes()!=baseline:raise ValueError('Original HBA not restored.')
        print('PASS actual prior API process gate: dynamic real container/IP assigned while shell held before first DB attempt; same endpoint initially HBA-refused; exact SCRAM handoff with outsider IP denied; real marker lost-ack fresh peer proof; actual old API exec PID1 and health after admission; baseline89/retained gateway unchanged; original HBA restored. Startup component only, not full adapter/provider/menu or owner compatibility.',flush=True)
    finally:
        # Fixture disposal only, never product exception cleanup.
        for name in [api,outside]:call(['docker','rm','-f',name],False)
        if path.read_bytes()!=baseline:path.write_bytes(baseline);catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        if created:call(['docker','exec',container,'psql','-X','-U','ci','-d','postgres','-c',f'DROP DATABASE {db} WITH (FORCE)'])
