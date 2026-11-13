#!/usr/bin/env python3
import importlib.util
import pathlib
import unittest
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('outside',HERE/'secure-postgres-outside.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
ORIGINAL=b'local all all trust\nhost all all 0.0.0.0/0 scram-sha-256\n'
REQUEST={'version':1,'transport':'private_tcp_plain','host':'172.20.0.2','port':5432,'database':'fixture','user':'ci','password':'synthetic-only'}
PEERS=[{'address':'172.20.0.4','user':'ci','ssl':False}]
class Namespace:
    SERVICES=frozenset({'web'})
    expected={'service':'web','container':'a'*64,'address':'172.20.0.3'}
    def __init__(self):self.status='connected';self.alive=True;self.after=None;self.calls=[]
    def check(self):
        if not self.alive:raise ValueError('Outside process changed')
    def probe(self,request):
        self.calls.append(request)
        if self.after:self.after()
        return {'version':1,'status':self.status}
class Tests(unittest.TestCase):
    def fixture(self):
        ns=Namespace();live=[ORIGINAL];allowed=[True]
        def guard():
            if not allowed[0]:raise ValueError('Scope changed')
        return m.OutsideProof(ns,REQUEST,ORIGINAL,lambda:live[0],guard),ns,live,allowed
    def test_same_source_original_restricted_original_no_secret_evidence(self):
        p,ns,live,allowed=self.fixture()
        proof=p.prove('original',[],ORIGINAL);self.assertEqual(proof['status'],'connected');self.assertNotIn(REQUEST['password'],str(proof))
        raw=m.hba.render(ORIGINAL,'fixture','ci',PEERS);live[0]=raw;ns.status='hba_denied'
        self.assertEqual(p.prove('restricted',PEERS,raw)['status'],'hba_denied')
        live[0]=ORIGINAL;ns.status='connected';self.assertEqual(p.prove('original',[],ORIGINAL)['source'],'172.20.0.3')
        self.assertEqual(len(ns.calls),3)
    def test_non_hba_failures_never_count_as_outside_denial(self):
        for status in ['connected','authentication_failed','database_closed','unverified']:
            p,ns,live,allowed=self.fixture();raw=m.hba.render(ORIGINAL,'fixture','ci',PEERS);live[0]=raw;ns.status=status
            with self.subTest(status=status),self.assertRaises(ValueError):p.prove('restricted',PEERS,raw)
    def test_shadowed_reject_wrong_bytes_and_admitted_source_refuse_before_probe(self):
        for damage in ['shadow','bytes','peer','original','phase']:
            p,ns,live,allowed=self.fixture();peers=list(PEERS);raw=m.hba.render(ORIGINAL,'fixture','ci',peers);phase='restricted'
            if damage=='shadow':raw=b'host all all 0.0.0.0/0 trust\n'+raw
            if damage=='bytes':raw+=b'# unrelated\n'
            if damage=='peer':peers=[{'address':'172.20.0.3','user':'ci','ssl':False}];raw=m.hba.render(ORIGINAL,'fixture','ci',peers)
            if damage=='original':phase='original'
            if damage=='phase':phase='exclusive'
            live[0]=raw
            with self.subTest(damage=damage),self.assertRaises(ValueError):p.prove(phase,peers,raw)
            self.assertFalse(ns.calls)
    def test_scope_live_bytes_process_prepost_drift_refuse(self):
        for damage in ['scope','bytes','process','postbytes','postprocess']:
            p,ns,live,allowed=self.fixture()
            if damage=='scope':allowed[0]=False
            if damage=='bytes':live[0]=b'changed'
            if damage=='process':ns.alive=False
            if damage=='postbytes':ns.after=lambda:live.__setitem__(0,b'changed')
            if damage=='postprocess':ns.after=lambda:setattr(ns,'alive',False)
            with self.subTest(damage=damage),self.assertRaises(ValueError):p.prove('original',[],ORIGINAL)
    def test_original_must_be_scram_and_outside_type(self):
        for raw in [ORIGINAL.replace(b'scram-sha-256',b'trust'),ORIGINAL.replace(b'scram-sha-256',b'reject')]:
            with self.assertRaises(ValueError):m.OutsideProof(Namespace(),REQUEST,raw,lambda:raw,lambda:None)
        ns=Namespace();ns.SERVICES=frozenset({'api','ingest','mcp'})
        with self.assertRaises(ValueError):m.OutsideProof(ns,REQUEST,ORIGINAL,lambda:ORIGINAL,lambda:None)
if __name__=='__main__':unittest.main()
