#!/usr/bin/env python3
"""Fresh native-only signed source apply after same-deployment secure pairing.

Synthetic Modbus TCP simulator only. Never runs on an owner installation or
connects to physical hardware. No discovery, scan or output command is enabled.
"""
import base64
import importlib.util
import ipaddress
import json
import os
import pathlib
import socket
import struct
import subprocess
import threading
import time
import uuid

HERE=pathlib.Path(__file__).resolve().parent

def exact_uuid(value):
    if not isinstance(value,str)or str(uuid.UUID(value))!=value:
        raise ValueError('Synthetic HTTPS returned an invalid UUID.')
    return value


def synthetic_response(request):
    if len(request)!=12:raise ValueError('Exact synthetic read request required.')
    transaction,protocol,length,unit,fn,address,count=struct.unpack('>HHHBBHH',request)
    if (protocol,length,unit,fn,address,count)!=(0,6,1,3,10,1):
        raise ValueError('Only synthetic FC03/register10/unit1/count1 read allowed.')
    return struct.pack('>HHHBBBH',transaction,0,5,unit,3,2,110)


def source_apply(root,bundle,project,image,host,bind,gid,admin_token):
    root=pathlib.Path(root).resolve();bundle=pathlib.Path(bundle).resolve()
    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0
            or root.parent!=pathlib.Path('/tmp') or not root.name.startswith('hexthings-compose-ci-')
            or not project.startswith('hexthings-secure-ci-') or bundle.parent!=root or host!='hexthings-ci.invalid'
            or not any(ipaddress.ip_address(bind) in ipaddress.ip_network(cidr)for cidr in ['10.0.0.0/8','172.16.0.0/12','192.168.0.0/16'])):
        raise ValueError('Fresh disposable native source fixture only; owner execution refused.')
    spec=importlib.util.spec_from_file_location('state',HERE/'secure-state.py');state=importlib.util.module_from_spec(spec);spec.loader.exec_module(state)
    candidate=bundle/'candidate-runtime.json';selected=state.selection(root)
    if not selected or selected['config']!=str(candidate) or selected['project']!=project:
        raise ValueError('Exact activated synthetic TLS selection required.')
    saved=root/'post-pairing-identity'
    names=['identity.key','identity.json','existing-config.yaml','identity.crt','ca.pem','device-ca.pem']
    before={name:(saved/name).read_bytes()for name in names}
    def call(args,input=None):
        result=subprocess.run(list(map(str,args)),cwd=root,capture_output=True,text=True,input=input,timeout=90)
        if result.returncode:raise ValueError('Synthetic native source operation failed: '+' '.join(map(str,args[:3])))
        return result.stdout+result.stderr if list(map(str,args[:2]))==['docker','logs'] else result.stdout
    def compose(*args):return call(['docker','compose','-p',project,'-f',candidate,*args])
    def sql(text):return compose('exec','-T','postgres','psql','-U','ci','-d','ci','-v','ON_ERROR_STOP=1','-Atc',text).strip()
    stop=threading.Event();bad=threading.Event();received=threading.Event()
    simulator=socket.socket();simulator.bind((bind,0));simulator.listen(4);simulator.settimeout(.5)
    port=simulator.getsockname()[1]
    def serve():
        while not stop.is_set():
            try:connection,_=simulator.accept()
            except socket.timeout:continue
            except OSError:return
            with connection:
                connection.settimeout(.5);buffer=b''
                while not stop.is_set():
                    try:part=connection.recv(12-len(buffer))
                    except socket.timeout:continue
                    except OSError:break
                    if not part:break
                    buffer+=part
                    if len(buffer)<12:continue
                    try:response=synthetic_response(buffer)
                    except ValueError:bad.set();return
                    buffer=b'';received.set()
                    try:connection.sendall(response)
                    except OSError:break
    worker=threading.Thread(target=serve,daemon=True);worker.start()
    edge_name=project+'-source-edge'
    origin='https://'+host+':8443';counter=0
    def api(method,path,payload=None,expected=200):
        nonlocal counter
        counter+=1;response=saved/('source-response-'+str(counter)+'.json')
        args=['curl','--noproxy','*','--silent','--show-error','--max-time','10','--cacert',root/'certs/https-ca.crt','--resolve',host+':8443:'+bind,'-H','Authorization: Bearer '+admin_token,'-H','Content-Type: application/json','-X',method,'-o',response,'-w','%{http_code}']
        if payload is not None:args+=['--data-binary','@-']
        status=call(args+[origin+path],input=json.dumps(payload)if payload is not None else None)
        body=response.read_text();response.chmod(0o600)
        if status!=str(expected):raise ValueError('Synthetic source HTTPS status '+status+' (expected '+str(expected)+').')
        return json.loads(body)if expected in {200,201,202} else None
    try:
        # Only rows created by seedDemo in this hardcoded fresh fixture.
        # No default serial or hardware poller is left in the desired list.
        sql("BEGIN; DELETE FROM points WHERE device_id IN (SELECT id FROM devices WHERE tenant_id='demo' AND gateway_id='demo-gw'); DELETE FROM devices WHERE tenant_id='demo' AND gateway_id='demo-gw'; COMMIT;")
        commands=sql('SELECT count(*) FROM commands')
        apply_count=sql('SELECT count(*) FROM gateway_config_pushes')
        connection={'host':bind,'net_port':port,'address':1,'interval_seconds':1}
        points=[{'id':'ci-synthetic-value','register':10,'func':3,'type':'u16','scale':1,'unit':'synthetic','min':0,'max':1000}]
        profile=api('POST','/v1/profiles',{'name':'Synthetic native source','driver_profile':'modbus-tcp','points':points,'connection_defaults':{}},201)
        session=api('POST','/v1/gateways/demo-gw/source-setup',{},201)
        profile_id=exact_uuid(profile.get('id'));session_id=exact_uuid(session.get('session_id'))
        session_path='/v1/commissioning/sessions/'+session_id
        if session.get('gateway_id')!='demo-gw' or session.get('device_id')is not None:
            raise ValueError('Source setup reclaimed or replaced the gateway.')
        assignment={'profile_id':profile_id,'device_name':'Synthetic native source','connection':connection}
        api('POST',session_path+'/profile',dict(assignment,connection=dict(connection,net_port=0)),400)
        if api('GET',session_path).get('device_id')is not None:
            raise ValueError('Invalid connection was saved.')
        assigned=api('POST',session_path+'/profile',assignment,201);device_id=exact_uuid(assigned.get('device_id'))
        if assigned.get('points')!=1:raise ValueError('Saved source points were not exact.')
        api('POST',session_path+'/profile',assignment,409)
        recovered=api('GET',session_path)
        if recovered.get('source')!={'device_id':device_id,'name':'Synthetic native source','driver':'modbus-tcp','connection':connection}:
            raise ValueError('HTTPS resume changed saved source connection.')
        initial_preview=api('GET',session_path+'/preview')
        if initial_preview.get('live')is not False or initial_preview.get('points')!=[]:
            raise ValueError('Saving source was incorrectly presented as a live reading.')
        if sql('SELECT count(*) FROM gateway_config_pushes')!=apply_count or received.is_set():
            raise ValueError('Saving source applied or started it without explicit review.')
        # Derive the public verifier from the fixture's fixed synthetic seed7.
        # Neither deployment credentials nor the device identity key are read.
        seedfile=saved/'synthetic-source-signer.der'
        seedfile.write_bytes(bytes.fromhex('302e020100300506032b657004220420')+bytes([7])*32);seedfile.chmod(0o600)
        public=subprocess.run(['openssl','pkey','-inform','DER','-in',str(seedfile),'-pubout','-outform','DER'],capture_output=True,check=True).stdout
        seedfile.unlink()
        if len(public)!=44:raise ValueError('Synthetic public verifier derivation failed.')
        public=base64.b64encode(public[-32:]).decode()
        runtime=saved/'source-runtime.yaml'
        runtime.write_text('gateway_id: demo-gw\ntenant_id: demo\nserial: AXON-DEMO-1\nmqtt: {host: mosquitto, port: 8883, tls: true}\nqueue_path: /saved/source-queue.db\nui: {listen: off}\nautodetect: {enabled: false}\nallowed_commands: []\nallow_automatic_commands: false\nremote_restart: false\noutputs: []\nrules: []\nmanaged_devices: true\nfleet_public_key: '+public+'\ndevices: []\n');runtime.chmod(0o600)
        call(['docker','run','-d','--name',edge_name,'--user','0','--network',project+'_default','-e','HEXMON_DATA_DIR=/saved','-v',str(saved)+':/saved',image,'/bin/edge-agent','-config','/saved/source-runtime.yaml','-identity-dir','/saved'])
        started=call(['docker','inspect','--format','{{.State.StartedAt}}',edge_name]).strip()
        def wait(condition,label,seconds=35):
            deadline=time.monotonic()+seconds
            while not condition():
                if bad.is_set():raise ValueError('Synthetic source attempted an unreviewed request.')
                if time.monotonic()>deadline:raise ValueError('Synthetic source '+label+' not observed.')
                time.sleep(.2)
        wait(lambda:'config push subscription ready' in call(['docker','logs',edge_name]),'config subscription')
        base='/v1/gateways/demo-gw'
        readiness=api('GET',base+'/source-readiness')
        digest=readiness.get('desired_sha256','')
        if not readiness.get('server_ready') or len(digest)!=64 or readiness.get('source_names')!=['Synthetic native source']:
            raise ValueError('Synthetic desired source is not exactly the reviewed source.')
        count=sql('SELECT count(*) FROM gateway_config_pushes')
        api('POST',base+'/config-push',{'expected_sha256':'stale'},409)
        if sql('SELECT count(*) FROM gateway_config_pushes')!=count:raise ValueError('Stale review created a source apply.')
        push=api('POST',base+'/config-push',{'expected_sha256':digest},202)
        pushid=push.get('push_id','')
        if not pushid or push.get('sha256')!=digest:raise ValueError('Source apply not bound to reviewed fingerprint.')
        def confirmed():
            rows=api('GET',base+'/config-push')
            return any(row.get('id')==pushid and row.get('status')=='confirmed' and row.get('sha256')==digest for row in rows)
        wait(confirmed,'independent confirmed result')
        wait(lambda:sql("SELECT count(*) FROM telemetry WHERE tenant_id='demo' AND gateway_id='demo-gw' AND device_id='"+device_id+"' AND point_id='ci-synthetic-value' AND value=110 AND unit='synthetic' AND observed_at>now()-interval '60 seconds'")!='0','tagged synthetic reading')
        readings=api('GET','/v1/telemetry/series?device_id='+device_id+'&point_id=ci-synthetic-value&hours=1')
        if not isinstance(readings,list) or not any(row.get('v')==110 for row in readings):
            raise ValueError('Synthetic reading not returned by authenticated HTTPS series.')
        preview=api('GET',session_path+'/preview')
        if preview.get('device_id')!=device_id or preview.get('live')is not True or not any(p.get('value')==110 and p.get('recent_measured')is True for p in preview.get('points',[])):
            raise ValueError('Wizard preview did not reflect the recent synthetic sample.')
        if api('GET',session_path).get('source')!=recovered.get('source'):
            raise ValueError('Applying source changed saved connection metadata.')
        if sql('SELECT count(*) FROM commands')!=commands:
            raise ValueError('Synthetic source apply created a device command.')
        if not received.is_set() or bad.is_set():raise ValueError('Synthetic read-only source scope not established.')
        if call(['docker','inspect','--format','{{.State.StartedAt}}',edge_name]).strip()!=started:
            raise ValueError('Source applied only after a service restart, not live reload.')
        for name,data in before.items():
            if (saved/name).read_bytes()!=data:raise ValueError('Signed source workflow replaced saved identity/key/cert/config.')
        print('PASS same activated-and-paired native TLS deployment: real HTTPS create-profile/save-source/duplicate-save refusal/resume, saved-not-live preview before explicit apply, exact source review, stale fingerprint refuses without apply row, signed source apply to compiled edge, broker+ingest independently confirmed result, live same-process poll reload and synthetic Modbus TCP FC03/register10 value110 stored in PostgreSQL and returned over authenticated HTTPS series and recent wizard preview; original saved key/identity/certs/config unchanged. Simulator only, no owner activation, discovery, scan, device output command or physical meter proof.',flush=True)
    finally:
        subprocess.run(['docker','rm','-f',edge_name],capture_output=True,check=False,timeout=20)
        stop.set();simulator.close();worker.join(timeout=2)
