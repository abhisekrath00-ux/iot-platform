#!/usr/bin/env python3
"""Read-only private deployment preflight. Never activate, restart or reload.
The private receipt binds exact Compose, certificate, ACL and current image
facts. It is evidence for a later approved activation, not authority to act.
"""
import argparse, hashlib, importlib.util, json, os, pathlib, re, subprocess, sys, tempfile

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('secure_setup',HERE/'secure-setup.py');prep=importlib.util.module_from_spec(spec);spec.loader.exec_module(prep)
spec=importlib.util.spec_from_file_location('secure_runtime',HERE/'secure-runtime.py');runtime=importlib.util.module_from_spec(spec);spec.loader.exec_module(runtime)
def sha(b):return hashlib.sha256(b).hexdigest()
def canonical(o):return json.dumps(o,sort_keys=True,separators=(',',':')).encode()
def read_json(path):return json.loads(pathlib.Path(path).read_text())
def repo_digest(reference):
 # Docker RepoDigests omit an optional tag in repository:tag@digest. Keep
 # registry port, exact repository and exact digest; do not accept another
 # repository merely because its bytes/digest happen to match.
 repository,sep,digest=reference.partition('@')
 if not sep or not re.fullmatch(r'sha256:[a-f0-9]{64}',digest):
  raise ValueError('Exact registry SHA256 digest required.')
 prefix,slash,name=repository.rpartition('/')
 name=name.split(':',1)[0]
 if not name:raise ValueError('Registry repository required.')
 return (prefix+slash+name)+'@'+digest
def private_file(path):
 p=pathlib.Path(path)
 if p.is_symlink() or not p.is_file() or p.stat().st_mode&0o077:raise ValueError('Private preflight input must be a regular owner-only file.')
 return p.read_bytes()
def issuer_source(config):
 api=config['services']['api'];target=api.get('environment',{}).get('MTLS_CA_CERT')
 if not target:return None
 for v in api.get('volumes',[]):
  if v.get('type')=='bind' and v.get('target')==target:return pathlib.Path(v['source'])
 raise ValueError('Existing device issuer is not an inspectable exact file bind. No activation can be approved.')
def validate_current(base,current,containers,project):
 if canonical(base)!=canonical(current):raise ValueError('Current Compose differs from preparation. Prepare a new bundle; no deployment changed.')
 expected=set(base['services'])
 running={}
 for c in containers:
  labels=c.get('Config',{}).get('Labels',{})
  if labels.get('com.docker.compose.project')!=project:raise ValueError('Container belongs to a different Compose project.')
  service=labels.get('com.docker.compose.service')
  if c.get('State',{}).get('Status')!='running':raise ValueError('All current project containers must be running before this preflight.')
  if c.get('State',{}).get('Health',{}).get('Status','healthy')!='healthy':raise ValueError('Current service is not healthy; do not migrate it.')
  if service in running:raise ValueError('Replicated services are not supported by this single-node migration.')
  if not re.fullmatch(r'sha256:[a-f0-9]{64}',c.get('Image','')):raise ValueError('Current immutable image ID is unavailable.')
  running[service]={'image':c['Image'],'container_id':c['Id']}
 if not {'api','ingest','mosquitto','web','postgres'}<=set(running) or set(running)-expected:raise ValueError('Current single-node service set is incomplete or changed.')
 return running

