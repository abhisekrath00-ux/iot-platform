#!/usr/bin/env python3
"""Grouped launch contract tests use fake Docker only."""
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('groups',HERE/'secure-application-startgroup.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Loss(BaseException):pass

class Adapter:
    def __init__(self,root):
        self.root=root;self.project='fixture';self.writers=['api','ingest','mcp'];self.events=[];self.count=0
        self.old={'services':{'postgres':{'image':'sha256:'+'f'*64}}}
        for name in self.writers:self.old['services'][name]={'image':'sha256:'+'a'*64,'command':['/bin/'+name],'environment':{'DATABASE_URL':'postgres://fixture'}}
        self.new=copy.deepcopy(self.old)
        for name in self.writers:self.new['services'][name]['image']='sha256:'+'b'*64
        self.values={};self.definitions={}
        self.paths={'previous':str(root/'previous.json'),'candidate':str(root/'candidate.json')}
        for key,data in [('previous',self.old),('candidate',self.new)]:m.gates.state.atomic(self.paths[key],data)
        for name,service in self.old['services'].items():self.replace(name,service,'exited'if name in self.writers else'running')
    def replace(self,name,service,status):
        self.count+=1
        self.values[name]={'Id':format(self.count,'064x'),'Image':service['image'],
            'State':{'Status':status,'Paused':False,'Restarting':False},
            'Config':{'Cmd':service.get('command',[]),'Entrypoint':service.get('entrypoint',[]),
                'Env':['PATH=/bin']+[key+'='+value for key,value in service.get('environment',{}).items()],
                'Labels':{'com.docker.compose.project':self.project,'com.docker.compose.service':name,**service.get('labels',{})}},
            'HostConfig':{'Privileged':False,'NetworkMode':'fixture_default'},
            'Mounts':[{'Type':'bind','Source':v['source'],'Destination':v['target'],'RW':False}for v in service.get('volumes',[])]}
        self.definitions[name]=copy.deepcopy(service)
    def containers(self,path):return copy.deepcopy(list(self.values.values()))
    def call(self,args):return json.dumps([{'Id':args[-1],'Config':{'Env':['PATH=/bin'],'Cmd':[],'Entrypoint':[]}}])
    def compose(self,path,*args):
        self.events.append('create'if args[0]=='up'and '--no-start'in args else args[0]);recipe=m.gates.state.read_private(path)
        if args[0]=='up'and '--no-start'in args:
            if '--no-deps'not in args:raise AssertionError('Dependency recreation could replace the bound PostgreSQL container')
            for name in self.writers:
                service=copy.deepcopy(recipe['services'][name])
                if service.get('labels',{}).get(m.gates.LABEL):
                    if '$$'not in service['command'][2]:raise AssertionError('Compose would interpolate the literal gate script')
                    service['command'][2]=service['command'][2].replace('$$','$')
                if self.definitions[name]!=service:self.replace(name,service,'created')
        elif args[0]=='start':
            # Every created ID must be durable before the start effect.
            for name in self.writers:
                records=[m.gates.state.read_private(path)for path in (self.root/'.secure-deployment').glob('startgate-*-'+name+'.json')]
                matches=[record for record in records if record['container']==self.values[name]['Id']]
                if len(matches)!=1:raise AssertionError('Container ID not durable before start')
                self.values[name]['State']['Status']='running'
        elif args[0]=='up':
            for name in self.writers:self.replace(name,recipe['services'][name],'running')
        else:raise AssertionError(args)

class Tests(unittest.TestCase):
    def setup(self,root,journal):
        m.gates.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
        adapter=Adapter(root);events=[]
        def admit(peers):
            self.assertEqual(set(peers),set(adapter.writers));events.append('admit')
            for name in peers:
                gate=m.Group(journal,lambda:None,lambda _:None,lambda _:None).gate(adapter,name,'candidate')
                self.assertFalse((pathlib.Path(gate.record()['directory'])/'release').exists())
        def prove(peers):self.assertIn('admit',events);self.assertEqual(set(peers),set(adapter.writers));events.append('proof')
        return adapter,m.Group(journal,lambda:None,admit,prove),events
    def test_all_created_ids_recorded_before_start_then_one_handoff_fresh_proof_each_release(self):
        with tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
            adapter,group,events=self.setup(pathlib.Path(td),journal);group.start(adapter,adapter.paths,'candidate')
            self.assertEqual(adapter.events,['create','start']);self.assertEqual(events,['admit','proof','proof','proof'])
            for name in adapter.writers:
                gate=group.gate(adapter,name,'candidate');self.assertEqual(gate.record()['stage'],'released')
                group.verify(adapter,adapter.new['services'][name],adapter.values[name],group.metadata(adapter,adapter.values[name]['Image']))
    def test_compatibility_slot_runs_exact_previous_definitions_with_separate_nonce(self):
        with tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
            adapter,group,events=self.setup(pathlib.Path(td),journal)
            group.admit=lambda values:None
            group.prove=lambda values:None
            group.start(adapter,adapter.paths,'compatibility')
            for name in adapter.writers:
                gate=group.gate(adapter,name,'compatibility')
                self.assertEqual(gate.record()['stage'],'released')
                self.assertEqual(adapter.values[name]['Image'],adapter.old['services'][name]['image'])
                group.verify(adapter,adapter.old['services'][name],adapter.values[name],group.metadata(adapter,adapter.values[name]['Image']))
                self.assertFalse(group.gate(adapter,name,'previous').path.exists())

    def test_failed_group_admission_keeps_all_markers_missing_and_no_direct_fallback(self):
        with tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
            adapter,group,events=self.setup(pathlib.Path(td),journal)
            def fail(_):raise ValueError('Outside IP still admitted')
            group.admit=fail
            with self.assertRaises(ValueError):group.start(adapter,adapter.paths,'candidate')
            self.assertEqual(adapter.events,['create','start'])
            for name in adapter.writers:
                gate=group.gate(adapter,name,'candidate');self.assertEqual(gate.record()['stage'],'held')
                self.assertFalse((pathlib.Path(gate.record()['directory'])/'release').exists())
    def test_lost_start_ack_fresh_group_uses_bound_same_held_ids_without_replacing_them(self):
        with tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
            adapter,group,events=self.setup(pathlib.Path(td),journal);ordinary=adapter.compose
            def lost(path,*args):
                result=ordinary(path,*args)
                if args[0]=='start':raise Loss()
                return result
            adapter.compose=lost
            with self.assertRaises(Loss):group.start(adapter,adapter.paths,'candidate')
            identities={name:item['Id']for name,item in adapter.values.items()};adapter.compose=ordinary
            fresh=m.Group(journal,lambda:None,group.admit,group.prove);fresh.start(adapter,adapter.paths,'candidate')
            self.assertEqual(identities,{name:item['Id']for name,item in adapter.values.items()})
    def test_no_gate_definition_bypass_without_explicit_manager_and_wrong_baseline_refuses(self):
        with tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
            adapter,group,events=self.setup(pathlib.Path(td),journal);group.start(adapter,adapter.paths,'candidate')
            role='api';actual=adapter.values[role];image=group.metadata(adapter,actual['Image'])
            with self.assertRaises(ValueError):group.verify(adapter,adapter.old['services'][role],actual,image)
            bare=object.__new__(m.gates.adapter.DockerApplication);bare.runtime_group=None
            with self.assertRaises(ValueError):bare.verify_definition(adapter.new['services'][role],actual,image)
    def test_final_direct_normalization_requires_settled_and_original_admission_and_live_acceptance(self):
        for problem in ['none','unsettled','wrong-target','admission','acceptance']:
            with self.subTest(problem=problem),tempfile.TemporaryDirectory()as td,m.gates.state.Journal(td)as journal:
                adapter,group,events=self.setup(pathlib.Path(td),journal);group.start(adapter,adapter.paths,'candidate')
                m.gates.state.atomic(journal.directory/'journal.json',{'version':1,'kind':'application_bootstrap','project':adapter.project,'phase':'changing'if problem=='unsettled'else'committed',**adapter.paths,
                    'previous_sha256':m.gates.state.digest(adapter.paths['previous']),'candidate_sha256':m.gates.state.digest(adapter.paths['candidate']),
                    'files':{path:m.gates.state.digest(path)for path in adapter.paths.values()}})
                def original():
                    if problem=='admission':raise ValueError('Original HBA not restored')
                    events.append('original-admission')
                def accepted(value,slot):
                    if problem=='acceptance':raise ValueError('Runtime not healthy')
                    self.assertEqual(slot,'candidate');events.append('final-accepted')
                if problem!='none':
                    with self.assertRaises(ValueError):group.normalize(adapter,adapter.paths,'previous'if problem=='wrong-target'else'candidate','committed',original,accepted)
                else:
                    group.normalize(adapter,adapter.paths,'candidate','committed',original,accepted)
                    self.assertEqual(m.gates.state.read_private(journal.directory/'startgroup-normalize.json')['stage'],'accepted')
                    for name in adapter.writers:
                        self.assertEqual(adapter.values[name]['Config']['Cmd'],['/bin/'+name]);self.assertEqual(adapter.values[name]['Mounts'],[])
                    self.assertIn('final-accepted',events)

if __name__=='__main__':unittest.main()
