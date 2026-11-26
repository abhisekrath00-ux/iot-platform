#!/usr/bin/env python3
"""Private ownership ledger for this opt-in disposable preparation fixture only."""
import json,os,pathlib,secrets,subprocess,sys,time
LABEL='hexthings.preparation.owner'
class Owner:
    def __init__(self,path,execute=subprocess.run):
        self.path=pathlib.Path(path);self.execute=execute
        st=self.path.lstat()
        if not self.path.is_file() or self.path.is_symlink() or st.st_mode&0o077 or st.st_uid!=os.geteuid():raise ValueError('Unsafe fixture ownership ledger')
        self.data=json.loads(self.path.read_text())
        if self.data.get('version')!=1 or len(self.data.get('token',''))!=48:raise ValueError('Invalid fixture ownership ledger')
        self.deadline=None
    @classmethod
    def create(cls,path,execute=subprocess.run):
        path=pathlib.Path(path)
        tmp=path.with_name(path.name+'.initial-'+secrets.token_hex(8))
        fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        try:
            with os.fdopen(fd,'w')as f:
                json.dump({'version':1,'token':secrets.token_hex(24),'projects':[],'images':[],'operators':[],'volumes':[],'status':'active','pending':[]},f);f.flush();os.fsync(f.fileno())
            os.link(tmp,path)  # Atomic create, refuses an existing ledger.
            fd=os.open(path.parent,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
        finally:tmp.unlink()
        return cls(path,execute)
    def save(self):
        tmp=self.path.with_name(self.path.name+'.new')
        fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        try:
            with os.fdopen(fd,'w')as f:json.dump(self.data,f);f.flush();os.fsync(f.fileno())
            os.replace(tmp,self.path)
            fd=os.open(self.path.parent,os.O_DIRECTORY);os.fsync(fd);os.close(fd)
        finally:
            if tmp.exists():tmp.unlink()
    def run(self,args):
        timeout=180 if self.deadline is None else min(20,self.deadline-time.monotonic())
        if timeout<=0:raise ValueError('Fixture cleanup deadline exceeded')
        r=self.execute(['docker',*args],capture_output=True,text=True,timeout=timeout)
        if r.returncode:raise ValueError('Fixture Docker check failed: '+str(args[:2]))
        return r.stdout
    def ids(self,kind,filters=()):
        args=([kind,'ls','-q']if kind!='container'else['ps','-aq'])
        for f in filters:args+=['--filter',f]
        return self.run(args).split()
    def inspect(self,kind,name):return json.loads(self.run([kind,'inspect',name]))[0]
    def labels(self,item,kind):return (item.get('Config',{}).get('Labels',{})if kind in ('container','image')else item.get('Labels',{}))or{}
    def owned(self,item,kind):
        if self.labels(item,kind).get(LABEL)!=self.data['token']:raise ValueError('Fixture ownership mismatch; retained evidence')
    def register_project(self,project):
        if project in self.data['projects']:raise ValueError('Duplicate fixture project')
        if self.ids('container',['label=com.docker.compose.project='+project])or self.ids('network',['name=^'+project+'_default$']):raise ValueError('Fixture project not absent')
        self.data['projects'].append(project);self.save()
    def register_images(self,tags):
        for tag in tags:
            if self.ids('image',['reference='+tag]):raise ValueError('Fixture image not absent')
        self.data['images']+=list(tags);self.save()
    def operator(self,project):
        if project not in self.data['projects']:raise ValueError('Unregistered fixture project')
        name=project+'-operator-'+str(len(self.data['operators']))
        if self.ids('container',['name=^/'+name+'$']):raise ValueError('Fixture operator not absent')
        self.data['operators'].append(name);self.save();return name
    def begin(self,kind):
        self.data=json.loads(self.path.read_text())
        key=kind+'-'+secrets.token_hex(8)
        self.data.setdefault('pending',[]).append(key);self.save();return key
    def complete(self,key):
        self.data=json.loads(self.path.read_text())
        self.data['pending'].remove(key);self.save()
    def decorate(self,base,project):
        for service in base['services'].values():service.setdefault('labels',{})[LABEL]=self.data['token']
        base['networks']={'default':{'labels':{LABEL:self.data['token']}}}
        # Pre-pull the same fixture base images that compose up would pull.
        # Give EVERY image-declared volume an owned named volume before start,
        # so even an interruption between volume and container creation is known.
        base['volumes']={}
        for service_name,service in base['services'].items():
            service.pop('volumes',None)  # Replace inherited parent fixture mounts.
            image=service['image']
            if not self.ids('image',['reference='+image]):self.run(['pull',image])
            declared=self.inspect('image',image).get('Config',{}).get('Volumes',{})or{}
            for index,target in enumerate(sorted(declared)):
                key=service_name+'-data-'+str(index);volume=project+'-'+key
                if self.ids('volume',['name=^'+volume+'$']):raise ValueError('Fixture volume not absent')
                self.data['volumes'].append(volume);self.save()
                base['volumes'][key]={'name':volume,'labels':{LABEL:self.data['token']}}
                service.setdefault('volumes',[]).append(key+':'+target)
    def cleanup(self):
        self.deadline=time.monotonic()+120;errors=[]
        try:
            containers=set()
            for project in self.data['projects']:containers.update(self.ids('container',['label=com.docker.compose.project='+project]))
            for name in self.data['operators']:containers.update(self.ids('container',['name=^/'+name+'$']))
            # Inspect every candidate before any deletion. Attached volume IDs
            # are durable before rm and never inferred from a global prefix.
            for cid in sorted(containers):
                item=self.inspect('container',cid);self.owned(item,'container')
                for mount in item.get('Mounts',[]):
                    if mount.get('Type')=='volume':
                        volume=mount['Name']
                        others=set(self.ids('container',['volume='+volume]))-containers
                        if others:raise ValueError('Fixture volume has nonowned users')
                        if volume not in self.data['volumes']:raise ValueError('Unregistered fixture volume')
                        self.owned(self.inspect('volume',volume),'volume')
            self.save()
            for cid in sorted(containers):
                self.owned(self.inspect('container',cid),'container');self.run(['container','rm','-f',cid])
            for volume in self.data['volumes']:
                if self.ids('volume',['name=^'+volume+'$']):
                    self.owned(self.inspect('volume',volume),'volume')
                    if self.ids('container',['volume='+volume]):raise ValueError('Fixture volume still in use')
                    self.run(['volume','rm',volume])
            for project in self.data['projects']:
                for nid in self.ids('network',['name=^'+project+'_default$']):
                    self.owned(self.inspect('network',nid),'network');self.run(['network','rm',nid])
            for tag in self.data['images']:
                if self.ids('image',['reference='+tag]):
                    self.owned(self.inspect('image',tag),'image');self.run(['image','rm',tag])
            # Independent second inventory, including all recorded resources.
            for project in self.data['projects']:
                if self.ids('container',['label=com.docker.compose.project='+project])or self.ids('network',['name=^'+project+'_default$']):raise ValueError('Project cleanup absence unverified')
            for name in self.data['operators']:
                if self.ids('container',['name=^/'+name+'$']):raise ValueError('Operator cleanup absence unverified')
            for tag in self.data['images']:
                if self.ids('image',['reference='+tag]):raise ValueError('Image cleanup absence unverified')
            for volume in self.data['volumes']:
                if self.ids('volume',['name=^'+volume+'$']):raise ValueError('Volume cleanup absence unverified')
        except Exception as error:errors.append(type(error).__name__+': '+str(error))
        if self.data.get('pending'):errors.append('Interrupted creation command: daemon completion cannot be proven; do not claim absence')
        self.data['status']='unresolved'if errors else'absent';self.data['errors']=errors;self.save()
        if errors:raise ValueError('Fixture cleanup unresolved; private ledger retained')
        print('PREPARATION_CLEANUP verified exact owned resources absent; ledger retained',flush=True)
def current():
    path=os.environ.get('HEXTHINGS_CLEANUP_MANIFEST')
    return Owner(path)if path else None
def diagnostics(owner,args,execute=subprocess.run):
    try:execute(args,capture_output=True,text=True,timeout=15)
    except Exception:print('Fixture diagnostics unavailable; cleanup continues',flush=True)
if __name__=='__main__':
    path=pathlib.Path(sys.argv[1])
    if path.exists():Owner(path).cleanup()
    else:print('No ledger: fixture setup never registered; no teardown claimed')
