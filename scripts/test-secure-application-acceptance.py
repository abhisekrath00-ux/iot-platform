#!/usr/bin/env python3
import copy
import importlib.util
import json
import pathlib
import unittest
from unittest import mock
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('acceptance',HERE/'secure-application-acceptance.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
spec=importlib.util.spec_from_file_location('fixtures',HERE/'test-secure-application-reads.py');f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)

class Adapter:
    candidate=pathlib.Path('/plan/candidate');previous=pathlib.Path('/plan/previous')
    def __init__(self):
        self.calls=[];self.ids=0;self.damage=None
    def checked_containers(self,path,running):
        assert running;self.ids+=1
        return {'api':{'Id':'exact-api'+str(self.ids)if self.damage=='changed'else'exact-api','Image':'exact-image'}}
    def call(self,args):
        self.calls.append(args);action=args[-1]
        if action=='setup-capabilities':v={'version':1,'protocols':list(m.adaptermod.readiness.REQUIRED_PROTOCOLS),'activates_deployment':False}
        elif action=='setup-readiness':v={'version':1,'status':'migration_ledger_current','applied_count':93,'packaged_count':93,'pending_migrations':[],'unknown_migrations':[],'deployment_approved':False}
        else:v={'version':1,'status':'exact_reviewed_additive_93','runtime_accepted':False}
        if self.damage==action:v['status']='bad';v['version']=0
        return json.dumps(v)
class Client:
    def __init__(self):self.calls=0;self.damage=False
    def compare(self,before):
        self.calls+=1
        if self.damage:raise ValueError('transport refused')
        return {'matched':True,'full_compatibility':False,'runtime_accepted':False}
class Tests(unittest.TestCase):
    def setUp(self):
        self.adapter=Adapter();self.client=Client();self.baseline=m.reads.snapshot(f.Tests().rows());self.behaviors=[]
        self.value={'candidate':'/plan/candidate','previous':'/plan/previous'}
    def callback(self,behavior=None):return m.RuntimeAcceptance(self.adapter,self.client,self.baseline,behavior or (lambda value,slot:self.behaviors.append(slot)))
    def test_candidate_exec_exact_running_cid_only_read_actions(self):
        self.callback()(self.value,'candidate');self.assertEqual(self.behaviors,['candidate']);self.assertEqual(self.client.calls,2)
        self.assertEqual([x[-1]for x in self.adapter.calls],['setup-capabilities','setup-readiness','inspect-reviewed'])
        self.assertTrue(all(x[:3]==['docker','exec','exact-api']for x in self.adapter.calls))
    def test_previous_never_assumes_new_operator_support(self):
        self.callback()(self.value,'previous');self.assertEqual(self.adapter.calls,[]);self.assertEqual(self.behaviors,['previous'])
    def test_unknown_readiness_schema_and_changed_runtime_refused(self):
        for damage in ['setup-capabilities','setup-readiness','inspect-reviewed','changed']:
            with self.subTest(damage=damage),self.assertRaises(ValueError):self.adapter.damage=damage;self.callback()(self.value,'candidate')
    def test_missing_behavior_or_approval_flag_cannot_accept(self):
        with self.assertRaises(ValueError):m.RuntimeAcceptance(self.adapter,self.client,self.baseline,None)
        with self.assertRaises(ValueError):self.callback(lambda *args:True)(self.value,'previous')
        def refused(*args):raise ValueError('missing behavioral evidence')
        with self.assertRaises(ValueError):self.callback(refused)(self.value,'candidate')
    def test_unknown_path_slot_baseline_transport_refused(self):
        with self.assertRaises(ValueError):self.callback()(dict(self.value,candidate='/other'),'candidate')
        with self.assertRaises(ValueError):self.callback()(self.value,'compatibility')
        bad=copy.deepcopy(self.baseline);bad['runtime_accepted']=True
        with self.assertRaises(ValueError):m.RuntimeAcceptance(self.adapter,self.client,bad,lambda *args:None)
        self.client.damage=True
        with self.assertRaises(ValueError):self.callback()(self.value,'previous')

class CompatibilityTests(unittest.TestCase):
    setUp=Tests.setUp
    def test_dedicated_group_fresh_old_runtime_retained_behavior(self):
        self.adapter.writers=['api'];record={'container':'exact-api','stage':'released'}
        class Group:
            def gate(*args):
                class Gate:
                    def record(self):return dict(record)
                return Gate()
        seen=[]
        callback=m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,lambda value:seen.append(value['previous']))
        callback(self.value)
        self.assertEqual(seen,['/plan/previous']);self.assertEqual(self.adapter.calls,[]);self.assertEqual(self.client.calls,2)
        for change,value in [('container','other'),('stage','held')]:
            old=record[change];record[change]=value
            with self.assertRaises(ValueError):callback(self.value)
            record[change]=old
        def drift(value):record['container']='changed'
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,drift)(self.value)
    def test_missing_compatibility_proof_or_approval_flag_refused(self):
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,None,self.client,self.baseline,lambda value:None)
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,object(),self.client,self.baseline,None)
        self.adapter.writers=['api']
        class Group:
            def gate(*args):
                class Gate:
                    def record(self):return {'container':'exact-api','stage':'released'}
                return Gate()
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,lambda value:True)(self.value)
        def refused(value):raise ValueError('Unproved old-image behavior')
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,refused)(self.value)
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,lambda value:None)(dict(self.value,previous='/wrong'))
        self.adapter.writers=[]
        with self.assertRaises(ValueError):m.PreviousCompatibility(self.adapter,Group(),self.client,self.baseline,lambda value:None)

if __name__=='__main__':unittest.main()
