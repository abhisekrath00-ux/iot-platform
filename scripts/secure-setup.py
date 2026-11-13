#!/usr/bin/env python3
"""Prepare, never activate, a private TLS deployment from existing certificates.
No CA/key/account generation, Docker mutation, network exposure or secret output.
Input is `docker compose config --format json`; output contains resolved secrets,
so it is private (0700 directory,0600 compose) and must never be shared.
"""
import argparse, copy, ipaddress, json, os, pathlib, re, subprocess, sys

def check_certificate(certs, stem, ca, purpose, names=()):
    crt,key=certs/(stem+'.crt'),certs/(stem+'.key')
    def run(*args):
        p=subprocess.run(['openssl',*map(str,args)],capture_output=True)
        if p.returncode: raise ValueError('Certificate check failed for '+stem+'; check chain, lifetime, purpose, SAN and key. No deployment changed.')
        return p.stdout
    run('verify','-CAfile',certs/ca,'-purpose',purpose,crt)
    run('x509','-in',crt,'-checkend','86400','-noout')
    for name in names:
        flag='-verify_ip' if is_ip(name) else '-verify_hostname'
        run('verify','-CAfile',certs/ca,'-purpose',purpose,flag,name,crt)
    a=run('x509','-in',crt,'-pubkey','-noout')
    b=run('pkey','-in',key,'-passin','pass:','-pubout')
    if a!=b: raise ValueError('Certificate/key mismatch for '+stem)
    if purpose=='sslclient':
        subject=run('x509','-in',crt,'-subject','-nameopt','RFC2253','-noout').decode().strip()
        if subject!='subject=CN='+stem: raise ValueError('Service certificate must contain only CN='+stem)

def is_ip(s):
    try: ipaddress.ip_address(s);return True
    except ValueError:return False

def check_issuing_ca(certs):
    """Match the runtime ECDSA issuer contract without signing or changing keys."""
    certs=pathlib.Path(certs)
    def run(*args):
        result=subprocess.run(['openssl',*map(str,args)],capture_output=True)
        if result.returncode:
            raise ValueError('Device issuing CA/key cannot be validated. No deployment changed.')
        return result.stdout
    cert=certs/'device-ca.crt';key=certs/'device-ca.key'
    run('verify','-partial_chain','-CAfile',cert,cert)
    run('x509','-in',cert,'-checkend','86400','-noout')
    constraints=run('x509','-in',cert,'-ext','basicConstraints,keyUsage','-noout')
    if not re.search(rb'^\s*CA:TRUE(?:,|\s*$)',constraints,re.M) or not re.search(rb'^\s*(?:[^\n]*, )?Certificate Sign(?:,|\s*$)',constraints,re.M):
        raise ValueError('Device issuer requires CA constraints and certificate signing key use.')
    pub=run('x509','-in',cert,'-pubkey','-noout')
    result=subprocess.run(['openssl','pkey','-pubin','-text','-noout'],input=pub,capture_output=True)
    if result.returncode:raise ValueError('Device CA public key cannot be validated.')
    info=result.stdout
    # x509 in the runtime supports the standard NIST curves, not arbitrary
    # OpenSSL named curves such as secp256k1. Inspect public data only.
    curves=[b'ASN1 OID: '+name for name in [b'secp224r1',b'prime256v1',b'secp384r1',b'secp521r1']]
    if not any(line.strip() in curves for line in info.splitlines()):
        raise ValueError('Device issuer curve is not supported by the runtime signer.')
    keypub=run('pkey','-in',key,'-passin','pass:','-pubout')
    if pub!=keypub:
        raise ValueError('Device issuing key does not match its CA.')


def mount(source,target,readonly=True):
    return dict(type='bind',source=str(source),target=target,read_only=readonly)

