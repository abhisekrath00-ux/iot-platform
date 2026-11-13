#!/usr/bin/env python3
import importlib.util,json,pathlib,tempfile,types,unittest
spec=importlib.util.spec_from_file_location('preflight',pathlib.Path(__file__).with_name('secure-preflight.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Tests(unittest.TestCase):
 def containers(self):
  return [{'Id':'fixture-'+s,'Image':'sha256:'+'a'*64,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':s}},'State':{'Status':'running'}}for s in ['api','ingest','mosquitto','web','postgres']]
 def test_tagged_digest_matches_docker_repodigest_without_losing_repository(self):
  digest='sha256:'+'a'*64
  self.assertEqual(m.repo_digest('eclipse-mosquitto:2@'+digest),'eclipse-mosquitto@'+digest)
  self.assertEqual(m.repo_digest('registry.local:5000/team/broker:2@'+digest),'registry.local:5000/team/broker@'+digest)
  self.assertNotEqual(m.repo_digest('other/broker:2@'+digest),m.repo_digest('team/broker@'+digest))
  self.assertNotEqual(m.repo_digest('team/broker@'+digest),m.repo_digest('team/broker@sha256:'+'b'*64))
  with self.assertRaises(ValueError):m.repo_digest('team/broker:2')
 def test_current_exact_project_service_images_and_health(self):
  base={'services':{s:{}for s in ['api','ingest','mosquitto','web','postgres']}}
  self.assertEqual(len(m.validate_current(base,base,self.containers(),'fixture')),5)
  for alter in ['config','project','stopped','unhealthy','replica','image','missing']:
   current=json.loads(json.dumps(base));c=self.containers()
   if alter=='config':current['services']['api']['extra']='changed'
   if alter=='project':c[0]['Config']['Labels']['com.docker.compose.project']='wrong'
   if alter=='stopped':c[0]['State']['Status']='exited'
   if alter=='unhealthy':c[0]['State']['Health']={'Status':'unhealthy'}
   if alter=='replica':c.append(c[0])
   if alter=='image':c[0]['Image']='mutable:tag'
   if alter=='missing':c.pop()
   with self.assertRaises(ValueError,msg=alter):m.validate_current(base,current,c,'fixture')
 def test_issuer_requires_exact_inspectable_bind(self):
  b={'services':{'api':{'environment':{}}}};self.assertIsNone(m.issuer_source(b));b['services']['api']['environment']['MTLS_CA_CERT']='/run/ca.crt'
  with self.assertRaises(ValueError):m.issuer_source(b)
  b['services']['api']['volumes']=[{'type':'bind','target':'/run/ca.crt','source':'/existing/ca.crt'}];self.assertEqual(m.issuer_source(b),pathlib.Path('/existing/ca.crt'))
 def test_private_receipt_inputs_fail_on_shared_or_symlink(self):
  with tempfile.TemporaryDirectory()as td:
   p=pathlib.Path(td)/'private';p.write_text('fixture');p.chmod(0o600);self.assertEqual(m.private_file(p),b'fixture');p.chmod(0o640)
   with self.assertRaises(ValueError):m.private_file(p)
   link=p.with_name('link');link.symlink_to(p)
   with self.assertRaises(ValueError):m.private_file(link)
if __name__=='__main__':unittest.main()
