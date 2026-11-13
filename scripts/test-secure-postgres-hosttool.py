#!/usr/bin/env python3
import importlib.util
import io
import json
import os
import pathlib
import subprocess
import struct
import tarfile
import tempfile
import unittest
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('hosttool',HERE/'secure-postgres-hosttool.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
IMAGE='sha256:'+'a'*64
def elf():
    raw=bytearray(128);raw[:8]=b'\x7fELF\x02\x01\x01\x00'
    struct.pack_into('<HHI',raw,16,2,62,1);struct.pack_into('<Q',raw,32,64)
    struct.pack_into('<HHH',raw,52,64,56,1);struct.pack_into('<I',raw,64,1)
    struct.pack_into('<Q',raw,96,len(raw));return raw
class Loss(BaseException):pass
class Runner:
    def __init__(self):self.item=None;self.calls=[];self.loss=None;self.damage=None
    def __call__(self,args,**kwargs):
        self.calls.append(args);cmd=args[1];out=''
        if cmd=='container':out=self.item['Id']+'\n'if self.item else ''
        elif cmd=='create':
            name=args[args.index('--name')+1];self.item={'Id':'b'*64,'Name':'/'+name,'Image':IMAGE,'State':{'Status':'created'},'HostConfig':{'NetworkMode':'none','Privileged':False},'Mounts':[],'Config':{'Entrypoint':['/bin/true'],'Labels':{'hexthings.hostprobe':name}}};out=self.item['Id']
        elif cmd=='inspect':out=json.dumps([self.item])
        elif cmd=='cp':
            raw=elf();stream=io.BytesIO()
            with tarfile.open(fileobj=stream,mode='w')as tar:
                info=tarfile.TarInfo('setupctl');info.size=len(raw);tar.addfile(info,io.BytesIO(raw))
            out=stream.getvalue()
        elif cmd=='rm':self.item=None
        else:raise AssertionError(args)
        if self.loss==cmd:self.loss=None;raise Loss('synthetic lost '+cmd+' acknowledgement')
        return subprocess.CompletedProcess(args,0,out,''if kwargs.get('text')else b'')
class Tests(unittest.TestCase):
    def fixture(self,root,journal,runner=None):
        m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'scope':'application_bootstrap'})
        return m.HostTool(journal,IMAGE,lambda:None,runner or Runner())
    def test_prepare_ready_release_no_start_or_secret(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            root=pathlib.Path(td);r=Runner();h=self.fixture(root,journal,r);p=h.prepare();self.assertEqual(h.ready(),p)
            self.assertEqual(p.stat().st_mode&0o777,0o500);self.assertIsNone(r.item)
            self.assertFalse(any('start'in x or 'run'in x or 'pull'in x for x in r.calls))
            h.release();self.assertFalse(p.exists());h.release()
            with self.assertRaises(ValueError):h.resume()
    def test_lost_create_copy_remove_ack_fresh_resume(self):
        for cmd in ['create','cp','rm']:
            with self.subTest(cmd=cmd),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                root=pathlib.Path(td);r=Runner();h=self.fixture(root,journal,r);r.loss=cmd
                with self.assertRaises(Loss):h.prepare()
                fresh=m.HostTool(journal,IMAGE,lambda:None,r);p=fresh.resume();self.assertEqual(fresh.ready(),p);self.assertIsNone(r.item);fresh.release()
    def test_binary_changed_symlink_hardlink_mode_refuse(self):
        for damage in ['bytes','symlink','hardlink','mode']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                root=pathlib.Path(td);h=self.fixture(root,journal);p=h.prepare()
                if damage=='bytes':p.chmod(0o700);p.write_bytes(b'changed');p.chmod(0o500)
                if damage=='symlink':p.unlink();p.symlink_to(root/'unrelated')
                if damage=='hardlink':os.link(p,root/'alias')
                if damage=='mode':p.chmod(0o555)
                with self.assertRaises((ValueError,OSError)):h.ready()
                with self.assertRaises((ValueError,OSError)):h.release()
    def test_wrong_source_and_scope_refuse_without_adopting(self):
        for damage in ['image','running','network','scope']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                root=pathlib.Path(td);r=Runner();h=self.fixture(root,journal,r);r.loss='create'
                with self.assertRaises(Loss):h.prepare()
                if damage=='image':r.item['Image']='sha256:'+'c'*64
                if damage=='running':r.item['State']['Status']='running'
                if damage=='network':r.item['HostConfig']['NetworkMode']='host'
                if damage=='scope':m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'scope':'transport_only'})
                with self.assertRaises(ValueError):m.HostTool(journal,IMAGE,lambda:None,r).resume()
    def test_replaced_exact_image_source_after_copy_refused(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            root=pathlib.Path(td);r=Runner();h=self.fixture(root,journal,r)
            original=h.binary
            def lost(v):raise Loss('synthetic loss after copy stage')
            h.binary=lost
            with self.assertRaises(Loss):h.prepare()
            self.assertEqual(h.record()['stage'],'copying')
            r.item['Id']='c'*64
            with self.assertRaises(ValueError):m.HostTool(journal,IMAGE,lambda:None,r).resume()
    def test_partial_host_write_refuses_without_overwrite(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            root=pathlib.Path(td);r=Runner();h=self.fixture(root,journal,r);r.loss='rm'
            with self.assertRaises(Loss):h.prepare()
            v=h.record();v['stage']='copying';m.state.atomic(h.path,v)
            r.item={'Id':v['cid'],'Name':'/'+v['source'],'Image':IMAGE,'State':{'Status':'created'},'HostConfig':{'NetworkMode':'none','Privileged':False},'Mounts':[],'Config':{'Entrypoint':['/bin/true'],'Labels':{'hexthings.hostprobe':v['source']}}}
            p=pathlib.Path(v['binary']);p.chmod(0o700);p.write_bytes(b'partial');p.chmod(0o500)
            with self.assertRaises(ValueError):m.HostTool(journal,IMAGE,lambda:None,r).resume()
            self.assertEqual(p.read_bytes(),b'partial')
    def test_archive_wrong_arch_path_link_duplicate_refused(self):
        for damage in ['arch','path','link','duplicate','dynamic','interpreter','headers','segment']:
            stream=io.BytesIO();raw=elf()
            if damage=='arch':raw[18:20]=b'\xb7\x00'
            if damage=='dynamic':struct.pack_into('<I',raw,64,2)
            if damage=='interpreter':struct.pack_into('<I',raw,64,3)
            if damage=='headers':struct.pack_into('<Q',raw,32,10000)
            if damage=='segment':struct.pack_into('<Q',raw,96,10000)
            with tarfile.open(fileobj=stream,mode='w')as tar:
                info=tarfile.TarInfo('../setupctl'if damage=='path'else'setupctl');info.size=len(raw)
                if damage=='link':info.type=tarfile.SYMTYPE;info.linkname='/unrelated';info.size=0
                tar.addfile(info,io.BytesIO(raw))
                if damage=='duplicate':tar.addfile(info,io.BytesIO(raw))
            with self.assertRaises(ValueError):m.HostTool.unpack(stream.getvalue())
if __name__=='__main__':unittest.main()
