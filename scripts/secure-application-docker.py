#!/usr/bin/env python3
"""Production application-only Docker adapter, internal and not menu-connected.

Never call this from discovery/startup. The caller must supply trusted target/
maintenance authority and real retained-data/compatibility acceptance. No caller
or owner activation CLI is supplied while those product checks remain open.
"""
import importlib.util
import json
import os
import pathlib
import re
import subprocess
import tempfile
from urllib.parse import urlparse,unquote,parse_qsl

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

app=load('secure-application');state=load('secure-state');preflight=load('secure-preflight');readiness=load('secure-readiness')


def database_target(config):
    """Only the existing local Compose Postgres target, no remote DB upgrade."""
    postgres=config['services']['postgres'];env=postgres.get('environment',{})
    user,db=env.get('POSTGRES_USER'),env.get('POSTGRES_DB')
    if not user or not db or any(not isinstance(x,str) or not x or '\x00' in x for x in [user,db]):
        raise ValueError('Explicit current Postgres user/database required.')
    roles=readiness.classify(config,{name:s['image']for name,s in config['services'].items()})
    for name in roles['writers']:
        value=urlparse(config['services'][name]['environment']['DATABASE_URL'])
        parameters=parse_qsl(value.query,keep_blank_values=True)
        if (any(key not in {'sslmode','connect_timeout','application_name'} for key,_ in parameters)
                or len({key for key,_ in parameters})!=len(parameters)):
            raise ValueError('Database connection overrides/custom schema options require separate review.')
        if (value.scheme not in {'postgres','postgresql'} or value.hostname!='postgres'
                or value.port not in {None,5432} or unquote(value.path.lstrip('/'))!=db
                or unquote(value.username or '')!=user):
            raise ValueError('All current writers must target the same local Compose database.')
    return user,db,roles['writers']



def verify_container_definition(service,container,image):
    """Bind actual running command/environment, not only desired Compose text.

    A container can retain old environment or a different entrypoint despite
    using the expected image ID. Never stop or mutate a different DB target.
    """
    def environment(items):
        result={}
        for item in items or []:
            key,separator,value=item.partition('=')
            if not separator or key in result:raise ValueError('Ambiguous actual/image environment; no application change.')
            result[key]=value
        return result
    expected=environment(image.get('Config',{}).get('Env',[]))
    configured=service.get('environment',{})
    if not isinstance(configured,dict) or any(not isinstance(key,str) or value is None for key,value in configured.items()):
        raise ValueError('Exact resolved service environment required.')
    expected.update({key:str(value)for key,value in configured.items()})
    if environment(container.get('Config',{}).get('Env',[]))!=expected:
        raise ValueError('Actual container environment differs from immutable plan.')
    for field,key in [('Cmd','command'),('Entrypoint','entrypoint')]:
        # Compose null inherits the image default; an explicit [] clears it.
        want=service.get(key)
        if want is None:want=image.get('Config',{}).get(field)
        actual=container.get('Config',{}).get(field)
        if want in [None,[]]:want=[]
        if actual in [None,[]]:actual=[]
        if not isinstance(want,list) or actual!=want:
            raise ValueError('Actual container '+field+' differs from immutable plan ('+type(want).__name__+'/'+type(actual).__name__+'); no application change.')
    if service.get('command') in [['/bin/api'],['/bin/ingest'],['/bin/mcp']]:
        if container.get('Config',{}).get('Entrypoint'):
            raise ValueError('Application writers require the supported direct binary entrypoint.')
        if any(key.startswith('PG') for key in expected):
            raise ValueError('Application writer libpq environment overrides need separate review.')
    host=container.get('HostConfig',{})
    if host.get('Privileged') or host.get('NetworkMode')=='host':
        raise ValueError('Actual privileged/host-network container is outside supported application upgrade.')


