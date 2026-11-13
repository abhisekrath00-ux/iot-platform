#!/usr/bin/env python3
"""Production adapter contract guards. Fake Docker only; no owner effects."""
import copy
import importlib.util
import json
import pathlib
import tempfile
import types
import unittest

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m

adapter=load('secure-application-docker');fixture=load('test-secure-application');state=load('secure-state')

class Tests(unittest.TestCase):
    def config(self):
        value=fixture.Tests().config();value['services']['postgres']['environment']={'POSTGRES_USER':'ci','POSTGRES_DB':'db'}
        for name in ['api','ingest','mcp']:value['services'][name]['environment']['DATABASE_URL']='postgres://ci:synthetic@postgres:5432/db?sslmode=disable'
        return value
    def test_local_database_target_refuses_remote_mismatched_or_unknown_writer(self):
        old=self.config();self.assertEqual(adapter.database_target(old),('ci','db',['api','ingest','mcp']))
        for uri in ['postgres://ci:synthetic@other:5432/db','postgres://ci:synthetic@postgres:9999/db','postgres://other:synthetic@postgres/db','postgres://ci:synthetic@postgres/other','postgres://ci:synthetic@postgres/db?host=remote','postgres://ci:synthetic@postgres/db?hostaddr=203.0.113.1','postgres://ci:synthetic@postgres/db?dbname=other','postgres://ci:synthetic@postgres/db?options=-csearch_path%3Dother','postgres://ci:synthetic@postgres/db?sslmode=disable&sslmode=prefer']:
            bad=copy.deepcopy(old);bad['services']['mcp']['environment']['DATABASE_URL']=uri
            with self.assertRaises(ValueError):adapter.database_target(bad)
    def test_actual_container_definition_not_only_desired_image_is_bound(self):
        image={'Config':{'Env':['PATH=/bin','BUILTIN=keep'],'Cmd':['/default'],'Entrypoint':None}}
        service={'command':['/bin/api'],'environment':{'DATABASE_URL':'postgres://ci@postgres/db'}}
        container={'Config':{'Env':['PATH=/bin','BUILTIN=keep','DATABASE_URL=postgres://ci@postgres/db'],'Cmd':['/bin/api'],'Entrypoint':None},'HostConfig':{'Privileged':False,'NetworkMode':'fixture_default'}}
        adapter.verify_container_definition(service,container,image)
        for change in ['database','command','entrypoint','extra-env','duplicate-env','privileged','host-network']:
            with self.subTest(change=change):
                bad=copy.deepcopy(container)
                if change=='database':bad['Config']['Env'][-1]='DATABASE_URL=postgres://ci@other/db'
                if change=='command':bad['Config']['Cmd']=['/bin/custom-writer']
                if change=='entrypoint':bad['Config']['Entrypoint']=['/bin/another']
                if change=='extra-env':bad['Config']['Env'].append('PGOPTIONS=-csearch_path=other')
                if change=='duplicate-env':bad['Config']['Env'].append('DATABASE_URL=postgres://ci@postgres/db')
                if change=='privileged':bad['HostConfig']['Privileged']=True
                if change=='host-network':bad['HostConfig']['NetworkMode']='host'
                with self.assertRaises(ValueError):adapter.verify_container_definition(service,bad,image)
    def test_null_command_and_entrypoint_inherit_image_defaults_but_empty_clears(self):
        image={'Config':{'Cmd':['postgres'],'Entrypoint':['docker-entrypoint.sh'],'Env':[]}}
        actual={'Config':{'Cmd':['postgres'],'Entrypoint':['docker-entrypoint.sh'],'Env':[]}}
        for service in [{},{'command':None,'entrypoint':None}]:
            adapter.verify_container_definition(service,actual,image)
        for field in ['command','entrypoint']:
            with self.assertRaises(ValueError):adapter.verify_container_definition({field:[]},actual,image)
        empty={'Config':{'Cmd':[],'Entrypoint':[],'Env':[]}}
        adapter.verify_container_definition({'command':[],'entrypoint':[]},empty,image)
    def test_planned_writer_wrappers_and_libpq_environment_also_refuse(self):
        for change in ['wrapper','PGOPTIONS','PGHOST','PGSERVICE']:
            image={'Config':{'Env':[],'Cmd':None,'Entrypoint':None}}
            service={'command':['/bin/api'],'environment':{}}
            actual={'Config':{'Cmd':['/bin/api'],'Entrypoint':None,'Env':[]}}
            if change=='wrapper':
                service['entrypoint']=['/another'];actual['Config']['Entrypoint']=['/another']
            else:
                service['environment'][change]='custom';actual['Config']['Env']=[change+'=custom']
            with self.assertRaises(ValueError):adapter.verify_container_definition(service,actual,image)
    def test_missing_callbacks_refuse_before_access_or_effects(self):
        for callbacks in [(None,lambda *a:None,lambda*a:None),(lambda*a:None,None,lambda*a:None),(lambda*a:None,lambda*a:None,None)]:
            with self.assertRaises(ValueError):adapter.DockerApplication('/missing','fixture','/missing/receipt',*callbacks)
    def plan(self,root):
        bundle=root/'plan';bundle.mkdir(mode=0o700);old=self.config();new=copy.deepcopy(old)
        for name in ['api','ingest','mcp']:new['services'][name]['image']=fixture.NEW
        previous,candidate=bundle/'previous-runtime.json',bundle/'candidate-runtime.json'
        for path,obj in [(previous,old),(candidate,new)]:state.atomic(path,obj)
        current={name:{'image':item['image'],'container_id':'id-'+name}for name,item in old['services'].items()}
        value={'version':1,'status':'preflight_only','activation_allowed':False,'scope':'application_bootstrap','root':str(root),'project':'fixture','files':{str(p):state.digest(p)for p in [previous,candidate]},'previous_files':{str(previous):state.digest(previous)},'writers':['api','ingest','mcp'],'database':{'user':'ci','name':'db'},'current_services':current}
        receipt=bundle/'preflight.json';state.atomic(receipt,value);return receipt,previous,candidate
    def test_wrong_scope_target_or_changed_inputs_refuse_constructor(self):
        for damage in ['scope','root','project','authority-boolean','candidate']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);receipt,previous,candidate=self.plan(root);value=state.read_private(receipt)
                if damage=='scope':value['scope']='transport_only'
                if damage=='root':value['root']='/another'
                if damage=='project':value['project']='other'
                if damage=='authority-boolean':value['activation_allowed']=True
                if damage=='candidate':candidate.write_text('{}')
                state.atomic(receipt,value)
                with self.assertRaises(ValueError):adapter.DockerApplication(root,'fixture',receipt,lambda*a:None,lambda*a:None,lambda*a:None)
    def test_all_writer_stop_is_verified_before_any_schema_operator(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);receipt,previous,candidate=self.plan(root);calls=[]
            def execute(args,**kw):
                calls.append(args)
                if args[-3:]==['ps','-a','-q']:return types.SimpleNamespace(returncode=0,stdout='id-api id-ingest id-mcp id-postgres id-mosquitto id-web')
                if args[:2]==['docker','inspect']:
                    values=[]
                    for name in ['api','ingest','mcp','postgres','mosquitto','web']:
                        values.append({'Image':fixture.OLD,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':name}},'State':{'Status':'running' if name=='mcp'or name not in ['api','ingest']else'exited'}})
                    return types.SimpleNamespace(returncode=0,stdout=json.dumps(values))
                raise AssertionError('Unexpected effect '+str(args))
            effects=adapter.DockerApplication(root,'fixture',receipt,lambda*a:None,lambda*a:None,lambda*a:None,execute)
            with self.assertRaises(ValueError):effects.inspect_reviewed_schema({'previous':str(previous),'candidate':str(candidate)})
            self.assertFalse(any('run'in args for args in calls))
    def test_authority_callback_is_invoked_and_cannot_be_a_receipt_boolean(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);receipt,previous,candidate=self.plan(root);calls=[]
            def deny(*args):calls.append(args);raise ValueError('trusted caller has not approved maintenance')
            effects=adapter.DockerApplication(root,'fixture',receipt,deny,lambda*a:None,lambda*a:None)
            with self.assertRaises(ValueError):effects.authorize('application-upgrade','fixture',previous,candidate)
            self.assertEqual(calls[0][0],'application-upgrade')

if __name__=='__main__':unittest.main()
