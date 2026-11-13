#!/usr/bin/env python3
"""Exact private plan review contracts; fake fixtures, no Docker/owner effects."""
import copy
import importlib.util
import pathlib
import tempfile
import unittest
import subprocess
import os
import shutil
import sys
sys.dont_write_bytecode=True
HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'));m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m
m=load('secure-update-review');fixture=load('test-secure-application-docker')

class Tests(unittest.TestCase):
    def snapshot(self,root):
        return {str(p.relative_to(root)):(p.read_bytes(),p.stat().st_mode,p.stat().st_mtime_ns)for p in root.rglob('*')if p.is_file()}
    def test_exact_images_redacted_review_never_approval_or_effect(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);receipt,old,new=fixture.Tests().plan(root);before=self.snapshot(root)
            v=m.inspect(root,'fixture',receipt)
            self.assertTrue(v['plan_files_verified']);self.assertFalse(v['ready_to_update']);self.assertFalse(v['changes_allowed']);self.assertFalse(v['live_runtime_verified']);self.assertFalse(v['dashboard_package_verified'])
            self.assertEqual(v['writers'],['api','ingest','mcp'])
            self.assertEqual([x['service']for x in v['images']if x['previous']!=x['candidate']],['api','ingest','mcp'])
            text=m.display(v)
            for forbidden in ['postgres://','synthetic','DATABASE_URL',str(root)]:self.assertNotIn(forbidden,text)
            self.assertIn('NOT approval',text);self.assertEqual(before,self.snapshot(root))
    def test_changed_or_unbound_plan_refuses_shared_execution_and_review(self):
        for damage in ['root','project','scope','approval','candidate','missing-candidate-manifest','current-image','missing-previous-manifest','extra-manifest','extra-previous-manifest','writer','extra-service','database']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);receipt,old,new=fixture.Tests().plan(root);v=m.state.read_private(receipt)
                if damage=='root':v['root']='/other'
                if damage=='project':v['project']='other'
                if damage=='scope':v['scope']='transport_only'
                if damage=='approval':v['activation_allowed']=True
                if damage=='candidate':new.write_text('{}')
                if damage=='missing-candidate-manifest':del v['files'][str(new)]
                if damage=='missing-previous-manifest':v['previous_files']={}
                if damage in {'extra-manifest','extra-previous-manifest'}:
                    extra=root/'extra';m.state.atomic(extra,{'unrelated':True})
                    v['files'if damage=='extra-manifest'else'previous_files'][str(extra)]=m.state.digest(extra)
                if damage=='current-image':v['current_services']['api']['image']='sha256:'+'c'*64
                if damage=='writer':v['writers']=['api','ingest']
                if damage=='extra-service':v['current_services']['unknown']={'image':'sha256:'+'c'*64}
                if damage=='database':v['database']['name']='other'
                m.state.atomic(receipt,v);before=self.snapshot(root)
                with self.assertRaises((ValueError,KeyError)):m.inspect(root,'fixture',receipt)
                with self.assertRaises((ValueError,KeyError)):m.adapter.DockerApplication(root,'fixture',receipt,lambda*a:None,lambda*a:None,lambda*a:None)
                self.assertEqual(before,self.snapshot(root))
    def test_readonly_bind_must_be_bound_in_both_manifests(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);receipt,old,new=fixture.Tests().plan(root)
            dependency=root/'broker.conf';dependency.write_text('fixture-only');dependency.chmod(0o600)
            v=m.state.read_private(receipt)
            for path in [old,new]:
                value=m.state.read_private(path);value['services']['mosquitto']['volumes']=[{'type':'bind','source':str(dependency),'target':'/etc/broker.conf','read_only':True}];m.state.atomic(path,value);v['files'][str(path)]=m.state.digest(path)
            v['previous_files'][str(old)]=m.state.digest(old);m.state.atomic(receipt,v)
            with self.assertRaises(ValueError):m.inspect(root,'fixture',receipt)
            v['files'][str(dependency)]=m.state.digest(dependency);v['previous_files'][str(dependency)]=m.state.digest(dependency);m.state.atomic(receipt,v)
            self.assertTrue(m.inspect(root,'fixture',receipt)['plan_files_verified'])
            dependency.write_text('changed')
            with self.assertRaises(ValueError):m.inspect(root,'fixture',receipt)
    def test_cli_and_menu_redact_failure_never_fallback(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);receipt,old,new=fixture.Tests().plan(root);scripts=root/'scripts';shutil.copytree(HERE,scripts,ignore=shutil.ignore_patterns('__pycache__'))
            bin=root/'bin';bin.mkdir();docker=bin/'docker';docker.write_text('#!/bin/sh\necho unexpected-docker >&2\nexit 88\n');docker.chmod(0o700)
            env=dict(os.environ,PATH=str(bin)+':'+os.environ['PATH']);before=self.snapshot(root)
            for cmd in [['python3',str(scripts/'secure-update-review.py'),'--root',str(root),'--project','fixture','--receipt',str(receipt),'--json'],['bash',str(scripts/'setup.sh'),'secure-update-review','fixture',str(receipt)]]:
                p=subprocess.run(cmd,capture_output=True,text=True,env=env);self.assertEqual(p.returncode,0,p.stderr);self.assertNotIn('unexpected-docker',p.stdout+p.stderr);self.assertNotIn('synthetic',p.stdout+p.stderr)
            self.assertEqual(before,self.snapshot(root))
            new.write_text('never-print-private-secret');before=self.snapshot(root)
            p=subprocess.run(['bash',str(scripts/'setup.sh'),'secure-update-review','fixture',str(receipt)],capture_output=True,text=True,env=env)
            self.assertNotEqual(p.returncode,0);self.assertNotIn('never-print-private-secret',p.stdout+p.stderr);self.assertNotIn('unexpected-docker',p.stdout+p.stderr);self.assertEqual(before,self.snapshot(root))
    def test_missing_input_creates_nothing(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td)
            with self.assertRaises(OSError):m.inspect(root,'fixture',root/'missing')
            self.assertEqual(list(root.iterdir()),[])
if __name__=='__main__':unittest.main()