def prepare(root,project,image,bundle,execute=subprocess.run):
    """Read-only Docker checks plus fresh private diagnostics, no docker run.

    The candidate image must already be local. No image build/pull, container
    start/stop, schema operator, ACL export or journal creation occurs here.
    """
    root=pathlib.Path(root).resolve();bundle=pathlib.Path(bundle).absolute()
    if (not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',project)
            or not re.fullmatch(r'sha256:[a-f0-9]{64}',image)):
        raise ValueError('Explicit Compose project and exact local candidate image ID required.')
    selected=state.selection(root)
    if selected and selected['project']!=project:raise ValueError('Current deployment project differs from selection.')
    if bundle.parent!=root or bundle.exists() or bundle.is_symlink():raise ValueError('Fresh private application plan directly inside the project root required.')
    compose=['docker','compose','-p',project]
    if selected:compose+=['-f',selected['config']]
    def call(args):
        result=execute(args,cwd=root,capture_output=True,text=True)
        if result.returncode:raise ValueError('Read-only application plan check failed; no application change.')
        return result.stdout
    config=json.loads(call(compose+['config','--format','json']))
    ids=call(compose+['ps','-a','-q']).split()
    if not ids:raise ValueError('Existing running project required.')
    containers=json.loads(call(['docker','inspect',*ids]))
    current=preflight.validate_current(config,config,containers,project)
    images={name:item['image']for name,item in current.items()}
    # freeze removes inactive optional profiles and pins exact local images.
    old,disabled=app.runtime.freeze(config,images)
    new=json.loads(json.dumps(old));user,db,writers=database_target(old)
    target=json.loads(call(['docker','image','inspect',image]))
    if len(target)!=1 or target[0].get('Id')!=image:raise ValueError('Exact candidate image is not available locally.')
    for oldimage in sorted(set(images.values())):
        found=json.loads(call(['docker','image','inspect',oldimage]))
        if len(found)!=1 or found[0].get('Id')!=oldimage:raise ValueError('Exact prior image unavailable for recovery.')
        for container in containers:
            if container['Image']==oldimage:
                name=container['Config']['Labels']['com.docker.compose.service']
                verify_container_definition(old['services'][name],container,found[0])
        if oldimage==images['api'] and any(target[0].get(key)!=found[0].get(key) for key in ['Os','Architecture']):
            raise ValueError('Candidate image platform differs from current API.')
    for name in writers:new['services'][name]['image']=image
    app.pair(old,new,images,{name:s['image']for name,s in new['services'].items()})
    dependencies={}
    for service in old['services'].values():
        for mount in service.get('volumes',[]):
            if mount.get('type')=='bind' and mount.get('read_only'):
                path=pathlib.Path(mount['source'])
                if path.is_symlink() or not path.is_file() or path.stat().st_mode&0o022:
                    raise ValueError('Read-only runtime dependencies require exact non-writable regular file binds.')
                dependencies[str(path)]=state.digest(path)
    bundle.mkdir(mode=0o700)
    try:
        for name,value in [('previous-runtime.json',old),('candidate-runtime.json',new)]:
            state.atomic(bundle/name,value)
            call(['docker','compose','-p',project,'-f',str(bundle/name),'config','--quiet'])
        previous=bundle/'previous-runtime.json';candidate=bundle/'candidate-runtime.json'
        previous_files={str(previous):state.digest(previous),**dependencies}
        files={**previous_files,str(candidate):state.digest(candidate)}
        receipt={'version':1,'status':'preflight_only','activation_allowed':False,
                 'scope':'application_bootstrap','root':str(root),'project':project,
                 'files':files,'previous_files':previous_files,'current_services':current,
                 'writers':writers,'database':{'user':user,'name':db},'disabled_profiles':disabled,
                 'limitations':['No schema operator or container change performed.',
                    'Candidate image provenance, external-writer maintenance boundary and target/downtime authority are caller prerequisites.',
                    'Full retained-data and previous-image compatibility acceptance are mandatory separate caller checks.']}
        state.atomic(bundle/'preflight.json',receipt)
        return receipt
    except BaseException:
        # Partial files are diagnostics only and cannot form a receipt.
        (bundle/'preflight.json').unlink(missing_ok=True)
        raise


