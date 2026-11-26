#!/usr/bin/env python3
"""Disposable actual Docker preparation wiring, not updater/runtime acceptance.

Synthetic sleeping processes only, no real application/DB/edge or owner files.
The fixture creates an explicit synthetic committed selector, never production
activation. Hosted Linux only; no volumes, host ports or privileged containers.
"""
import importlib.util,json,os,pathlib,subprocess,tempfile,uuid
HERE=pathlib.Path(__file__).resolve().parent

def call(args,root):
    p=subprocess.run(args,cwd=root,capture_output=True,text=True,timeout=120)
    if p.returncode:raise ValueError('Disposable Docker fixture command failed: '+' '.join(args[:3]))
    return p.stdout

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux':raise SystemExit('Only a disposable Linux Actions runner is supported.')
    spec=importlib.util.spec_from_file_location('gui',HERE/'secure-update-gui.py');gui=importlib.util.module_from_spec(spec);spec.loader.exec_module(gui)
    state=gui.backend.review.state;project='hexthings-preparation-ci-'+uuid.uuid4().hex[:12]
    with tempfile.TemporaryDirectory(prefix='hexthings-preparation-ci-',dir='/tmp')as td:
        root=pathlib.Path(td);images=[];compose=['docker','compose','-p',project,'-f',str(root/'selected-runtime.json')]
        try:
            # The fixture base has no host binds, ports, DB or command effects.
            # These tiny images only keep explicit synthetic containers running.
            for version in ['previous','candidate']:
                folder=root/version;folder.mkdir()
                (folder/'Dockerfile').write_text('FROM alpine:3.20\nRUN for name in api ingest mcp; do printf "#!/bin/sh\\nexec sleep 900\\n" > /bin/$name; chmod +x /bin/$name; done\nLABEL hexthings.synthetic.preparation='+version+'\n')
                call(['docker','build','--iidfile',str(folder/'image.id'),str(folder)],root)
                images.append((folder/'image.id').read_text().strip())
            old,new=images;services={name:{'image':old,'pull_policy':'never','command':['sleep','900']}for name in ['api','ingest','mcp','postgres','mosquitto','web','secure-web']}
            for name in ['api','ingest','mcp']:services[name].update(command=['/bin/'+name],environment={'DATABASE_URL':'postgres://synthetic@postgres/fixture?sslmode=disable'})
            services['postgres']['environment']={'POSTGRES_USER':'synthetic','POSTGRES_DB':'fixture'}
            services['secure-web'].update(network_mode='service:api',depends_on=['api','web'])
            config=root/'selected-runtime.json';state.atomic(config,{'services':services})
            marker=root/'.secure-deployment';marker.mkdir(mode=0o700);digest=state.digest(config);files={str(config):digest}
            state.atomic(marker/'selected.json',{'version':1,'project':project,'config':str(config),'sha256':digest,'files':files})
            state.atomic(marker/'journal.json',{'version':1,'phase':'committed','project':project,'candidate':str(config),'candidate_sha256':digest,'files':files})
            call(compose+['up','-d','--no-build','--pull','never'],root)
            def ids():return sorted(call(compose+['ps','-a','-q'],root).split())
            before_ids=ids();before={p.name:p.read_bytes()for p in marker.iterdir()};config_before=config.read_bytes()
            session=gui.Session(root);session.refresh();text=session.run(project,new,'private-plan')
            if 'NOT approval'not in text or 'synthetic@'in text:raise ValueError('Review authority/redaction mismatch')
            if before_ids!=ids()or before!={p.name:p.read_bytes()for p in marker.iterdir()}or config.read_bytes()!=config_before:raise ValueError('Preparation changed selection or containers')
            if (root/'private-plan').stat().st_mode&0o777!=0o700:raise ValueError('Shared plan folder')
            for p in (root/'private-plan').iterdir():
                if p.stat().st_mode&0o777!=0o600:raise ValueError('Shared plan file')
            try:session.run(project,new,'another-plan')
            except ValueError:pass
            else:raise ValueError('GUI session repeated its operation')
            if (root/'another-plan').exists():raise ValueError('Retry wrote files')
            # A real missing local image must fail without pull/run fallback.
            missing=gui.Session(root);missing.refresh()
            try:missing.run(project,'sha256:'+'0'*64,'missing-image')
            except ValueError:pass
            else:raise ValueError('Unknown image accepted')
            if before_ids!=ids()or before!={p.name:p.read_bytes()for p in marker.iterdir()}:raise ValueError('Failed preparation changed runtime')
            print('PASS actual Docker preparation wiring: synthetic committed secure selector, unchanged sidecar/config/container IDs, private plans/redacted review, no retry and missing-image refusal. Sleeping fixture processes only, NOT real application, database, updater execution/rollback or Windows acceptance.')
        finally:
            if (root/'selected-runtime.json').exists():call(compose+['down','--remove-orphans'],root)
            for image in set(images):call(['docker','image','rm',image],root)
if __name__=='__main__':main()
