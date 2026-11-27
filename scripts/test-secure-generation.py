#!/usr/bin/env python3
"""Prototype filesystem-only handoff fault tests, no production integration."""
import copy,importlib.util,pathlib,tempfile,unittest
HERE=pathlib.Path(__file__).resolve().parent
def load(n):
 s=importlib.util.spec_from_file_location(n,HERE/(n+'.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
m=load('secure-generation');fixture=load('test-secure-execution')
class Loss(BaseException):pass
class Tests(unittest.TestCase):
 def ready(self,root):
  caller,e,p,receipt=fixture.Tests().fixture(root);caller.run(root,'fixture',receipt,'activate',baseline=fixture.BASELINE)
  selected=m.state.selection(root);nextdir=root/'second';nextdir.mkdir(mode=0o700)
  old=m.state.read_private(selected['config']);new=copy.deepcopy(old)
  for n in ['api','ingest','mcp']:new['services'][n]['image']='sha256:'+'c'*64
  previous,candidate,target=[nextdir/n for n in ['previous-runtime.json','candidate-runtime.json','preflight.json']]
  for path,v in [(previous,old),(candidate,new)]:m.state.atomic(path,v)
  plan=m.state.read_private(receipt);plan.update(files={str(p):m.state.digest(p)for p in [previous,candidate]},previous_files={str(previous):m.state.digest(previous)},current_services={n:{'image':s['image']}for n,s in old['services'].items()})
  m.state.atomic(target,plan);return target
 def test_requires_held_lock(self):
  with tempfile.TemporaryDirectory()as td:
   with self.assertRaises(ValueError):m.Generation(m.state.Journal(td))
 def test_second_generation_preserves_predecessor_and_blocks_own_selection(self):
  with tempfile.TemporaryDirectory()as td:
   root=pathlib.Path(td);receipt=self.ready(root)
   with m.state.Journal(root)as j:
    old=(j.directory/'journal.json').read_bytes();g=m.Generation(j);v=g.prepare('fixture',receipt)
    self.assertEqual(v['state'],'predecessor_held');self.assertEqual((j.directory/'journal.json').read_bytes(),old)
    with self.assertRaises(ValueError):g.selected()
    v=g.publish(lambda _:None);self.assertEqual(v['state'],'successor_prepared')
    self.assertEqual((pathlib.Path(v['archive'])/'journal.json').read_bytes(),old)
    with self.assertRaises(ValueError):g.publish(lambda _:None)
    with self.assertRaises(ValueError):m.state.selection(root)
 def test_failed_maintenance_never_publishes(self):
  for callback in [None,lambda _:True,lambda _:(_ for _ in ()).throw(ValueError('lost fence'))]:
   with self.subTest(callback=callback),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);receipt=self.ready(root)
    with m.state.Journal(root)as j:
     g=m.Generation(j);g.prepare('fixture',receipt);old=(j.directory/'journal.json').read_bytes()
     with self.assertRaises(ValueError):g.publish(callback)
     self.assertEqual((j.directory/'journal.json').read_bytes(),old);self.assertEqual(g.inspect()['state'],'predecessor_held')
 def test_changed_predecessor_plan_and_archive_refuse(self):
  for damage in ['journal','selector','receipt','archive','candidate']:
   with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);receipt=self.ready(root)
    with m.state.Journal(root)as j:
     g=m.Generation(j);v=g.prepare('fixture',receipt)
     p={'journal':j.directory/'journal.json','selector':j.directory/'selected.json','receipt':receipt,'archive':pathlib.Path(v['archive'])/'journal.json','candidate':receipt.parent/'candidate-runtime.json'}[damage]
     p.write_text('{}')
     with self.assertRaises(ValueError):g.publish(lambda _:None)
 def test_crash_before_and_after_successor_atomic_publish_classifies_without_replay(self):
  for after in [False,True]:
   with self.subTest(after=after),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);receipt=self.ready(root)
    with m.state.Journal(root)as j:
     g=m.Generation(j);g.prepare('fixture',receipt);original=m.state.atomic
     def fail(path,value):
      if pathlib.Path(path)==j.directory/'journal.json':
       if after:original(path,value)
       raise Loss()
      return original(path,value)
     m.state.atomic=fail
     try:
      with self.assertRaises(Loss):g.publish(lambda _:None)
     finally:m.state.atomic=original
    with m.state.Journal(root)as j:
     g=m.Generation(j);self.assertEqual(g.inspect()['state'],'successor_prepared'if after else'predecessor_held')
 def test_wrong_scope_and_predecessor_config_refuse_before_intent(self):
  for damage in ['scope','project','config']:
   with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
    root=pathlib.Path(td);receipt=self.ready(root);plan=m.state.read_private(receipt)
    if damage=='scope':plan['scope']='transport_only'
    if damage=='project':plan['project']='other'
    if damage=='config':
     p=receipt.parent/'previous-runtime.json';v=m.state.read_private(p);v['changed']=True;m.state.atomic(p,v);plan['files'][str(p)]=m.state.digest(p)
    m.state.atomic(receipt,plan)
    with m.state.Journal(root)as j:
     g=m.Generation(j)
     with self.assertRaises(ValueError):g.prepare('fixture',receipt)
     self.assertFalse(g.intent.exists())
if __name__=='__main__':unittest.main()