def inspect_plan(root,project,receipt):
    """Pure private-file verification shared by review and execution constructor.

    No callbacks, Docker, journal, owner authority or live acceptance involved.
    Only the exact immutable prepared application-only inputs are checked.
    """
    root=pathlib.Path(root).resolve();receipt=pathlib.Path(receipt).absolute()
    if not isinstance(project,str)or not re.fullmatch(state.PROJECT,project):
        raise ValueError('Exact existing project required.')
    plan=state.read_private(receipt)
    if (plan.get('scope')!='application_bootstrap'or plan.get('version')!=1
            or plan.get('root')!=str(root)or plan.get('project')!=project
            or plan.get('status')!='preflight_only'or plan.get('activation_allowed')is not False):
        raise ValueError('Exact private application-only plan required, not authority.')
    files=plan.get('files');state.check_manifest(files)
    previous=receipt.parent/'previous-runtime.json';candidate=receipt.parent/'candidate-runtime.json'
    # A manifest without the runtime paths must never pass by hashing unrelated
    # private files. The same check protects both constructor and menu review.
    for path in [previous,candidate]:
        if files.get(str(path))!=state.digest(path):
            raise ValueError('Exact runtime file missing from application plan manifest.')
    old,new=state.read_private(previous),state.read_private(candidate)
    recovery=plan.get('previous_files');state.check_manifest(recovery)
    required={str(previous):state.digest(previous)}
    for service in old['services'].values():
        for mount in service.get('volumes',[]):
            if mount.get('type')=='bind'and mount.get('read_only'):
                path=pathlib.Path(mount['source'])
                required[str(path)]=state.digest(path)
    if recovery!=required or files!={**required,str(candidate):state.digest(candidate)}:
        raise ValueError('Exact runtime and recovery dependency manifests required.')
    user,db,writers=database_target(old)
    if writers!=plan.get('writers')or plan.get('database')!={'user':user,'name':db}:
        raise ValueError('Application writer/database target changed.')
    images={name:item['image']for name,item in old['services'].items()}
    if (not isinstance(plan.get('current_services'),dict)or set(plan['current_services'])!=set(images)
            or any(not isinstance(plan['current_services'][name],dict)or plan['current_services'][name].get('image')!=image for name,image in images.items())):
        raise ValueError('Recorded current image set differs from application plan.')
    app.pair(old,new,images,{name:item['image']for name,item in new['services'].items()})
    return {'plan':plan,'previous':old,'candidate':new,'user':user,'database':db,'writers':writers}


