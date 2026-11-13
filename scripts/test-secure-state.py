#!/usr/bin/env python3
import importlib.util,json,os,pathlib,shutil,subprocess,tempfile,unittest
spec=importlib.util.spec_from_file_location('state',pathlib.Path(__file__).with_name('secure-state.py'));s=importlib.util.module_from_spec(spec);spec.loader.exec_module(s)
cspec=importlib.util.spec_from_file_location('compose',pathlib.Path(__file__).with_name('selected-compose.py'));c=importlib.util.module_from_spec(cspec);cspec.loader.exec_module(c)
class Tests(unittest.TestCase):
 def files(self,root):
  files=[]
  for name in ['old.json','new.json','preflight.json']:
   p=root/name;p.write_text(json.dumps({'fixture':name}));p.chmod(0o600);files.append(p)
  images={n:{'image':'sha256:'+'a'*64}for n in ['api','ingest','mosquitto','web','postgres']}
  files[2].write_text(json.dumps({'version':1,'status':'preflight_only','activation_allowed':False,'project':'fixture','root':str(root),'files':{str(p):s.digest(p)for p in files[:2]},'current_services':images}))
  return files
 def begin(self,j,files):return j.begin('fixture',*files[:2],{n:'sha256:'+'a'*64 for n in ['api','ingest','mosquitto','web','postgres']},files[2])
 def test_normal_route_and_no_unverified_commit(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root);self.assertEqual(c.command(root,['ps']),['docker','compose','ps'])
   with s.Journal(root)as j:
    self.begin(j,f)
    with self.assertRaises(ValueError):j.commit()
    with self.assertRaises(ValueError):c.command(root,['up','-d'])
    j.transition('changing');j.transition('verified');j.commit()
   self.assertEqual(c.command(root,['ps']),['docker','compose','-p','fixture','-f',str(f[1]),'ps'])
 def test_crash_at_each_phase_blocks_lab_and_returns_exact_rollback(self):
  for phase in ['prepared','changing','verified','rollback_required']:
   with self.subTest(phase=phase),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);f=self.files(root)
    with s.Journal(root)as j:
     self.begin(j,f)
     if phase!='prepared':j.transition('changing')
     if phase=='verified':j.transition('verified')
     if phase=='rollback_required':j.transition('rollback_required')
    with s.Journal(root)as recovered:
     with self.assertRaises(ValueError):c.command(root,['start'])
     with self.assertRaises(ValueError):self.begin(recovered,f)
     plan=recovered.recovery_plan();self.assertEqual(plan['config'],str(f[0]));self.assertEqual(plan['project'],'fixture');self.assertTrue(plan['may_change_services'])
 def test_os_lock_and_corrupt_modified_or_missing_selection_fail_closed(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root)
   with s.Journal(root)as j:
    with self.assertRaises(ValueError):
     with s.Journal(root):pass
    self.begin(j,f);j.transition('changing');j.transition('verified');j.commit()
   chosen=root/'.secure-deployment/selected.json';saved=chosen.read_bytes();f[1].write_text('changed')
   with self.assertRaises(ValueError):s.selection(root)
   f[1].write_text(json.dumps({'fixture':'new.json'}));chosen.write_text('corrupt')
   with self.assertRaises((ValueError,KeyError)):s.selection(root)
   chosen.write_bytes(saved);chosen.unlink()
   with self.assertRaises(ValueError):s.selection(root)
 def test_selection_before_commit_mark_still_blocks(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root)
   with s.Journal(root)as j:
    value=self.begin(j,f);j.transition('changing');j.transition('verified')
    s.atomic(j.directory/'selected.json',{'version':1,'project':'fixture','config':str(f[1]),'sha256':s.digest(f[1])})
   with self.assertRaises(ValueError):s.selection(root)
 def test_rollback_settlement_clears_partial_selection_but_never_assumes_lab_route(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root)
   with s.Journal(root)as j:
    self.begin(j,f);j.transition('changing');j.transition('verified')
    s.atomic(j.directory/'selected.json',{'version':1,'project':'fixture','config':str(f[1]),'sha256':s.digest(f[1])})
    j.finish_rollback()
   with self.assertRaises(ValueError):s.selection(root)
   self.assertFalse((root/'.secure-deployment/selected.json').exists())
 def test_unlocked_state_and_symlinks_are_rejected(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root)
   with self.assertRaises(ValueError):self.begin(s.Journal(root),f)
   with s.Journal(root)as j:
    link=root/'link';link.symlink_to(f[1])
    with self.assertRaises(ValueError):self.begin(j,[f[0],link,f[2]])
   directory=root/'.secure-deployment';shutil.rmtree(directory);directory.symlink_to(root/'missing')
   with self.assertRaises((ValueError,OSError)):s.selection(root)
 def test_real_shell_guards_and_selector_never_invoke_docker_on_pending_state(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);scripts=root/'scripts';scripts.mkdir();bin=root/'bin';bin.mkdir();log=root/'calls'
   source=pathlib.Path(__file__).parent
   for name in ['setup.sh','upgrade.sh','install.sh','backup.sh','restore.sh','recover-admin-local.sh','secure-state.py','selected-compose.py','hx-ui.sh']:
    shutil.copy2(source/name,scripts/name)
   (root/'docker-compose.yml').write_text('services: {}')
   docker=bin/'docker';docker.write_text('#!/bin/sh\nprintf "%s\\n" "$*" >> "$CALLS"\n');docker.chmod(0o700)
   env=dict(os.environ,PATH=str(bin)+':'+os.environ['PATH'],CALLS=str(log),NO_COLOR='1')
   f=self.files(root)
   with s.Journal(root)as j:self.begin(j,f)
   for args in [['setup.sh','start'],['setup.sh','open'],['upgrade.sh','--yes'],['install.sh','--yes'],['backup.sh'],['recover-admin-local.sh','--workspace','fixture','--email','ci@example.invalid']]:
    result=subprocess.run(['bash',str(scripts/args[0]),*args[1:]],cwd=root,env=env,capture_output=True,text=True)
    self.assertNotEqual(result.returncode,0,args)
    if args[0]=='upgrade.sh':self.assertIn('Secure server upgrade is unavailable',result.stderr);self.assertIn('review is read-only',result.stderr)
    calls=log.read_text()if log.exists()else ''
    self.assertNotIn('compose up',calls,args);self.assertNotIn('compose exec',calls,args)
   with s.Journal(root)as j:j.transition('changing');j.transition('verified');j.commit()
   log.write_text('')
   result=subprocess.run(['python3',str(scripts/'selected-compose.py'),str(root),'ps'],env=env,capture_output=True,text=True)
   self.assertEqual(result.returncode,0,result.stderr);self.assertIn('compose -p fixture -f '+str(f[1])+' ps',log.read_text())
 def test_dependency_tamper_blocks_selection_and_does_not_prevent_failure_record(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);f=self.files(root)
   cert=root/'cert.crt';cert.write_text('synthetic');cert.chmod(0o640)
   receipt=json.loads(f[2].read_text());receipt['files'][str(cert)]=s.digest(cert);f[2].write_text(json.dumps(receipt))
   with s.Journal(root)as j:
    self.begin(j,f);j.transition('changing');cert.write_text('changed')
    with self.assertRaises(ValueError):j.transition('verified')
    j.transition('rollback_required')
    self.assertEqual(j.recovery_plan()['action'],'explicit_rollback_required')
 def test_preflight_receipt_is_required_but_never_treated_as_permission(self):
  for changed in ['root','images','manifest','activation']:
   with self.subTest(changed=changed),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);f=self.files(root);value=json.loads(f[2].read_text())
    if changed=='root':value['root']='/different'
    if changed=='images':value['current_services']['api']['image']='sha256:'+'b'*64
    if changed=='manifest':value['files'].pop(str(f[1]))
    if changed=='activation':value['activation_allowed']=True
    f[2].write_text(json.dumps(value))
    with s.Journal(root)as j:
     with self.assertRaises(ValueError):self.begin(j,f)
    self.assertFalse((root/'.secure-deployment/journal.json').exists())
if __name__=='__main__':unittest.main()
