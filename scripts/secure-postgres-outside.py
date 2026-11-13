#!/usr/bin/env python3
"""Fresh outside-negative evidence from an explicit existing pinned web source.
No sidecar, host-as-outsider assumption, HBA mutation or permission grant.
An HBA denial is not evidence that datallowconn is false; catalog proves that.
"""
import importlib.util
import ipaddress
import pathlib
HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
hba=load('secure-postgres-hba')

class OutsideProof:
    def __init__(self,namespace,request,original,read_live,guard):
        if (getattr(namespace,'SERVICES',None)!=frozenset({'web'})or namespace.expected.get('service')!='web'
                or not callable(read_live)or not callable(guard)or not isinstance(request,dict)
                or set(request)!={'version','transport','host','port','database','user','password'}
                or type(request.get('version'))is not int or request.get('version')!=1 or request.get('transport')!='private_tcp_plain'):
            raise ValueError('Explicit pinned outside web source and exact private TCP request required.')
        hba.name(request['database']);hba.name(request['user'])
        self.namespace,self.request=namespace,dict(request)
        self.original,self.read_live,self.guard=original,read_live,guard
        self.source=ipaddress.IPv4Address(namespace.expected['address'])
        first=self.rule(original)
        if not first or first['method']!='scram-sha-256':raise ValueError('Outside source must already match original SCRAM, not trust or assumed refusal.')
    def rule(self,raw):
        return next((r for r in hba.rules(raw)if hba.matches(r,self.request['database'],self.request['user'],self.source,False)),None)
    def check(self,expected):
        self.namespace.check()
        if self.guard()is not None or self.read_live()!=expected:raise ValueError('Exact live PostgreSQL/HBA proof binding changed.')
        self.namespace.check()
    def prove(self,phase,peers,raw):
        if phase not in {'original','restricted'}or not isinstance(peers,list):raise ValueError('Explicit original or restricted outside proof required.')
        if phase=='original':
            if peers or raw!=self.original:raise ValueError('Exact original outside admission required.')
            want='connected'
        else:
            for p in peers:
                if not isinstance(p,dict)or set(p)!={'address','user','ssl'}:raise ValueError('Exact admitted peer tuples required.')
                if ipaddress.ip_address(p['address'])==self.source:raise ValueError('Outside source is in admitted peer inventory.')
            # Binding to rendered bytes, not trusting a phase flag or a distant
            # reject line that a prior permissive first-match rule shadows.
            if raw!=hba.render(self.original,self.request['database'],self.request['user'],peers):
                raise ValueError('Exact restricted HBA bytes required for outside proof.')
            first=self.rule(raw)
            if not first or first['method']!='reject':raise ValueError('Outside source is not first-match HBA rejected.')
            want='hba_denied'
        self.check(raw);result=self.namespace.probe(dict(self.request));self.check(raw)
        if result.get('status')!=want:raise ValueError('Outside endpoint did not produce the exact required connection outcome.')
        # Namespace validates all connected source/target fields; never persist
        # request/password or raw PostgreSQL errors in proof evidence.
        return {'version':1,'phase':phase,'source':str(self.source),'status':want,
                'container':self.namespace.expected['container'],'hba_sha256':hba.digest(raw)}
