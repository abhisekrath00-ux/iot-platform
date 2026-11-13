#!/usr/bin/env python3
import importlib.util
import pathlib
import unittest
from types import SimpleNamespace
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('local_admission',HERE/'secure-application-local-admission.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Tests(unittest.TestCase):
    def fixture(self):
        state=[True];calls=[]
        def guard(phase,raw):
            calls.append((phase,raw))
            if not state[0]:raise ValueError('Exact transition changed')
        return SimpleNamespace(connection_guard=guard,catalog=SimpleNamespace(database='app',user='admin')),state,calls
    def test_exact_local_positive_and_closed_never_hba_inference(self):
        p,state,calls=self.fixture()
        for phase in ['restricted','original','exclusive']:
            result={'version':1,'status':'database_closed'}if phase=='exclusive'else{'version':1,'status':'connected','database':'app','user':'admin'}
            self.assertIsNone(m.LocalAdmission(p,lambda:result)(phase,b'exact'))
        self.assertEqual(len(calls),6)
    def test_closed_failures_or_extra_data_never_count(self):
        for value in [None,True,{'version':True,'status':'database_closed'}, {'version':1,'status':'database_closed','extra':True},
                      *[{'version':1,'status':s}for s in ['connected','hba_denied','authentication_failed','unverified']]]:
            p,state,calls=self.fixture()
            with self.subTest(value=value),self.assertRaises(ValueError):m.LocalAdmission(p,lambda:value)('exclusive',b'exact')
    def test_connected_wrong_identity_or_network_context_refuses(self):
        for damage in ['database','user','server','status']:
            p,state,calls=self.fixture();result={'version':1,'status':'connected','database':'app','user':'admin'};result[damage]='wrong'
            with self.subTest(damage=damage),self.assertRaises(ValueError):m.LocalAdmission(p,lambda:result)('restricted',b'exact')
    def test_pre_and_post_transition_scope_changes_refuse(self):
        for post in [False,True]:
            p,state,calls=self.fixture()
            if not post:state[0]=False
            def probe():state[0]=False;return {'version':1,'status':'database_closed'}
            with self.subTest(post=post),self.assertRaises(ValueError):m.LocalAdmission(p,probe)('exclusive',b'exact')
if __name__=='__main__':unittest.main()
