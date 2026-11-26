#!/usr/bin/env python3
"""Local fake-Podman contract tests. No process/container/service effects."""
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('host',Path(__file__).with_name('host.py'))
host=importlib.util.module_from_spec(spec);spec.loader.exec_module(host)


def valid_inspect():
    return {'HostConfig':{'Privileged':False,'NetworkMode':'none','CgroupMode':'private','Cgroups':'default','PidMode':'private','PortBindings':{},'Binds':[], 'SecurityOpt':[], 'PidsLimit':128,'Memory':805306368,'CpuPeriod':100000,'CpuQuota':200000,'LogConfig':{'Type':'k8s-file'}}, 'Mounts':[], 'EffectiveCaps':['CAP_'+c for c in host.CAPS]}


class Fake:
    def __init__(self,state):
        self.x={'Id':'a'*64,'Image':state['image'],'Config':{'Labels':{'hex.confined.owner':state['token']}},'State':{'Running':True}}
        self.calls=[];self.fail_stop=False
    def run(self,args,**kwargs):
        self.calls.append(tuple(args));return subprocess.CompletedProcess(args,1 if self.x is None else 0)
    def call(self,*args,**kwargs):
        self.calls.append(args)
        if args[:2]==('container','inspect'):return json.dumps([self.x])
        if args[0]=='stop':
            if self.fail_stop:raise RuntimeError('synthetic stop refusal')
            self.x['State']['Running']=False;return ''
        if args[0]=='rm':self.x=None;return ''
        if args[0]=='logs':return 'synthetic bounded log'
        raise AssertionError(args)


