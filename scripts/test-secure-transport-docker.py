#!/usr/bin/env python3
"""Production transport guard tests. Fake effects, no Docker or owner changes."""
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest
p=pathlib.Path(__file__).with_name('secure-transport-docker.py');s=importlib.util.spec_from_file_location('transport',p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class Tests(unittest.TestCase):
    def plan(self,root):
        bundle=root/'prepared';bundle.mkdir(mode=0o700);(bundle/'acl').mkdir()
        base={'services':{name:{'image':'sha256:'+'a'*64}for name in ['api','ingest','mcp','postgres','mosquitto','web']}}
        base['services']['postgres']['environment']={'POSTGRES_USER':'ci','POSTGRES_DB':'db'}
        for name in ['api','ingest','mcp']:
            base['services'][name].update(command=['/bin/'+name],environment={'DATABASE_URL':'postgres://ci@postgres/db'})
        base['services']['api']['environment']['FLEET_SIGNING_KEY']='synthetic'
        inputs={'bind':'192.168.1.20','host':'synthetic.invalid','certs':str(root/'certs'),'shared_gid':1000}
        rendered=m.preflight.prep.render(base,inputs['bind'],inputs['host'],inputs['certs'],bundle,1000)
        images={name:item['image']for name,item in base['services'].items()};proposed=dict(images,mosquitto='sha256:'+'b'*64,**{'secure-web':'sha256:'+'c'*64})
        pair=m.runtime.pair(base,rendered,images,proposed)
        for name,value in [('previous-compose.json',base),('compose.json',rendered),('prepare-inputs.json',inputs),('previous-runtime.json',pair['previous']),('candidate-runtime.json',pair['candidate'])]:m.state.atomic(bundle/name,value)
        files={str(p):m.state.digest(p)for p in bundle.glob('*.json')}
        receipt={'version':1,'status':'preflight_only','activation_allowed':False,'root':str(root),'project':'fixture','bundle':str(bundle),'runtime_configs':{'previous':str(bundle/'previous-runtime.json'),'candidate':str(bundle/'candidate-runtime.json')},'files':files,'previous_files':{str(bundle/'previous-runtime.json'):m.state.digest(bundle/'previous-runtime.json')},'proposed_images':proposed,'current_services':{},'acl_sha256':m.preflight.sha(b'# hexmon broker ACL fixture\nuser api\nuser ingest\n')}
        path=bundle/'preflight.json';m.state.atomic(path,receipt);return path
    def effect(self,root,path):return m.DockerTransport(root,'fixture',path,lambda*a:None,lambda*a:None)
    def bind(self,effect,config):
        rows=[]
        for name,service in config['services'].items():
            rows.append({'Id':'id-'+name,'Image':service['image'],'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':name},'Env':[k+'='+str(v)for k,v in service.get('environment',{}).items()],'Cmd':service.get('command'),'Entrypoint':service.get('entrypoint')},'State':{'Status':'running'}})
        effect.containers=lambda path:rows
        effect.call=lambda args:json.dumps([{'Id':args[-1],'Config':{}}])
        return rows
    def test_shared_checked_containers_keeps_transport_direct_only_without_gate_manager(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path)
            rows=self.bind(effect,effect.old)
            self.assertEqual(set(effect.checked_containers(effect.previous,True)),set(effect.old['services']))
            rows[0]['Config']['Labels']['hexthings.startgate']='unreviewed'
            with self.assertRaises(ValueError):effect.checked_containers(effect.previous,True)
    def test_missing_callbacks_refuse_before_file_access(self):
        for callbacks in [(None,lambda*a:None),(lambda*a:None,None)]:
            with self.assertRaises(ValueError):m.DockerTransport('/missing','fixture','/missing/receipt',*callbacks)
    def test_exact_render_and_transport_only_pair(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path)
            self.assertEqual(effect.writers,['api','ingest','mcp'])
            candidate=effect.candidate;changed=m.state.read_private(candidate);changed['services']['api']['environment']['UNEXPECTED']='unreviewed';m.state.atomic(candidate,changed)
            receipt=m.state.read_private(path);receipt['files'][str(candidate)]=m.state.digest(candidate);m.state.atomic(path,receipt)
            with self.assertRaises(ValueError):self.effect(root,path)
    def test_every_writer_stopped_before_acl_export_and_drift_refuses(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path);rows=self.bind(effect,effect.old);calls=[]
            def compose(*args,**kw):calls.append(args);return '# hexmon broker ACL fixture\nuser api\nuser ingest\n'
            effect.compose=compose;value={'previous':str(effect.previous)}
            with self.assertRaises(ValueError):effect.export_acl(value)
            self.assertFalse(calls)
            for row in rows:
                if row['Config']['Labels']['com.docker.compose.service']in effect.writers:row['State']['Status']='exited'
            self.assertTrue(effect.export_acl(value).startswith('# hexmon'))
            effect.compose=lambda*a:'# hexmon broker ACL changed'
            with self.assertRaises(ValueError):effect.export_acl(value)
    def test_unplanned_actual_runtime_refuses_before_stop(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path);rows=self.bind(effect,effect.old);calls=[];effect.compose=lambda*a:calls.append(a)
            rows[0]['Config']['Env'].append('PGHOST=other')
            with self.assertRaises(ValueError):effect.stop_writers({'previous':str(effect.previous)})
            self.assertFalse(calls)
    def test_current_operator_must_prove_ledger_and_exact_writer_inventory(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path)
            good={'status':'migration_ledger_current','writers':effect.writers,'current_services':effect.plan['current_services']}
            with patch.object(m.application.readiness,'inspect',return_value=good):effect.verify_current_operator()
            for change in ['schema','writers','containers']:
                bad=copy.deepcopy(good)
                if change=='schema':bad['status']='application_bootstrap_required'
                if change=='writers':bad['writers']=['api','ingest']
                if change=='containers':bad['current_services']={'changed':{}}
                with patch.object(m.application.readiness,'inspect',return_value=bad),self.assertRaises(ValueError):effect.verify_current_operator()
    def test_runtime_issuer_recheck_is_required_before_effects(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root);effect=self.effect(root,path)
            with patch.object(m.preflight.prep,'check_certificate')as leaf,patch.object(m.preflight.prep,'check_issuing_ca')as issuer:
                effect.verify_tls_inputs();self.assertEqual(leaf.call_count,4);issuer.assert_called_once_with(root/'certs')
            with patch.object(m.preflight.prep,'check_certificate'),patch.object(m.preflight.prep,'check_issuing_ca',side_effect=ValueError('unusable issuer')):
                with self.assertRaises(ValueError):effect.verify_tls_inputs()
    def test_authority_and_runtime_callbacks_are_not_bypassed(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);path=self.plan(root)
            def deny(*a):raise ValueError('synthetic refusal')
            effect=m.DockerTransport(root,'fixture',path,deny,deny)
            with self.assertRaises(ValueError):effect.authorize('activate','fixture',effect.previous,effect.candidate)
            with self.assertRaises(ValueError):effect.accept({'candidate':str(effect.candidate)},'candidate')
if __name__=='__main__':unittest.main()
