#!/usr/bin/env python3
import copy
import hashlib
import importlib.util
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock
from types import SimpleNamespace
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('namespace',HERE/'secure-postgres-namespace.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
EXPECTED={'container':'a'*64,'image':'sha256:'+'b'*64,'started':'2026-11-13T00:00:00Z','project':'fixture','service':'ingest','network':'fixture_default','network_id':'c'*64,'address':'172.20.0.4'}
REQUEST={'version':1,'transport':'private_tcp_plain','host':'172.20.0.2','port':5432,'database':'fixture','user':'ci','password':'synthetic-only'}
class Tool:
    def __init__(self,root):
        self.path=root/'probe';self.path.write_bytes(b'unit fake executable, never actually run');self.path.chmod(0o500);self.hash=hashlib.sha256(self.path.read_bytes()).hexdigest();self.allowed=True
    def check(self):
        if not self.allowed:raise ValueError('scope changed')
    def ready(self):self.check();return self.path
    def record(self):return {'sha256':self.hash}
class Process:
    def __init__(self,pid,cid):self.pid=pid;self.ns=os.open('/dev/null',os.O_RDONLY);self.alive=True;self.closed=False
    def check(self):
        if not self.alive:raise ValueError('process changed')
    def close(self):os.close(self.ns);self.closed=True
class Runner:
    def __init__(self):
        e=EXPECTED
        self.item={'Id':e['container'],'Image':e['image'],'State':{'StartedAt':e['started'],'Status':'running','Paused':False,'Restarting':False,'Pid':23456},'HostConfig':{'Privileged':False,'NetworkMode':'fixture_default'},'Config':{'Labels':{'com.docker.compose.project':e['project'],'com.docker.compose.service':e['service']}},'NetworkSettings':{'Networks':{e['network']:{'IPAddress':e['address'],'NetworkID':e['network_id']}}}}
        self.outcome={'version':1,'status':'connected','client':e['address'],'server':REQUEST['host'],'database':REQUEST['database'],'user':REQUEST['user']};self.calls=[];self.after=None
    def __call__(self,args,**kwargs):
        self.calls.append((args,kwargs))
        if args[0]=='docker':out=json.dumps([self.item])
        else:
            assert args[:1]==['/usr/bin/nsenter']
            assert args[1].startswith('--net=/proc/self/fd/')and args[2]=='--'and args[3].startswith('/proc/self/fd/')and args[4]=='connection-readiness'and len(args)==5
            assert kwargs['env']=={'PATH':'/usr/bin:/bin','LANG':'C'}and len(kwargs['pass_fds'])==2
            assert kwargs['input']==json.dumps(REQUEST)
            assert REQUEST['password']not in str(args)
            out=json.dumps(self.outcome)
            if self.after:self.after()
        return subprocess.CompletedProcess(args,0,out,'')
class Tests(unittest.TestCase):
    def fixture(self,root):
        tool=Tool(root);runner=Runner();guards=[]
        def guard():guards.append(True)
        return m.Namespace(tool,EXPECTED,guard,runner,Process),runner,tool,guards
    def test_explicit_web_outside_type_never_writer(self):
        with tempfile.TemporaryDirectory()as td:
            tool=Tool(pathlib.Path(td));r=Runner();expected=dict(EXPECTED,service='web')
            r.item['Config']['Labels']['com.docker.compose.service']='web';r.outcome['client']=expected['address']
            with self.assertRaises(ValueError):m.Namespace(tool,expected,lambda:None,r,Process)
            with self.assertRaises(ValueError):m.OutsideNamespace(tool,EXPECTED,lambda:None,r,Process)
            with m.OutsideNamespace(tool,expected,lambda:None,r,Process)as n:
                self.assertEqual(n.probe(REQUEST)['status'],'connected')
    def test_pinned_net_only_host_binary_stdin_clean_env(self):
        with tempfile.TemporaryDirectory()as td:
            n,r,t,g=self.fixture(pathlib.Path(td));n.bind();self.assertEqual(n.probe(REQUEST),r.outcome);self.assertTrue(g)
            with self.assertRaises(ValueError):n.bind()
            process=n.process;n.close();self.assertTrue(process.closed)
            with self.assertRaises(ValueError):n.probe(REQUEST)
    def test_live_container_drift_before_probe_refuses(self):
        for change in ['pid','started','image','address','network','paused','cap','host','pidmode','service','project']:
            with self.subTest(change=change),tempfile.TemporaryDirectory()as td:
                n,r,t,g=self.fixture(pathlib.Path(td));n.bind()
                if change=='pid':r.item['State']['Pid']+=1
                if change=='started':r.item['State']['StartedAt']='new'
                if change=='image':r.item['Image']='sha256:'+'d'*64
                if change=='address':r.item['NetworkSettings']['Networks'][EXPECTED['network']]['IPAddress']='172.20.0.9'
                if change=='network':r.item['NetworkSettings']['Networks'][EXPECTED['network']]['NetworkID']='d'*64
                if change=='paused':r.item['State']['Paused']=True
                if change=='cap':r.item['HostConfig']['CapAdd']=['SYS_ADMIN']
                if change=='host':r.item['HostConfig']['NetworkMode']='host'
                if change=='pidmode':r.item['HostConfig']['PidMode']='container:other'
                if change in {'service','project'}:r.item['Config']['Labels']['com.docker.compose.'+change]='other'
                with self.assertRaises(ValueError):n.probe(REQUEST)
                self.assertFalse(any(args[0]=='/usr/bin/nsenter'for args,_ in r.calls));n.close()
    def test_lost_process_or_admission_and_postprobe_drift_refuse(self):
        for change in ['exit','guard','postpid','postexit']:
            with self.subTest(change=change),tempfile.TemporaryDirectory()as td:
                n,r,t,g=self.fixture(pathlib.Path(td));n.bind()
                if change=='exit':n.process.alive=False
                if change=='guard':t.allowed=False
                if change=='postpid':r.after=lambda:r.item['State'].update(Pid=54321)
                if change=='postexit':r.after=lambda:setattr(n.process,'alive',False)
                with self.assertRaises(ValueError):n.probe(REQUEST)
                n.close()
    def test_exact_response_and_binary_refusals(self):
        for change in ['client','server','user','database','version','extra','mode','hash','symlink','outcome']:
            with self.subTest(change=change),tempfile.TemporaryDirectory()as td:
                n,r,t,g=self.fixture(pathlib.Path(td));n.bind()
                if change in {'client','server','user','database'}:r.outcome[change]='unrelated'
                if change=='version':r.outcome['version']=2
                if change=='extra':r.outcome['private']='unrelated'
                if change=='outcome':r.outcome={'version':1,'status':'other'}
                if change=='mode':t.path.chmod(0o555)
                if change=='hash':t.path.chmod(0o700);t.path.write_bytes(b'changed');t.path.chmod(0o500)
                if change=='symlink':t.path.unlink();t.path.symlink_to('/dev/null')
                with self.assertRaises(ValueError):n.probe(REQUEST)
                n.close()
    def test_narrow_negative_outcomes_and_unverified_not_success(self):
        with tempfile.TemporaryDirectory()as td:
            n,r,t,g=self.fixture(pathlib.Path(td));n.bind()
            for status in ['hba_denied','database_closed','authentication_failed','unverified']:
                r.outcome={'version':1,'status':status};self.assertEqual(n.probe(REQUEST),r.outcome)
            n.close()
    def test_kernel_identity_parser_strict_docker_cgroups_and_start(self):
        cid=EXPECTED['container'];pid=23456;boot='12345678-1234-1234-1234-123456789abc'
        # comm may contain closing parentheses/spaces. Start time is field22.
        raw=str(pid)+' (odd ) name) '+' '.join(['S']+['0']*18+['998877'])
        ns=SimpleNamespace(st_dev=4,st_ino=55);host=SimpleNamespace(st_dev=4,st_ino=66)
        for path in ['/docker/'+cid,'/system.slice/docker-'+cid+'.scope']:
            value=m.LinuxProcess.parse(pid,cid,raw,['0::'+path],ns,host,boot)
            self.assertEqual(value['ticks'],'998877');self.assertEqual(value['namespace'],[4,55])
        for bad in [[],['broken'],['0::/'],['0::/docker/'+cid+'x'],['0::/docker/'+cid,'malformed']]:
            with self.assertRaises(ValueError):m.LinuxProcess.parse(pid,cid,raw,bad,ns,host,boot)
        for bad in [raw.replace('998877','missing'),raw.replace('(odd ) name) S','(odd ) name) Z'),raw.replace(str(pid),'34567',1)]:
            with self.assertRaises(ValueError):m.LinuxProcess.parse(pid,cid,bad,['0::/docker/'+cid],ns,host,boot)
        with self.assertRaises(ValueError):m.LinuxProcess.parse(pid,cid,raw,['0::/docker/'+cid],ns,ns,boot)
    def test_actual_pidfd_and_netfd_lifecycle_with_mocked_identity(self):
        info=os.stat('/proc/self/ns/net')
        identity={'pid':os.getpid(),'ticks':'test','cgroups':['mocked only'],
                  'namespace':[info.st_dev,info.st_ino],'boot':'mocked only'}
        # This isolates real descriptor lifetime and drift detection only.
        # Real Docker/cgroup/network identity remains a native fixture proof.
        with mock.patch.object(m.LinuxProcess,'snapshot',return_value=identity):
            proc=m.LinuxProcess(os.getpid(),'a'*64);proc.check();ns,pidfd=proc.ns,proc.pidfd
            with mock.patch.object(proc,'snapshot',return_value=dict(identity,ticks='changed')):
                with self.assertRaises(ValueError):proc.check()
            proc.close()
            with self.assertRaises(ValueError):proc.check()
            for fd in [ns,pidfd]:
                with self.assertRaises(OSError):os.fstat(fd)
    def test_actual_linux_foreign_process_refused_and_pidfd_closed(self):
        opened=[];real=os.pidfd_open
        def pidfd(pid):
            fd=real(pid);opened.append(fd);return fd
        with mock.patch.object(m.os,'pidfd_open',pidfd):
            with self.assertRaises(ValueError):m.LinuxProcess(os.getpid(),'a'*64)
        self.assertEqual(len(opened),1)
        with self.assertRaises(OSError):os.fstat(opened[0])
    def test_reject_unbound_sources_and_oversize(self):
        with tempfile.TemporaryDirectory()as td:
            n,r,t,g=self.fixture(pathlib.Path(td))
            for address in ['127.0.0.1','0.0.0.0','192.0.2.1','8.8.8.8','224.0.0.1','240.0.0.1']:
                e=dict(EXPECTED,address=address)
                with self.assertRaises(ValueError):m.Namespace(t,e,lambda:None,r,Process)
            n.bind()
            with self.assertRaises(ValueError):n.probe(dict(REQUEST,password='x'*9000))
            n.close()
if __name__=='__main__':unittest.main()