class Contracts(unittest.TestCase):
    def setup_fixture(self,d):
        state={'name':'exact-random-name','token':'exact-random-owner','image':'sha256:'+'1'*64}
        host.save(d,state);return state,Fake(state)
    def test_create_before_id_save_and_repeat(self):
        with tempfile.TemporaryDirectory() as directory:
            d=Path(directory);state,f=self.setup_fixture(d)
            # create completed but immutable ID was not saved. Exact label+image
            # let cleanup recover it without name-prefix scanning.
            with patch.object(host,'call',f.call),patch.object(host.subprocess,'run',f.run):
                host.cleanup(d);host.cleanup(d)
            saved=json.loads((d/'owner.json').read_text())
            self.assertEqual(saved['id'],'a'*64);self.assertIsNone(f.x)
            self.assertEqual(saved['cleanup'],'exact container absent')
    def test_identity_drift_refusal(self):
        for drift in ['label','image','id','different-image','different-id']:
            with self.subTest(drift=drift),tempfile.TemporaryDirectory() as directory:
                d=Path(directory);state,f=self.setup_fixture(d)
                if drift=='label':f.x['Config']['Labels']['hex.confined.owner']='foreign'
                if drift=='image':f.x['Image']='foreign'
                if drift=='id':state['id']='foreign';host.save(d,state)
                if drift=='different-image':f.x['Image']='2'*64
                if drift=='different-id':state['id']='b'*64;host.save(d,state)
                with patch.object(host,'call',f.call),patch.object(host.subprocess,'run',f.run),self.assertRaises(AssertionError):host.cleanup(d)
                self.assertFalse(any(c[0] in ['stop','rm'] for c in f.calls))
    def test_cleanup_failure_preserves(self):
        with tempfile.TemporaryDirectory() as directory:
            d=Path(directory);state,f=self.setup_fixture(d);f.fail_stop=True
            with patch.object(host,'call',f.call),patch.object(host.subprocess,'run',f.run),self.assertRaises(RuntimeError):host.cleanup(d)
            self.assertIsNotNone(f.x)
            self.assertNotIn('cleanup',json.loads((d/'owner.json').read_text()))
    def test_failed_probe_then_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            d=Path(directory);image='sha256:'+'1'*64;imagefile=d/'image';imagefile.write_text(image)
            f=Fake({'image':image,'token':'unused'});original=f.call
            def call(*args,**kw):
                if args[:2]==('image','inspect'):return image
                if args[0]=='create':
                    state=json.loads((d/'owner.json').read_text());f.x['Config']['Labels']['hex.confined.owner']=state['token']
                    return 'a'*64
                return original(*args,**kw)
            f.x.update(valid_inspect())
            base_call=call
            def probe_call(*args,**kw):
                if args[0]=='start':return ''
                if args[0]=='exec':raise RuntimeError('synthetic actual inside probe failure')
                return base_call(*args,**kw)
            def run_command(args,**kw):
                if args[:2]==['podman','exec']:return subprocess.CompletedProcess(args,0,'Version=255\n','')
                return f.run(args,**kw)
            # A failure during the actual inside probe retains failed status
            # while exercising real finally and cleanup(), not mock cleanup.
            with patch.dict(host.os.environ,{'PROBE_IMAGE_ID_FILE':str(imagefile)}),patch.object(host,'call',probe_call),patch.object(host.subprocess,'run',run_command),self.assertRaises(RuntimeError):host.run(d)
            saved=json.loads((d/'owner.json').read_text())
            self.assertEqual(saved['result'],'failed');self.assertEqual(saved['cleanup'],'exact container absent');self.assertIsNone(f.x)
    def test_image_canonical_equivalence_and_malformed(self):
        for value in ['1'*64,'sha256:'+'1'*64]:
            self.assertEqual(host.image_id(value),'sha256:'+'1'*64)
        for value in ['', '1'*63, '1'*65, 'A'*64, 'sha512:'+'1'*64, 'ubuntu:latest', 'sha256:'+'g'*64, '1'*64+'\n', ' '+ '1'*64, None, 123]:
            with self.subTest(value=value),self.assertRaises(AssertionError):host.image_id(value)
    def test_cleanup_equivalent_images(self):
        for observed in ['1'*64,'sha256:'+'1'*64]:
            with tempfile.TemporaryDirectory() as directory:
                d=Path(directory);state,f=self.setup_fixture(d);f.x['Image']=observed
                with patch.object(host,'call',f.call),patch.object(host.subprocess,'run',f.run):host.cleanup(d)
                self.assertIsNone(f.x)
    def test_precreate_ids_diagnostics_and_refusal(self):
        for observed in ['1'*64, 'sha256:'+'1'*64, '2'*64, '1'*63, '1'*64+'\nextra']:
            with self.subTest(observed=observed),tempfile.TemporaryDirectory() as directory:
                d=Path(directory);f=d/'iid';f.write_text('sha256:'+'1'*64);calls=[]
                def call(*args,**kw):
                    calls.append(args)
                    if args[:2]==('image','inspect'):return observed+'\n'
                    if args[0]=='create':raise RuntimeError('stop at fake create')
                    if args[0]=='logs':return ''
                    raise AssertionError(args)
                with patch.dict(host.os.environ,{'PROBE_IMAGE_ID_FILE':str(f)}),patch.object(host,'call',call),patch.object(host,'cleanup'),self.assertRaises((AssertionError,RuntimeError)):host.run(d)
                evidence=json.loads((d/'image-identity.json').read_text())
                self.assertEqual(evidence['inspect_stdout'],observed+'\n')
                self.assertEqual(any(c[0]=='create' for c in calls),observed in ['1'*64,'sha256:'+'1'*64])
    def test_exact_version_inspect_schema(self):
        import copy
        host.validate_inspect(valid_inspect())
        for key,value in [('CgroupMode','host'),('PidMode','host'),('Cgroups','disabled'),('Privileged',True),('NetworkMode','host'),('PortBindings',{'80/tcp':[{}]}),('Binds',['/host:/guest']),('SecurityOpt',['seccomp=unconfined']),('Memory',1)]:
            x=valid_inspect();x['HostConfig'][key]=value
            with self.subTest(key=key),self.assertRaises(AssertionError):host.validate_inspect(x)
        for key in ['CgroupMode','PidMode','Binds','PortBindings','SecurityOpt']:
            x=valid_inspect();del x['HostConfig'][key]
            with self.subTest(missing=key),self.assertRaises(KeyError):host.validate_inspect(x)
        x=valid_inspect();del x['HostConfig']['CgroupMode'];x['HostConfig']['CgroupnsMode']='private'
        with self.assertRaises(KeyError):host.validate_inspect(x)
        x=valid_inspect();x['EffectiveCaps'].append('CAP_SYS_PTRACE')
        with self.assertRaises(AssertionError):host.validate_inspect(x)
        x=valid_inspect();x['Mounts']=[{'Source':'/host'}]
        with self.assertRaises(AssertionError):host.validate_inspect(x)
    def test_successful_lifecycle_with_exact_version_shape(self):
        for iid in ['1'*64,'sha256:'+'1'*64]:
            with self.subTest(inspect=iid),tempfile.TemporaryDirectory() as directory:
                d=Path(directory);image='sha256:'+'1'*64;p=d/'iid';p.write_text(image)
                f=Fake({'image':'1'*64,'token':'unused'});f.x.update(valid_inspect());base=f.call
                def call(*args,**kw):
                    if args[:2]==('image','inspect'):return iid+'\n'
                    if args[0]=='create':
                        state=json.loads((d/'owner.json').read_text());f.x['Config']['Labels']['hex.confined.owner']=state['token'];return 'a'*64+'\n'
                    if args[0]=='start':return 'a'*64+'\n'
                    if args[0]=='exec':return 'fake inside PASS'
                    return base(*args,**kw)
                def run_command(args,**kw):
                    if args[:2]==['podman','exec']:return subprocess.CompletedProcess(args,0,'Version=255\n','')
                    return f.run(args,**kw)
                with patch.dict(host.os.environ,{'PROBE_IMAGE_ID_FILE':str(p)}),patch.object(host,'call',call),patch.object(host.subprocess,'run',run_command):host.run(d)
                state=json.loads((d/'owner.json').read_text());self.assertEqual(state['result'],'passed');self.assertEqual(state['cleanup'],'exact container absent');self.assertIsNone(f.x)
                self.assertTrue((d/'image-identity.json').exists());self.assertTrue((d/'inspect-before-start.json').exists())
    def test_version_grammar(self):
        import re
        pattern=r'systemd 255(?:\.[0-9]+)*(?: \([^\r\n]+\))?'
        for value in ['systemd 255','systemd 255.4','systemd 255 (255.4-1ubuntu8.18)']:
            self.assertIsNotNone(re.fullmatch(pattern,value))
        for value in ['systemd 256','systemd 255bad','systemd 255.','systemd 255\nmalformed']:
            self.assertIsNone(re.fullmatch(pattern,value))


if __name__=='__main__':unittest.main()
