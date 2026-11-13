#!/usr/bin/env python3
"""Effect-order, rollback and process-interruption tests. No real Docker calls."""
import importlib.util
import json
import pathlib
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

state=load('secure-state')
transaction=load('secure-transaction')

class Interrupted(BaseException):
    pass

class Effects:
    def __init__(self,root,fail=None,crash=None):
        self.root=root;self.calls=[];self.fail=fail;self.crash=crash
        self.archive=root/'backup.dump';self.archive.write_bytes(b'disposable verified archive fixture');self.archive.chmod(0o600)
    def action(self,name):
        self.calls.append(name)
        if name==self.crash:raise Interrupted(name)
        if name==self.fail:raise ValueError('synthetic '+name+' failure')
    def authorize(self,*args):self.action('authorize-'+args[0])
    def revalidate(self,*args):self.action('revalidate')
    def backup(self,*args):
        self.action('backup')
        return {'path':str(self.archive),'sha256':state.digest(self.archive),'verified_archive':True}
    def stop_writers(self,*args):self.action('stop-writers')
    def export_acl(self,*args):self.action('export-current-acl');return 'synthetic full ACL'
    def stage_acl(self,*args):self.action('stage-acl')
    def start_candidate(self,*args):self.action('start-candidate')
    def accept(self,value,target):self.action('accept-'+target)
    def verify_recovery_inputs(self,*args):self.action('verify-recovery')
    def stop_candidate(self,*args):self.action('stop-candidate')
    def restore_previous(self,*args):self.action('restore-previous')

class Tests(unittest.TestCase):
    def fixture(self,root):
        old,new,receipt=[root/name for name in ['previous-runtime.json','candidate-runtime.json','preflight.json']]
        for path in [old,new]:path.write_text(json.dumps({'fixture':path.name}));path.chmod(0o600)
        images={name:'sha256:'+'a'*64 for name in ['api','ingest','mosquitto','web','postgres']}
        receipt.write_text(json.dumps({'version':1,'status':'preflight_only','activation_allowed':False,'project':'fixture','root':str(root),'current_services':{name:{'image':image}for name,image in images.items()},'files':{str(path):state.digest(path)for path in [old,new]},'previous_files':{str(old):state.digest(old)}}));receipt.chmod(0o600)
        return ('fixture',old,new,images,receipt)
    def test_order_commits_only_after_acceptance(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root)
            with state.Journal(root)as journal:
                result=transaction.Transaction(journal,effects).activate(*args)
                self.assertEqual(journal.read()['phase'],'committed')
            self.assertEqual(effects.calls,['authorize-activate','revalidate','backup','stop-writers','export-current-acl','stage-acl','start-candidate','accept-candidate'])
            self.assertEqual(state.selection(root)['config'],str(args[2]))
            self.assertFalse(result['source_reading_proven'])
    def test_failure_before_downtime_has_no_journal_or_service_effect(self):
        for failure in ['authorize-activate','revalidate','backup']:
            with self.subTest(failure=failure),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,fail=failure)
                with state.Journal(root)as journal:
                    with self.assertRaises(ValueError):transaction.Transaction(journal,effects).activate(*args)
                    self.assertFalse((journal.directory/'journal.json').exists())
                self.assertNotIn('stop-writers',effects.calls)
    def test_each_partial_change_rolls_back_and_selects_exact_prior_runtime(self):
        for failure in ['stop-writers','export-current-acl','stage-acl','start-candidate','accept-candidate']:
            with self.subTest(failure=failure),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,fail=failure)
                with state.Journal(root)as journal:
                    with self.assertRaises(RuntimeError):transaction.Transaction(journal,effects).activate(*args)
                    self.assertEqual(journal.read()['phase'],'restored')
                self.assertEqual(effects.calls[-4:],['verify-recovery','stop-candidate','restore-previous','accept-previous'])
                self.assertEqual(state.selection(root)['config'],str(args[1]))
    def test_crash_after_each_effect_blocks_normal_menu_until_explicit_recovery(self):
        for crash in ['stop-writers','export-current-acl','stage-acl','start-candidate','accept-candidate']:
            with self.subTest(crash=crash),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,crash=crash)
                with state.Journal(root)as journal:
                    with self.assertRaises(Interrupted):transaction.Transaction(journal,effects).activate(*args)
                with self.assertRaises(ValueError):state.selection(root)
                recovery=Effects(root)
                with state.Journal(root)as journal:
                    transaction.Transaction(journal,recovery).recover()
                    self.assertEqual(journal.read()['phase'],'restored')
                self.assertEqual(recovery.calls,['authorize-recover','verify-recovery','verify-recovery','stop-candidate','restore-previous','accept-previous'])
                self.assertEqual(state.selection(root)['config'],str(args[1]))
    def test_failed_rollback_or_damaged_backup_stays_blocked(self):
        for damage in ['rollback','archive']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,crash='start-candidate')
                with state.Journal(root)as journal:
                    with self.assertRaises(Interrupted):transaction.Transaction(journal,effects).activate(*args)
                if damage=='archive':effects.archive.write_bytes(b'damaged')
                effects.fail='accept-previous';effects.crash=None
                with state.Journal(root)as journal:
                    with self.assertRaises(ValueError):transaction.Transaction(journal,effects).recover()
                with self.assertRaises(ValueError):state.selection(root)
    def test_restored_selector_crash_window_cannot_become_lab_fallback(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root,crash='start-candidate')
            with state.Journal(root)as journal:
                with self.assertRaises(Interrupted):transaction.Transaction(journal,effects).activate(*args)
                journal.finish_rollback()
                with self.assertRaises(ValueError):state.selection(root)
                journal.select_restored()
            self.assertEqual(state.selection(root)['config'],str(args[1]))

    def test_prepared_crash_before_backup_metadata_never_stops_services(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);args=self.fixture(root);effects=Effects(root)
            with state.Journal(root)as journal:journal.begin(*args)
            with state.Journal(root)as journal:
                transaction.Transaction(journal,effects).recover()
                self.assertEqual(journal.read()['phase'],'restored')
            self.assertEqual(effects.calls,['authorize-recover','verify-recovery','verify-recovery','accept-previous'])
            self.assertEqual(state.selection(root)['config'],str(args[1]))

if __name__=='__main__':unittest.main()