def preflight(bundle,project,root,execute=subprocess.run):
 bundle=pathlib.Path(bundle).resolve();root=pathlib.Path(root).resolve()
 # Any retry invalidates the prior diagnostic receipt before rechecking.
 for stale in ['preflight.json','previous-runtime.json','candidate-runtime.json']:(bundle/stale).unlink(missing_ok=True)
 if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',project):raise ValueError('Explicit existing Compose project name required.')
 inputs=read_json(bundle/'prepare-inputs.json');base=json.loads(private_file(bundle/'previous-compose.json'));candidate=json.loads(private_file(bundle/'compose.json'))
 expected=prep.render(base,inputs['bind'],inputs['host'],inputs['certs'],bundle,inputs['shared_gid'])
 if canonical(candidate)!=canonical(expected):raise ValueError('Prepared candidate changed; prepare it again.')
 certs=pathlib.Path(inputs['certs']);prep.check_certificate(certs,'broker','broker-ca.crt','sslserver',('mosquitto',inputs['host']));prep.check_certificate(certs,'https','https-ca.crt','sslserver',(inputs['host'],))
 for name in ['api','ingest']:prep.check_certificate(certs,name,'device-ca.crt','sslclient')
 def call(args):
  p=execute(args,cwd=root,capture_output=True,text=True)
  if p.returncode:raise ValueError('Read-only Docker preflight failed. No services changed; inspect local Docker/config access.')
  return p.stdout
 compose=['docker','compose','-p',project]
 current=json.loads(call(compose+['config','--format','json']))
 ids=call(compose+['ps','-q']).split()
 if not ids:raise ValueError('No running current Compose project found.')
 containers=json.loads(call(['docker','inspect',*ids]))
 running=validate_current(base,current,containers,project)
 # Inspect only local images. A missing transport image is a preparation
 # prerequisite, not authority to pull it. Current app IDs cannot change.
 images={name:item['image'] for name,item in running.items()}
 for image in sorted(set(images.values())):
  found=json.loads(call(['docker','image','inspect',image]))
  if len(found)!=1 or found[0].get('Id')!=image:raise ValueError('A running immutable image is no longer available locally for rollback.')
 proposed=dict(images)
 for name in ['mosquitto','secure-web']:
  reference=candidate['services'][name]['image']
  found=json.loads(call(['docker','image','inspect',reference]))
  if len(found)!=1 or not re.fullmatch(r'sha256:[a-f0-9]{64}',found[0].get('Id','')):raise ValueError('Prepared transport image is not available as one local immutable image. No pull/build performed.')
  if '@sha256:' in reference and repo_digest(reference) not in [repo_digest(item) for item in found[0].get('RepoDigests',[])]:raise ValueError('Local broker image does not prove the prepared registry digest.')
  proposed[name]=found[0]['Id']
 plans=runtime.pair(base,candidate,images,proposed)
 call(['docker','compose','-p',project,'-f',str(bundle/'compose.json'),'config','--quiet'])
 # Only read through the current operator binary; never docker-run a candidate
 # image, build/pull, launch a service or silently fall back to a SQL query.
 acl=call(compose+['exec','-T','api','/bin/tenantctl','broker-acl-export'])
 if not acl.startswith('# hexmon broker ACL'):raise ValueError('Current API has no verified full ACL exporter. Upgrade its code safely first; no preflight receipt produced.')
 count=len(re.findall(r'^user ',acl,re.M))-2
 if count<0:raise ValueError('Invalid ACL snapshot.')
 oldca=issuer_source(base)
 if count:
  if oldca is None:raise ValueError('Existing certificate gateways have no verifiable current issuer mount. Do not replace their broker trust.')
  old=subprocess.run(['openssl','x509','-in',str(oldca),'-outform','DER'],capture_output=True)
  new=subprocess.run(['openssl','x509','-in',str(certs/'device-ca.crt'),'-outform','DER'],capture_output=True)
  if old.returncode or new.returncode or old.stdout!=new.stdout:raise ValueError('Device issuer changed or cannot be verified. Existing gateway certificates could be disconnected; no migration receipt produced.')
 # No live broker/config files changed. These private plans contain resolved
 # secrets. They pin available local image IDs, but cannot authorize activation.
 for name,value in [('previous-runtime.json',plans['previous']),('candidate-runtime.json',plans['candidate'])]:
  path=bundle/name
  fd,tmp=tempfile.mkstemp(prefix='.'+name+'-',dir=bundle)
  try:
   with os.fdopen(fd,'w')as f:json.dump(value,f,indent=2);f.flush();os.fsync(f.fileno())
   os.replace(tmp,path)
  finally:
   if os.path.exists(tmp):os.unlink(tmp)
  call(['docker','compose','-p',project,'-f',str(path),'config','--quiet'])
 # Persist the receipt last, so partial diagnostic output cannot be accepted.
 files={}
 for name in ['compose.json','previous-compose.json','previous-runtime.json','candidate-runtime.json','prepare-inputs.json','broker.conf','https.conf','broker-acl-supervisor.sh']:
  p=bundle/name;files[str(p)]=sha(p.read_bytes())
 for p in certs.glob('*.crt'):files[str(p.resolve())]=sha(p.read_bytes())
 for name in ['device-ca.key','broker.key','https.key','api.key','ingest.key']:files[str((certs/name).resolve())]=sha((certs/name).read_bytes())
 previous_files={str(bundle/'previous-runtime.json'):sha((bundle/'previous-runtime.json').read_bytes())}
 for service in plans['previous']['services'].values():
  for mount in service.get('volumes',[]):
   if mount.get('type')=='bind' and mount.get('read_only'):
    path=pathlib.Path(mount['source'])
    if path.is_symlink() or not path.is_file() or path.stat().st_mode&0o022:raise ValueError('Prior read-only bind is not an inspectable stable regular file; no rollback receipt produced.')
    previous_files[str(path)]=sha(path.read_bytes())
 receipt={'version':1,'status':'preflight_only','activation_allowed':False,'project':project,'root':str(root),'bundle':str(bundle),'gateway_count':count,'acl_sha256':sha(acl.encode()),'files':files,'current_services':running,'previous_files':previous_files,'proposed_images':proposed,'runtime_configs':{'previous':str(bundle/'previous-runtime.json'),'candidate':str(bundle/'candidate-runtime.json')},'disabled_profiles':plans['disabled_profiles'],'limitations':['No container restart, broker reload, trust installation or network change.','Immutable runtime plans are diagnostics; service activation, runtime rollback and restored selection are not implemented.','This is not proof of edge reachability, source apply or physical meter readings.']}
 path=bundle/'preflight.json'
 fd,tmp=tempfile.mkstemp(prefix='.preflight-',dir=bundle)
 try:
  with os.fdopen(fd,'w')as f:json.dump(receipt,f,indent=2);f.flush();os.fsync(f.fileno())
  os.replace(tmp,path)
 finally:
  if os.path.exists(tmp):os.unlink(tmp)
 print('Read-only preflight recorded privately. No deployment changed. Activation/rollback remain unavailable.')
 return receipt

if __name__=='__main__':
 p=argparse.ArgumentParser(description=__doc__);p.add_argument('--bundle',required=True);p.add_argument('--project',required=True);p.add_argument('--root',required=True)
 try:preflight(**vars(p.parse_args()))
 except (ValueError,OSError,subprocess.SubprocessError,KeyError)as e:print(str(e),file=sys.stderr);sys.exit(1)
