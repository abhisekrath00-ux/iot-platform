#!/usr/bin/env python3
"""Transition-bound local database admission check, never full network proof.
The exact candidate local operator and transition-safe provider scope must be
supplied. No psql error-text parsing, peer/HBA-denial inference or owner grant.
"""
class LocalAdmission:
    def __init__(self,provider,probe):
        if not callable(getattr(provider,'connection_guard',None))or not callable(probe):
            raise ValueError('Exact transition guard and local candidate connection probe required.')
        self.provider,self.probe=provider,probe
    def __call__(self,phase,raw):
        self.provider.connection_guard(phase,raw)
        result=self.probe()
        self.provider.connection_guard(phase,raw)
        if not isinstance(result,dict)or type(result.get('version'))is not int or result['version']!=1:
            raise ValueError('Exact bounded local admission result required.')
        if phase=='exclusive':
            if result!={'version':1,'status':'database_closed'}:
                raise ValueError('Closed catalog needs an independent exact local connection denial.')
        elif (set(result)!={'version','status','database','user'}or result['status']!='connected'
                or result['database']!=self.provider.catalog.database or result['user']!=self.provider.catalog.user):
            raise ValueError('Exact local maintenance admission not verified.')
