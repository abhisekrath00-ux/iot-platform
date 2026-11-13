#!/usr/bin/env python3
"""Explicit transition-bound connection callback assembly, no default entry.
Already-staged candidate host/local tools and a reviewed existing outside web
endpoint are required. No discovery, new sidecars, staging cleanup or authority
is supplied here. Caller owns exact tool lifecycle and final release ordering.
"""
import copy
import importlib.util
import pathlib
HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module
net=load('secure-postgres-namespace');outside=load('secure-postgres-outside');local=load('secure-application-local-admission')

class Connections:
    def __init__(self,provider,tool,outside_endpoint,request,original,local_probe,namespace_factory=None,outside_factory=None):
        if (not callable(getattr(provider,'connection_guard',None))or not callable(local_probe)
                or not isinstance(outside_endpoint,dict)or outside_endpoint.get('service')!='web'
                or not isinstance(request,dict)or set(request)!={'version','transport','host','port','database','user','password'}
                or request.get('transport')!='private_tcp_plain'or type(request.get('version'))is not int or request.get('version')!=1
                or request.get('database')!=provider.catalog.database or request.get('user')!=provider.catalog.user
                or outside_endpoint.get('project')!=provider.target.project):
            raise ValueError('Explicit reviewed outside endpoint, exact target and local connection probe required.')
        self.provider,self.tool=provider,tool
        self.endpoint,self.request,self.original=copy.deepcopy(outside_endpoint),dict(request),original
        self.local_probe=local_probe;self.namespace_factory=namespace_factory or net.Namespace;self.outside_factory=outside_factory or net.OutsideNamespace
        self.context=None
    def guard(self):
        if self.context is None:raise ValueError('No active transition proof invocation.')
        phase,raw=self.context;self.provider.connection_guard(phase,raw)
    def live(self):
        self.guard();value=self.provider.host.server()
        try:return bytes.fromhex(value['bytes'])
        except (KeyError,TypeError,ValueError):raise ValueError('Exact live server HBA bytes unavailable.')from None
    def inspect(self,cid):
        self.guard();items=self.provider.host.docker(['inspect',cid]);self.guard()
        if not isinstance(items,list)or len(items)!=1 or items[0].get('Id')!=cid:raise ValueError('One exact source/target container required.')
        return items[0]
    def target(self):
        p=self.provider;pg=self.inspect(p.catalog.container)
        if (pg.get('Image')!=p.host.binding['image']or pg.get('State',{}).get('StartedAt')!=p.host.binding['started']
                or pg.get('State',{}).get('Status')!='running'):raise ValueError('Exact bound PostgreSQL target changed.')
        endpoint=pg.get('NetworkSettings',{}).get('Networks',{}).get(self.endpoint['network'],{})
        if endpoint.get('NetworkID')!=self.endpoint['network_id']or endpoint.get('IPAddress')!=self.request['host']:
            raise ValueError('Explicit outside bridge/server address no longer match bound PostgreSQL.')
        settings=p.catalog.sql("SELECT json_build_object('port',current_setting('port'))")
        if settings!={'port':str(self.request['port'])}:raise ValueError('Explicit PostgreSQL target port changed.')
        return pg
    def writers(self,peers,pg):
        p=self.provider;values=p.record()['peers']
        tuples=[{k:v[k]for k in ['address','user','ssl']}for v in values]
        if tuples!=peers:raise ValueError('Callback tuples differ from exact durable admitted peer inventory.')
        if not peers:return []
        if len(peers)!=3 or any(v['ssl']is not False or v['user']!=p.catalog.user for v in values):
            raise ValueError('Only exact full private-plaintext application writer set supported.')
        p.host.verify_peers(values);result=[];services=set();ids=set()
        for value in values:
            item=self.inspect(value['container']);labels=item.get('Config',{}).get('Labels',{});service=labels.get('com.docker.compose.service')
            if (service not in {'api','ingest','mcp'}or service in services or item['Id']in ids
                    or item['Id']==self.endpoint['container']or value['address']==self.endpoint['address']
                    or labels.get('com.docker.compose.project')!=p.target.project):
                raise ValueError('Exact distinct admitted writer set excluding outside source required.')
            services.add(service);ids.add(item['Id'])
            image=item.get('Image');start=item.get('State',{}).get('StartedAt')
            # A peer CID must also bind the provider's exact immutable previous or
            # candidate runtime definitions, not only a matching Compose label.
            if p.adapter is None or image not in {p.adapter.old['services'][service]['image'],p.adapter.new['services'][service]['image']}:
                raise ValueError('Admitted writer image is not in the reviewed runtime definitions.')
            network=value['network'];endpoint=item.get('NetworkSettings',{}).get('Networks',{}).get(network,{})
            server=pg.get('NetworkSettings',{}).get('Networks',{}).get(network,{})
            if (network!=self.endpoint['network']or endpoint.get('NetworkID')!=self.endpoint['network_id']
                    or endpoint.get('IPAddress')!=value['address']or server.get('IPAddress')!=self.request['host']):
                raise ValueError('Exact admitted writer/source/server bridge required.')
            result.append({'container':item['Id'],'image':image,'started':start,'project':p.target.project,'service':service,
                           'network':network,'network_id':endpoint['NetworkID'],'address':value['address']})
        if services!={'api','ingest','mcp'}:raise ValueError('Complete exact writer set required.')
        return result
    def __call__(self,phase,peers,raw):
        if self.context is not None:raise ValueError('Connection verification cannot recurse or overlap.')
        self.context=(phase,raw)
        try:
            self.guard();pg=self.target()
            if raw==self.original:
                if phase!='original'or peers:raise ValueError('Original connection proof requires exact original HBA without restricted peers.')
                sources=[]
            else:
                if phase not in {'restricted','exclusive'}:raise ValueError('Explicit restricted or exclusive phase required.')
                sources=self.writers(peers,pg)
                if phase=='exclusive'and sources:raise ValueError('Exclusive database cannot have admitted writers.')
            local.LocalAdmission(self.provider,self.local_probe)(phase,raw)
            with self.outside_factory(self.tool,self.endpoint,self.guard)as ns:
                proof=outside.OutsideProof(ns,self.request,self.original,self.live,self.guard)
                proof.prove('original'if phase=='original'else'restricted',peers,raw)
            for source in sources:
                with self.namespace_factory(self.tool,source,self.guard)as ns:
                    if ns.probe(dict(self.request)).get('status')!='connected':
                        raise ValueError('Exact admitted writer did not authenticate from its pinned namespace.')
            fresh_pg=self.target()
            if phase!='original'and self.writers(peers,fresh_pg)!=sources:
                raise ValueError('Exact target or admitted source inventory changed during proof.')
            with self.outside_factory(self.tool,self.endpoint,self.guard)as ns:ns.check()
            self.guard()
        finally:self.context=None
