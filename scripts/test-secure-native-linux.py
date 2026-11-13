#!/usr/bin/env python3
"""Disposable standard Ubuntu runner only: synthetic Compose TLS startup test.
No owner config, saved identity, remote host, signing grant or deployment changes.
"""
import importlib.util, json, os, pathlib, subprocess, tempfile, types, time, urllib.request

def run(*args, **kw):
    return subprocess.run(list(map(str,args)),check=True,**kw)

def main():
    if os.environ.get('GITHUB_ACTIONS')!='true' or os.environ.get('RUNNER_OS')!='Linux' or os.geteuid()!=0:
        raise SystemExit('Only run as root on a disposable Ubuntu Actions runner')
    root=pathlib.Path(__file__).resolve().parent.parent
    spec=importlib.util.spec_from_file_location('secure_setup',root/'scripts/secure-setup.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
    with tempfile.TemporaryDirectory(prefix='hexthings-compose-ci-') as td:
        work=pathlib.Path(td);certs=work/'certs';certs.mkdir();gid=29991
        host='hexthings-ci.invalid';bind=subprocess.check_output(['ip','-4','-o','addr','show','scope','global'],text=True).split()[3].split('/')[0]
        def openssl(*a):run('openssl',*a,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        for stem in ['device-ca','broker-ca','https-ca']:
            openssl('req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',certs/(stem+'.key'),'-out',certs/(stem+'.crt'),'-days','2','-subj','/CN=Synthetic '+stem,'-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign')
        for stem,issuer,purpose,san in [('broker','broker-ca','serverAuth','DNS:mosquitto,DNS:'+host),('https','https-ca','serverAuth','DNS:'+host),('api','device-ca','clientAuth',''),('ingest','device-ca','clientAuth',''),('AXON-DEMO-1','device-ca','clientAuth','')]:
            openssl('req','-new','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',certs/(stem+'.key'),'-out',certs/(stem+'.csr'),'-subj','/CN='+stem)
            ext=certs/(stem+'.ext');ext.write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature\nextendedKeyUsage='+purpose+'\n'+('subjectAltName='+san+'\n' if san else ''))
            openssl('x509','-req','-in',certs/(stem+'.csr'),'-CA',certs/(issuer+'.crt'),'-CAkey',certs/(issuer+'.key'),'-CAcreateserial','-out',certs/(stem+'.crt'),'-days','2','-extfile',ext)
        for p in certs.iterdir():os.chown(p,-1,gid);p.chmod(0o640)
        os.chown(certs,-1,gid);certs.chmod(0o750)
        # Build real API/ingest locally, but a small CI image without package/release outputs.
        for binary in ['api','ingest','tenantctl']:
            run('go','build','-o',work/binary,'./cmd/'+binary,cwd=root/'server',env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='auto'))
        run('go','build','-o',work/'edge-agent','./cmd/edge-agent',cwd=root/'edge',env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='auto'))
        (work/'Dockerfile').write_text('FROM alpine:3.20\nRUN adduser -D app\nCOPY api ingest tenantctl edge-agent /bin/\nCOPY migrations /migrations\nUSER app\nWORKDIR /\n')
        import shutil
        shutil.copytree(root/'server/migrations',work/'migrations')
        project='hexthings-secure-ci-'+str(os.getpid());image=project+':local'
        run('docker','build','-t',image,work)
        db='postgres://ci:synthetic-ci-only@postgres:5432/ci?sslmode=disable'
        base={'services':{
          'postgres':{'image':'postgres:16-alpine','environment':{'POSTGRES_USER':'ci','POSTGRES_PASSWORD':'synthetic-ci-only','POSTGRES_DB':'ci'},'healthcheck':{'test':['CMD-SHELL','pg_isready -U ci'],'interval':'1s','timeout':'2s','retries':30}},
          'api':{'image':image,'command':['/bin/api'],'environment':{'DATABASE_URL':db,'JWT_SIGNING_SECRET':'synthetic-ci-only-not-for-deployment','FLEET_SIGNING_KEY':'BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc=','LOCAL_LOGIN':'1'},'ports':[{'target':8000,'published':'18100','host_ip':'127.0.0.1'}],'depends_on':{'postgres':{'condition':'service_healthy'}}},
          'ingest':{'image':image,'command':['/bin/ingest'],'environment':{'DATABASE_URL':db},'depends_on':{'postgres':{'condition':'service_healthy'}}},
          'mosquitto':{'image':'eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98','ports':[{'target':1883,'published':'1883','host_ip':'127.0.0.1'}]},
          'web':{'image':'nginx:1.27-alpine','ports':[{'target':80,'published':'18800','host_ip':'127.0.0.1'}]}}}
        basepath=work/'base.json';basepath.write_text(json.dumps(base));out=work/'prepared'
        m.prepare(types.SimpleNamespace(certs=certs,output=out,confirm_private_route=True,base_config=basepath,bind=bind,host=host,shared_gid=gid))
        # Synthetic fixture only. Production must preserve/generate the full current gateway ACL.
        acl=out/'acl/acl';acl.write_text('user api\ntopic write t/+/g/+/config\ntopic read t/#\nuser ingest\ntopic read t/#\n');os.chown(acl,-1,gid);acl.chmod(0o640)
        compose=['docker','compose','-p',project,'-f',str(out/'compose.json')]
        try:
            run(*compose,'config','--quiet')
            run(*compose,'up','-d')
            for _ in range(90):
                try:
                    with urllib.request.urlopen('http://127.0.0.1:18100/healthz',timeout=2) as r:
                        if r.status==200:break
                except Exception:pass
                time.sleep(1)
            else:raise RuntimeError('Real API container did not become healthy')
            run('curl','--noproxy','*','--fail','--silent','--show-error','--cacert',certs/'https-ca.crt','--resolve',host+':8443:'+bind,'https://'+host+':8443/','-o',work/'web.html')
            if 'Welcome to nginx' not in (work/'web.html').read_text():raise RuntimeError('HTTPS web proxy did not reach web service')
            code=subprocess.check_output(['curl','--noproxy','*','--silent','--show-error','--cacert',str(certs/'https-ca.crt'),'--resolve',host+':8443:'+bind,'https://'+host+':8443/v1/gateways','-o',str(work/'api.txt'),'-w','%{http_code}'],text=True)
            if code not in ['401','403']:raise RuntimeError('HTTPS API proxy route did not enforce auth: '+code)
            # A full DB snapshot replaces the bootstrap fixture ACL, then reloads.
            # Two tenants and direct/read-only kinds are covered by DB unit tests.
            run(*compose,'exec','-T','api','/bin/api','-seed-demo')
            run(*compose,'exec','-T','postgres','psql','-U','ci','-d','ci','-v','ON_ERROR_STOP=1','-c',"UPDATE gateways SET cert_fingerprint='synthetic-fingerprint' WHERE id='demo-gw'")
            exported=subprocess.check_output(compose+['exec','-T','api','/bin/tenantctl','broker-acl-export'],text=True)
            if 'user AXON-DEMO-1' not in exported or 't/demo/g/demo-gw/telemetry' not in exported:raise RuntimeError('Snapshot lost current gateway ACL')
            staged=out/'acl/acl.next';staged.write_text(exported);os.chown(staged,-1,gid);staged.chmod(0o640);os.replace(staged,acl)
            # Supervisor must notice the atomic replacement itself, without Docker access.
            time.sleep(2)
            # Separate device and broker CAs, internal SAN, service UID/group and ACL.
            client=['docker','run','--rm','--network',project+'_default','--group-add',str(gid),'-v',str(certs)+':/certs:ro','--entrypoint','mosquitto_pub','eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98','-V','mqttv5','-h','mosquitto','-p','8883','--cafile','/certs/broker-ca.crt','-t','t/ci/g/ci/config','-m','synthetic-ci-no-owner-command','-r','-q','1']
            run(*client,'--cert','/certs/api.crt','--key','/certs/api.key')
            gwclient=client.copy();gwclient[gwclient.index('-t')+1]='t/demo/g/demo-gw/telemetry'
            run(*gwclient,'--cert','/certs/AXON-DEMO-1.crt','--key','/certs/AXON-DEMO-1.key')
            def retained(topic):
                sub=client[:client.index('-t')];sub[sub.index('mosquitto_pub')]='mosquitto_sub'
                return subprocess.run(sub+['-t',topic,'--cert','/certs/ingest.crt','--key','/certs/ingest.key','-C','1','-W','3'],capture_output=True,text=True)
            own=retained('t/demo/g/demo-gw/telemetry')
            if own.returncode!=0 or own.stdout.strip()!='synthetic-ci-no-owner-command':raise RuntimeError('Own gateway publish not received by authenticated subscriber')
            # A new topic outside the regenerated gateway grant must be rejected.
            gwclient[gwclient.index('-t')+1]='t/foreign/g/foreign/telemetry'
            denied=subprocess.run(gwclient+['--cert','/certs/AXON-DEMO-1.crt','--key','/certs/AXON-DEMO-1.key'],capture_output=True)
            # mosquitto_pub exit0 is not an authorization receipt on every version.
            # Observe the broker state with a service certificate instead.
            foreign=retained('t/foreign/g/foreign/telemetry')
            if foreign.returncode!=27 or foreign.stdout.strip():raise RuntimeError('Cross-tenant retained message present or negative read inconclusive')
            bad=subprocess.run(client,capture_output=True)
            if bad.returncode==0:raise RuntimeError('Certificate-required listener accepted anonymous publisher')
            run(*compose,'exec','-T','api','sh','-c','test -r /run/tls/api.key && test -r /run/tls/device-ca.key && touch /run/broker-acl/ci-write && rm /run/broker-acl/ci-write')
            run(*compose,'exec','-T','ingest','sh','-c','test -r /run/tls/ingest.key')
            state=subprocess.check_output(compose+['ps','--services','--status','running'],text=True).splitlines()
            for service in ['api','ingest','mosquitto','web','secure-web','postgres']:
                if service not in state:raise RuntimeError('Container not running: '+service)
            print('PASS synthetic Docker Compose schema, real API+ingest startup, TLS web/API routing, separate CAs, container-group key/ACL access, full DB gateway ACL snapshot/reload, own-gateway publish, cross-tenant refusal, service-cert MQTT publish and anonymous refusal. Not activation/rollback or owner deployment evidence.',flush=True)
            # Separate real transport migration and forced post-start failure.
            # Reset only this disposable project's DB and volumes, never owner state.
            run(*compose,'down','--volumes','--remove-orphans')
            import copy
            lab=copy.deepcopy(base)
            for service in ['api','ingest']:lab['services'][service]['environment'].update(MQTT_HOST='mosquitto',MQTT_PORT='1883',MQTT_TLS='false')
            plain=work/'lab-broker.conf';plain.write_text('listener 1883\nallow_anonymous true\npersistence false\n');plain.chmod(0o644)
            lab['services']['mosquitto']['volumes']=[dict(type='bind',source=str(plain),target='/mosquitto/config/mosquitto.conf',read_only=True)]
            lab['services']['api']['environment']['MTLS_CA_CERT']='/run/old-device-ca.crt'
            lab['services']['api']['volumes']=[dict(type='bind',source=str(certs/'device-ca.crt'),target='/run/old-device-ca.crt',read_only=True)]
            # root default file makes read-only current Compose inspection exact.
            default=work/'docker-compose.yml';default.write_text(json.dumps(lab));default.chmod(0o600)
            oldcompose=['docker','compose','-p',project,'-f',str(default)]
            run(*oldcompose,'up','-d')
            for _ in range(90):
                try:
                    with urllib.request.urlopen('http://127.0.0.1:18100/healthz',timeout=2)as response:
                        if response.status==200:break
                except Exception:pass
                time.sleep(.5)
            else:raise RuntimeError('Baseline API did not become healthy before migration')
            resolved=json.loads(subprocess.check_output(oldcompose+['config','--format','json'],text=True))
            txbase=work/'transaction-base.json';txbase.write_text(json.dumps(resolved));txbase.chmod(0o600)
            txout=work/'transaction-prepared'
            m.prepare(types.SimpleNamespace(certs=certs,output=txout,confirm_private_route=True,base_config=txbase,bind=bind,host=host,shared_gid=gid))
            def load(name):
                spec=importlib.util.spec_from_file_location(name,root/'scripts'/(name+'.py'));module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
            pf=load('secure-preflight');state=load('secure-state');engine=load('secure-transaction');adapter=load('secure-disposable-docker');production=load('secure-transport-docker')
            readiness=load('secure-readiness').inspect(work,project)
            if readiness['status']!='migration_ledger_current' or readiness['transport_activation_allowed'] or readiness['writers']!=['api','ingest']:
                raise RuntimeError('Read-only current operator/schema/writer check did not match fixture')
            print('PASS read-only current API setup protocol and full packaged migration ledger; no bootstrap or activation.',flush=True)
            receipt=pf.preflight(txout,project,work)
            class NewDataRollback(adapter.DisposableDocker):
                def accept(self,value,target):
                    super().accept(value,target)
                    if target=='candidate':
                        # Synthetic row is created after the verified backup.
                        # Its survival distinguishes config rollback from an
                        # accidental replay of that earlier archive.
                        self.compose(value['candidate'],'exec','-T','postgres','psql','-U','ci','-d','ci','-v','ON_ERROR_STOP=1','-c',"CREATE TABLE ci_after_backup_proof (id integer PRIMARY KEY); INSERT INTO ci_after_backup_proof VALUES (1)")
                        raise ValueError('Deliberate post-acceptance fixture failure with new DB data.')
            observer=NewDataRollback(work,txout,project,gid)
            effects=production.DockerTransport(work,project,txout/'preflight.json',observer.authorize,observer.accept)
            images={name:item['image']for name,item in receipt['current_services'].items()}
            with state.Journal(work)as journal:
                try:engine.Transaction(journal,effects).activate(project,txout/'previous-runtime.json',txout/'candidate-runtime.json',images,txout/'preflight.json')
                except RuntimeError as error:
                    if journal.read()['phase']!='restored' or not observer.accepted_candidate:raise
                    print('Expected synthetic post-start failure restored exact previous runtime:',str(error),flush=True)
                else:raise RuntimeError('Deliberate candidate failure was not exercised')
            selected=state.selection(work)
            if selected['config']!=str(txout/'previous-runtime.json'):raise RuntimeError('Rollback fell back instead of selecting exact prior runtime')
            # The DB was backed up/verified but never restored or removed during
            # configuration-only rollback. Existing schema ledger is preserved.
            ledger=subprocess.check_output(oldcompose+['exec','-T','postgres','psql','-U','ci','-d','ci','-Atc','SELECT count(*) FROM schema_migrations'],text=True).strip()
            if not ledger.isdigit() or int(ledger)<93:raise RuntimeError('Rollback lost the existing database migration ledger')
            sentinel=subprocess.check_output(oldcompose+['exec','-T','postgres','psql','-U','ci','-d','ci','-Atc','SELECT count(*) FROM ci_after_backup_proof'],text=True).strip()
            if sentinel!='1':raise RuntimeError('Configuration rollback lost data created after the backup')
            print('PASS production transport Docker adapter: real disposable baseline -> private immutable preflight -> verified pg_dump/listing -> stopped-writer current-image ACL export -> TLS candidate acceptance -> deliberate post-start failure -> exact-image/config rollback and restored selection; post-backup DB row retained. Not owner activation/bootstrap or power-loss proof.',flush=True)
            # A second, distinct synthetic transaction verifies the successful
            # commit path too. Archive evidence only in this disposable fixture;
            # no owner state settlement/delete interface is supplied.
            os.rename(work/'.secure-deployment',work/'.ci-rollback-evidence')
            receipt=pf.preflight(txout,project,work)
            observer=adapter.DisposableDocker(work,txout,project,gid)
            effects=production.DockerTransport(work,project,txout/'preflight.json',observer.authorize,observer.accept)
            images={name:item['image'] for name,item in receipt['current_services'].items()}
            with state.Journal(work) as journal:
                result=engine.Transaction(journal,effects).activate(project,txout/'previous-runtime.json',txout/'candidate-runtime.json',images,txout/'preflight.json')
                if result['state']!='committed' or not observer.accepted_candidate:raise RuntimeError('Positive synthetic transaction did not commit after acceptance')
            selected=state.selection(work)
            if selected['config']!=str(txout/'candidate-runtime.json'):raise RuntimeError('Committed selector is not the exact candidate runtime')
            print('PASS production transport Docker adapter: second disposable transaction accepted exact candidate images, HTTPS, authenticated MQTT, explicit anonymous refusal and committed selection. Not owner activation/bootstrap.',flush=True)
            load('test-secure-pairing-native').post_pair(work,txout,project,image,host,bind,gid)
            # Export the Go test binary, not credentials; execute inside the
            # same disposable Compose network/DB via a one-shot test image.
            # No old app or candidate operator is substituted for ACL export.
            run('go','test','-c','-o',work/'bootstrap-tests','./internal/setupbootstrap',cwd=root/'server',env=dict(os.environ,CGO_ENABLED='0',GOTOOLCHAIN='auto'))
            testdocker=work/'Bootstrap.Dockerfile'
            testdocker.write_text('FROM alpine:3.20\nCOPY bootstrap-tests /bootstrap-tests\nCOPY migrations /migrations\nWORKDIR /\nENTRYPOINT ["/bootstrap-tests"]\n')
            testimage=project+':bootstrap-tests'
            run('docker','build','-f',testdocker,'-t',testimage,work)
            try:
                run('docker','run','--rm','--network',project+'_default','-e','TEST_DATABASE_URL='+db,'-e','BOOTSTRAP_TEST_MIGRATIONS=/migrations',testimage,'-test.v')
            finally:run('docker','image','rm',testimage)
            print('PASS isolated reviewed additive bootstrap catalog/history guards, atomic rollback and retained identity/source data in real PostgreSQL. Not owner app upgrade or old API compatibility test.',flush=True)
            run(*oldcompose,'down','--volumes','--remove-orphans')

        finally:
            subprocess.run(compose+['logs','--no-color','--tail','80'])
            subprocess.run(compose+['down','--volumes','--remove-orphans'],check=False)
            subprocess.run(['docker','image','rm',image],check=False)

if __name__=='__main__':main()
