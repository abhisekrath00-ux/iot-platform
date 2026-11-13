#!/usr/bin/env python3
"""Unit contract for the disposable native write proof, not integration evidence."""
import copy
import importlib.util
import pathlib
import unittest
spec=importlib.util.spec_from_file_location('writes',pathlib.Path(__file__).with_name('test-secure-application-writes-fixture.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)

class API:
    def __init__(self):self.profiles=[];self.events={};self.damage=None
    def __call__(self,method,path,body,role,tenant):
        if method=='POST':
            if role is None:return 401,None
            if role=='viewer':return 403,None
            if path.endswith('/profiles'):
                row=copy.deepcopy(body);row['id']='p'+str(len(self.profiles));self.profiles.append(row);return 201,{'id':row['id']}
            if tenant!=m.TENANT:return 404,None
            row=copy.deepcopy(body['readings'][0])
            if row['value']>500:return 422,{'accepted':0,'rejected':[{'index':0,'reason':'out of range'}]}
            key=row['event_id'] if self.damage!='duplicate' else str(len(self.events))
            self.events[key]=row;return 200,{'accepted':1,'rejected':[]}
        if path.endswith('/profiles'):
            rows=copy.deepcopy(self.profiles)
            if self.damage=='profile'and rows:rows[0]['points']=[]
            return 200,rows
        if '/count?'in path:return 200,{'count':len(self.events)if tenant==m.TENANT or self.damage=='tenant'else 0}
        if tenant!=m.TENANT:return 200,[]
        row=list(self.events.values())[-1]
        return 200,[{'point_id':m.POINT,'value':row['value']if self.damage!='value'else -1,'unit':'V','quality':'measured'}]

class Tests(unittest.TestCase):
    def test_multiple_runtime_stages_retain_profiles_events(self):
        proof=m.Writes();api=API()
        for stage in ['old89','candidate93','previous93','recovered93']:
            result=proof.prove(api,stage)
            self.assertFalse(result['full_compatibility']);self.assertFalse(result['runtime_accepted'])
        self.assertEqual((len(api.events),len(api.profiles)),(4,4))
    def test_duplicate_event_profile_drift_latest_and_tenant_rejected(self):
        for damage in ['duplicate','profile','value','tenant']:
            with self.subTest(damage=damage),self.assertRaises(ValueError):
                proof=m.Writes();api=API();api.damage=damage;proof.prove(api,'bad')
    def test_prior_data_loss_and_unknown_status_refused(self):
        proof=m.Writes();api=API();proof.prove(api,'first');api.events={}
        with self.assertRaises(ValueError):proof.prove(api,'lost')
        with self.assertRaises(ValueError):m.Writes().prove(lambda *args:(500,None),'broken')
    def test_duplicate_stage_refused_before_transport(self):
        proof=m.Writes();api=API();proof.prove(api,'stage')
        with self.assertRaises(ValueError):proof.prove(lambda *args:self.fail('transport called'),'stage')

if __name__=='__main__':unittest.main()