def render(base,bind_ip,host,certdir,out,shared_gid):
    ip=ipaddress.ip_address(bind_ip)
    if ip.version!=4 or not any(ip in ipaddress.ip_network(cidr) for cidr in ['10.0.0.0/8','172.16.0.0/12','192.168.0.0/16']): raise ValueError('Choose an explicit private LAN/VPN IPv4 address on the server, not loopback, public or wildcard.')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9.-]{0,252}',host):raise ValueError('Use a confirmed broker/API hostname or IPv4 address only.')
    if not 1<=shared_gid<=2147483647:raise ValueError('A non-root shared host group ID is required.')
    cfg=copy.deepcopy(base);services=cfg.get('services',{})
    for name in ['api','ingest','mosquitto','web']:
        if name not in services:raise ValueError('This supports the single-node API/ingest/mosquitto/web Compose installation only.')
        if services[name].get('network_mode')=='host' or services[name].get('privileged'):raise ValueError('Host networking/privileged service not supported.')
    for name in ['api','web','mosquitto']:
        for p in services[name].get('ports',[]):
            if not isinstance(p,dict) or p.get('host_ip') not in ['127.0.0.1','::1']:raise ValueError('Existing service has non-loopback exposure; review it before secure migration.')
    certdir=pathlib.Path(certdir).resolve();out=pathlib.Path(out).resolve()
    def mounts(stem):return [mount(certdir/(stem+'.crt'),'/run/tls/'+stem+'.crt'),mount(certdir/(stem+'.key'),'/run/tls/'+stem+'.key'),mount(certdir/'broker-ca.crt','/run/tls/broker-ca.crt')]
    for service in ['api','ingest']:
        s=services[service];s.setdefault('environment',{}).update(BROKER_CERT_ONLY='1',MQTT_HOST='mosquitto',MQTT_PORT='8883',MQTT_TLS='true',MQTT_CA_FILE='/run/tls/broker-ca.crt',MQTT_CERT_FILE='/run/tls/'+service+'.crt',MQTT_KEY_FILE='/run/tls/'+service+'.key')
        s.setdefault('volumes',[]).extend(mounts(service));s.setdefault('group_add',[]).append(str(shared_gid))
    api=services['api']
    if not api['environment'].get('FLEET_SIGNING_KEY'):raise ValueError('Existing source-update signing key is not in the resolved API configuration. Configure an approved existing signer before preparing source setup; this tool never generates one.')
    api['environment'].update(MTLS_CA_CERT='/run/tls/device-ca.crt',MTLS_CA_KEY='/run/tls/device-ca.key',EDGE_MQTT_HOST=host,EDGE_MQTT_PORT='8883',EDGE_MQTT_TLS='true',EDGE_MQTT_CA_FILE='/run/tls/broker-ca.crt',BROKER_ACL_FILE='/run/broker-acl/acl')
    api['volumes'].extend([mount(certdir/'device-ca.crt','/run/tls/device-ca.crt'),mount(certdir/'device-ca.key','/run/tls/device-ca.key'),mount(out/'acl','/run/broker-acl',False)])
    api.setdefault('ports',[]).append(dict(target=8443,published='8443',host_ip=bind_ip,protocol='tcp'))
    broker=services['mosquitto'];broker['user']='mosquitto';broker['image']='eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98';broker['ports']=[dict(target=8883,published='8883',host_ip=bind_ip,protocol='tcp')];broker['group_add']=[str(shared_gid)]
    broker['entrypoint']=['/bin/sh','/run/broker-acl-supervisor.sh'];broker['command']=[]
    broker['volumes']=[mount(out/'broker-acl-supervisor.sh','/run/broker-acl-supervisor.sh'),mount(out/'broker.conf','/mosquitto/config/mosquitto.conf'),mount(certdir/'device-ca.crt','/run/tls/device-ca.crt'),*mounts('broker'),mount(out/'acl','/run/broker-acl')]
    services['secure-web']={'image':'nginx:1.27-alpine','network_mode':'service:api','depends_on':['api','web'],'group_add':[str(shared_gid)],'volumes':[mount(out/'https.conf','/etc/nginx/conf.d/default.conf'),*mounts('https')]}
    return cfg

