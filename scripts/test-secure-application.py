#!/usr/bin/env python3
"""Application-only transaction order/crash safety. Fake effects, no Docker."""
import copy
import importlib.util
import json
import pathlib
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

app=load('secure-application');state=load('secure-state');transport=load('secure-transaction')
OLD='sha256:'+'a'*64;NEW='sha256:'+'b'*64

class Interrupted(BaseException):pass

class Effects:
    def __init__(self,root,fail=None,crash=None):
        self.calls=[];self.fail=fail;self.crash=crash;self.schema='exact_reviewed_baseline_89'
        self.archive=root/'backup.dump';self.archive.write_bytes(b'private synthetic archive');self.archive.chmod(0o600)
        self.rows=['retained-source','retained-identity'];self.stopped=False
    def action(self,name):
        self.calls.append(name)
        if name==self.crash:raise Interrupted(name)
        if name==self.fail:raise ValueError('injected '+name)
    def authorize(self,action,*args):self.action(action)
    def revalidate(self,*args):self.action('revalidate')
    def backup(self,*args):
        self.action('backup')
        return {'path':str(self.archive),'sha256':state.digest(self.archive),'verified_archive':True}
    def stop_writers(self,*args):self.stopped=True;self.action('stop-all-writers')
    def verify_writers_stopped(self,*args):
        self.action('verify-all-writers-stopped')
        if not self.stopped:raise ValueError('writer still active')
    def apply_reviewed_schema(self,*args):
        self.schema='exact_reviewed_additive_93';self.rows.append('post-backup-telemetry');self.action('apply-reviewed-schema')
    def inspect_reviewed_schema(self,*args):
        self.action('inspect-schema');return {'version':1,'status':self.schema,'runtime_accepted':False}
    def start_candidate(self,*args):self.stopped=False;self.action('start-candidate')
    def accept(self,value,target):self.action('accept-'+target)
    def verify_recovery_inputs(self,*args):self.action('verify-recovery-inputs')
    def stop_candidate(self,*args):self.stopped=True;self.action('stop-all-candidate-writers')
    def verify_previous_schema_compatibility(self,*args):self.action('verify-prior-schema-compatibility')
    def restore_previous(self,*args):self.stopped=False;self.action('restore-previous')

