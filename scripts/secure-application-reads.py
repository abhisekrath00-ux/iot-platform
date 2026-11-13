#!/usr/bin/env python3
"""Limited retained-list read comparison, never authority or full compatibility.

The caller owns authenticated GET transport, tenant binding, endpoint origin,
maintenance, full runtime/behavior acceptance and private snapshot storage.
This module makes no network requests and never accepts a substring as proof.
Only the reviewed new empty connection_defaults field may differ from old API.
"""
import copy
import json

PATHS={'/v1/gateways':500,'/v1/profiles':None,'/v1/devices':1000}
FIELDS={
 '/v1/gateways':{'id','serial','status','kind','site_id'},
 '/v1/profiles':{'id','name','driver_profile','points','created_at','connection_defaults'},
 '/v1/devices':{'id','profile','name','gateway_id','created_at','tags','asset_id','lat_point','lon_point'}}


def snapshot(responses):
    if not isinstance(responses,dict) or set(responses)!=set(PATHS):
        raise ValueError('All supported authenticated retained-list reads required.')
    result={}
    for path,limit in PATHS.items():
        rows=responses[path]
        if not isinstance(rows,list) or (limit and len(rows)>=limit):
            raise ValueError('Incomplete or capped retained-list inventory; no acceptance.')
        found={}
        for raw in rows:
            if not isinstance(raw,dict):raise ValueError('Invalid retained-list row.')
            row=copy.deepcopy(raw)
            if path=='/v1/profiles':row.setdefault('connection_defaults',{})
            if set(row)!=FIELDS[path] or not isinstance(row['id'],str) or not row['id'] or row['id'] in found:
                raise ValueError('Incomplete, unknown or duplicate retained-list record.')
            if path=='/v1/profiles' and (not isinstance(row['points'],list) or not isinstance(row['connection_defaults'],dict)):
                raise ValueError('Invalid retained profile shape.')
            # Roundtrip rejects objects that were not JSON values and prevents
            # retaining mutable references. Non-finite floats are invalid JSON.
            try:found[row['id']]=json.loads(json.dumps(row,allow_nan=False))
            except (TypeError,ValueError):raise ValueError('Invalid retained-list JSON.')
        result[path]=found
    return {'version':1,'scope':'limited_retained_lists','records':result,
            'full_compatibility':False,'runtime_accepted':False}


def compare(before,responses):
    if not isinstance(before,dict) or before.get('version')!=1 or before.get('scope')!='limited_retained_lists' or before.get('full_compatibility') is not False or before.get('runtime_accepted') is not False:
        raise ValueError('Exact limited retained-list snapshot required, not authority.')
    after=snapshot(responses)
    if before!=after:raise ValueError('Retained list identity/source metadata changed; no acceptance.')
    return {'version':1,'scope':'limited_retained_lists','matched':True,
            'full_compatibility':False,'runtime_accepted':False}
