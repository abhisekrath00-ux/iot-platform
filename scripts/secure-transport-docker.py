#!/usr/bin/env python3
"""Internal production transport effects, no CLI or owner/menu activation.

Caller-supplied current target/route/trust/downtime/maintenance authority and
real HTTPS/broker/per-gateway runtime acceptance are mandatory, not receipts.
Application images and schema cannot change in this transport transaction.
"""
import importlib.util
import json
import os
import pathlib
import tempfile

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

application=load('secure-application-docker');state=application.state
runtime=application.app.runtime;preflight=application.preflight

class DockerTransport:
    def __init__(self,root,project,receipt,authorize,accept,execute=None):
        if not all(callable(x) for x in [authorize,accept]):
            raise ValueError('Explicit transport authority and runtime acceptance verifiers required.')
        self.root=pathlib.Path(root).resolve();self.project=project
        self.receipt=pathlib.Path(receipt).absolute();self.bundle=self.receipt.parent
        self.authority,self.acceptance=authorize,accept
        self.selection_reader=state.selection
        self.execute=execute or application.subprocess.run
        self.plan=state.read_private(self.receipt)
        if (self.bundle.parent!=self.root or self.plan.get('version')!=1
                or self.plan.get('status')!='preflight_only' or self.plan.get('activation_allowed') is not False
                or self.plan.get('root')!=str(self.root) or self.plan.get('project')!=project
                or self.plan.get('bundle')!=str(self.bundle)
                or self.plan.get('scope') not in {None,'transport_only'}):
            raise ValueError('Exact private transport plan required, not authority.')
        self.previous=self.bundle/'previous-runtime.json';self.candidate=self.bundle/'candidate-runtime.json'
        if self.plan.get('runtime_configs')!={'previous':str(self.previous),'candidate':str(self.candidate)}:
            raise ValueError('Transport runtime paths changed.')
        state.check_manifest(self.plan['files'])
        self.old=state.read_private(self.previous);self.new=state.read_private(self.candidate)
        images={name:item['image']for name,item in self.old['services'].items()}
        proposed={name:item['image']for name,item in self.new['services'].items()}
        pair=runtime.pair(self.old,self.new,images,proposed)
        if pair['previous']!=self.old or pair['candidate']!=self.new or self.plan.get('proposed_images')!=proposed:
            raise ValueError('Immutable transport-only runtime pair required.')
        self.user,self.db,self.writers=application.database_target(self.old)
        newuser,newdb,newwriters=application.database_target({**self.new,'services':{name:value for name,value in self.new['services'].items()if name!='secure-web'}})
        if (newuser,newdb,newwriters)!=(self.user,self.db,self.writers):raise ValueError('Transport cannot change the database target or writers.')
        self.input=state.read_private(self.bundle/'prepare-inputs.json');self.gid=self.input['shared_gid']
        base=state.read_private(self.bundle/'previous-compose.json')
        rendered=preflight.prep.render(base,self.input['bind'],self.input['host'],self.input['certs'],self.bundle,self.gid)
        if rendered!=state.read_private(self.bundle/'compose.json'):
            raise ValueError('Transport configuration differs from exact supported rendering.')
        exact=runtime.pair(base,rendered,images,proposed)
        if exact['previous']!=self.old or exact['candidate']!=self.new:
            raise ValueError('Transport runtime plan is not the exact rendered pair.')
        self.changing=sorted({'mosquitto','web','secure-web',*self.writers})
    call=application.DockerApplication.call
    compose=application.DockerApplication.compose
    containers=application.DockerApplication.containers
    def verify_definition(self,service,item,image):
        # Shared container enumeration must not acquire application-only gate
        # semantics. Transport transactions require exact direct definitions.
        if item.get('Config',{}).get('Labels',{}).get('hexthings.startgate'):
            raise ValueError('Application process gates are outside transport-only runtime scope.')
        application.verify_container_definition(service,item,image)
    checked_containers=application.DockerApplication.checked_containers
    check_image=application.DockerApplication.check_image
    backup=application.DockerApplication.backup
    authorize=application.DockerApplication.authorize
    def verify_partial_runtime(self):
        found={}
        for item in self.containers(self.previous):
            labels=item.get('Config',{}).get('Labels',{});name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=self.project or name in found or name not in self.new['services']:
                raise ValueError('Unclassified current transport runtime; no effects.')
            found[name]=item
            metadata=json.loads(self.call(['docker','image','inspect',item.get('Image','')]))
            if len(metadata)!=1 or metadata[0].get('Id')!=item.get('Image'):raise ValueError('Exact current transport image unavailable.')
            valid=False
            for config in [self.old,self.new]:
                service=config['services'].get(name)
                if service and service['image']==item.get('Image'):
                    try:application.verify_container_definition(service,item,metadata[0]);valid=True;break
                    except ValueError:pass
            if not valid:raise ValueError('Current transport definition is not a planned old/new runtime.')
        for name in set(self.old['services'])-set(self.changing):
            if name not in found or found[name]['State']['Status']!='running':raise ValueError('Unchanged store/service unavailable during transport transaction.')
        return found
    def revalidate(self,project,receipt):
        if project!=self.project or pathlib.Path(receipt)!=self.receipt or state.read_private(receipt)!=self.plan:
            raise ValueError('Transport plan changed.')
        state.check_manifest(self.plan['files'])
        found=self.checked_containers(self.previous,True)
        if {name:{'image':item['Image'],'container_id':item['Id']}for name,item in found.items()}!=self.plan['current_services']:
            raise ValueError('Current transport containers changed since preparation.')
        selected=self.selection_reader(self.root)
        if selected and selected['project']!=self.project:raise ValueError('Transport selection project changed.')
        command=['docker','compose','-p',self.project]
        if selected:command+=['-f',selected['config']]
        current=json.loads(self.call(command+['config','--format','json']))
        if current!=state.read_private(self.bundle/'previous-compose.json'):raise ValueError('Current Compose changed since transport preparation.')
        self.verify_current_operator()
        for image in set(self.plan['proposed_images'].values()):self.check_image(image)
        self.verify_tls_inputs()
    def verify_tls_inputs(self):
        certs=pathlib.Path(self.input['certs'])
        preflight.prep.check_certificate(certs,'broker','broker-ca.crt','sslserver',('mosquitto',self.input['host']))
        preflight.prep.check_certificate(certs,'https','https-ca.crt','sslserver',(self.input['host'],))
        for service in ['api','ingest']:preflight.prep.check_certificate(certs,service,'device-ca.crt','sslclient')
        preflight.prep.check_issuing_ca(certs)
    def verify_current_operator(self):
        result=application.readiness.inspect(self.root,self.project,execute=self.execute,selection_reader=self.selection_reader)
        if (result['status']!='migration_ledger_current' or result['writers']!=self.writers
                or result['current_services']!=self.plan['current_services']):
            raise ValueError('Current operator/schema/writer inventory changed; no transport effects.')
    def stop_writers(self,value):
        self.verify_partial_runtime();self.compose(value['previous'],'stop',*self.writers)
    def verify_writers_stopped(self):
        found=self.verify_partial_runtime()
        if not set(self.old['services'])<=set(found):raise ValueError('Current service inventory incomplete before ACL export.')
        if any(found[name]['State']['Status'] not in {'exited','created'}for name in self.writers):
            raise ValueError('Every database writer must stop before current-image ACL export.')
    def export_acl(self,value):
        self.verify_writers_stopped()
        text=self.compose(value['previous'],'run','--rm','--no-deps','--pull','never','--entrypoint','/bin/tenantctl','api','broker-acl-export')
        if not text.startswith('# hexmon broker ACL') or preflight.sha(text.encode())!=self.plan['acl_sha256']:
            raise ValueError('Full current-image ACL changed since preparation; reprepare, no stale snapshot accepted.')
        return text
    def stage_acl(self,value,acl):
        if preflight.sha(acl.encode())!=self.plan['acl_sha256']:raise ValueError('Unbound current ACL snapshot.')
        directory=self.bundle/'acl';target=directory/'acl'
        if directory.is_symlink() or not directory.is_dir() or target.is_symlink():raise ValueError('Exact generated ACL directory required.')
        fd,name=tempfile.mkstemp(prefix='.acl-',dir=directory)
        try:
            with os.fdopen(fd,'w')as output:output.write(acl);output.flush();os.fsync(output.fileno())
            os.chown(name,-1,self.gid);os.chmod(name,0o640);os.replace(name,target);state.sync_directory(directory)
        finally:
            if os.path.exists(name):os.unlink(name)
    def start_candidate(self,value):
        state.check_manifest(self.plan['files']);self.verify_writers_stopped()
        self.compose(value['candidate'],'up','-d','--no-deps','--no-build','--pull','never',*self.changing)
    def verify_recovery_inputs(self,value):
        if value.get('previous')!=str(self.previous) or value.get('candidate')!=str(self.candidate):raise ValueError('Transport recovery target changed.')
        state.check_manifest(value['previous_files'])
        if value['previous_files'].get(str(self.previous))!=state.digest(self.previous):raise ValueError('Incomplete prior transport manifest.')
        for image in set(value['previous_images'].values()):self.check_image(image)
        self.verify_partial_runtime()
    def stop_candidate(self,value):
        self.verify_partial_runtime();self.compose(value['candidate'],'stop',*self.changing)
        self.compose(value['candidate'],'rm','-f','secure-web')
    def restore_previous(self,value):
        self.compose(value['previous'],'up','-d','--no-deps','--no-build','--pull','never',*[name for name in self.changing if name!='secure-web'])
    def accept(self,value,target):
        self.acceptance(value,target)
        self.checked_containers(value[target],True)
