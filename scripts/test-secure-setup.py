#!/usr/bin/env python3
import importlib.util,pathlib,tempfile,unittest,subprocess,os,json,types
spec=importlib.util.spec_from_file_location('secure_setup',pathlib.Path(__file__).with_name('secure-setup.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Tests(unittest.TestCase):
 def base(self):return {'services':{k:{'ports':[{'target':p,'published':str(p),'host_ip':'127.0.0.1'}],'environment':{'FLEET_SIGNING_KEY':'synthetic-existing-only'} if k=='api' else {}} for k,p in [('api',8000),('ingest',9000),('web',80),('mosquitto',1883)]}}
 def test_full_render_is_private_and_tls_not_merge_with_lab_listener(self):
  base=self.base();cfg=m.render(base,'192.168.1.20','meter.example.invalid','/existing/certs','/private/prepared',1000);s=cfg['services'];self.assertEqual(s['mosquitto']['user'],'mosquitto');self.assertEqual(s['mosquitto']['image'],'eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98');self.assertEqual(s['mosquitto']['ports'],[dict(target=8883,published='8883',host_ip='192.168.1.20',protocol='tcp')]);self.assertEqual(s['api']['environment']['MQTT_TLS'],'true');self.assertEqual(s['ingest']['environment']['MQTT_HOST'],'mosquitto');self.assertEqual(s['secure-web']['network_mode'],'service:api');self.assertNotIn('ports',s['secure-web']);self.assertNotEqual(s['api']['environment']['MTLS_CA_CERT'],s['api']['environment']['MQTT_CA_FILE']);self.assertEqual(base['services']['mosquitto']['ports'][0]['target'],1883)
  for v in s['api']['volumes']:self.assertTrue(v['read_only'] or v['target']=='/run/broker-acl')
 def test_wrong_route_public_bind_missing_signer_fail_closed(self):
  for ip in ['0.0.0.0','127.0.0.1','8.8.8.8','169.254.1.2','240.1.2.3','192.0.0.1','::1']:
   with self.assertRaises(ValueError):m.render(self.base(),ip,'meter.example.invalid','/certs','/out',1000)
  base=self.base();base['services']['api']['environment']={}
  with self.assertRaises(ValueError):m.render(base,'192.168.1.20','meter.example.invalid','/certs','/out',1000)
 def test_existing_exposure_is_not_silently_rewritten(self):
  base=self.base();base['services']['web']['ports'][0]['host_ip']='0.0.0.0'
  with self.assertRaises(ValueError):m.render(base,'192.168.1.20','meter.example.invalid','/certs','/out',1000)
 def test_issuing_ca_matches_runtime_without_key_changes(self):
  with tempfile.TemporaryDirectory() as td:
   certs=pathlib.Path(td)
   def generate(curve='P-256',ca='TRUE',usage='keyCertSign,cRLSign',rsa=False):
    args=['openssl','req','-x509','-newkey',('rsa:2048' if rsa else 'ec')]
    if not rsa:args+=['-pkeyopt','ec_paramgen_curve:'+curve]
    args+=['-nodes','-keyout',str(certs/'device-ca.key'),'-out',str(certs/'device-ca.crt'),'-days','2','-subj','/CN=Synthetic Issuer','-addext','basicConstraints=critical,CA:'+ca,'-addext','keyUsage=critical,'+usage]
    self.assertEqual(subprocess.run(args,capture_output=True).returncode,0)
   for curve in ['P-256','P-384','P-521']:
    generate(curve);before=(certs/'device-ca.key').read_bytes();m.check_issuing_ca(certs);self.assertEqual((certs/'device-ca.key').read_bytes(),before)
   # Legacy SEC1 encoding remains accepted, with no conversion of supplied keys.
   subprocess.run(['openssl','ec','-in',str(certs/'device-ca.key'),'-out',str(certs/'sec1.key')],capture_output=True,check=True)
   (certs/'device-ca.key').write_bytes((certs/'sec1.key').read_bytes());m.check_issuing_ca(certs)
   for options in [dict(rsa=True),dict(curve='secp256k1'),dict(ca='FALSE'),dict(usage='digitalSignature')]:
    generate(**options);before=(certs/'device-ca.key').read_bytes()
    with self.assertRaises(ValueError):m.check_issuing_ca(certs)
    self.assertEqual((certs/'device-ca.key').read_bytes(),before)
   generate();old=(certs/'device-ca.key').read_bytes();generate();(certs/'device-ca.key').write_bytes(old)
   with self.assertRaises(ValueError):m.check_issuing_ca(certs)
 def test_synthetic_certificates_prepare_private_files_without_changing_base_or_keys(self):
  with tempfile.TemporaryDirectory() as td:
   root=pathlib.Path(td);certs=root/'certs';certs.mkdir()
   def run(*a):
    p=subprocess.run(['openssl',*map(str,a)],capture_output=True)
    self.assertEqual(p.returncode,0,'Synthetic certificate fixture generation failed')
   for stem in ['device-ca','broker-ca','https-ca']:
    run('req','-x509','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',certs/(stem+'.key'),'-out',certs/(stem+'.crt'),'-days','2','-subj','/CN=Synthetic '+stem,'-addext','basicConstraints=critical,CA:TRUE','-addext','keyUsage=critical,keyCertSign,cRLSign')
   for stem,issuer,purpose,san in [('broker','broker-ca','serverAuth','DNS:mosquitto,DNS:meter.example.invalid'),('https','https-ca','serverAuth','DNS:meter.example.invalid'),('api','device-ca','clientAuth',''),('ingest','device-ca','clientAuth','')]:
    run('req','-new','-newkey','ec','-pkeyopt','ec_paramgen_curve:P-256','-nodes','-keyout',certs/(stem+'.key'),'-out',certs/(stem+'.csr'),'-subj','/CN='+stem)
    ext=certs/(stem+'.ext');ext.write_text('basicConstraints=CA:FALSE\nkeyUsage=digitalSignature\nextendedKeyUsage='+purpose+'\n'+('subjectAltName='+san+'\n' if san else ''))
    run('x509','-req','-in',certs/(stem+'.csr'),'-CA',certs/(issuer+'.crt'),'-CAkey',certs/(issuer+'.key'),'-CAcreateserial','-out',certs/(stem+'.crt'),'-days','2','-extfile',ext)
   base=root/'base.json';base.write_text(json.dumps(self.base()));out=root/'prepared';before={p.name:p.read_bytes() for p in certs.glob('*.key')}
   args=types.SimpleNamespace(certs=certs,output=out,confirm_private_route=True,base_config=base,bind='192.168.1.20',host='meter.example.invalid',shared_gid=(29991 if os.getgid()==0 else os.getgid()))
   m.prepare(args);self.assertTrue((out/'compose.json').exists());self.assertEqual((out/'compose.json').stat().st_mode&0o777,0o600);self.assertEqual(out.stat().st_mode&0o777,0o700);self.assertEqual(before,{p.name:p.read_bytes() for p in certs.glob('*.key')});self.assertFalse((out/'acl'/'acl').exists());self.assertEqual(json.loads(base.read_text()),self.base())
   pspec=importlib.util.spec_from_file_location('preflight',pathlib.Path(__file__).with_name('secure-preflight.py'));pf=importlib.util.module_from_spec(pspec);pspec.loader.exec_module(pf)
   fixturebase=self.base();fixturebase['services']['postgres']={}
   # Exact baseline with issuer file bind. Nothing here touches real Docker.
   fixturebase['services']['api']['environment']['MTLS_CA_CERT']='/run/old-ca.crt'
   fixturebase['services']['api']['volumes']=[dict(type='bind',source=str(certs/'device-ca.crt'),target='/run/old-ca.crt')]
   (out/'previous-compose.json').write_text(json.dumps(fixturebase));(out/'compose.json').write_text(json.dumps(m.render(fixturebase,args.bind,args.host,certs,out,args.shared_gid)))
   calls=[]
   def fake(argv,**kw):
    calls.append(argv)
    if argv[:2]==['docker','inspect']:answer=json.dumps([dict(Id='fixture-'+name,Image='sha256:'+'a'*64,Config={'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':name}},State={'Status':'running'})for name in fixturebase['services']])
    elif argv[:3]==['docker','image','inspect']:
     reference=argv[3];answer=json.dumps([{'Id':reference if reference.startswith('sha256:')else 'sha256:'+'b'*64,'RepoDigests':[reference]}])
    elif argv[-3:]==['config','--format','json']:answer=json.dumps(fixturebase)
    elif argv[-2:]==['ps','-q']:answer='fixture-id'
    elif argv[-1]=='--quiet':answer=''
    elif argv[-1]=='broker-acl-export':answer='# hexmon broker ACL - fixture\nuser api\nuser ingest\nuser GATEWAY-1\n'
    else:raise AssertionError('Unexpected Docker action '+str(argv))
    return types.SimpleNamespace(returncode=0,stdout=answer)
   receipt=pf.preflight(out,'fixture',root,execute=fake)
   self.assertEqual(set(receipt['runtime_configs']),{'previous','candidate'});self.assertEqual((out/'previous-runtime.json').stat().st_mode&0o777,0o600);self.assertEqual(json.loads((out/'candidate-runtime.json').read_text())['services']['api']['pull_policy'],'never');self.assertFalse(receipt['activation_allowed']);self.assertEqual(receipt['gateway_count'],1);self.assertEqual((out/'preflight.json').stat().st_mode&0o777,0o600)
   self.assertTrue(all(not any(x in a for x in ['up','stop','start','run','kill','build','pull'])for a in calls));self.assertEqual(before,{p.name:p.read_bytes() for p in certs.glob('*.key')})
   # A failed local image read must not reuse old immutable plans/receipt or
   # decide to pull/build anything. No unsupported fallback is attempted.
   def missing_image(argv,**kw):
    if argv[:3]==['docker','image','inspect']:return types.SimpleNamespace(returncode=1,stdout='')
    return fake(argv,**kw)
   with self.assertRaises(ValueError):pf.preflight(out,'fixture',root,execute=missing_image)
   for name in ['preflight.json','previous-runtime.json','candidate-runtime.json']:self.assertFalse((out/name).exists())
   def wrong_digest(argv,**kw):
    if argv[:3]==['docker','image','inspect'] and '@sha256:'in argv[3]:return types.SimpleNamespace(returncode=0,stdout=json.dumps([{'Id':'sha256:'+'b'*64,'RepoDigests':[]}]))
    return fake(argv,**kw)
   with self.assertRaises(ValueError):pf.preflight(out,'fixture',root,execute=wrong_digest)
   self.assertFalse((out/'preflight.json').exists())
   pf.preflight(out,'fixture',root,execute=fake)
   # Same CN does not establish the same issuer: compare real DER certificates.
   (out/'previous-compose.json').write_text(json.dumps(fixturebase));fixturebase['services']['api']['volumes'][0]['source']=str(certs/'https-ca.crt')
   (out/'previous-compose.json').write_text(json.dumps(fixturebase));(out/'compose.json').write_text(json.dumps(m.render(fixturebase,args.bind,args.host,certs,out,args.shared_gid)))
   with self.assertRaises(ValueError):pf.preflight(out,'fixture',root,execute=fake)
   self.assertFalse((out/'preflight.json').exists())
   args.output=root/'bad-host';args.host='other.example.invalid'
   with self.assertRaises(ValueError):m.prepare(args)
   self.assertFalse(args.output.exists())
if __name__=='__main__':unittest.main()
