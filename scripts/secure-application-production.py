#!/usr/bin/env python3
"""Explicit production Linux application provider assembly, no CLI or defaults.

Caller supplies reviewed outside web endpoint/connection request and fresh owner
scope, runtime acceptance and previous compatibility. No discovery, credentials,
owner grant or menu activation is added. Exact plan image and original durable
HBA are resolved only after Coordinator binds its held journal.
"""
import copy
import importlib.util
import os
import pathlib
import re

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

base=load('secure-application-provider')
connections=load('secure-application-connections')
hosttools=load('secure-postgres-hosttool')

class Provider(base.Provider):
    def __init__(self,catalog,reviewer,outside_endpoint,request,runner=None):
        if not isinstance(outside_endpoint,dict)or not isinstance(request,dict):
            raise ValueError('Explicit reviewed outside endpoint and connection request required.')
        if (set(request)!={'version','transport','host','port','database','user','password'}
                or type(request.get('version'))is not int or request['version']!=1
                or request.get('transport')!='private_tcp_plain'
                or request.get('database')!=catalog.database or request.get('user')!=catalog.user):
            raise ValueError('Exact bounded local application network request required.')
        # Constructor validation only; no namespace is entered and no tool is run.
        connections.net.OutsideNamespace(None,outside_endpoint,lambda:None)
        self.outside_endpoint=copy.deepcopy(outside_endpoint)
        self.request=copy.deepcopy(request)
        self.image=None
        super().__init__(catalog,reviewer,self.connection_proof,runner)

    def bind_journal(self,journal,target):
        super().bind_journal(journal,target)
        candidate=self.checked_candidate(target)
        image=candidate['services']['api']['image']
        if not isinstance(image,str)or not re.fullmatch(base.state.IMAGE,image):
            raise ValueError('Exact reviewed candidate API image required for static tools.')
        if self.outside_endpoint.get('project')!=target.project:
            raise ValueError('Reviewed outside source belongs to a different project.')
        self.image=image

    def checked_candidate(self,target):
        receipt=pathlib.Path(target.previous).parent/'preflight.json'
        if base.state.digest(receipt)!=target.receipt_sha256:
            raise ValueError('Candidate tools require the exact reviewed receipt.')
        files=base.state.read_private(receipt)['files']
        if target.candidate not in files:
            raise ValueError('Candidate tool definition is absent from the reviewed manifest.')
        base.state.check_manifest(files)
        return base.state.read_private(target.candidate)

    def review(self,target):
        super().review(target)
        if self.checked_candidate(target)['services']['api']['image']!=self.image:
            raise ValueError('Candidate tool image changed after journal binding.')

    def tools(self,guard):
        if self.image is None:raise ValueError('Bind exact held plan before connection tool lifecycle.')
        return (hosttools.HostTool(self.journal,self.image,guard,self.runner),
                base.operators.LocalOperator(self.journal,self.catalog,self.image,guard,self.runner))

    def rollover_released(self,tool,phase,raw):
        # Cleanup may complete before its acknowledgement or Coordinator's marker
        # removal. A fresh settled recovery needs new proofs, not a silent resume
        # of a released generation. Keep its exact record as durable evidence.
        value=tool.record()
        if value['stage']!='released':return
        self.connection_guard(phase,raw)
        final=self.journal.read()
        if (phase!='original'or self.target.action!='recover'
                or final.get('phase')not in {'committed','restored'}):
            raise ValueError('Released tools require explicit settled original-admission recovery.')
        tool.release()  # idempotently proves no released binary remains
        destination=tool.path.with_name('postgres-hosttool-released-'+value['source']+'.json')
        if destination.exists()or destination.is_symlink():
            raise ValueError('Released tool evidence destination already exists.')
        self.connection_guard(phase,raw)
        os.rename(tool.path,destination);base.state.sync_directory(destination.parent)
        self.connection_guard(phase,raw)

    def connection_proof(self,phase,peers,raw):
        guard=lambda:self.connection_guard(phase,raw)
        guard()
        tool,local=self.tools(guard)
        if tool.path.exists()or tool.path.is_symlink():
            self.rollover_released(tool,phase,raw)
        if not tool.path.exists():tool.prepare()
        elif tool.record()['stage']!='ready':tool.resume()
        if local.path.exists()or local.path.is_symlink():
            if local.record()['stage']in {'creating','copying','removed'}:local.recover_staging()
        if not local.path.exists():local.prepare()
        original=bytes.fromhex(self.barrier.read()['original'])
        connections.Connections(self,tool,self.outside_endpoint,self.request,original,local.connection)(phase,peers,raw)
        guard()

    def release(self,ticket,target):
        # Failure in base settlement or connection proof never triggers cleanup.
        super().release(ticket,target)
        raw=self.host.path.read_bytes();guard=lambda:self.connection_guard('original',raw)
        guard();tool,local=self.tools(guard)
        # Local first, host last. Either terminal stage can be inspected by a
        # fresh explicit recovery; nothing is removed as exception cleanup.
        if local.path.exists()or local.path.is_symlink():
            value=local.record()
            if value['stage']in {'creating','copying'}:
                raise ValueError('Unsettled local staging remains after provider release.')
            if value['stage']=='ready':local.cleanup()
            elif value['stage']!='removed':
                raise ValueError('Unknown local staging state after provider release.')
        if tool.path.exists()or tool.path.is_symlink():tool.release()
        guard()
