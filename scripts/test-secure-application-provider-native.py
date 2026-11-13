#!/usr/bin/env python3
"""Full real provider/Coordinator path in a separate disposable Docker project.

Owner authority is synthetic and fixture-bound. Runtime checks cover retained
lists, profile creation and HTTP telemetry on a dedicated synthetic tenant.
This is not full background compatibility.
"""
import copy
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import time
import faulthandler
import urllib.request
import urllib.error
import base64
import hmac
import hashlib

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

m=load('secure-application-provider');production=load('secure-application-production');fixture=load('test-secure-application-native');writesmod=load('test-secure-application-writes-fixture');acceptmod=load('secure-application-acceptance');connectionmod=load('secure-application-connections');hosttoolmod=load('secure-postgres-hosttool')
class Loss(BaseException):pass

def prove(parent_root,parent_project,newimage,testimage,basepath):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or parent_root.parent!=pathlib.Path('/tmp')or not parent_root.name.startswith('hexthings-application-ci-')
            or not parent_project.startswith('hexthings-app-ci-')):raise ValueError('Fresh native fixture only.')
    faulthandler.dump_traceback_later(120,repeat=True)
    project=parent_project+'-provider';outside=project+'-outside';network=project+'_default'
    with tempfile.TemporaryDirectory(prefix='hexthings-application-ci-provider-')as td:
        root=pathlib.Path(td);path=root/'docker-compose.yml';base=json.loads(basepath.read_text())
        base['services']['api']['ports'][0]['published']='18103';path.write_text(json.dumps(base));path.chmod(0o600)
        compose=['docker','compose','-p',project,'-f',str(path)]
        def call(args,check=True):return subprocess.run(list(map(str,args)),cwd=root,capture_output=True,text=True,timeout=180,check=check)
        def inspect(cid):return json.loads(call(['docker','inspect',cid]).stdout)[0]
        commands=[]
        started=time.monotonic();last_report=[started]
        def checkpoint(stage):
            print('PROVIDER_NATIVE_STAGE='+json.dumps({'stage':stage,'elapsed':round(time.monotonic()-started,2),'commands':len(commands)}),flush=True)
        def run(args,**kwargs):
            commands.append(list(args))
            if time.monotonic()-last_report[0]>20:
                checkpoint('command '+str(args[:3]));last_report[0]=time.monotonic()
            kwargs.setdefault('timeout',90)
            return subprocess.run(args,**kwargs)
        try:
            checkpoint('create separate disposable project')
            call(compose+['up','-d','--wait','postgres','mosquitto','web'])
            cid=call(compose+['ps','-q','postgres']).stdout.strip()
            helper=fixture.Effects(root,project,root/'old.json',root/'new.json',testimage)
            checkpoint('seed baseline89')
            helper.operator('seed89');helper.operator('seed-writes')
            call(compose+['up','-d','--no-deps','api','ingest','mcp']);helper.probe(18103)
            writes=writesmod.Writes()
            def request(method,path,body,role,tenant):
                headers={}
                if role is not None:
                    def enc(value):return base64.urlsafe_b64encode(json.dumps(value,separators=(',',':')).encode()).rstrip(b'=')
                    unsigned=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc({'sub':('write-viewer'if role=='viewer'else'write-user')if tenant=='write-tenant'else'retain-user','tenant_id':tenant,'role':role,'exp':int(time.time())+120})
                    signature=base64.urlsafe_b64encode(hmac.new(b'synthetic-application-only',unsigned,hashlib.sha256).digest()).rstrip(b'=')
                    headers['Authorization']='Bearer '+(unsigned+b'.'+signature).decode()
                data=None if body is None else json.dumps(body,allow_nan=False).encode()
                if data is not None:headers['Content-Type']='application/json'
                req=urllib.request.Request('http://127.0.0.1:18103'+path,data=data,headers=headers,method=method)
                try:
                    with urllib.request.urlopen(req,timeout=10)as response:return response.status,json.load(response)
                except urllib.error.HTTPError as error:
                    raw=error.read()
                    try:data=json.loads(raw)
                    except ValueError:data=None
                    return error.code,data
            def write_proof(stage):
                proof=writes.prove(request,stage)
                print('PROVIDER_NATIVE_WRITES='+json.dumps(proof,sort_keys=True),flush=True)
            write_proof('baseline-old89')
            snapshot=helper.retained_snapshot
            image=json.loads(call(['docker','image','inspect',newimage]).stdout)[0]['Id']
            checkpoint('prepare exact application plan')
            m.application.prepare(root,project,image,root/'plan')
            receipt=root/'plan/preflight.json';helper.old=root/'plan/previous-runtime.json';helper.new=root/'plan/candidate-runtime.json'
            baseline_host=m.hostmod.DockerHost(m.fence.DockerCatalog(cid,'ci','application_fixture'),project)
            hba=baseline_host.bind();baseline=hba.read_bytes()
            # Explicit existing reviewed web endpoint, never a probe sidecar.
            web=inspect(helper.identities(path)['web']['container_id']);pg=inspect(cid)
            webnet=web['NetworkSettings']['Networks'][network];pgnet=pg['NetworkSettings']['Networks'][network]
            outside_endpoint={'container':web['Id'],'image':web['Image'],'started':web['State']['StartedAt'],
                'project':project,'service':'web','network':network,'network_id':webnet['NetworkID'],'address':webnet['IPAddress']}
            connection_request={'version':1,'transport':'private_tcp_plain','host':pgnet['IPAddress'],'port':5432,
                'database':'application_fixture','user':'ci','password':'synthetic-ci-only'}
            active=[None];proofs=[];force_failure=[True];compatibility=[]
            def local(sql):return call(['docker','exec',cid,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','application_fixture','-Atc',sql]).stdout.strip()
            def accepted_behavior(value,slot):
                checkpoint('accept '+slot)
                helper.probe(18103)
                write_proof(slot+'-'+str(len(writes.stages)))
                if helper.retained_snapshot!=snapshot or local('SELECT count(*) FROM schema_migrations')!='93':raise ValueError('Provider retained/schema acceptance failed.')
                expected=m.state.read_private(value[slot])['services']
                found=helper.identities(value[slot])
                if any(found[name]['image']!=service['image']for name,service in expected.items()):raise ValueError('Provider accepted wrong immutable image.')
                if slot=='candidate':
                    # Execute the production bounded connection mode in the
                    # exact already-running candidate API, not a sidecar. This
                    # proves only that endpoint, not provider outside-peer tests.
                    api=active[0].adapter.checked_containers(value[slot],True)['api']
                    pg=json.loads(call(['docker','inspect',cid]).stdout)[0]
                    shared=set(api['NetworkSettings']['Networks'])&set(pg['NetworkSettings']['Networks'])
                    if len(shared)!=1:raise ValueError('One exact native probe network required.')
                    net=next(iter(shared));endpoint=pg['NetworkSettings']['Networks'][net]['IPAddress']
                    address=api['NetworkSettings']['Networks'][net]['IPAddress']
                    request={'version':1,'transport':'private_tcp_plain','host':endpoint,'port':5432,'database':'application_fixture','user':'ci','password':'synthetic-ci-only'}
                    def connection(change=None):
                        body=dict(request);body.update(change or {})
                        result=subprocess.run(['docker','exec','-i',api['Id'],'/bin/setupctl','connection-readiness'],input=json.dumps(body),capture_output=True,text=True,timeout=12)
                        if result.returncode:raise ValueError('Production bounded connection probe failed.')
                        proof=json.loads(result.stdout)
                        if 'synthetic-ci-only'in result.stdout+result.stderr:raise ValueError('Probe leaked credentials.')
                        return proof
                    proof=connection()
                    if proof!={'version':1,'status':'connected','database':'application_fixture','user':'ci','server':endpoint,'client':address}:raise ValueError('Exact admitted native probe endpoint unverified.')
                    if connection({'password':'wrong-synthetic-password'})!={'version':1,'status':'authentication_failed'}:raise ValueError('Native probe did not prove actual password authentication.')
                    restricted=active[0].record()['stage']=='peers'
                    if restricted and connection({'user':'not-admitted-fixture'})!={'version':1,'status':'hba_denied'}:raise ValueError('Native unlisted-user HBA denial unverified.')
                    print('PROVIDER_NATIVE_CONNECTION='+json.dumps({'exact_candidate_endpoint':True,'scram_wrong_password_denied':True,'unlisted_user_hba_denied':restricted,'provider_connection_callback':False}),flush=True)
                    local('CREATE TABLE IF NOT EXISTS post_backup_retention(id integer PRIMARY KEY);INSERT INTO post_backup_retention VALUES(1) ON CONFLICT DO NOTHING')
                    if force_failure[0]:raise ValueError('Forced actual candidate acceptance failure for rollback proof.')
            def accepted(value,slot):
                with helper.retained_client(18103)as client:
                    acceptmod.RuntimeAcceptance(active[0].adapter,client,snapshot,accepted_behavior)(value,slot)
                print('PROVIDER_NATIVE_ACCEPTANCE='+json.dumps({'slot':slot,'exact_runtime':True,'candidate_operator93':slot=='candidate','retained_client':True,'additional_behavior':'synthetic fixture only','full_compatibility':False}),flush=True)
            def compatible_behavior(value):
                checkpoint('compatibility previous gated group')
                helper.probe(18103)
                found=active[0].adapter.checked_containers(value['previous'],True)
                for name in active[0].adapter.writers:
                    gate=active[0].group.gate(active[0].adapter,name,'compatibility')
                    if gate.record()['container']!=found[name]['Id']or gate.record()['stage']!='released':raise ValueError('Compatibility did not use actual dedicated previous-image gates.')
                write_proof('previous-gated93')
                compatibility.append('actual-previous-group')
            def compatible(value):
                with helper.retained_client(18103)as client:
                    acceptmod.PreviousCompatibility(active[0].adapter,active[0].group,client,snapshot,compatible_behavior)(value)
                print('PROVIDER_NATIVE_COMPATIBILITY='+json.dumps({'exact_previous_group':True,'retained_client':True,'additional_behavior':'synthetic fixture only','full_compatibility':False}),flush=True)
            def reviewer(target):
                if (target.root!=str(root)or target.project!=project or target.scope!='application_bootstrap'
                        or target.previous!=str(helper.old)or target.candidate!=str(helper.new)):
                    raise ValueError('Synthetic fixture authority does not cover this target.')
            class ObservedProvider(production.Provider):
                def save(self,value,stage,peers=None):
                    checkpoint('provider '+stage)
                    return super().save(value,stage,peers)
                def bind_journal(self,journal,target):
                    checkpoint('bind actual Coordinator journal')
                    return super().bind_journal(journal,target)
                def connection_proof(self,phase,peers,raw):
                    before=len(commands);super().connection_proof(phase,peers,raw)
                    if any(c[:2]in [['docker','run'],['docker','start'],['docker','pull']]for c in commands[before:]):
                        raise ValueError('Production callback introduced a sidecar or started a source.')
                    proofs.append((phase,len(peers)))
                    print('PROVIDER_NATIVE_PRODUCTION_CONNECTION='+json.dumps({'phase':phase,'peers':len(peers),
                        'existing_web':True,'local_closed':phase=='exclusive','pinned_writer_namespaces':phase=='restricted'and len(peers)==3,
                        'new_probe_sidecar':False,'full_network_callback':True,'production_lifecycle':True}),flush=True)
                def release(self,ticket,target):
                    super().release(ticket,target)
                    print('PASS exact production connection staging released only after provider settled original admission/direct runtime.',flush=True)
            class LostCleanup(ObservedProvider):
                def release(self,ticket,target):
                    super().release(ticket,target)
                    raise Loss('Lost acknowledgement after production host/local tool cleanup, admission still held')
            class LostRelease(ObservedProvider):
                def release(self,ticket,target):
                    normal=self.barrier.recover_release
                    def lost():
                        normal();raise Loss('Lost acknowledgement after actual original HBA restoration, before direct normalization')
                    self.barrier.recover_release=lost
                    return super().release(ticket,target)
            def coordinator(kind=ObservedProvider):
                provider=kind(m.fence.DockerCatalog(cid,'ci','application_fixture'),reviewer,outside_endpoint,connection_request,run);active[0]=provider
                return m.execution.Coordinator(provider,accepted,compatible,provider.factory),provider
            # Actual 89->93 and forced candidate failure: runtime-group previous
            # compatibility, no network migration container or DB archive replay.
            caller,provider=coordinator();checkpoint('activate with forced candidate failure')
            try:caller.run(root,project,receipt,'activate')
            except RuntimeError:
                with m.state.Journal(root)as journal:
                    if journal.read()['phase']!='restored' or journal.read().get('maintenance_state')!='held':raise
            else:raise ValueError('Forced provider acceptance failure did not roll back.')
            if compatibility!=['actual-previous-group']or local('SELECT count(*) FROM post_backup_retention')!='1':raise ValueError('Actual previous compatibility/data retention missing.')
            caller,provider=coordinator(LostCleanup);checkpoint('recover previous runtime with lost cleanup acknowledgement')
            try:caller.run(root,project,receipt,'recover')
            except Loss:pass
            else:raise ValueError('Actual production cleanup interruption missing.')
            try:m.state.selection(root)
            except ValueError:pass
            else:raise ValueError('Lost cleanup permitted ordinary selection.')
            before_cleanup_sql=sum('apply-reviewed'in cmd for cmd in commands)
            caller,provider=coordinator();checkpoint('fresh recover after released host/local tool cleanup');caller.run(root,project,receipt,'recover')
            if sum('apply-reviewed'in cmd for cmd in commands)!=before_cleanup_sql:raise ValueError('Released-tool recovery replayed schema.')
            evidence=list((root/'.secure-deployment').glob('postgres-hosttool-released-*.json'))
            if len(evidence)!=1 or m.state.read_private(evidence[0])['stage']!='released':raise ValueError('Released host generation evidence missing.')
            print('PASS actual production provider fresh settled recovery after lost host/local cleanup acknowledgement: released generation archived, new exact tools proved, no schema replay or early admission release.',flush=True)
            if m.state.selection(root)['config']!=str(helper.old)or hba.read_bytes()!=baseline:raise ValueError('Actual provider rollback release failed.')
            os.rename(root/'.secure-deployment',root/'.provider-restored-evidence')
            plan=m.state.read_private(receipt);plan['current_services']=helper.identities(helper.old);m.state.atomic(receipt,plan)
            force_failure[0]=False;caller,provider=coordinator(LostRelease);checkpoint('activate with lost release acknowledgement')
            try:caller.run(root,project,receipt,'activate')
            except Loss:pass
            else:raise ValueError('Actual provider settled release interruption missing.')
            with m.state.Journal(root)as journal:
                if journal.read()['phase']!='committed'or journal.read().get('maintenance_state')!='held':raise ValueError('Lost release did not retain durably committed admission block.')
                if m.state.read_private(journal.directory/'postgres-hba.json')['stage']!='released':raise ValueError('Original HBA release did not actually finish before loss.')
            try:m.state.selection(root)
            except ValueError:pass
            else:raise ValueError('Lost release permitted ordinary selection.')
            before_sql=sum('apply-reviewed'in cmd for cmd in commands)
            caller,provider=coordinator();checkpoint('recover committed original-HBA release');result=caller.run(root,project,receipt,'recover')
            if result['state']!='committed'or result['changed']is not False:raise ValueError('Settled provider recovery repeated transaction.')
            if sum('apply-reviewed'in cmd for cmd in commands)!=before_sql:raise ValueError('Settled provider recovery replayed schema.')
            if m.state.selection(root)['config']!=str(helper.new)or hba.read_bytes()!=baseline:raise ValueError('Settled provider recovery chose wrong runtime/HBA.')
            # Drop gate manager for final inspection: original Cmd/env/mounts,
            # PID1, image/health and limited authenticated retained-list proof.
            provider.adapter.runtime_group=None
            found=provider.adapter.checked_containers(helper.new,True)
            for name in provider.adapter.writers:
                if inspect(found[name]['Id'])['Config']['Cmd']!=['/bin/'+name]or call(['docker','exec',found[name]['Id'],'cat','/proc/1/comm']).stdout.strip()!=name:
                    raise ValueError('Final runtime is not direct actual binary PID1.')
            if not any(phase=='restricted'and size==3 for phase,size in proofs)or not any(phase=='original'for phase,size in proofs):raise ValueError('Concrete connection proofs incomplete.')
            if local('SELECT count(*) FROM post_backup_retention')!='1':raise ValueError('Settled provider lost post-backup data.')
            write_proof('final-direct93')
            if len(writes.stages)<6:raise ValueError('Missing write proofs across provider lifecycle.')
            print('PASS actual concrete Linux host provider through real Coordinator: exact immutable PG catalog/volume/HBA; original89 to additive93 using static candidate local socket operator; all API/ingest/MCP dynamic endpoints gated and exact SCRAM admitted with fresh outside-IP denial; forced actual candidate failure -> dedicated previous-image compatibility group -> exact previous restoration; post-backup row retained; real committed journal followed by lost original-HBA-release acknowledgement; fresh settled recovery performs no apply-reviewed replay; direct final Cmd/PID1/images/health with no gate manager and original HBA; private admission removed only last. Synthetic fixture scope/limited authenticated retained lists only, limited profile-create and HTTP telemetry write/read/retry/role/tenant proof, not full background compatibility, owner activation or physical reading.',flush=True)
        finally:
            faulthandler.cancel_dump_traceback_later()
            result=call(compose+['logs','--no-color','--tail','20'],False);print(result.stdout[-6000:],flush=True)
            call(compose+['down','--volumes','--remove-orphans'],False)