class Tests(unittest.TestCase):
    def config(self):
        services={name:{'image':OLD,'pull_policy':'never'} for name in ['api','ingest','mcp','postgres','mosquitto','web']}
        for name in ['api','ingest','mcp']:
            services[name].update(command=['/bin/'+name],environment={'DATABASE_URL':'synthetic'})
        return {'services':services}
    def fixture(self,root):
        old=self.config();new=copy.deepcopy(old)
        for name in ['api','ingest','mcp']:new['services'][name]['image']=NEW
        previous,candidate,receipt=[root/name for name in ['previous.json','candidate.json','receipt.json']]
        for path,value in [(previous,old),(candidate,new)]:path.write_text(json.dumps(value));path.chmod(0o600)
        images={name:item['image']for name,item in old['services'].items()}
        receipt.write_text(json.dumps({'version':1,'status':'preflight_only','activation_allowed':False,'project':'fixture','root':str(root),'current_services':{name:{'image':image}for name,image in images.items()},'files':{str(path):state.digest(path)for path in [previous,candidate]},'previous_files':{str(previous):state.digest(previous)}}));receipt.chmod(0o600)
        return ('fixture',previous,candidate,images,receipt)
    def test_only_writer_images_change_and_mcp_is_included(self):
        old=self.config();new=copy.deepcopy(old);images={name:OLD for name in old['services']}
        for name in ['api','ingest','mcp']:new['services'][name]['image']=NEW
        proposed={name:s['image']for name,s in new['services'].items()}
        self.assertEqual(app.pair(old,new,images,proposed)['writers'],['api','ingest','mcp'])
        for change in ['port','env','command','mount','broker','mcp-old','unknown','remove']:
            with self.subTest(change=change):
                bad=copy.deepcopy(new)
                if change=='port':bad['services']['api']['ports']=['0.0.0.0:8000:8000']
                if change=='env':bad['services']['api']['environment']['DATABASE_URL']='another database'
                if change=='command':bad['services']['api']['command']=['custom-writer']
                if change=='mount':bad['services']['api']['volumes']=[{'type':'bind','source':'/tmp/other','target':'/data'}]
                if change=='broker':bad['services']['mosquitto']['image']=NEW
                if change=='mcp-old':bad['services']['mcp']['image']=OLD
                if change=='unknown':bad['services']['custom']={'image':NEW}
                if change=='remove':del bad['services']['mcp']
                with self.assertRaises(ValueError):app.pair(old,bad,images,{name:s['image']for name,s in bad['services'].items()})
    def test_commit_order_and_separate_scope(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root)
            with state.Journal(root)as journal:
                result=app.ApplicationTransaction(journal,effects).activate(*args)
                self.assertEqual(journal.read()['schema_attempted'],app.ADDED)
                self.assertTrue(journal.read()['schema_upgrade'])
            self.assertEqual(effects.calls,['application-upgrade','revalidate','backup','stop-all-writers','verify-all-writers-stopped','apply-reviewed-schema','inspect-schema','start-candidate','accept-candidate'])
            self.assertFalse(result['transport_activated']);self.assertFalse(result['source_reading_proven'])
            self.assertEqual(state.selection(root)['config'],str(args[2]))
    def test_failure_before_downtime_has_no_journal(self):
        for fail in ['application-upgrade','revalidate','backup']:
            with self.subTest(fail=fail),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,fail=fail)
                with state.Journal(root)as journal:
                    with self.assertRaises(ValueError):app.ApplicationTransaction(journal,effects).activate(*args)
                    self.assertFalse((journal.directory/'journal.json').exists())
                self.assertNotIn('stop-all-writers',effects.calls)
    def test_failed_candidate_restores_old_runtime_without_erasing_post_backup_data(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,fail='accept-candidate')
            with state.Journal(root)as journal:
                with self.assertRaises(RuntimeError):app.ApplicationTransaction(journal,effects).activate(*args)
                self.assertEqual(journal.read()['phase'],'restored')
            self.assertIn('verify-prior-schema-compatibility',effects.calls)
            self.assertEqual(effects.schema,'exact_reviewed_additive_93')
            self.assertIn('post-backup-telemetry',effects.rows)
            self.assertEqual(state.selection(root)['config'],str(args[1]))
    def test_process_loss_at_every_effect_blocks_then_requires_exact_schema_recovery(self):
        for crash in ['stop-all-writers','verify-all-writers-stopped','apply-reviewed-schema','inspect-schema','start-candidate','accept-candidate']:
            with self.subTest(crash=crash),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,crash=crash)
                with state.Journal(root)as journal:
                    with self.assertRaises(Interrupted):app.ApplicationTransaction(journal,effects).activate(*args)
                with self.assertRaises(ValueError):state.selection(root)
                effects.crash=None
                with state.Journal(root)as journal:
                    with self.assertRaises(ValueError):transport.Transaction(journal,effects).recover()
                    app.ApplicationTransaction(journal,effects).recover()
                    self.assertEqual(journal.read()['phase'],'restored')
                if effects.schema=='exact_reviewed_additive_93':self.assertIn('verify-prior-schema-compatibility',effects.calls)
                self.assertEqual(state.selection(root)['config'],str(args[1]))
    def test_partial_schema_or_failed_compatibility_keeps_writers_stopped_and_menu_blocked(self):
        for damage in ['partial','compatibility','archive']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,crash='apply-reviewed-schema')
                with state.Journal(root)as journal:
                    with self.assertRaises(Interrupted):app.ApplicationTransaction(journal,effects).activate(*args)
                effects.crash=None
                if damage=='partial':effects.schema='unknown_partial_91'
                if damage=='compatibility':effects.fail='verify-prior-schema-compatibility'
                if damage=='archive':effects.archive.write_bytes(b'damaged')
                with state.Journal(root)as journal:
                    with self.assertRaises(ValueError):app.ApplicationTransaction(journal,effects).recover()
                self.assertNotIn('restore-previous',effects.calls)
                self.assertTrue(effects.stopped)
                with self.assertRaises(ValueError):state.selection(root)
    def test_prepared_crash_has_no_schema_or_stop_effect(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root)
            with state.Journal(root)as journal:journal.begin(*args,kind='application_bootstrap')
            with state.Journal(root)as journal:app.ApplicationTransaction(journal,effects).recover()
            self.assertEqual(effects.calls,['application-recover','verify-recovery-inputs','verify-recovery-inputs','accept-previous'])
            self.assertEqual(state.selection(root)['config'],str(args[1]))

if __name__=='__main__':unittest.main()
