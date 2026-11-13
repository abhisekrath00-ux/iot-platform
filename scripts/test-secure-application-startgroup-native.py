#!/usr/bin/env python3
"""Actual grouped prior-image API/ingest/MCP startup and direct normalization.

Uses a separate disposable baseline89 clone and real adapter methods. The
settled journal is a fixture, not a production Coordinator/provider claim.
"""
import copy
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

groups=load('secure-application-startgroup');hostmod=load('secure-postgres-hba-docker');fence=load('secure-postgres-fence')


def prove(container,fixture_root,project,image,basepath):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or fixture_root.parent!=pathlib.Path('/tmp')or not fixture_root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')):raise ValueError('Fresh native fixture only.')
    db='startgroup_native_fixture';network=project+'_default';outside=project+'-group-outside';created=False;adapter=None
    catalog=fence.DockerCatalog(container,'ci',db);host=hostmod.DockerHost(catalog,project);hba=host.bind();baseline=hba.read_bytes()
    def call(args,check=True):return subprocess.run(list(map(str,args)),capture_output=True,text=True,timeout=40,check=check)
    def local(sql):return call(['docker','exec',container,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d',db,'-Atc',sql]).stdout.strip()
    def outside_connection():return call(['docker','exec','-e','PGPASSWORD=synthetic-ci-only',outside,'psql','-X','-h','postgres','-U','ci','-d',db,'-Atc','SELECT 7'],False)
    try:
        call(['docker','exec',container,'psql','-X','-U','ci','-d','postgres','-c',f'CREATE DATABASE {db} TEMPLATE application_fixture']);created=True
        call(['docker','run','-d','--name',outside,'--network',network,'postgres:16-alpine','sleep','240'])
        with tempfile.TemporaryDirectory(prefix='startgroup-',dir=fixture_root)as td:
            root=pathlib.Path(td)
            base=json.loads(call(['docker','compose','-p',project,'-f',basepath,'config','--format','json']).stdout)
            base.pop('name',None)
            for name,service in base['services'].items():
                service['image']=image if name in {'api','ingest','mcp'}else json.loads(call(['docker','image','inspect',service['image']]).stdout)[0]['Id']
                if name in {'api','ingest','mcp'}:service['environment']['DATABASE_URL']=f'postgres://ci:synthetic-ci-only@postgres:5432/{db}?sslmode=disable'
            adapter=object.__new__(groups.gates.adapter.DockerApplication)
            adapter.root=root;adapter.project=project;adapter.old=base;adapter.new=copy.deepcopy(base)
            adapter.writers=['api','ingest','mcp'];adapter.runtime_group=None
            emitted=set()
            def observed(args,**kwargs):
                result=subprocess.run(args,**kwargs)
                if args[:2]==['docker','inspect']and result.returncode==0:
                    for item in json.loads(result.stdout):
                        config=item.get('Config',{});token=config.get('Labels',{}).get(groups.gates.LABEL)
                        if token and item['Id']not in emitted:
                            emitted.add(item['Id']);cmd=config.get('Cmd',[])
                            print('SYNTHETIC_GATE_CMD='+json.dumps({'service':config['Labels'].get('com.docker.compose.service'),
                                'actual':cmd,'literal_script_equal':len(cmd)>2 and cmd[2]==groups.gates.SCRIPT}),flush=True)
                return result
            adapter.execute=observed
            value={key:str(root/(key+'.json'))for key in ['previous','candidate']}
            for path in value.values():groups.gates.state.atomic(path,base)
            # Direct old containers exist but have never run against this clone.
            adapter.compose(value['previous'],'up','--no-start','--no-deps','--no-build','--pull','never',*adapter.writers)
            with groups.gates.state.Journal(root)as journal:
                groups.gates.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
                def closed_connections(_):
                    if local('SELECT 7')!='7':raise ValueError('Local maintenance channel unavailable.')
                    denied=outside_connection()
                    if denied.returncode==0 or 'pg_hba.conf rejects connection'not in denied.stderr:raise ValueError('Outside IP not refused.')
                def reload(expected):host.reload_and_verify(expected,closed_connections)
                barrier=hostmod.hba.Barrier(journal,hba,db,'ci',host.check,reload);barrier.enter()
                def guard():
                    live,_=barrier.checked();record=barrier.read()
                    if record['stage']=='held'and live==bytes.fromhex(record['current']):return
                    if record['stage']=='released'and live==baseline:return
                    raise ValueError('Exact held or final-restored HBA required for grouped fixture.')
                def peers(values):
                    result=[]
                    for name,item in values.items():
                        endpoint=item['NetworkSettings']['Networks'][network]
                        result.append({'container':item['Id'],'network':network,'address':endpoint['IPAddress'],'user':'ci','ssl':False})
                    host.verify_peers(result);return result
                def prove_peer_connections(values):
                    closed_connections(None);peers(values)
                    for item in values.values():
                        answer=call(['docker','run','--rm','--network','container:'+item['Id'],'-e','PGPASSWORD=synthetic-ci-only','postgres:16-alpine',
                                     'psql','-X','-h','postgres','-U','ci','-d',db,'-Atc','SELECT 7'],False)
                        if answer.returncode or answer.stdout.strip()!='7':raise ValueError('Exact grouped endpoint not admitted.')
                def admit(values):
                    for name,item in values.items():
                        if call(['docker','exec',item['Id'],'cat','/proc/1/comm']).stdout.strip()not in {'sh','ash'}:
                            raise ValueError('A grouped application binary ran before peer admission.')
                    bound=peers(values)
                    barrier.handoff([{key:peer[key]for key in ['address','user','ssl']}for peer in bound])
                    prove_peer_connections(values)
                group=groups.Group(journal,guard,admit,prove_peer_connections);adapter.runtime_group=group
                adapter.start_candidate(value)
                found=adapter.checked_containers(value['candidate'],False)
                for name in adapter.writers:
                    if found[name]['Config']['Labels'].get(groups.gates.LABEL)is None:raise ValueError('Grouped verification did not inspect actual gates.')
                def healthy():
                    until=time.monotonic()+20
                    while True:
                        found=adapter.checked_containers(value['candidate'],False)
                        if any(found[name]['State']['Status']!='running'for name in adapter.writers):raise ValueError('Grouped writer exited.')
                        answer=call(['docker','exec',found['api']['Id'],'wget','-qO-','http://127.0.0.1:8000/healthz'],False)
                        if answer.returncode==0:return
                        if time.monotonic()>=until:raise ValueError('Grouped actual API did not become healthy.')
                        time.sleep(.15)
                healthy()
                for name in adapter.writers:
                    if call(['docker','exec',found[name]['Id'],'cat','/proc/1/comm']).stdout.strip()!=name:
                        raise ValueError('Grouped gate did not exec actual application PID1.')
                if local('SELECT count(*) FROM schema_migrations')!='89':raise ValueError('Grouped startup changed schema history.')
                # This settled journal is an explicit fixture, not a Coordinator
                # commit claim. It gates the normalization method's contract.
                groups.gates.state.atomic(journal.directory/'journal.json',{
                    'version':1,'kind':'application_bootstrap','project':project,'phase':'committed',**value,
                    'previous_sha256':groups.gates.state.digest(value['previous']),'candidate_sha256':groups.gates.state.digest(value['candidate']),
                    'files':{path:groups.gates.state.digest(path)for path in value.values()}})
                def original_reload(expected):
                    if expected!=baseline:reload(expected);return
                    def permitted(_):
                        if outside_connection().returncode:raise ValueError('Original HBA authentication not restored.')
                    host.reload_and_verify(expected,permitted)
                barrier.reload=original_reload;barrier.release()
                def original():
                    host.check()
                    if hba.read_bytes()!=baseline or outside_connection().returncode:raise ValueError('Exact original HBA not restored before normalization.')
                def accepted(value,slot):
                    healthy()
                    if local("SELECT serial FROM gateways WHERE id='retain-gw'")!='KEEP-SERIAL':raise ValueError('Final normalization lost retained gateway.')
                group.normalize(adapter,value,'candidate','committed',original,accepted)
                # Final runtime can be inspected with no gate manager at all.
                adapter.runtime_group=None;found=adapter.checked_containers(value['candidate'],True)
                for name in adapter.writers:
                    if found[name]['Config']['Cmd']!=['/bin/'+name]or found[name].get('Mounts'):
                        raise ValueError('Final direct runtime retains transient gate command/mount.')
                if groups.gates.state.read_private(journal.directory/'startgroup-normalize.json')['stage']!='accepted':raise ValueError('Normalization acceptance not durable.')
                adapter.compose(value['candidate'],'rm','-s','-f',*adapter.writers)
        print('PASS actual grouped startup component through DockerApplication callback: API/ingest/MCP created IDs durable before start; all three real dynamic endpoints gated before DB; exact grouped SCRAM handoff with outsider denial and fresh per-release proofs; actual binaries PID1/health/baseline89; fixture-settled original-HBA restoration then direct command/mount normalization and acceptance; final strict inspection without gate manager. Not full Coordinator/host-provider release or compatibility/owner acceptance.',flush=True)
    finally:
        # Fixture hygiene only; never a production exception recovery recipe.
        if adapter is not None:
            adapter.root=fixture_root
            for item in adapter.containers(basepath):
                if item.get('Config',{}).get('Labels',{}).get('com.docker.compose.service')in {'api','ingest','mcp'}:
                    call(['docker','rm','-f',item['Id']],False)
        call(['docker','rm','-f',outside],False)
        if hba.read_bytes()!=baseline:hba.write_bytes(baseline);catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        if created:call(['docker','exec',container,'psql','-X','-U','ci','-d','postgres','-c',f'DROP DATABASE {db} WITH (FORCE)'])
