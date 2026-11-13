#!/usr/bin/env python3
"""Disposable real old-writer network namespaces with candidate host probe.
Explicit private TCP only. Does not prove outside denial or provider activation.
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
hostmod=load('secure-postgres-hosttool');netmod=load('secure-postgres-namespace')

def prove(image,root,project,config,helper):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or root.parent!=pathlib.Path('/tmp')or not root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')or pathlib.Path(config).parent!=root):
        raise ValueError('Disposable explicit namespace fixture only.')
    baseline=helper.identities(config);commands=[]
    if not {'api','ingest','mcp','postgres'}.issubset(baseline):raise ValueError('Complete live fixture group required.')
    def run(args,**kwargs):commands.append(list(args));return subprocess.run(args,**kwargs)
    def guard():
        if helper.identities(config)!=baseline:raise ValueError('Exact accepted fixture group changed.')
        value=json.loads(subprocess.run(['docker','image','inspect',image],capture_output=True,text=True,check=True,timeout=10).stdout)
        if len(value)!=1 or value[0]['Id']!=image:raise ValueError('Exact candidate source image changed.')
    def inspect(cid):
        return json.loads(subprocess.run(['docker','inspect',cid],capture_output=True,text=True,check=True,timeout=10).stdout)[0]
    pg=inspect(baseline['postgres']['container_id'])
    with tempfile.TemporaryDirectory(prefix='hexthings-host-namespace-ci-')as td:
        staging=pathlib.Path(td)
        with hostmod.state.Journal(staging)as journal:
            hostmod.state.atomic(journal.directory/'maintenance.json',{'root':str(staging),'scope':'application_bootstrap','project':project})
            tool=hostmod.HostTool(journal,image,guard,run)
            tool.prepare()
            try:
                proofs=[]
                for service in ['api','ingest','mcp']:
                    item=inspect(baseline[service]['container_id']);networks=item['NetworkSettings']['Networks'];pgnetworks=pg['NetworkSettings']['Networks']
                    shared=set(networks)&set(pgnetworks)
                    if len(shared)!=1:raise ValueError('One exact source/server fixture network required.')
                    network=next(iter(shared));peer=networks[network];server=pgnetworks[network]
                    if peer['NetworkID']!=server['NetworkID']:raise ValueError('Exact shared bridge network required.')
                    expected={'container':item['Id'],'image':item['Image'],'started':item['State']['StartedAt'],
                        'project':project,'service':service,'network':network,'network_id':peer['NetworkID'],'address':peer['IPAddress']}
                    request={'version':1,'transport':'private_tcp_plain','host':server['IPAddress'],'port':5432,
                        'database':'application_fixture','user':'ci','password':'synthetic-ci-only'}
                    with netmod.Namespace(tool,expected,guard,run)as ns:
                        result=ns.probe(request)
                        if result.get('status')!='connected':raise ValueError('Actual previous writer namespace did not connect.')
                        wrong=ns.probe(dict(request,password='synthetic-wrong-password'))
                        if wrong!={'version':1,'status':'authentication_failed'}:raise ValueError('Actual namespace SCRAM refusal unverified.')
                        identity=ns.process.identity
                        proofs.append({'service':service,'container':item['Id'],'image':item['Image'],
                            'source':result['client'],'server':result['server'],'pid':identity['pid'],
                            'ticks':identity['ticks'],'namespace':identity['namespace'],'scram_denied':True})
                guard()
                if any(args[:2]in [['docker','start'],['docker','run'],['docker','exec'],['docker','pull']]for args in commands):
                    raise ValueError('Host namespace proof unexpectedly started or execed a container.')
                print('NATIVE_HOST_NAMESPACES='+json.dumps(proofs,sort_keys=True),flush=True)
                print('PASS actual previous-image API/ingest/MCP PID/start/cgroup/boot/network namespace pinning: candidate static host binary executes through pinned network-only nsenter FDs, clean environment and stdin-only synthetic credentials; all3 exact source/server read-only connections and wrong-password SCRAM refusals, no previous-image tool assumption or probe sidecar. No outside-negative/provider connection/owner authority proof.',flush=True)
            finally:
                tool.release()
            if pathlib.Path(tool.record()['binary']).exists():raise ValueError('Actual namespace static probe cleanup failed.')
