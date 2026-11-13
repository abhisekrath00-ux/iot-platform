#!/usr/bin/env python3
"""Real disposable old/new app image transaction. Not an owner effect adapter."""
import base64
import contextlib
import hashlib
import hmac
import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import time
import urllib.request
import urllib.error

HERE=pathlib.Path(__file__).resolve().parent
ROOT=HERE.parent
OLD_COMMIT='bfe5d89b46324a566fd089b6c83ab9c111dc9231'

class SyntheticProcessLoss(BaseException):pass

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

app=load('secure-application');state=load('secure-state');runtime=load('secure-runtime');production=load('secure-application-docker');retained=load('secure-application-reads');execution=load('secure-execution');clientmod=load('secure-application-client')

def call(args,cwd=None,binary=False,input=None):
    result=subprocess.run(list(map(str,args)),cwd=cwd,capture_output=True,text=not binary,input=input)
    if result.returncode:
        detail=result.stderr.decode(errors='replace') if binary else result.stderr
        raise ValueError('Disposable application fixture command failed: '+' '.join(map(str,args[:3]))+'\n'+detail[-6000:])
    return result.stdout

class Effects:
    def __init__(self,root,project,old,new,testimage,fail=False):
        if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0
                or root.parent!=pathlib.Path('/tmp') or not root.name.startswith('hexthings-application-ci-')
                or not project.startswith('hexthings-app-ci-')):
            raise ValueError('Only fresh disposable native application fixture allowed.')
        self.root,self.project,self.old,self.new,self.testimage=root,project,old,new,testimage
        self.retained_snapshot=None
        self.fail=fail;self.crash_after_schema=False;self.accepted=False;self.writers=['api','ingest','mcp']
        self.db='postgres://ci:synthetic-ci-only@postgres:5432/application_fixture?sslmode=disable'
    def compose(self,path,*args,binary=False,input=None):return call(['docker','compose','-p',self.project,'-f',path,*args],self.root,binary,input)
    def operator(self,action):
        output=call(['docker','run','--rm','--network',self.project+'_default','-e','GITHUB_ACTIONS=true','-e','TEST_DATABASE_URL='+self.db,'-e','BOOTSTRAP_TEST_MIGRATIONS=/migrations','-e','HEXTHINGS_SYNTHETIC_APP_ACTION='+action,self.testimage,'-test.run','^TestSyntheticApplicationFixtureOperator$','-test.v'])
        if action=='inspect':
            lines=[line[len('HEXTHINGS_SCHEMA='):]for line in output.splitlines()if line.startswith('HEXTHINGS_SCHEMA=')]
            if len(lines)!=1:raise ValueError('No exact synthetic schema result')
            return json.loads(lines[0])
    def containers(self,config):
        ids=self.compose(config,'ps','-a','-q').split()
        return json.loads(call(['docker','inspect',*ids])) if ids else []
    def identities(self,config):
        result={}
        for item in self.containers(config):
            labels=item['Config']['Labels'];name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in result:raise ValueError('Unexpected project or replica')
            if item['State']['Status']!='running':raise ValueError('Expected running synthetic service')
            result[name]={'image':item['Image'],'container_id':item['Id']}
        return result
    def authorize(self,action,project,previous,candidate):
        if action not in {'application-upgrade','application-recover'} or project!=self.project or pathlib.Path(previous)!=self.old or pathlib.Path(candidate)!=self.new:raise ValueError('Synthetic app target or action changed')
    def revalidate(self,project,receipt):
        value=json.loads(pathlib.Path(receipt).read_text())
        if project!=self.project or value['current_services']!=self.identities(self.old):raise ValueError('Current application identity changed')
        old=json.loads(self.old.read_text());new=json.loads(self.new.read_text())
        app.pair(old,new,{name:x['image']for name,x in old['services'].items()},{name:x['image']for name,x in new['services'].items()})
        for image in {x['image']for x in new['services'].values()}:
            found=json.loads(call(['docker','image','inspect',image]))
            if len(found)!=1 or found[0]['Id']!=image:raise ValueError('Candidate exact local image unavailable')
    def backup(self,project,previous):
        raw=self.compose(previous,'exec','-T','postgres','pg_dump','-U','ci','-Fc','application_fixture',binary=True)
        self.compose(previous,'exec','-T','postgres','pg_restore','-l',binary=True,input=raw)
        fd,name=tempfile.mkstemp(prefix='app-backup-',suffix='.dump',dir=self.root)
        with os.fdopen(fd,'wb')as output:output.write(raw);output.flush();os.fsync(output.fileno())
        return {'path':name,'sha256':hashlib.sha256(raw).hexdigest(),'verified_archive':True}
    def stop_writers(self,value):self.compose(value['previous'],'stop',*self.writers)
    def verify_writers_stopped(self,value):
        items=self.containers(value['previous']);names={}
        for item in items:
            labels=item['Config']['Labels'];name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in names:raise ValueError('Unknown writer or project')
            names[name]=item
        if set(names)!=set(json.loads(self.old.read_text())['services']):raise ValueError('Unexpected service set during writer stop')
        for name in self.writers:
            if names[name]['State']['Status'] not in {'exited','created'}:raise ValueError('Writer is still running: '+name)
    def apply_reviewed_schema(self,value):
        if value.get('schema_attempted')!=app.ADDED:raise ValueError('Schema attempt must be durable before SQL')
        self.operator('apply')
        if self.crash_after_schema:raise SyntheticProcessLoss('Synthetic loss after schema commit before acknowledgement')
    def inspect_reviewed_schema(self,value):return self.operator('inspect')
    def start_candidate(self,value):self.compose(value['candidate'],'up','-d','--no-deps','--no-build','--pull','never',*self.writers)
    def verify_recovery_inputs(self,value):
        for name,want in value['files'].items():
            if state.digest(name)!=want:raise ValueError('Application input changed')
        for image in set(value['previous_images'].values()):
            found=json.loads(call(['docker','image','inspect',image]))
            if len(found)!=1 or found[0]['Id']!=image:raise ValueError('Previous exact image unavailable')
    def stop_candidate(self,value):self.compose(value['candidate'],'stop',*self.writers)
    def verify_previous_schema_compatibility(self,value):
        # Run the exact old API as a separate short-lived compatibility probe.
        # Authenticated retained-data reads are limited evidence, not proof of
        # every old application/background operation against the additive schema.
        config=json.loads(self.old.read_text());image=config['services']['api']['image'];env=config['services']['api']['environment']
        name=self.project+'-compatibility'
        args=['docker','run','-d','--name',name,'--network',self.project+'_default','-p','127.0.0.1:18102:8000']
        for key,value in env.items():args+=['-e',key+'='+value]
        args +=[image,'/bin/api']
        call(args)
        try:self.probe(18102)
        finally:call(['docker','rm','-f',name])
    def restore_previous(self,value):self.compose(value['previous'],'up','-d','--no-deps','--no-build','--pull','never',*self.writers)
    @staticmethod
    def token_for(tenant):
        def enc(value):return base64.urlsafe_b64encode(json.dumps(value,separators=(',',':')).encode()).rstrip(b'=')
        header=enc({'alg':'HS256','typ':'JWT'});payload=enc({'sub':'retain-user','tenant_id':tenant,'role':'admin','exp':int(time.time())+120})
        unsigned=header+b'.'+payload;signature=base64.urlsafe_b64encode(hmac.new(b'synthetic-application-only',unsigned,hashlib.sha256).digest()).rstrip(b'=')
        return (unsigned+b'.'+signature).decode()
    @contextlib.contextmanager
    def retained_client(self,port):
        fd,name=tempfile.mkstemp(prefix='synthetic-retained-session-',dir=self.root)
        session=pathlib.Path(name)
        with os.fdopen(fd,'w')as file:file.write(self.token_for('retain-tenant'))
        try:yield clientmod.Client('http://127.0.0.1:'+str(port),session,'retain-tenant','retain-user',allow_loopback_http=True)
        finally:session.unlink()
    def probe(self,port=18101):
        until=time.monotonic()+35
        while True:
            try:
                with urllib.request.urlopen('http://127.0.0.1:'+str(port)+'/healthz',timeout=2)as response:
                    if response.status==200:break
            except Exception:
                if time.monotonic()>until:raise ValueError('Synthetic API health did not pass')
                time.sleep(.2)
        with self.retained_client(port)as client:after=client.snapshot()
        for path in retained.PATHS:
            origin='http://127.0.0.1:'+str(port)+path
            try:
                urllib.request.urlopen(origin,timeout=3)
            except urllib.error.HTTPError as error:
                if error.code!=401:raise ValueError('Synthetic unauthenticated retained-list access did not refuse')
            else:raise ValueError('Synthetic unauthenticated retained-list access was allowed')
            request=urllib.request.Request(origin,headers={'Authorization':'Bearer '+self.token_for('other-synthetic-tenant')})
            with urllib.request.urlopen(request,timeout=3)as response:
                if json.load(response)!=[]:raise ValueError('Synthetic cross-tenant retained-list isolation failed')
        if self.retained_snapshot is None:self.retained_snapshot=after
        elif self.retained_snapshot!=after:raise ValueError('Authenticated retained inventory changed.')
    def accept(self,value,target):
        path=value[target];expected=json.loads(pathlib.Path(path).read_text())['services'];until=time.monotonic()+35
        while True:
            try:
                found=self.identities(path)
                if set(found)!=set(expected) or any(found[name]['image']!=item['image']for name,item in expected.items()):raise ValueError('Application runtime image mismatch')
                self.probe();break
            except Exception:
                if time.monotonic()>until:raise ValueError('Application runtime acceptance timed out')
                time.sleep(.2)
        self.operator('rows')
        if target=='candidate':
            self.accepted=True
            self.compose(path,'exec','-T','postgres','psql','-U','ci','-d','application_fixture','-v','ON_ERROR_STOP=1','-c','CREATE TABLE IF NOT EXISTS post_backup_retention(id integer PRIMARY KEY);INSERT INTO post_backup_retention VALUES(1) ON CONFLICT DO NOTHING')
            if self.fail:raise ValueError('Forced post-candidate-acceptance application rollback')

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0:raise SystemExit('Disposable standard Ubuntu runner only')
    with tempfile.TemporaryDirectory(prefix='hexthings-application-ci-')as td:
        work=pathlib.Path(td);project='hexthings-app-ci-'+str(os.getpid());oldimage=project+':old';newimage=project+':new';testimage=project+':operator'
        # Read the exact public checkpoint only; never checkout/merge/tag/push.
        if subprocess.run(['git','cat-file','-e',OLD_COMMIT+'^{commit}'],cwd=ROOT,capture_output=True).returncode:
            call(['git','fetch','--no-tags','--depth=1','origin',OLD_COMMIT],ROOT)
        archive=call(['git','archive',OLD_COMMIT,'server'],ROOT,binary=True);oldsrc=work/'old-source';oldsrc.mkdir()
        subprocess.run(['tar','-x','-C',oldsrc],input=archive,check=True)
        for label,source in [('old',oldsrc/'server'),('new',ROOT/'server')]:
            context=work/label;context.mkdir();shutil.copytree(source/'migrations',context/'migrations')
            for binary in (['api','ingest','mcp','tenantctl'] if label=='old' else ['api','ingest','mcp','tenantctl','setupctl']):
                subprocess.run(['go','build','-o',str(context/binary),'./cmd/'+binary],cwd=source,env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='auto'),check=True)
            (context/'Dockerfile').write_text('FROM alpine:3.20\nCOPY '+('api ingest mcp tenantctl' if label=='old' else 'api ingest mcp tenantctl setupctl')+' /bin/\nCOPY migrations /migrations\nWORKDIR /\n')
            call(['docker','build','-t',oldimage if label=='old' else newimage,context])
        subprocess.run(['go','test','-c','-o',str(work/'bootstrap-tests'),'./internal/setupbootstrap'],cwd=ROOT/'server',env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='auto'),check=True)
        shutil.copytree(ROOT/'server/migrations',work/'migrations');(work/'Dockerfile').write_text('FROM alpine:3.20\nCOPY bootstrap-tests /bootstrap-tests\nCOPY migrations /migrations\nWORKDIR /\nENTRYPOINT ["/bootstrap-tests"]\n');call(['docker','build','-t',testimage,work])
        db='postgres://ci:synthetic-ci-only@postgres:5432/application_fixture?sslmode=disable'
        base={'services':{
         'postgres':{'image':'postgres:16-alpine','environment':{'POSTGRES_USER':'ci','POSTGRES_PASSWORD':'synthetic-ci-only','POSTGRES_DB':'application_fixture'},'healthcheck':{'test':['CMD-SHELL','pg_isready -U ci'],'interval':'1s','timeout':'2s','retries':30}},
         'mosquitto':{'image':'eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98'},
         'web':{'image':'nginx:1.27-alpine'}}}
        for name in ['api','ingest','mcp']:base['services'][name]={'image':oldimage,'command':['/bin/'+name],'environment':{'DATABASE_URL':db,'JWT_SIGNING_SECRET':'synthetic-application-only','MQTT_TLS':'false','MQTT_HOST':'mosquitto','MQTT_PORT':'1883'},'depends_on':{'postgres':{'condition':'service_healthy'}}}
        base['services']['api']['ports']=[{'target':8000,'published':'18101','host_ip':'127.0.0.1'}]
        basepath=work/'docker-compose.yml';basepath.write_text(json.dumps(base));basepath.chmod(0o600)
        compose=['docker','compose','-p',project,'-f',str(basepath)]
        try:
            call(compose+['up','-d','--wait','postgres','mosquitto','web'])
            postgres_id=call(compose+['ps','-q','postgres']).strip()
            load('test-secure-postgres-fence-native').prove(postgres_id,work,project)
            load('test-secure-postgres-hba-native').prove(postgres_id,work,project)
            helper=Effects(work,project,work/'previous.json',work/'candidate.json',testimage)
            helper.operator('seed89')
            operator_image=json.loads(call(['docker','image','inspect',newimage]))[0]['Id']
            # Run the new concrete integration before repeated component proofs
            # so the fixed job budget yields its bounded stage diagnostics.
            load('test-secure-postgres-hosttool-native').prove(operator_image,work,project)
            load('test-secure-application-provider-native').prove(work,project,newimage,testimage,basepath)
            load('test-secure-postgres-operator-native').prove(postgres_id,work,project,operator_image)
            old_image_id=json.loads(call(['docker','image','inspect',oldimage]))[0]['Id']
            load('test-secure-application-startgate-native').prove(postgres_id,work,project,old_image_id)
            load('test-secure-application-startgroup-native').prove(postgres_id,work,project,old_image_id,basepath)
            call(compose+['up','-d','--no-deps','api','ingest','mcp']);helper.probe()
            load('test-secure-postgres-namespace-native').prove(operator_image,work,project,basepath,helper)
            load('test-secure-postgres-outside-native').prove(operator_image,work,project,basepath,helper)
            target=json.loads(call(['docker','image','inspect',newimage]))[0]['Id']
            # Safe only inside this hardcoded fresh synthetic fixture. Record
            # command/default semantics for grounded native guard failures.
            resolved=json.loads(call(compose+['config','--format','json']))
            for container in helper.containers(basepath):
                name=container['Config']['Labels']['com.docker.compose.service']
                metadata=json.loads(call(['docker','image','inspect',container['Image']]))[0]
                print('SYNTHETIC_DEFINITION='+json.dumps({'service':name,'compose':{k:resolved['services'][name].get(k)for k in ['command','entrypoint']},'image':{k:metadata['Config'].get(k)for k in ['Cmd','Entrypoint']},'actual':{k:container['Config'].get(k)for k in ['Cmd','Entrypoint']}}),flush=True)
            plan=production.prepare(work,project,target,work/'application-plan')
            helper.old=work/'application-plan/previous-runtime.json';helper.new=work/'application-plan/candidate-runtime.json'
            images={name:item['image']for name,item in plan['current_services'].items()}
            receiptpath=work/'application-plan/preflight.json'
            def receipt():
                value=state.read_private(receiptpath);value['current_services']=helper.identities(helper.old);state.atomic(receiptpath,value);return receiptpath
            class SyntheticSession:pass
            class FixtureMaintenance:
                # Only this fresh CI fixture. This is a synthetic lifecycle
                # verifier, NOT production outside-writer prevention/authority.
                def __init__(self):self.ticket=SyntheticSession();self.events=[]
                def review(self,target):
                    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux'
                            or os.geteuid()!=0 or target.root!=str(work) or target.project!=project
                            or target.scope!='application_bootstrap' or target.previous!=str(helper.old)
                            or target.candidate!=str(helper.new)):
                        raise ValueError('Synthetic coordinator scope changed')
                    self.events.append('review')
                def enter(self,target):self.review(target);self.events.append('enter');return self.ticket
                def check(self,ticket,target):
                    if ticket is not self.ticket:raise ValueError('Synthetic maintenance session changed')
                    self.review(target);self.events.append('check')
                def release(self,ticket,target):self.check(ticket,target);self.events.append('release')
                def retain(self,ticket,target):self.events.append('retain')
            def coordinator(observer,adapter_type=production.DockerApplication):
                observer.retained_snapshot=helper.retained_snapshot
                maintenance=FixtureMaintenance()
                def factory(target,path,authorize,accept,compatible):
                    return adapter_type(work,project,path,authorize,accept,compatible)
                caller=execution.Coordinator(maintenance,observer.accept,observer.verify_previous_schema_compatibility,factory)
                return caller,maintenance
            # Lose the process during maintenance admission, before begin() or
            # any backup/writer/schema effect. The sidecar must still block.
            receipt();caller,maintenance=coordinator(helper)
            before_admission=helper.identities(helper.old)
            normal_enter=maintenance.enter
            def lose_admission(target):
                if not (work/'.secure-deployment/maintenance.json').is_file():raise ValueError('Admission not durable before provider entry')
                normal_enter(target)
                raise SyntheticProcessLoss('Synthetic process loss during maintenance admission')
            maintenance.enter=lose_admission
            try:caller.run(work,project,receiptpath,'activate')
            except SyntheticProcessLoss:pass
            else:raise ValueError('Admission interruption did not occur')
            if (work/'.secure-deployment/journal.json').exists() or helper.identities(helper.old)!=before_admission:raise ValueError('Admission loss changed transaction services')
            try:state.selection(work)
            except ValueError:pass
            else:raise ValueError('Admission interruption allowed normal selection')
            class AdmissionObserver(Effects):
                def accept(self,value,target):
                    if target!='previous' or self.identities(value[target])!=before_admission:raise ValueError('Admission recovery target/runtime changed')
                    self.probe()
                    if self.operator('inspect')['status']!='exact_reviewed_baseline_89':raise ValueError('Admission recovery changed baseline schema')
            admission_observer=AdmissionObserver(work,project,helper.old,helper.new,testimage)
            caller,maintenance=coordinator(admission_observer)
            settled=caller.run(work,project,receiptpath,'recover')
            if settled['state']!='maintenance_settled_before_changes' or state.selection(work)is not None or helper.identities(helper.old)!=before_admission:raise ValueError('Admission recovery changed runtime or selected a deployment')
            print('PASS production caller admission interruption: durable maintenance marker precedes provider entry; process loss before transaction blocks selection; fresh prior-runtime acceptance/release settles without service/schema changes. Synthetic maintenance provider only, not owner outside-writer prevention.',flush=True)
            helper.fail=True
            receipt();caller,maintenance=coordinator(helper)
            try:caller.run(work,project,receiptpath,'activate')
            except RuntimeError:
                with state.Journal(work)as journal:
                    if journal.read()['phase']!='restored' or journal.read().get('maintenance_state')!='held' or not helper.accepted:raise
                if maintenance.events[-1]!='retain':raise ValueError('Failure released synthetic maintenance')
            else:raise ValueError('Expected synthetic post-acceptance failure')
            try:state.selection(work)
            except ValueError:pass
            else:raise ValueError('Unreleased maintenance did not block normal selection')
            helper.fail=False
            caller,maintenance=coordinator(helper);caller.run(work,project,receiptpath,'recover')
            if maintenance.events[-1]!='release':raise ValueError('Settled recovery did not release synthetic session')
            retained=helper.compose(helper.old,'exec','-T','postgres','psql','-U','ci','-d','application_fixture','-Atc','SELECT count(*) FROM post_backup_retention').strip()
            if retained!='1' or state.selection(work)['config']!=str(helper.old):raise ValueError('Application rollback erased post-backup data or selected wrong runtime')
            print('PASS production Docker adapter and setupctl: real exact bfe5d89 -> reviewed additive schema -> new API/ingest/MCP image acceptance -> forced failure -> old API compatibility probe -> exact previous writers restored; additive schema and post-backup data retained. Synthetic only; exact retained-list and synthetic source-config compatibility, not owner activation.',flush=True)
            os.rename(work/'.secure-deployment',work/'.synthetic-restored-evidence');helper.fail=False;helper.accepted=False
            receipt();caller,maintenance=coordinator(helper)
            result=caller.run(work,project,receiptpath,'activate')
            if result['state']!='committed' or not helper.accepted or maintenance.events[-1]!='release':raise ValueError('Positive application coordinator not settled')
            if state.selection(work)['config']!=str(helper.new):raise ValueError('Wrong application selector')
            print('PASS production Docker adapter and setupctl: second real disposable application transaction committed exact new API/ingest/MCP images after all-writer stop, unique verified backup, reviewed-schema inspection and authenticated retained-data reads. Not TLS activation, secure Upgrade, full compatibility or physical reading.',flush=True)
            # Return to the exact old images in this synthetic fixture only,
            # archive the committed test journal, then interrupt the coordinator
            # immediately after SQL commit with a separate recovery instance.
            helper.compose(helper.new,'stop',*helper.writers)
            helper.compose(helper.old,'up','-d','--no-deps','--no-build','--pull','never',*helper.writers)
            helper.probe()
            os.rename(work/'.secure-deployment',work/'.synthetic-committed-evidence')
            class LostAcknowledgement(production.DockerApplication):
                def apply_reviewed_schema(self,value):
                    super().apply_reviewed_schema(value)
                    raise SyntheticProcessLoss('Synthetic loss after production schema operator commit')
            receipt();caller,maintenance=coordinator(helper,LostAcknowledgement)
            try:caller.run(work,project,receiptpath,'activate')
            except SyntheticProcessLoss:
                with state.Journal(work)as journal:value=journal.read()
                if value['phase']!='changing' or value.get('schema_attempted')!=app.ADDED or value.get('schema_result') or value.get('maintenance_state')!='held':raise ValueError('Interrupted schema acknowledgement not durably blocked')
                if maintenance.events[-1]!='retain':raise ValueError('Process loss released synthetic maintenance')
            else:raise ValueError('Synthetic coordinator interruption did not occur')
            try:state.selection(work)
            except ValueError:pass
            else:raise ValueError('Interrupted application transaction allowed normal selection')
            recovery=Effects(work,project,helper.old,helper.new,testimage)
            caller,maintenance=coordinator(recovery)
            caller.run(work,project,receiptpath,'recover')
            with state.Journal(work)as journal:
                if journal.read()['phase']!='restored' or journal.read().get('maintenance_state')!='released' or maintenance.events[-1]!='release':raise ValueError('Explicit interrupted recovery did not settle')
            retained=helper.compose(helper.old,'exec','-T','postgres','psql','-U','ci','-d','application_fixture','-Atc','SELECT count(*) FROM post_backup_retention').strip()
            if retained!='1' or state.selection(work)['config']!=str(helper.old):raise ValueError('Interrupted recovery lost retained data or exact previous runtime')
            print('PASS production execution caller with synthetic maintenance-session checks: activation/failure/settled recovery/commit/lost-ack recovery kept held markers blocked until release; fresh runtime acceptance before settlement. Not owner authority or production outside-writer prevention.',flush=True)
            print('PASS production Docker adapter and setupctl: real disposable interrupted coordinator after committed reviewed SQL before acknowledgement blocked normal selection; separate recovery inspected actual schema, checked old-API compatibility and restored exact prior writers without replaying the DB archive. Simulated process loss, not physical power loss.',flush=True)

        finally:
            result=subprocess.run(compose+['logs','--no-color','--tail','25'],capture_output=True,text=True)
            if result.returncode:print('Synthetic application logs unavailable')
            subprocess.run(compose+['down','--volumes','--remove-orphans'],check=False)
            for image in [oldimage,newimage,testimage]:subprocess.run(['docker','image','rm',image],check=False)

if __name__=='__main__':main()
