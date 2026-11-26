#!/usr/bin/env python3
"""Read-only local Docker metadata for desktop preparation. Never pull or run.

The returned allowlist contains project names, immutable image IDs and platform
only, never container environment, mounts, labels, tags or inspect diagnostics.
Platform match is not application compatibility or candidate provenance.
"""
import importlib.util,json,os,pathlib,re,subprocess
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('state',HERE/'secure-state.py');state=importlib.util.module_from_spec(spec);spec.loader.exec_module(state)
ERROR='Local inventory unavailable or changed. Check local Docker access and the installed Compose project. No files or services changed.'
IMAGE=re.compile(r'sha256:[a-f0-9]{64}')
class LocalTransport:
    """One verified local endpoint and private environment for a whole plan.

    No endpoint is included in repr, UI results or plan receipts. The command
    seam is also used by preparation's existing execute injection.
    """
    def __init__(self,root,execute=None):
        self._execute=execute or subprocess.run;self._environment=dict(os.environ)
        try:
            result=self._execute(['docker','context','inspect'],cwd=pathlib.Path(root).resolve(),capture_output=True,text=True,timeout=20,env=dict(self._environment))
            if result.returncode:raise ValueError(ERROR)
            contexts=json.loads(result.stdout)
            if len(contexts)!=1:raise ValueError(ERROR)
            host=contexts[0]['Endpoints']['docker']['Host']
            local_unix=isinstance(host,str)and host.startswith('unix:///')and len(host)>len('unix:///')and not host.startswith('unix:////')
            local_pipe=isinstance(host,str)and re.fullmatch(r'npipe:////\./pipe/[a-zA-Z0-9_.-]+',host)is not None
            if not (local_unix or local_pipe):raise ValueError(ERROR)
            self._endpoint=host
            for key in ['DOCKER_CONTEXT','DOCKER_HOST','DOCKER_TLS','DOCKER_TLS_VERIFY','DOCKER_CERT_PATH']:
                self._environment.pop(key,None)
        except (OSError,subprocess.SubprocessError,ValueError,KeyError,TypeError,AttributeError):
            raise ValueError(ERROR)from None
    def execute(self,args,**kwargs):
        if not args or args[0]!='docker' or any(x in args[1:]for x in ['--host','-H','--context','-c']):
            raise ValueError(ERROR)
        # This private transport owns destination/environment, never caller input.
        kwargs['env']=dict(self._environment);kwargs.setdefault('timeout',20)
        try:return self._execute(['docker','--host',self._endpoint,*args[1:]],**kwargs)
        except (OSError,subprocess.SubprocessError):raise ValueError(ERROR)from None

def inventory(root,execute=subprocess.run):
    return scan(root,LocalTransport(root,execute))

def scan(root,transport):
    root=pathlib.Path(root).resolve()
    def call(args):
        p=transport.execute(args,cwd=root,capture_output=True,text=True)
        if p.returncode:raise ValueError(ERROR)
        return p.stdout
    try:
        selected=state.selection(root)
        config=pathlib.Path(selected['config'])if selected else root/'docker-compose.yml'
        if config.is_symlink()or not config.is_file():raise ValueError(ERROR)
        ids=call(['docker','ps','--all','--quiet','--no-trunc','--filter','label=com.docker.compose.project']).split()
        if len(set(ids))!=len(ids)or any(not re.fullmatch(r'[a-f0-9]{64}',i)for i in ids):raise ValueError(ERROR)
        if not ids:return {'projects':[],'images':[]}
        containers=json.loads(call(['docker','inspect',*ids]));groups={}
        if len(containers)!=len(ids) or {c['Id']for c in containers}!=set(ids):raise ValueError(ERROR)
        for c in containers:
            labels=c.get('Config',{}).get('Labels',{})or {};project=labels.get('com.docker.compose.project','')
            if re.fullmatch(state.PROJECT,project):groups.setdefault(project,[]).append(c)
        projects=[]
        for name,items in sorted(groups.items()):
            if selected and name!=selected['project']:continue
            rows=[];services=set();valid=True
            for c in items:
                labels=c.get('Config',{}).get('Labels',{});service=labels.get('com.docker.compose.service','')
                # Exact labels bind this installation, not an unrelated same-name project.
                if (labels.get('com.docker.compose.project.working_dir')!=str(root)
                    or labels.get('com.docker.compose.project.config_files')!=str(config)
                    or labels.get('com.docker.compose.oneoff','False').lower()!='false'
                    or not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',service)
                    or service in services or not IMAGE.fullmatch(c.get('Image',''))
                    or c.get('State',{}).get('Status')!='running'
                    or ('Health'in c.get('State',{})and (not isinstance(c['State']['Health'],dict)or c['State']['Health'].get('Status')!='healthy'))):
                    valid=False;break
                services.add(service);rows.append({'service':service,'id':c['Id'],'image':c['Image']})
            if valid and {'api','ingest','mcp','postgres','mosquitto','web'}<=services:
                projects.append({'name':name,'containers':sorted(rows,key=lambda r:r['service'])})
        image_ids=sorted(set(call(['docker','image','ls','--no-trunc','--quiet']).split()))
        if any(not IMAGE.fullmatch(i)for i in image_ids):raise ValueError(ERROR)
        images=[]
        if image_ids:
            for i in json.loads(call(['docker','image','inspect',*image_ids])):
                ident=i.get('Id','');osname=i.get('Os','');arch=i.get('Architecture','');variant=i.get('Variant','')
                if ident not in image_ids or not all(isinstance(x,str) and re.fullmatch(r'[a-zA-Z0-9_.-]{0,32}',x)for x in [osname,arch,variant])or not osname or not arch:raise ValueError(ERROR)
                images.append({'id':ident,'platform':'/'.join(x for x in [osname,arch,variant]if x)})
        if len(images)!=len(image_ids)or len({i['id']for i in images})!=len(image_ids):raise ValueError(ERROR)
        platforms={i['id']:i['platform']for i in images}
        for project in projects:
            api=next(c['image']for c in project['containers']if c['service']=='api')
            project['platform']=platforms.get(api)
            if not project['platform']:raise ValueError(ERROR)
        return {'projects':projects,'images':sorted(images,key=lambda r:r['id'])}
    except (OSError,subprocess.SubprocessError,ValueError,KeyError,TypeError,AttributeError):
        raise ValueError(ERROR)from None

def choice(value,project,image):
    found=[p for p in value['projects']if p['name']==project]
    if len(found)!=1 or not any(i['id']==image and i['platform']==found[0]['platform']for i in value['images']):
        raise ValueError('Choose an inventoried project and same-platform local immutable image. Refresh first if unavailable.')
    return {'project':found[0],'image':next(i for i in value['images']if i['id']==image)}
