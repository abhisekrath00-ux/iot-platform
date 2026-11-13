#!/usr/bin/env python3
"""Production assembly lifecycle only. Native suite exercises real Docker paths."""
import copy
import importlib.util
import pathlib
import tempfile
import unittest
from types import SimpleNamespace
from unittest.mock import patch

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('production',HERE/'secure-application-production.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
spec=importlib.util.spec_from_file_location('connection_fixture',HERE/'test-secure-application-connections.py')
f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)

class Loss(BaseException):pass

class Tests(unittest.TestCase):
    def fixture(self,root):
        events=[];failure=[None]
        catalog=SimpleNamespace(container='a'*64,user='ci',database='app')
        p=m.Provider(catalog,lambda _:None,f.OUT,f.REQUEST)
        p.journal=SimpleNamespace(directory=root,read=lambda:dict(phase='committed'))
        p.target=SimpleNamespace(project='fixture',action='recover');p.image=f.IMAGE
        path=root/'hba';path.write_bytes(f.ORIGINAL);p.host=SimpleNamespace(path=path)
        p.barrier=SimpleNamespace(read=lambda:dict(original=f.ORIGINAL.hex()))
        def guard(phase,raw):
            events.append(('guard',phase))
            if failure[0]=='guard':raise ValueError('changed target')
        p.connection_guard=guard
        class Host:
            def __init__(self,journal,image,check,runner):
                self.path=root/'host.json';self.guard=check
                self.image=image
            def record(self):return m.base.state.read_private(self.path)
            def prepare(self):
                self.guard();events.append(('host','prepare'));m.base.state.atomic(self.path,dict(stage='ready',source='hexthings-hostprobe-'+'1'*32))
            def resume(self):
                self.guard();events.append(('host','resume'));v=self.record();v['stage']='ready';m.base.state.atomic(self.path,v)
            def release(self):
                self.guard();events.append(('host','release'));v=self.record();v['stage']='released';m.base.state.atomic(self.path,v)
                if failure[0]=='cleanup':raise Loss()
        class Local:
            def __init__(self,journal,catalog,image,check,runner):self.path=root/'local.json';self.guard=check
            def record(self):return m.base.state.read_private(self.path)
            def recover_staging(self):self.guard();events.append(('local','recover'));self.path.unlink()
            def prepare(self):self.guard();events.append(('local','prepare'));m.base.state.atomic(self.path,dict(stage='ready'))
            def connection(self):return dict(version=1,status='connected',database='app',user='ci')
            def cleanup(self):self.guard();events.append(('local','cleanup'));m.base.state.atomic(self.path,dict(stage='removed'))
        class Callback:
            def __init__(self,provider,tool,endpoint,request,original,local):
                self.p=provider;self.assertions=(endpoint==f.OUT and request==f.REQUEST and original==f.ORIGINAL)
                self.local=local
            def __call__(self,phase,peers,raw):
                if not self.assertions:raise ValueError('unbound callback inputs')
                events.append(('proof',phase));self.p.connection_guard(phase,raw);self.local()
                if failure[0]=='proof':raise ValueError('unverified proof')
        return p,events,failure,Host,Local,Callback

    def using(self,classes):
        host,local,callback=classes
        from contextlib import ExitStack
        stack=ExitStack();stack.enter_context(patch.object(m.hosttools,'HostTool',host));stack.enter_context(patch.object(m.base.operators,'LocalOperator',local));stack.enter_context(patch.object(m.connections,'Connections',callback));return stack

    def test_fresh_and_ready_tools_use_exact_bound_callback_without_restage(self):
        with tempfile.TemporaryDirectory()as td:
            p,events,failure,*classes=self.fixture(pathlib.Path(td))
            with self.using(classes):p.connection_proof('original',[],f.ORIGINAL);p.connection_proof('original',[],f.ORIGINAL)
            self.assertEqual(events.count(('host','prepare')),1);self.assertEqual(events.count(('local','prepare')),1)
            self.assertEqual(events.count(('proof','original')),2);self.assertNotIn(('host','release'),events)

    def test_partial_staging_resumes_on_fresh_provider_invocation(self):
        for stage in ['creating','copying','removed']:
            with self.subTest(stage=stage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root)
                m.base.state.atomic(root/'host.json',dict(stage='copying',source='hexthings-hostprobe-'+'1'*32))
                m.base.state.atomic(root/'local.json',dict(stage=stage))
                with self.using(classes):p.connection_proof('original',[],f.ORIGINAL)
                self.assertIn(('host','resume'),events);self.assertIn(('local','recover'),events)
                self.assertIn(('local','prepare'),events);self.assertNotIn(('host','release'),events)

    def test_failed_proof_retains_ready_tools_and_no_cleanup(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root);failure[0]='proof'
            with self.using(classes),self.assertRaises(ValueError):p.connection_proof('original',[],f.ORIGINAL)
            self.assertEqual(m.base.state.read_private(root/'host.json')['stage'],'ready')
            self.assertEqual(m.base.state.read_private(root/'local.json')['stage'],'ready')
            self.assertNotIn(('local','cleanup'),events)

    def test_cleanup_follows_successful_base_release_and_retains_on_base_failure(self):
        for fail in [False,True]:
            with self.subTest(fail=fail),tempfile.TemporaryDirectory()as td:
                p,events,failure,*classes=self.fixture(pathlib.Path(td))
                def settled(*args):
                    events.append(('base','release'))
                    if fail:raise ValueError('runtime unsettled')
                with self.using(classes),patch.object(m.base.Provider,'release',settled):
                    p.connection_proof('original',[],f.ORIGINAL);events.clear()
                    if fail:
                        with self.assertRaises(ValueError):p.release(object(),p.target)
                        self.assertEqual(events,[('base','release')])
                    else:
                        p.release(object(),p.target)
                        self.assertLess(events.index(('base','release')),events.index(('local','cleanup')))
                        self.assertLess(events.index(('local','cleanup')),events.index(('host','release')))

    def test_lost_host_cleanup_ack_rolls_verified_released_generation_only_in_settled_recovery(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root)
            with self.using(classes),patch.object(m.base.Provider,'release',lambda *a:None):
                p.connection_proof('original',[],f.ORIGINAL);failure[0]='cleanup'
                with self.assertRaises(Loss):p.release(object(),p.target)
                failure[0]=None;p.connection_proof('original',[],f.ORIGINAL)
                archived=list(root.glob('postgres-hosttool-released-*.json'));self.assertEqual(len(archived),1)
                self.assertEqual(m.base.state.read_private(archived[0])['stage'],'released')
                self.assertEqual(m.base.state.read_private(root/'host.json')['stage'],'ready')
                self.assertEqual(m.base.state.read_private(root/'local.json')['stage'],'ready')

    def test_released_generation_never_rearmed_on_activation_restricted_unsettled_or_collision(self):
        for damage in ['activate','restricted','unsettled','collision','guard']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root);phase='original'
                m.base.state.atomic(root/'host.json',dict(stage='released',source='hexthings-hostprobe-'+'1'*32))
                if damage=='activate':p.target.action='activate'
                if damage=='restricted':phase='restricted'
                if damage=='unsettled':p.journal.read=lambda:dict(phase='changing')
                if damage=='collision':(root/('postgres-hosttool-released-hexthings-hostprobe-'+'1'*32+'.json')).write_text('existing')
                if damage=='guard':failure[0]='guard'
                with self.using(classes),self.assertRaises(ValueError):p.connection_proof(phase,[],f.ORIGINAL)
                self.assertTrue((root/'host.json').exists());self.assertNotIn(('host','prepare'),events)

    def test_explicit_endpoint_request_and_plan_image_required_no_defaults(self):
        catalog=SimpleNamespace(container='a'*64,user='ci',database='app')
        for endpoint,request in [(None,f.REQUEST),(f.OUT,None),({},f.REQUEST),(f.OUT,{**f.REQUEST,'user':'other'}),(f.OUT,{**f.REQUEST,'transport':'local_socket'})]:
            with self.assertRaises((ValueError,KeyError)):m.Provider(catalog,lambda _:None,endpoint,request)
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root);p.image=None
            with self.using(classes),self.assertRaises(ValueError):p.connection_proof('original',[],f.ORIGINAL)
            self.assertNotIn(('host','prepare'),events)

    def test_bound_plan_api_image_not_tag_or_fixture_global(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root);candidate=root/'candidate.json'
            target=SimpleNamespace(candidate=str(candidate),previous=str(root/'previous.json'),project='fixture')
            for image in [f.IMAGE,'candidate:latest']:
                m.base.state.atomic(candidate,dict(services=dict(api=dict(image=image))))
                receipt=root/'preflight.json';m.base.state.atomic(receipt,dict(files={str(candidate):m.base.state.digest(candidate)}));target.receipt_sha256=m.base.state.digest(receipt)
                with patch.object(m.base.Provider,'bind_journal',lambda *a:None):
                    if image==f.IMAGE:p.bind_journal(p.journal,target);self.assertEqual(p.image,image)
                    else:
                        with self.assertRaises(ValueError):p.bind_journal(p.journal,target)

    def test_candidate_manifest_drift_fails_before_tool_staging(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);p,events,failure,*classes=self.fixture(root)
            candidate=root/'candidate.json';m.base.state.atomic(candidate,dict(services=dict(api=dict(image=f.IMAGE))))
            receipt=root/'preflight.json';m.base.state.atomic(receipt,dict(files={str(candidate):m.base.state.digest(candidate)}))
            target=SimpleNamespace(candidate=str(candidate),previous=str(root/'previous.json'),receipt_sha256=m.base.state.digest(receipt))
            m.base.state.atomic(candidate,dict(services=dict(api=dict(image='sha256:'+'f'*64))))
            with self.assertRaises(ValueError):p.checked_candidate(target)
            self.assertNotIn(('host','prepare'),events)

if __name__=='__main__':unittest.main()
