#!/usr/bin/env python3
import copy
import importlib.util
import pathlib
import unittest
from types import SimpleNamespace
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('connections',HERE/'secure-application-connections.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
ORIGINAL=b'local all all trust\nhost all all 0.0.0.0/0 scram-sha-256\n'
IMAGE='sha256:'+'b'*64
OUT={'container':'d'*64,'image':IMAGE,'started':'webstart','project':'fixture','service':'web','network':'fixture_default','network_id':'e'*64,'address':'172.20.0.3'}
REQUEST={'version':1,'transport':'private_tcp_plain','host':'172.20.0.2','port':5432,'database':'app','user':'ci','password':'synthetic-only'}
class Tests(unittest.TestCase):
    def fixture(self):
        live=[ORIGINAL];phase=['original'];events=[];fail=[None];after=[None];items={};values=[]
        for service,cid,address in [('postgres','a'*64,'172.20.0.2'),('web','d'*64,'172.20.0.3'),('api','1'*64,'172.20.0.4'),('ingest','2'*64,'172.20.0.5'),('mcp','3'*64,'172.20.0.6')]:
            items[cid]={'Id':cid,'Image':IMAGE,'State':{'StartedAt':'pgstart'if service=='postgres'else'webstart'if service=='web'else service+'start','Status':'running'},
                'Config':{'Labels':{'com.docker.compose.service':service,'com.docker.compose.project':'fixture'}},
                'NetworkSettings':{'Networks':{'fixture_default':{'NetworkID':'e'*64,'IPAddress':address}}}}
            if service in {'api','ingest','mcp'}:values.append({'container':cid,'network':'fixture_default','address':address,'user':'ci','ssl':False})
        def guard(want,raw):
            events.append(('guard',want))
            if want!=phase[0]or raw!=live[0]or fail[0]=='guard':raise ValueError('Transition changed')
        def docker(args):return [copy.deepcopy(items[args[1]])]
        catalog=SimpleNamespace(container='a'*64,database='app',user='ci',sql=lambda _:dict(port='5432'))
        host=SimpleNamespace(binding={'image':IMAGE,'started':'pgstart'},docker=docker,server=lambda:dict(bytes=live[0].hex()),verify_peers=lambda _:None)
        definitions={'services':{name:{'image':IMAGE}for name in ['api','ingest','mcp']}}
        p=SimpleNamespace(connection_guard=guard,catalog=catalog,host=host,target=SimpleNamespace(project='fixture'),
                          adapter=SimpleNamespace(old=definitions,new=copy.deepcopy(definitions)),record=lambda:dict(peers=values))
        def local():
            events.append(('local',phase[0]))
            return {'version':1,'status':'database_closed'}if phase[0]=='exclusive'else{'version':1,'status':'connected','database':'app','user':'ci'}
        class NS:
            def __init__(self,tool,expected,check):
                self.expected=expected;self.guard=check
                self.SERVICES=frozenset({'web'})if expected['service']=='web'else frozenset({'api','ingest','mcp'})
            def check(self):
                self.guard();item=items[self.expected['container']];net=item['NetworkSettings']['Networks']['fixture_default']
                if item['Image']!=self.expected['image']or item['State']['StartedAt']!=self.expected['started']or net['IPAddress']!=self.expected['address']:
                    raise ValueError('Exact namespace changed')
            def __enter__(self):self.check();return self
            def __exit__(self,*args):pass
            def probe(self,request):
                self.check();service=self.expected['service'];events.append(('probe',service))
                status='hba_denied'if service=='web'and phase[0]!='original'else'connected'
                if fail[0]in {service,'all'}:status='authentication_failed'
                if after[0]:after[0](service)
                self.check();return {'version':1,'status':status}
        c=m.Connections(p,object(),OUT,REQUEST,ORIGINAL,local,NS,NS)
        return c,p,live,phase,events,fail,after,items,values
    def test_exact_original_restricted_three_writers_and_exclusive(self):
        c,p,live,phase,events,fail,after,items,values=self.fixture()
        c('original',[],ORIGINAL)
        peers=[{k:v[k]for k in ['address','user','ssl']}for v in values]
        phase[0]='restricted';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',peers);c('restricted',peers,live[0])
        self.assertEqual([x for x in events if x[0]=='probe'],[('probe','web'),('probe','web'),('probe','api'),('probe','ingest'),('probe','mcp')])
        values.clear();phase[0]='exclusive';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',[]);c('exclusive',[],live[0]);self.assertIsNone(c.context)
    def test_narrow_outside_and_writer_failures_refuse_and_context_clears(self):
        for service in ['web','api','ingest','mcp']:
            c,p,live,phase,events,fail,after,items,values=self.fixture();peers=[{k:v[k]for k in ['address','user','ssl']}for v in values]
            phase[0]='restricted';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',peers);fail[0]=service
            with self.subTest(service=service),self.assertRaises(ValueError):c('restricted',peers,live[0])
            self.assertIsNone(c.context)
    def test_changed_target_port_peer_inventory_and_unreviewed_image_refuse(self):
        for damage in ['port','target','network','image','service','missing','tls','tuples','outside']:
            c,p,live,phase,events,fail,after,items,values=self.fixture();peers=[{k:v[k]for k in ['address','user','ssl']}for v in values]
            phase[0]='restricted';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',peers)
            if damage=='port':p.catalog.sql=lambda _:dict(port='6432')
            if damage=='target':items['a'*64]['Image']='sha256:'+'f'*64
            if damage=='network':items['1'*64]['NetworkSettings']['Networks']['fixture_default']['NetworkID']='f'*64
            if damage=='image':items['1'*64]['Image']='sha256:'+'f'*64
            if damage=='service':items['1'*64]['Config']['Labels']['com.docker.compose.service']='web'
            if damage=='missing':values.pop()
            if damage=='tls':values[0]['ssl']=True
            if damage=='tuples':peers[0]['user']='other'
            if damage=='outside':values[0]['container']='d'*64
            with self.subTest(damage=damage),self.assertRaises(ValueError):c('restricted',peers,live[0])
            self.assertFalse(any(e[0]=='probe'for e in events))
    def test_changed_source_after_proof_and_recursive_callback_refuse(self):
        for damage in ['drift','recursion']:
            c,p,live,phase,events,fail,after,items,values=self.fixture();peers=[{k:v[k]for k in ['address','user','ssl']}for v in values]
            phase[0]='restricted';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',peers)
            def changed(service):
                if service!='mcp':return
                if damage=='recursion':c('restricted',peers,live[0])
                else:items['1'*64]['State']['StartedAt']='newstart'
            after[0]=changed
            with self.subTest(damage=damage),self.assertRaises(ValueError):c('restricted',peers,live[0])
            self.assertIsNone(c.context)
    def test_local_failure_and_unbound_phase_guard_refuse(self):
        c,p,live,phase,events,fail,after,items,values=self.fixture()
        with self.assertRaises(ValueError):c.guard()
        c.local_probe=lambda:dict(version=1,status='hba_denied')
        with self.assertRaises(ValueError):c('original',[],ORIGINAL)
        self.assertFalse(any(e[0]=='probe'for e in events))
    def test_exclusive_requires_no_admitted_writers(self):
        c,p,live,phase,events,fail,after,items,values=self.fixture();phase[0]='exclusive';live[0]=m.outside.hba.render(ORIGINAL,'app','ci',[])
        with self.assertRaises(ValueError):c('exclusive',[],live[0])
if __name__=='__main__':unittest.main()
