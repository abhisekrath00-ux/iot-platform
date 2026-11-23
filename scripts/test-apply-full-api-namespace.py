#!/usr/bin/env python3
"""Opt-in full API executable characterization, synthetic local network only.
Run with unshare -Urn after building exact historical/current API binaries.
This does not exercise containers, an updater, device execution or owner state.
TEST_PRODUCT_ARCHIVE_SCRIPTS=1 additionally runs unchanged backup/restore scripts
through a strict test launcher that delegates only to real PostgreSQL tools.
"""
import base64
import hashlib
import hmac
import http.client
import json
import os
import pathlib
import signal
import shutil
import socket
import struct
import subprocess
import tempfile
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]

def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, timeout=60, **kwargs).stdout

def main():
    if os.environ.get('TEST_FULL_API_NAMESPACE') != '1':
        raise SystemExit('explicit TEST_FULL_API_NAMESPACE=1 required')
    interfaces = {item['ifname'] for item in json.loads(run('ip','-json','link','show'))}
    if interfaces != {'lo'}:
        raise SystemExit('requires an isolated namespace with loopback only')
    run('ip', 'link', 'set', 'lo', 'up')
    old = pathlib.Path(os.environ['TEST_OLD_API_BINARY']).resolve()
    new = pathlib.Path(os.environ['TEST_NEW_API_BINARY']).resolve()
    migrations = pathlib.Path(os.environ['TEST_API_MIGRATIONS']).resolve()
    broker_binary = pathlib.Path(os.environ['TEST_MOSQUITTO_BINARY']).resolve()
    pg_host = os.environ['TEST_PG_SOCKET']
    pg_port = os.environ.get('TEST_PG_PORT', '55432')
    pg_user = os.environ.get('TEST_PG_USER', 'sandbox')
    def pg(db, sql):
        return run('psql','-X','-h',pg_host,'-p',pg_port,'-U',pg_user,'-d',db,'-At','-v','ON_ERROR_STOP=1','-c',sql).decode().strip()
    def url(db):
        return f'postgres://{pg_user}@/{db}?host={pg_host}&port={pg_port}&sslmode=disable'
    def packet(sock):
        first=sock.recv(1)
        if not first: raise RuntimeError('MQTT socket closed')
        n=0; mul=1
        while True:
            b=sock.recv(1)[0]; n+=(b&127)*mul
            if b<128: break
            mul*=128
        body=b''
        while len(body)<n:
            body+=sock.recv(n-len(body))
        return first[0],body
    def mqtt_string(value):
        b=value.encode(); return struct.pack('!H',len(b))+b
    def send(sock, kind, body):
        n=len(body); encoded=b''
        while True:
            b=n%128;n//=128
            encoded+=bytes([b|128 if n else b])
            if not n: break
        sock.sendall(bytes([kind])+encoded+body)
    secret='synthetic-full-api-namespace-only'
    def b64(value): return base64.urlsafe_b64encode(value).rstrip(b'=')
    head=b64(b'{"alg":"HS256","typ":"JWT"}')
    claims=b64(json.dumps({'tenant_id':'native-fixture','role':'admin','sub':'native-user','iat':int(time.time())-1,'exp':int(time.time())+600}).encode())
    signed=head+b'.'+claims
    token=(signed+b'.'+b64(hmac.new(secret.encode(),signed,hashlib.sha256).digest())).decode()
    with tempfile.TemporaryDirectory(prefix='full-api-compat-') as tmp:
        temp=pathlib.Path(tmp)
        config=temp/'mosquitto.conf'
        config.write_text('user root\nlistener 18883 127.0.0.1\nallow_anonymous true\npersistence false\n')
        brokerlog=(temp/'broker.log').open('wb')
        broker=subprocess.Popen([str(broker_binary),'-c',str(config)],stdout=brokerlog,stderr=subprocess.STDOUT)
        api=None; observer=None; created=[]; results=[]
        product_scripts=os.environ.get('TEST_PRODUCT_ARCHIVE_SCRIPTS')=='1'
        script_root=temp/'product-script-fixture'
        adapter=None
        if product_scripts:
            (script_root/'scripts').mkdir(parents=True)
            (script_root/'bin').mkdir()
            for name in ('backup.sh','restore.sh'):
                shutil.copy2(ROOT/'scripts'/name,script_root/'scripts'/name)
            adapter=script_root/'bin/docker'
            adapter.write_text(r'''#!/usr/bin/env python3
import os,sys
args=sys.argv[1:]
if len(args)<5 or args[:4]!=['compose','exec','-T','postgres'] or args[4] not in ('pg_dump','pg_restore','psql'):
    raise SystemExit('test adapter refuses non archive/database command')
with open(os.environ['TEST_ADAPTER_LOG'],'a') as log:log.write(args[4]+'\n')
program=args[4]
if program=='pg_restore' and args[5:]==['-l']:
    os.execvp(program,[program,'-l'])
os.execvp(program,[program,'-h',os.environ['TEST_PG_SOCKET'],'-p',os.environ['TEST_PG_PORT']]+args[5:])
''')
            adapter.chmod(0o700)
        def script_env(db):
            return {'PATH':str(script_root/'bin')+':'+os.environ['PATH'],'HOME':tmp,'POSTGRES_USER':pg_user,'POSTGRES_DB':db,'TEST_PG_SOCKET':pg_host,'TEST_PG_PORT':pg_port,'TEST_ADAPTER_LOG':str(temp/'archive-adapter.log')}
        def snapshot(db,archive,tag):
            if not product_scripts:
                run('pg_dump','--dbname',url(db),'--format=custom','--file',str(archive))
                return archive
            destination=script_root/('backups-'+tag)
            out=run('bash',str(script_root/'scripts/backup.sh'),str(destination),env=script_env(db))
            (temp/(tag+'-backup.log')).write_bytes(out)
            archives=list(destination.glob('*.dump.gz'))
            if len(archives)!=1:raise RuntimeError('script backup count')
            selected=archives[0]
            out=run('bash',str(script_root/'scripts/restore.sh'),'--verify',str(selected),env=script_env(db))
            (temp/(tag+'-verify.log')).write_bytes(out)
            # Invalid bytes must fail before even the archive-listing adapter call.
            damaged=destination/'damaged.dump.gz'
            damaged.write_bytes(selected.read_bytes()+b'damaged')
            trusted=selected.with_name(selected.name+'.sha256').read_text().split()[0]
            damaged.with_name(damaged.name+'.sha256').write_text(trusted+'  '+damaged.name+'\n')
            adapter_before=(temp/'archive-adapter.log').read_bytes()
            denied=subprocess.run(['bash',str(script_root/'scripts/restore.sh'),'--verify',str(damaged)],env=script_env(db),capture_output=True,timeout=10)
            if denied.returncode==0 or b'Checksum mismatch' not in denied.stderr or (temp/'archive-adapter.log').read_bytes()!=adapter_before:
                raise RuntimeError('damaged archive was not refused before database-tool invocation')
            (temp/(tag+'-damaged-refused.log')).write_bytes(denied.stdout+denied.stderr)
            return selected
        def restore_archive(db,archive,tag):
            if not product_scripts:
                run('pg_restore','--dbname',url(db),'--no-owner','--exit-on-error','--single-transaction',str(archive))
                return
            out=run('bash',str(script_root/'scripts/restore.sh'),str(archive),env=script_env(db))
            (temp/(tag+'-restore.log')).write_bytes(out)
        def stop(check=True):
            nonlocal api
            if api:
                api.send_signal(signal.SIGINT)
                try: api.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    api.kill();api.wait();raise
                code=api.returncode
                api=None
                if check and code!=0: raise RuntimeError(f'API stop failed: {code}')
                api=None
        def start(binary,db,tag):
            nonlocal api
            # Do not inherit any owner credentials, proxies, service endpoints or auth.
            env={'PATH':os.environ['PATH'],'HOME':tmp,'DATABASE_URL':url(db),'MIGRATIONS_DIR':str(migrations),'JWT_SIGNING_SECRET':secret,'FLEET_SIGNING_KEY':base64.b64encode(bytes(32)).decode(),'API_PORT':'18080','MQTT_HOST':'127.0.0.1','MQTT_PORT':'18883','CACHE_BACKEND':'off','OAUTH_STATE_BACKEND':'memory'}
            log=(temp/(tag+'.log')).open('wb')
            api=subprocess.Popen([str(binary)],env=env,cwd=tmp,stdout=log,stderr=subprocess.STDOUT)
            for _ in range(150):
                if api.poll() is not None: raise RuntimeError((temp/(tag+'.log')).read_text())
                try:
                    c=http.client.HTTPConnection('127.0.0.1',18080,timeout=1);c.request('GET','/healthz');r=c.getresponse();r.read();c.close()
                    if r.status==200:return
                except OSError: pass
                time.sleep(.1)
            raise RuntimeError('API health timeout')
        def request(method):
            c=http.client.HTTPConnection('127.0.0.1',18080,timeout=20)
            c.request(method,'/v1/gateways/native-gw/config-push',body='{}' if method=='POST' else None,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
            r=c.getresponse();body=r.read().decode();c.close();return r.status,body
        def delivery():
            kind,body=packet(observer)
            if kind>>4!=3:raise RuntimeError('not MQTT publication')
            n=struct.unpack('!H',body[:2])[0]
            topic=body[2:2+n].decode();pos=2+n
            qos=(kind>>1)&3
            if qos:
                pid=body[pos:pos+2];pos+=2
                send(observer,0x40,pid)
            envelope=json.loads(body[pos:])
            if topic!='t/native-fixture/g/native-gw/config' or envelope['tenant_id']!='native-fixture' or not envelope.get('signature'):raise RuntimeError('wrong envelope')
            return envelope['push_id']
        try:
            for _ in range(50):
                try:observer=socket.create_connection(('127.0.0.1',18883),timeout=2);break
                except OSError:time.sleep(.1)
            if observer is None:raise RuntimeError('broker startup failed: '+(temp/'broker.log').read_text())
            observer.settimeout(3)
            send(observer,0x10,mqtt_string('MQTT')+b'\x04\x02\x00\x3c'+mqtt_string('readonly-compat-observer'))
            if packet(observer)!=(0x20,b'\x00\x00'):raise RuntimeError('CONNACK failed')
            send(observer,0x82,b'\x00\x01'+mqtt_string('t/native-fixture/g/native-gw/config')+b'\x01')
            if packet(observer)[0]!=0x90:raise RuntimeError('SUBACK failed')
            for direction,first,second in [('old-to-new',old,new),('new-to-old',new,old)]:
                for mode in ('baseline','guarded-post-snapshot','guarded-pre-snapshot','guarded-retained-database'):
                    guarded=mode!='baseline'
                    db='native_compat_'+uuid.uuid4().hex
                    pg('postgres','CREATE DATABASE '+db);created.append(db)
                    tag=direction+'-'+mode
                    start(first,db,tag+'-first')
                    pg(db,"INSERT INTO tenants(id,name) VALUES('native-fixture','Synthetic'); INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('native-user','native-fixture','native@example.invalid','Synthetic','admin'); INSERT INTO sites(id,tenant_id,name) VALUES('native-site','native-fixture','Synthetic'); INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('native-gw','native-fixture','native-site','SYNTHETIC-NATIVE','active')")
                    if guarded:pg(db,(ROOT/'server/internal/applyadmission/candidate.sql').read_text())
                    archive=temp/(tag+'.dump')
                    if mode=='guarded-pre-snapshot':archive=snapshot(db,archive,tag)
                    first_response=request('POST')
                    if first_response[0]!=202:raise RuntimeError(first_response)
                    first_id=delivery()
                    pg(db,"UPDATE gateway_config_pushes SET created_at=now()-interval '1 day'")
                    stop()
                    row_before=pg(db,"SELECT row_to_json(p)::text FROM gateway_config_pushes p")
                    if guarded and mode!='guarded-retained-database':
                        restored=db+'_restore';pg('postgres','CREATE DATABASE '+restored);created.append(restored)
                        if mode!='guarded-pre-snapshot':archive=snapshot(db,archive,tag)
                        restore_archive(restored,archive,tag)
                        db=restored
                        catalog=pg(db,"SELECT (to_regprocedure('apply_attempt_guard_candidate()') IS NOT NULL)::int || ',' || (EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='gateway_config_pushes'::regclass AND tgname='apply_attempt_guard_candidate' AND tgenabled='O'))::int || ',' || (EXISTS(SELECT 1 FROM pg_index WHERE indexrelid='apply_open_attempt_candidate'::regclass AND indisunique AND indisvalid))::int")
                        if catalog!='1,1,1':raise RuntimeError('restore catalog mismatch: '+catalog)
                        row_after=pg(db,"SELECT row_to_json(p)::text FROM gateway_config_pushes p")
                        if mode in ('guarded-post-snapshot','guarded-retained-database') and row_after!=row_before:raise RuntimeError('restored attempt changed')
                        if mode=='guarded-pre-snapshot' and row_after:raise RuntimeError('earlier snapshot unexpectedly contains later attempt')
                    start(second,db,tag+'-second')
                    seen=request('GET');again=request('POST')
                    if seen[0]!=200:raise RuntimeError(seen)
                    expected_status='sent' if mode in ('guarded-post-snapshot','guarded-retained-database') else 'expired'
                    seen_rows=json.loads(seen[1])
                    if mode=='guarded-pre-snapshot':
                        if seen_rows:raise RuntimeError('stale snapshot retained later attempt')
                        expected_status='absent-post-snapshot-attempt'
                    elif seen_rows[0]['status']!=expected_status:raise RuntimeError(seen)
                    if again[0]!=(409 if mode in ('guarded-post-snapshot','guarded-retained-database') else 202):raise RuntimeError((seen,again))
                    ids=[first_id]
                    if mode not in ('guarded-post-snapshot','guarded-retained-database'):ids.append(delivery())
                    else:
                        observer.settimeout(.3)
                        try:packet(observer);raise RuntimeError('unexpected second delivery')
                        except socket.timeout:pass
                        observer.settimeout(3)
                    if pg(db,'SELECT count(*) FROM schema_migrations')!='93':raise RuntimeError('ledger changed')
                    stop()
                    results.append({'case':tag,'first_http':202,'get_status':expected_status,'next_http':again[0],'actual_broker_deliveries':len(ids),'distinct_push_ids':len(set(ids)),'restore':'NONE, current database retained' if mode=='guarded-retained-database' else ('full database before attempt' if mode=='guarded-pre-snapshot' else 'full database after attempt') if guarded else 'none','candidate_guard_catalog': 'function/enabled trigger/valid unique index checked' if guarded else 'absent', 'scope':'actual unmodified full API binaries, isolated plaintext loopback broker, no executor/device'})
            print(json.dumps({'archive_script_sha256':{name:hashlib.sha256((ROOT/'scripts'/name).read_bytes()).hexdigest() for name in ('backup.sh','restore.sh')} if product_scripts else None, 'archive_method':'actual unchanged product scripts with narrow container-launch adapter, real PG tools' if product_scripts else 'direct pg_dump/atomic pg_restore', 'binary_sha256':{str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in (old,new)},'results':results,'limits':['Not container/image/update acceptance; sequential process replacement, not simultaneous mixed writers','Both binaries built from complete fixed commit source trees','Plaintext synthetic broker inside loopback-only namespace, not mTLS acceptance','Shared production MQTT publisher unchanged, ACK-loss resend remains open','No gateway executor or physical application','Product scripts use legacy Compose dispatch with a test launcher, not secure deployment selector or Docker; damaged checksums refused before launcher invocation']},indent=2),flush=True)
        finally:
            stop(check=False)
            if observer:observer.close()
            broker.terminate();broker.wait(timeout=5);brokerlog.close()
            for db in reversed(created):pg('postgres','DROP DATABASE '+db+' WITH (FORCE)')
            if os.environ.get('TEST_API_LOG_DIR'):
                destination=pathlib.Path(os.environ['TEST_API_LOG_DIR']);destination.mkdir(parents=True,exist_ok=True)
                for log in temp.glob('*.log'):shutil.copy2(log,destination/log.name)

if __name__=='__main__':main()
