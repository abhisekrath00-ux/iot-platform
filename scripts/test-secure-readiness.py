#!/usr/bin/env python3
"""Read-only setup protocol and real default service-role regression tests."""
import copy
import importlib.util
import json
import pathlib
import tempfile
import types
import unittest

spec=importlib.util.spec_from_file_location('readiness',pathlib.Path(__file__).with_name('secure-readiness.py'))
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
IMAGE='sha256:'+'a'*64

class Tests(unittest.TestCase):
    def config(self):
        services={name:{'image':IMAGE} for name in ['api','ingest','mcp','postgres','mosquitto','web','elasticsearch','redis','ai-runtime']}
        for name in ['api','ingest','mcp']:services[name].update(command=['/bin/'+name],environment={'DATABASE_URL':'synthetic-never-log'})
        services['redis']['profiles']=['ha'];services['ai-runtime']['profiles']=['ai']
        return {'services':services}
    def test_default_writers_include_mcp_and_optional_profiles_stay_inactive(self):
        config=self.config();images={name:IMAGE for name in config['services'] if name not in ['redis','ai-runtime']}
        value=m.classify(config,images)
        self.assertEqual(value['writers'],['api','ingest','mcp']);self.assertEqual(value['disabled_profiles'],['ai-runtime','redis'])
        self.assertEqual(value['roles']['elasticsearch'],'data_store')
        for name in ['redis','ai-runtime']:images[name]=IMAGE
        self.assertEqual(m.classify(config,images)['disabled_profiles'],[])
    def test_unknown_missing_replica_or_unexpected_writer_refuses(self):
        for change in ['custom','command','missing','db','host','replica']:
            config=self.config();images={name:IMAGE for name in config['services']}
            if change=='custom':config['services']['custom']={'image':IMAGE};images['custom']=IMAGE
            if change=='command':config['services']['mcp']['command']=['/bin/custom']
            if change=='missing':images.pop('mcp')
            if change=='db':config['services']['web']['environment']={'DATABASE_URL':'unclassified'}
            if change=='host':config['services']['api']['network_mode']='host'
            if change=='replica':config['services']['ingest']['scale']=2
            with self.subTest(change=change),self.assertRaises(ValueError):m.classify(config,images)
    def fixture(self,root,alter=None):
        config=self.config();active=[n for n in config['services'] if n not in ['redis','ai-runtime']]
        calls=[]
        def fake(argv,**kwargs):
            calls.append(argv)
            if argv[:2]==['docker','inspect']:
                data=[{'Id':'fixture-'+n,'Image':IMAGE,'State':{'Status':'running'},'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':n}}} for n in active]
            elif argv[-3:]==['config','--format','json']:data=config
            elif argv[-2:]==['ps','-q']:return types.SimpleNamespace(returncode=0,stdout='fixture-ids')
            elif argv[-1]=='setup-capabilities':
                if alter=='old_operator':return types.SimpleNamespace(returncode=1,stdout='')
                data={'version':1,'activates_deployment':False,'protocols':sorted(m.REQUIRED_PROTOCOLS)}
                if alter=='bad_capability':data['activates_deployment']=True
            elif argv[-1]=='setup-readiness':
                data={'version':1,'deployment_approved':False,'status':'migration_ledger_current','applied_count':93,'packaged_count':93,'pending_migrations':[],'unknown_migrations':[]}
                if alter=='pending':data.update(status='application_bootstrap_required',applied_count=92,pending_migrations=['0093_fixture.sql'])
                if alter=='contradiction':data['pending_migrations']=['changed.sql']
            else:raise AssertionError('Unexpected effect '+str(argv))
            return types.SimpleNamespace(returncode=0,stdout=json.dumps(data))
        return fake,calls
    def test_inspection_only_current_exec_no_files_secrets_or_activation(self):
        with tempfile.TemporaryDirectory() as td:
            root=pathlib.Path(td);fake,calls=self.fixture(root)
            result=m.inspect(root,'fixture',fake)
            self.assertEqual(result['writers'],['api','ingest','mcp']);self.assertFalse(result['transport_activation_allowed'])
            self.assertEqual(list(root.iterdir()),[]);self.assertNotIn('synthetic-never-log',json.dumps(result))
            self.assertTrue(all(not any(x in call for x in ['run','pull','build','up','stop','start','kill','pg_restore']) for call in calls))
            self.assertEqual([call[-1] for call in calls if 'exec' in call],['setup-capabilities','setup-readiness'])
    def test_bootstrap_or_unverified_operator_never_becomes_activation(self):
        for alter in ['old_operator','bad_capability','pending','contradiction']:
            with self.subTest(alter=alter),tempfile.TemporaryDirectory() as td:
                fake,calls=self.fixture(pathlib.Path(td),alter)
                if alter=='pending':
                    result=m.inspect(td,'fixture',fake);self.assertEqual(result['status'],'application_bootstrap_required');self.assertFalse(result['transport_activation_allowed'])
                else:
                    with self.assertRaises(ValueError):m.inspect(td,'fixture',fake)
                self.assertNotIn('broker-acl-export',[call[-1] for call in calls])
    def test_pending_secure_state_never_falls_back_to_lab_compose(self):
        with tempfile.TemporaryDirectory() as td:
            root=pathlib.Path(td);d=root/'.secure-deployment';d.mkdir(mode=0o700);(d/'journal.json').write_text('{"version":1,"phase":"changing"}');(d/'journal.json').chmod(0o600)
            fake,calls=self.fixture(root)
            with self.assertRaises(ValueError):m.inspect(root,'fixture',fake)
            self.assertEqual(calls,[])

if __name__=='__main__':unittest.main()