def prepare(args):
    certs=pathlib.Path(args.certs).resolve();out=pathlib.Path(args.output).resolve()
    if out.exists():raise ValueError('Output already exists; do not overwrite a prepared deployment.')
    if not args.confirm_private_route:raise ValueError('A confirmed private LAN/VPN route is required. Preparation does not authorize activation.')
    with open(args.base_config) as f:base=json.load(f)
    cfg=render(base,args.bind,args.host,certs,out,args.shared_gid)
    check_certificate(certs,'broker','broker-ca.crt','sslserver',('mosquitto',args.host))
    check_certificate(certs,'https','https-ca.crt','sslserver',(args.host,))
    for service in ['api','ingest']:check_certificate(certs,service,'device-ca.crt','sslclient')
    check_issuing_ca(certs)
    out.mkdir(mode=0o700);acl=out/'acl';acl.mkdir(mode=0o770);os.chown(acl,-1,args.shared_gid);os.chmod(acl,0o2770)
    def write(name,text,mode=0o600):
        p=out/name;p.write_text(text);os.chmod(p,mode)
    # API and broker receive the shared group, but no secret is made world-readable.
    write('compose.json',json.dumps(cfg,indent=2))
    write('broker-acl-supervisor.sh',pathlib.Path(__file__).with_name('broker-acl-supervisor.sh').read_text(),0o640)
    os.chown(out/'broker-acl-supervisor.sh',-1,args.shared_gid)
    write('previous-compose.json',json.dumps(base,indent=2))
    write('prepare-inputs.json',json.dumps(dict(bind=args.bind,host=args.host,certs=str(certs),shared_gid=args.shared_gid),indent=2))
    write('broker.conf','listener 8883\nprotocol mqtt\ncafile /run/tls/device-ca.crt\ncertfile /run/tls/broker.crt\nkeyfile /run/tls/broker.key\nrequire_certificate true\nuse_identity_as_username true\nallow_anonymous false\nacl_file /run/broker-acl/acl\npersistence false\n',0o640)
    os.chown(out/'broker.conf',-1,args.shared_gid)
    write('https.conf','server {\n listen 8443 ssl;\n ssl_protocols TLSv1.2 TLSv1.3;\n ssl_certificate /run/tls/https.crt;\n ssl_certificate_key /run/tls/https.key;\n client_max_body_size 25m;\n location /v1/ { proxy_pass http://127.0.0.1:8000; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto https; proxy_set_header X-Forwarded-For $remote_addr; }\n location /auth/ { proxy_pass http://127.0.0.1:8000; proxy_set_header Host $host; proxy_set_header X-Forwarded-Proto https; }\n location / { proxy_pass http://web:80; proxy_set_header Host $host; }\n}\n',0o640)
    os.chown(out/'https.conf',-1,args.shared_gid)
    write('README.txt','PREPARED ONLY. No services restarted or ports opened.\nPrivate resolved Compose contains secrets: do not share this directory.\nThe supplied TLS files remain outside the bundle, mounted read-only per service.\nCertificate directories/files must be traversable/readable by the chosen shared group; no chmod/chown of supplied files was performed.\nThe existing full gateway ACL must be generated into acl/acl and checked before starting the secure broker. An empty service-only ACL is not a migration of existing gateways.\nThe deployment still needs explicit owner approval of route, bind, trust installation, downtime and rollback, followed by validated Compose, HTTPS trust, TLS broker and per-gateway source-apply/ACK/reading tests.\nDo not run the prepared file as an unattended upgrade. Normal setup.sh still selects the existing deployment until a safe activation/rollback path is built.\n')
    print('Prepared private deployment bundle at '+str(out)+'. No deployment changed. Activation and rollback are not implemented; see README.txt.')

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--base-config',required=True);p.add_argument('--certs',required=True);p.add_argument('--output',required=True);p.add_argument('--bind',required=True);p.add_argument('--host',required=True);p.add_argument('--shared-gid',required=True,type=int);p.add_argument('--confirm-private-route',action='store_true')
    try:prepare(p.parse_args())
    except (ValueError,OSError,subprocess.SubprocessError) as e:print(str(e),file=sys.stderr);sys.exit(1)
