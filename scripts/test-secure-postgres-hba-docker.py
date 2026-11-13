#!/usr/bin/env python3
"""Docker/volume/peer guards, without an owner Docker daemon."""
import copy
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('hbadocker',HERE/'secure-postgres-hba-docker.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
BASE=b'local all all trust\nhost all all all scram-sha-256\n'

class Catalog:
    container='a'*64; user='admin';database='application'
    def __init__(self,path):self.path=path;self.loaded='one';self.serverpath='/var/lib/postgresql/data/pg_hba.conf';self.errors=0
    def sql(self,sql):
        if 'pg_reload_conf()' in sql:self.loaded+='x';return {'reload':True}
        return {'path':self.serverpath,'bytes':self.path.read_bytes().hex(),'loaded':self.loaded,'errors':self.errors}

class Tests(unittest.TestCase):
    def fixture(self,root):
        path=root/'pg_hba.conf';path.write_bytes(BASE);path.chmod(0o600);catalog=Catalog(path)
        mount={'Type':'volume','Name':'fixture','Driver':'local','Source':str(root),'Destination':'/var/lib/postgresql/data','RW':True}
        network={'IPAddress':'172.28.0.2','NetworkID':'network-id'}
        pg={'Id':'a'*64,'Image':'sha256:'+'b'*64,'Config':{'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':'postgres'}},
            'HostConfig':{'Privileged':False,'NetworkMode':'fixture_default','PidMode':'','CapAdd':None,'Devices':[]},
            'State':{'Status':'running','Paused':False,'Restarting':False,'StartedAt':'started'},'Mounts':[mount],
            'NetworkSettings':{'Networks':{'fixture_default':network}}}
        peer=copy.deepcopy(pg);peer['Id']='c'*64;peer['Config']['Labels']['com.docker.compose.service']='api'
        peer['NetworkSettings']['Networks']['fixture_default']['IPAddress']='172.28.0.3'
        def run(command,**kwargs):
            self.assertEqual(command[:2],['docker','inspect']);self.assertNotIn('shell',kwargs)
            return subprocess.CompletedProcess(command,0,json.dumps([pg if cid==catalog.container else peer for cid in command[2:]]),'')
        host=m.DockerHost(catalog,'fixture',run)
        binding={'container':peer['Id'],'network':'fixture_default','address':'172.28.0.3','user':'application','ssl':False}
        return host,catalog,pg,peer,binding
    def test_binding_rechecks_container_volume_and_server_bytes(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);host,catalog,pg,peer,binding=self.fixture(root)
            self.assertEqual(host.bind(),root/'pg_hba.conf');host.check();host.verify_peers([binding])
            observed=[];host.reload_and_verify(BASE,lambda expected:observed.append(expected))
            self.assertEqual(observed,[BASE])
            with self.assertRaises(ValueError):host.bind()
            with self.assertRaises(ValueError):host.reload_and_verify(BASE,lambda _:True)
            with self.assertRaises(ValueError):host.reload_and_verify(b'wrong',lambda _:None)
    def test_peer_snapshot_is_fresh_batched_complete_and_never_reused(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);host,catalog,pg,peer,binding=self.fixture(root);host.bind()
            original=host.run;calls=[]
            def observed(command,**kwargs):
                calls.append(command);return original(command,**kwargs)
            host.run=observed
            host.verify_peers([binding]);host.verify_peers([binding])
            grouped=[call for call in calls if len(call)>3]
            self.assertEqual(grouped,[['docker','inspect',catalog.container,peer['Id']]]*2)
            def missing(command,**kwargs):
                result=original(command,**kwargs)
                if len(command)>3:result.stdout=json.dumps([pg])
                return result
            host.run=missing
            with self.assertRaises(ValueError):host.verify_peers([binding])
            host.run=original
            with self.assertRaises(ValueError):host.verify_peers([binding,binding])

    def test_unsupported_initial_identity_and_volume_shapes_refused(self):
        for changed in ['container','image','paused','restart','privileged','hostnet','hostpid','cap','device','project','service','mounttype','driver','rw','ambiguous','path','errors','bytes']:
            with self.subTest(changed=changed),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);host,catalog,pg,peer,binding=self.fixture(root)
                if changed=='container':pg['Id']='d'*64
                if changed=='image':pg['Image']='postgres:latest'
                if changed=='paused':pg['State']['Paused']=True
                if changed=='restart':pg['State']['Restarting']=True
                if changed=='privileged':pg['HostConfig']['Privileged']=True
                if changed=='hostnet':pg['HostConfig']['NetworkMode']='host'
                if changed=='hostpid':pg['HostConfig']['PidMode']='container:other'
                if changed=='cap':pg['HostConfig']['CapAdd']=['SYS_ADMIN']
                if changed=='device':pg['HostConfig']['Devices']=[{}]
                if changed=='project':pg['Config']['Labels']['com.docker.compose.project']='other'
                if changed=='service':pg['Config']['Labels']['com.docker.compose.service']='api'
                if changed=='mounttype':pg['Mounts'][0]['Type']='bind'
                if changed=='driver':pg['Mounts'][0]['Driver']='remote'
                if changed=='rw':pg['Mounts'][0]['RW']=False
                if changed=='ambiguous':pg['Mounts'].append(copy.deepcopy(pg['Mounts'][0]))
                if changed=='path':catalog.serverpath='/var/lib/postgresql/data/../pg_hba.conf'
                if changed=='errors':catalog.errors=1
                if changed=='bytes':
                    original=catalog.sql
                    def wrong(sql):
                        result=original(sql);result['bytes']=b'different'.hex();return result
                    catalog.sql=wrong
                with self.assertRaises(ValueError):host.bind()
    def test_replacement_container_restart_volume_image_or_path_refuses(self):
        for changed in ['start','image','name','source']:
            with self.subTest(changed=changed),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);host,catalog,pg,peer,binding=self.fixture(root);host.bind()
                if changed=='start':pg['State']['StartedAt']='another-start'
                if changed=='image':pg['Image']='sha256:'+'e'*64
                if changed=='name':pg['Mounts'][0]['Name']='another-volume'
                if changed=='source':pg['Mounts'][0]['Source']=str(root/'other')
                with self.assertRaises(ValueError):host.check()
    def test_exact_peer_inventory_no_ip_reuse_unknown_service_privilege_or_network(self):
        for changed in ['id','stopped','privileged','hostnet','service','project','address','networkid','network','shape']:
            with self.subTest(changed=changed),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td);host,catalog,pg,peer,binding=self.fixture(root);host.bind()
                if changed=='id':peer['Id']='d'*64
                if changed=='stopped':peer['State']['Status']='exited'
                if changed=='privileged':peer['HostConfig']['Privileged']=True
                if changed=='hostnet':peer['HostConfig']['NetworkMode']='host'
                if changed=='service':peer['Config']['Labels']['com.docker.compose.service']='unrelated'
                if changed=='project':peer['Config']['Labels']['com.docker.compose.project']='other'
                if changed=='address':peer['NetworkSettings']['Networks']['fixture_default']['IPAddress']='172.28.0.99'
                if changed=='networkid':peer['NetworkSettings']['Networks']['fixture_default']['NetworkID']='other'
                if changed=='network':binding['network']='other'
                if changed=='shape':binding['approval']=True
                with self.assertRaises(ValueError):host.verify_peers([binding])

if __name__=='__main__':unittest.main()
