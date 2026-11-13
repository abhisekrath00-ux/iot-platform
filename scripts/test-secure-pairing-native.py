#!/usr/bin/env python3
"""Post-pairing receipt on the actual disposable activated native deployment.

No owner command. Called only from the bounded standard-runner TLS fixture.
Fresh synthetic saved identity/JWT, demo rows and certificates only. No source
start, serial probe, physical read, subscription or managed command by edge.
"""
import base64
import hashlib
import hmac
import importlib.util
import json
import os
import pathlib
import subprocess
import time

HERE=pathlib.Path(__file__).resolve().parent
BROKER_IMAGE='eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98'

def post_pair(root,bundle,project,image,host,bind,gid):
    root=pathlib.Path(root).resolve();bundle=pathlib.Path(bundle).resolve()
    if (os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0
            or root.parent!=pathlib.Path('/tmp') or not root.name.startswith('hexthings-compose-ci-')
            or not project.startswith('hexthings-secure-ci-') or bundle.parent!=root or host!='hexthings-ci.invalid'):
        raise ValueError('Fresh disposable native TLS fixture only; owner pairing refused.')
    spec=importlib.util.spec_from_file_location('state',HERE/'secure-state.py');state=importlib.util.module_from_spec(spec);spec.loader.exec_module(state)
    selected=state.selection(root);candidate=bundle/'candidate-runtime.json'
    if not selected or selected['config']!=str(candidate) or selected['project']!=project:
        raise ValueError('Exact activated synthetic TLS selection required before post-pairing test.')
    def call(args,input=None,expected=0):
        result=subprocess.run(list(map(str,args)),cwd=root,capture_output=True,text=True,input=input,timeout=90)
        if result.returncode!=expected:raise ValueError('Synthetic native post-pairing operation refused or unverified: '+' '.join(map(str,args[:3])))
        return result.stdout
    def compose(*args):return call(['docker','compose','-p',project,'-f',candidate,*args])
    def enc(value):return base64.urlsafe_b64encode(json.dumps(value,separators=(',',':')).encode()).rstrip(b'=')
    def token(user,role):
        now=int(time.time());message=enc({'alg':'HS256','typ':'JWT'})+b'.'+enc({'sub':user,'tenant_id':'demo','role':role,'iat':now,'exp':now+600})
        signature=base64.urlsafe_b64encode(hmac.new(b'synthetic-ci-only-not-for-deployment',message,hashlib.sha256).digest()).rstrip(b'=')
        return (message+b'.'+signature).decode()
    saved=root/'post-pairing-identity';saved.mkdir(mode=0o700)
    call(['openssl','ecparam','-name','prime256v1','-genkey','-noout','-out',saved/'identity.key'])
    (saved/'identity.key').chmod(0o600)
    (saved/'identity.json').write_text(json.dumps({'gateway_id':'demo-gw','ingest_token':token('demo-gw','gateway')}));(saved/'identity.json').chmod(0o600)
    (saved/'existing-config.yaml').write_text('synthetic_saved_config: preserved\ndevices: []\n');(saved/'existing-config.yaml').chmod(0o600)
    before={name:(saved/name).read_bytes()for name in ['identity.key','identity.json','existing-config.yaml']}
    acl=bundle/'acl/acl';oldacl=state.digest(acl)
    compose('exec','-T','api','/bin/api','-seed-demo')
    origin='https://'+host+':8443'
    # Real production read-only client over the fixture's activated HTTPS API.
    # Test-only DNS maps the synthetic hostname to the exact private fixture
    # address, like curl --resolve. TLS still verifies the hostname and CA.
    from unittest import mock
    import socket
    spec=importlib.util.spec_from_file_location('retained_client',HERE/'secure-application-client.py')
    retained_client=importlib.util.module_from_spec(spec);spec.loader.exec_module(retained_client)
    session=root/'synthetic-retained-session';session.write_text(token('demo-admin','admin'));session.chmod(0o600)
    original_resolver=socket.getaddrinfo
    def resolver(name,port,*args,**kwargs):
        return original_resolver(bind if name==host and port==8443 else name,port,*args,**kwargs)
    try:
        with mock.patch('socket.getaddrinfo',side_effect=resolver):
            client=retained_client.Client(origin,session,'demo','demo-admin',ca_file=root/'certs/https-ca.crt')
            snapshot=client.snapshot();client.compare(snapshot)
    finally:session.unlink()
    print('PASS actual production retained-read client on activated HTTPS API: explicit CA/hostname, live exact unrestricted administrator before/after bounded inventory, private synthetic session removed; not activation authority or full compatibility.',flush=True)

    def edge(flag,expected=0):
        return call(['docker','run','--rm','--user','0','--network',project+'_default','--add-host',host+':'+bind,'-e','SSL_CERT_FILE=/certs/https-ca.crt','-v',str(bundle.parent/'certs')+':/certs:ro','-v',str(saved)+':/saved',image,'/bin/edge-agent',flag,'-setup-server',origin,'-setup-serial','AXON-DEMO-1','-identity-dir','/saved','-config','/saved/existing-config.yaml'],expected=expected)
    edge('-request-secure-pairing')
    request=json.loads((saved/'migration-request.json').read_text())
    payload=json.dumps({'key_sha256':request['key_sha256'],'csr_sha256':request['csr_sha256'],'compared_local_fingerprint':True})
    responsefile=saved/'approval-response.json'
    status=call(['curl','--noproxy','*','--silent','--show-error','--max-time','10','--cacert',root/'certs/https-ca.crt','--resolve',host+':8443:'+bind,'-H','Authorization: Bearer '+token('demo-admin','admin'),'-H','Content-Type: application/json','--data-binary','@-','-o',responsefile,'-w','%{http_code}',origin+'/v1/edge/identity-migrations/'+request['id']+'/approve'],input=payload)
    approval=responsefile.read_text();responsefile.chmod(0o600)
    if status!='200':
        # This hardcoded synthetic fixture has no owner records. Only log an
        # error response, never the successful certificate bundle or JWT.
        raise ValueError('Synthetic HTTPS approval status '+status+': '+approval[:300])
    if json.loads(approval).get('state')!='issued':raise ValueError('Synthetic exact-fingerprint administrator approval did not issue a certificate.')
    # An accepted TLS connection/publish cannot replace independent ingest
    # observation. Deliberately stop only this disposable ingest for one attempt.
    compose('stop','ingest')
    try:
        refused=edge('-finish-secure-pairing',expected=1)
        if 'server has not observed own-gateway broker authorization' not in refused and 'context deadline exceeded' not in refused:
            raise ValueError('Synthetic missing-observation failure was not the intended receipt timeout.')
        pending=compose('exec','-T','postgres','psql','-U','ci','-d','ci','-Atc',"SELECT count(*) FROM edge_pairing_proofs WHERE gateway_id='demo-gw' AND observed_at IS NULL").strip()
        if pending!='1':raise ValueError('Missing-ingest attempt did not create an unobserved challenge.')
        if (saved/'identity.crt').exists():raise ValueError('Certificate installed without fresh server observation.')
        for name,data in before.items():
            if (saved/name).read_bytes()!=data:raise ValueError('Saved identity/key/config changed before authorization receipt.')
    finally:compose('up','-d','--no-deps','--no-build','--pull','never','ingest')
    output=edge('-finish-secure-pairing')
    if 'server observed a fresh own-gateway broker proof' not in output or not (saved/'identity.crt').is_file():
        raise ValueError('Actual activated deployment did not supply a fresh own-gateway receipt.')
    for name,data in before.items():
        if (saved/name).read_bytes()!=data:raise ValueError('Post-pairing replaced saved identity/key/config.')
    proof=compose('exec','-T','postgres','psql','-U','ci','-d','ci','-Atc',"SELECT count(*) FROM edge_pairing_proofs p JOIN gateways g ON g.id=p.gateway_id AND g.tenant_id=p.tenant_id WHERE p.gateway_id='demo-gw' AND p.tenant_id='demo' AND p.observed_at>now()-interval '60 seconds' AND p.cert_fingerprint=g.cert_fingerprint").strip()
    if proof!='1' or oldacl==state.digest(acl):raise ValueError('Fresh DB observation or changed complete ACL not established.')
    logs=compose('logs','--no-color','mosquitto')
    if 'Generated broker ACL changed; reload signal sent' not in logs:raise ValueError('Activated broker ACL watcher did not observe replacement.')
    # Exact new gateway cert must still have no foreign-tenant publish grant.
    common=['docker','run','--rm','--user','0','--network',project+'_default','--group-add',str(gid),'-v',str(root/'certs')+':/certs:ro','-v',str(saved)+':/saved:ro','--entrypoint']
    pub=common+['mosquitto_pub',BROKER_IMAGE,'-V','mqttv5','-h','mosquitto','-p','8883','--cafile','/certs/broker-ca.crt','--cert','/saved/identity.crt','--key','/saved/identity.key','-t','t/foreign/g/foreign/telemetry','-q','1','-r','-m','synthetic-post-pairing-forbidden']
    # pub exit0 varies with broker/client versions; absence is checked through
    # an independent authenticated service subscriber, not the publisher code.
    subprocess.run(pub,capture_output=True,check=False,timeout=15)
    foreign=call(common+['mosquitto_sub',BROKER_IMAGE,'-h','mosquitto','-p','8883','--cafile','/certs/broker-ca.crt','--cert','/certs/ingest.crt','--key','/certs/ingest.key','-t','t/foreign/g/foreign/telemetry','-C','1','-W','3'],expected=27)
    if foreign.strip():raise ValueError('Foreign retained data appeared after pairing.')
    print('PASS actual activated production-transport deployment: compiled edge saved-key request, real HTTPS administrator exact-fingerprint approval, complete post-pairing ACL refresh/watcher reload, missing-ingest observation refuses install with identity/config unchanged, explicit retry obtains fresh own-gateway nonce through real broker+ingest+PostgreSQL receipt before certificate install, and new cert foreign-tenant retained publish absent. Synthetic only; no owner activation, source start or physical meter reading.',flush=True)

    spec=importlib.util.spec_from_file_location('source_fixture',HERE/'test-secure-source-native.py')
    source=importlib.util.module_from_spec(spec);spec.loader.exec_module(source)
    source.source_apply(root,bundle,project,image,host,bind,gid,token('demo-admin','admin'))