class DockerApplication:
    """Actual effects with mandatory authority/acceptance callbacks.

    authorize(action,project,previous,candidate) must establish trusted caller
    permission AND prevention of outside database writers reconnecting during
    maintenance. accept(value,target) and compatible(value) must establish real
    runtime retained-data behavior/old-image compatibility, not generic booleans.
    This class cannot invent those proofs from receipts or filename counts.
    """
    def __init__(self,root,project,receipt,authorize,accept,compatible,execute=subprocess.run,maintenance_operator=None,runtime_group=None):
        if not all(callable(x) for x in [authorize,accept,compatible]):raise ValueError('Explicit authority, runtime and previous-compatibility verifiers required.')
        self.root=pathlib.Path(root).resolve();self.project=project;self.receipt=pathlib.Path(receipt).absolute()
        self.authority,self.acceptance,self.compatibility=authorize,accept,compatible;self.execute=execute
        if maintenance_operator is not None and not callable(maintenance_operator):raise ValueError('Explicit controlled maintenance operator required.')
        self.maintenance_operator=maintenance_operator
        if runtime_group is not None and not all(callable(getattr(runtime_group,key,None))for key in ['start','verify']):
            raise ValueError('Explicit grouped application launch and exact definition verifier required.')
        self.runtime_group=runtime_group
        self.selection_reader=state.selection
        checked=inspect_plan(self.root,project,self.receipt)
        self.plan,self.old,self.new=checked['plan'],checked['previous'],checked['candidate']
        self.previous=self.receipt.parent/'previous-runtime.json';self.candidate=self.receipt.parent/'candidate-runtime.json'
        self.user,self.db,self.writers=checked['user'],checked['database'],checked['writers']
    def call(self,args,binary=False,input=None):
        result=self.execute(args,cwd=self.root,capture_output=True,text=not binary,input=input)
        if result.returncode:raise ValueError('Application Docker operation failed; inspect private local diagnostics. No success proved.')
        return result.stdout
    def compose(self,path,*args,binary=False,input=None):
        return self.call(['docker','compose','-p',self.project,'-f',str(path),*args],binary,input)
    def containers(self,path):
        ids=self.compose(path,'ps','-a','-q').split()
        return json.loads(self.call(['docker','inspect',*ids])) if ids else []
    def verify_definition(self,service,item,image):
        if item.get('Config',{}).get('Labels',{}).get('hexthings.startgate'):
            if self.runtime_group is None:raise ValueError('Unconfigured application process gate cannot replace direct runtime verification.')
            if self.runtime_group.verify(self,service,item,image)is not None:
                raise ValueError('Exact grouped runtime verifier must raise, not return approval.')
        else:verify_container_definition(service,item,image)
    def checked_containers(self,path,running):
        found={}
        for item in self.containers(path):
            labels=item.get('Config',{}).get('Labels',{});name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in found:raise ValueError('Unexpected project or replica during application transaction.')
            found[name]=item
        expected=state.read_private(path)['services']
        if set(found)!=set(expected):raise ValueError('Current service set changed or is unavailable.')
        for name,item in found.items():
            if item.get('Image')!=expected[name]['image']:raise ValueError('Current immutable application image changed.')
            metadata=json.loads(self.call(['docker','image','inspect',item['Image']]))
            if len(metadata)!=1:raise ValueError('Exact image definition unavailable.')
            self.verify_definition(expected[name],item,metadata[0])
            if running and (item['State']['Status']!='running' or item['State'].get('Health',{}).get('Status','healthy')!='healthy'):raise ValueError('Current application runtime is not healthy.')
        return found
    def authorize(self,action,project,previous,candidate):
        if project!=self.project or pathlib.Path(previous)!=self.previous or pathlib.Path(candidate)!=self.candidate:
            raise ValueError('Application target changed.')
        self.authority(action,project,previous,candidate)
    def revalidate(self,project,receipt):
        if project!=self.project or pathlib.Path(receipt)!=self.receipt or state.read_private(receipt)!=self.plan:raise ValueError('Application receipt changed.')
        state.check_manifest(self.plan['files'])
        found=self.checked_containers(self.previous,True)
        current={name:{'image':item['Image'],'container_id':item['Id']}for name,item in found.items()}
        if current!=self.plan['current_services']:raise ValueError('Application containers changed after preparation.')
        # Check the original selection/config again, not only its frozen snapshot.
        selected=self.selection_reader(self.root)
        command=['docker','compose','-p',self.project]
        if selected:command+=['-f',selected['config']]
        config=json.loads(self.call(command+['config','--format','json']))
        frozen,_=app.runtime.freeze(config,{name:item['Image']for name,item in found.items()})
        if frozen!=self.old:raise ValueError('Live resolved Compose changed after preparation.')
        self.verify_recovery_inputs({'files':self.plan['files'],'previous_images':{name:s['image']for name,s in self.old['services'].items()}})
        for image in set(s['image']for s in self.new['services'].values()):self.check_image(image)
    def check_image(self,image):
        found=json.loads(self.call(['docker','image','inspect',image]))
        if len(found)!=1 or found[0].get('Id')!=image:raise ValueError('Exact local runtime image unavailable; no pull or build attempted.')
    def backup(self,project,previous):
        raw=self.compose(previous,'exec','-T','postgres','pg_dump','-U',self.user,'-Fc','--dbname='+self.db,binary=True)
        self.compose(previous,'exec','-T','postgres','pg_restore','-l',binary=True,input=raw)
        fd,name=tempfile.mkstemp(prefix='application-backup-',suffix='.dump',dir=self.receipt.parent)
        with os.fdopen(fd,'wb')as output:output.write(raw);output.flush();os.fsync(output.fileno())
        state.sync_directory(self.receipt.parent)
        return {'path':name,'sha256':state.digest(name),'verified_archive':True}
    def verify_partial_runtime(self):
        found={}
        for item in self.containers(self.previous):
            labels=item.get('Config',{}).get('Labels',{});name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in found or name not in self.old['services']:
                raise ValueError('Unclassified current container during application recovery.')
            found[name]=item
            image=item.get('Image')
            allowed={self.old['services'][name]['image'],self.new['services'][name]['image']}
            if image not in allowed:raise ValueError('Current partial runtime uses an unplanned image.')
            metadata=json.loads(self.call(['docker','image','inspect',image]))
            if len(metadata)!=1 or metadata[0].get('Id')!=image:raise ValueError('Current partial image cannot be inspected.')
            service=self.new['services'][name] if image==self.new['services'][name]['image'] else self.old['services'][name]
            self.verify_definition(service,item,metadata[0])
        for name in set(self.old['services'])-set(self.writers):
            if name not in found or found[name]['State']['Status']!='running':
                raise ValueError('Existing data store/non-writer unavailable during application recovery.')
        return found
    def prepare_stop(self):
        hook=getattr(self.runtime_group,'before_stop',None)
        if hook is not None:
            if not callable(hook)or hook()is not None:raise ValueError('Explicit pre-stop admission hook must raise on failure.')
    def stop_writers(self,value):
        self.prepare_stop()
        self.verify_partial_runtime()
        self.compose(value['previous'],'stop',*self.writers)
    def verify_writers_stopped(self,value):
        found={}
        for item in self.containers(self.previous):
            labels=item['Config']['Labels'];name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in found:raise ValueError('Unexpected application writer/project.')
            found[name]=item
        if set(found)!=set(self.old['services']):raise ValueError('Service inventory changed during maintenance.')
        for name in self.writers:
            if found[name]['State']['Status'] not in {'exited','created'}:raise ValueError('A database writer is still active; schema operation refused.')
        for name in set(found)-set(self.writers):
            if found[name]['Image']!=self.old['services'][name]['image'] or found[name]['State']['Status']!='running':raise ValueError('Non-writer runtime changed during application maintenance.')
        # Rebind actual definitions immediately before any schema operator, too.
        self.verify_partial_runtime()
    def operator(self,value,action):
        self.verify_writers_stopped(value)
        if self.maintenance_operator is not None:
            result=self.maintenance_operator(value,action)
            app.check_schema(result);return result
        args=['run','--rm','--no-deps','--pull','never','--entrypoint','/bin/setupctl','api',action]
        acknowledgement=None
        if action=='apply-reviewed':acknowledgement=json.dumps({'version':1,'scope':'application_bootstrap','writers_stopped':True,'backup_verified':True})
        output=self.compose(value['candidate'],*args,input=acknowledgement)
        result=json.loads(output);app.check_schema(result);return result
    def apply_reviewed_schema(self,value):
        app.transport.check_backup(value['backup'])
        if value.get('schema_attempted')!=app.ADDED:raise ValueError('Durable reviewed schema attempt required before invoking operator.')
        self.operator(value,'apply-reviewed')
    def inspect_reviewed_schema(self,value):return self.operator(value,'inspect-reviewed')
    def start_candidate(self,value):
        if self.runtime_group is not None:return self.runtime_group.start(self,value,'candidate')
        self.compose(value['candidate'],'up','-d','--no-deps','--no-build','--pull','never',*self.writers)
    def stop_candidate(self,value):
        self.prepare_stop()
        self.verify_partial_runtime()
        self.compose(value['candidate'],'stop',*self.writers)
    def verify_recovery_inputs(self,value):
        state.check_manifest(value['files'])
        for image in set(value['previous_images'].values()):self.check_image(image)
        self.verify_partial_runtime()
    def verify_previous_schema_compatibility(self,value):self.compatibility(value)
    def restore_previous(self,value):
        if self.runtime_group is not None:return self.runtime_group.start(self,value,'previous')
        self.compose(value['previous'],'up','-d','--no-deps','--no-build','--pull','never',*self.writers)
    def accept(self,value,target):
        self.acceptance(value,target)
        self.checked_containers(value[target],True)
