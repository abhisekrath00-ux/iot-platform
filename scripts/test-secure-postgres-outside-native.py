#!/usr/bin/env python3
"""Actual existing web namespace proves outside HBA refusal, no probe sidecar.
Separate disposable DB and held barrier; not a provider-default activation.
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
netmod=load('secure-postgres-namespace');toolmod=load('secure-postgres-hosttool');outside=load('secure-postgres-outside');hostmod=load('secure-postgres-hba-docker');fence=load('secure-postgres-fence')
class Loss(BaseException):pass

def prove(image,root,project,config,helper):
    if (os.environ.get('GITHUB_ACTIONS')!='true'or os.environ.get('RUNNER_OS')!='Linux'or os.geteuid()!=0
            or root.parent!=pathlib.Path('/tmp')or not root.name.startswith('hexthings-application-ci-')
            or not project.startswith('hexthings-app-ci-')or pathlib.Path(config).parent!=root):
        raise ValueError('Disposable explicit outside namespace fixture only.')
    baseline=helper.identities(config);commands=[];db='outside_namespace_fixture'
    def call(args,**kw):return subprocess.run(args,capture_output=True,text=True,check=True,timeout=20,**kw)
    def run(args,**kw):commands.append(list(args));return subprocess.run(args,**kw)
    def inspect(cid):return json.loads(call(['docker','inspect',cid]).stdout)[0]
    pg=inspect(baseline['postgres']['container_id']);web=inspect(baseline['web']['container_id'])
    networks=web['NetworkSettings']['Networks'];pgnetworks=pg['NetworkSettings']['Networks'];shared=set(networks)&set(pgnetworks)
    if len(shared)!=1:raise ValueError('One exact shared outside/server network required.')
    network=next(iter(shared));peer=networks[network];server=pgnetworks[network]
    if peer['NetworkID']!=server['NetworkID']:raise ValueError('Exact shared outside bridge required.')
    expected={'container':web['Id'],'image':web['Image'],'started':web['State']['StartedAt'],'project':project,'service':'web',
              'network':network,'network_id':peer['NetworkID'],'address':peer['IPAddress']}
    api=inspect(baseline['api']['container_id']);app_peer={'address':api['NetworkSettings']['Networks'][network]['IPAddress'],'user':'ci','ssl':False}
    if app_peer['address']==expected['address']:raise ValueError('Outside endpoint overlaps accepted writer.')
    cid=pg['Id'];catalog=fence.DockerCatalog(cid,'ci',db);host=hostmod.DockerHost(catalog,project);path=host.bind();original=path.read_bytes();metadata=path.stat();created=False
    def guard():
        if helper.identities(config)!=baseline:raise ValueError('Fixture exact runtime changed.')
        host.check()
    def live():host.check();return bytes.fromhex(host.server()['bytes'])
    try:
        call(['docker','exec',cid,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','postgres','-c','CREATE DATABASE '+db]);created=True
        with tempfile.TemporaryDirectory(prefix='hexthings-outside-namespace-ci-')as td:
            staging=pathlib.Path(td);proofs=[]
            with toolmod.state.Journal(staging)as journal:
                toolmod.state.atomic(journal.directory/'maintenance.json',{'root':str(staging),'scope':'application_bootstrap','project':project})
                tool=toolmod.HostTool(journal,image,guard,run);tool.prepare()
                request={'version':1,'transport':'private_tcp_plain','host':server['IPAddress'],'port':5432,'database':db,'user':'ci','password':'synthetic-ci-only'}
                with netmod.OutsideNamespace(tool,expected,guard,run)as ns:
                    proof=outside.OutsideProof(ns,request,original,live,guard);current=[[]]
                    def connections(raw):
                        phase='original'if raw==original else'restricted'
                        proofs.append(proof.prove(phase,[]if phase=='original'else current[0],raw))
                    def reload(raw):host.reload_and_verify(raw,connections)
                    proofs.append(proof.prove('original',[],original))
                    barrier=hostmod.hba.Barrier(journal,path,db,'ci',guard,reload)
                    barrier.enter();barrier.check();current[0]=[app_peer];barrier.handoff(current[0])
                    original_reload=barrier.reload
                    def lost(raw):original_reload(raw);raise Loss('Actual outside HBA reload lost acknowledgement')
                    barrier.reload=lost
                    try:barrier.handoff(current[0])
                    except Loss:pass
                    else:raise ValueError('Expected real outside proof lost acknowledgement.')
                # Real lock release, new tool/namespace/barrier objects, same exact
                # source container and durable HBA intent, no silent rebase.
            with toolmod.state.Journal(staging)as journal:
                tool=toolmod.HostTool(journal,image,guard,run);tool.resume()
                with netmod.OutsideNamespace(tool,expected,guard,run)as ns:
                    proof=outside.OutsideProof(ns,request,original,live,guard);current[0]=[]
                    fresh=hostmod.hba.Barrier(journal,path,db,'ci',guard,reload)
                    fresh.enter(recovery=True);fresh.check();fresh.release()
                tool.release()
                if pathlib.Path(tool.record()['binary']).exists():raise ValueError('Outside static host binary cleanup failed.')
            if path.read_bytes()!=original or path.stat().st_mode!=metadata.st_mode or path.stat().st_uid!=metadata.st_uid:
                raise ValueError('Exact original HBA bytes/mode/owner changed.')
            if any(a[:2]in [['docker','run'],['docker','start'],['docker','exec'],['docker','pull']]for a in commands):
                raise ValueError('Outside host probe introduced a sidecar or container exec.')
            print('NATIVE_OUTSIDE_NAMESPACE='+json.dumps({'service':'web','container':web['Id'],'source':expected['address'],
                'server':request['host'],'proofs':proofs,'fresh_lost_ack_recovery':True,'new_probe_sidecar':False},sort_keys=True),flush=True)
            print('PASS actual existing web outside namespace: original SCRAM connection -> exact first-match restricted HBA refusal before/after admitted API peer handoff -> real lost reload acknowledgement/fresh held-barrier recovery -> original connection restored; exact source CID/image/start/PID/cgroup/netns pinned, static candidate hostbinaryFD/stdin-only credentials, no new probe sidecar or old-image tool. HBA denial is not database-closed proof. No production provider callback/owner/menu acceptance.',flush=True)
    finally:
        # Only disposable fixture hygiene, never product recovery overwrites.
        if path.read_bytes()!=original:
            path.write_bytes(original);os.chown(path,metadata.st_uid,metadata.st_gid);os.chmod(path,metadata.st_mode&0o777)
            catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        if created:call(['docker','exec',cid,'psql','-X','-v','ON_ERROR_STOP=1','-U','ci','-d','postgres','-c','DROP DATABASE '+db+' WITH (FORCE)'])
