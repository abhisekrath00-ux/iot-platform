#!/usr/bin/env python3
"""Disposable native fixture only. Not a production acceptance mutation probe.

The fixture provides loopback transport bound to its own API, fixed synthetic
principals and fresh database. Evidence is limited to profile CRUD-create and
HTTP telemetry behavior. No full background/production compatibility claim.
"""
import copy

TENANT='write-tenant'
DEVICE='write-device'
POINT='write-voltage'

class Writes:
    def __init__(self):
        self.profiles={}
        self.events=[]
        self.stages=[]

    def prove(self,request,stage):
        if stage in self.stages or not isinstance(stage,str) or not stage:
            raise ValueError('Unique synthetic write proof stage required.')
        def req(method,path,body=None,role='admin',tenant=TENANT,status=200):
            code,data=request(method,path,body,role,tenant)
            if code!=status:raise ValueError('Synthetic write proof status mismatch: '+method+' '+path+' '+str(code)+' expected '+str(status))
            return data
        def count(tenant=TENANT):
            value=req('GET','/v1/telemetry/count?device_id='+DEVICE,tenant=tenant)
            if not isinstance(value,dict)or type(value.get('count'))is not int:raise ValueError('Missing exact telemetry count.')
            return value['count']
        def profiles():
            rows=req('GET','/v1/profiles')
            if not isinstance(rows,list)or len(rows)!=len(self.profiles):raise ValueError('Synthetic profile inventory changed.')
            found={}
            for row in rows:
                if not isinstance(row,dict)or row.get('id')in found:raise ValueError('Invalid/duplicate synthetic profile.')
                found[row['id']]={key:row.get(key)for key in ['name','driver_profile','points']}
                if row.get('connection_defaults',{})!={}:raise ValueError('Unexpected synthetic profile defaults.')
            if found!=self.profiles:raise ValueError('Synthetic profile not retained across runtime transitions.')
        profiles()
        if count()!=len(self.events):raise ValueError('Prior synthetic event retention failed.')
        payload={'name':'Synthetic '+stage,'driver_profile':'modbus-generic','points':[{'id':'volts','register':42,'type':'u16','scale':1,'min':0,'max':500}]}
        # Denials must not leave a profile or telemetry row behind.
        req('POST','/v1/profiles',payload,role='viewer',status=403)
        req('POST','/v1/profiles',payload,role=None,status=401)
        profiles()
        created=req('POST','/v1/profiles',payload,status=201)
        if not isinstance(created,dict)or set(created)!={'id'}or not isinstance(created['id'],str)or not created['id']or created['id']in self.profiles:
            raise ValueError('Missing unique created profile id.')
        self.profiles[created['id']]=copy.deepcopy(payload)
        profiles()
        body={'device_id':DEVICE,'readings':[{'point':POINT,'value':230+len(self.events),'event_id':'synthetic-'+stage}]}
        req('POST','/v1/telemetry/ingest',body,role='viewer',status=403)
        req('POST','/v1/telemetry/ingest',body,role=None,status=401)
        req('POST','/v1/telemetry/ingest',body,tenant='retain-tenant',status=404)
        invalid=copy.deepcopy(body);invalid['readings'][0]['value']=501
        rejected=req('POST','/v1/telemetry/ingest',invalid,status=422)
        if rejected!={'accepted':0,'rejected':[{'index':0,'reason':'out of range'}]}:raise ValueError('Invalid reading was not precisely refused.')
        if count()!=len(self.events):raise ValueError('Denied/rejected write changed row count.')
        for attempt in range(2):
            accepted=req('POST','/v1/telemetry/ingest',body)
            if accepted!={'accepted':1,'rejected':[]}:raise ValueError('Synthetic telemetry/retry not accepted.')
            if count()!=len(self.events)+1:raise ValueError('Synthetic event retry was not deduplicated.')
        self.events.append(body['readings'][0])
        rows=req('GET','/v1/telemetry/latest?device_id='+DEVICE)
        if len(rows)!=1 or rows[0].get('point_id')!=POINT or rows[0].get('value')!=self.events[-1]['value']or rows[0].get('unit')!='V'or rows[0].get('quality')!='measured':
            raise ValueError('Latest synthetic reading failed exact readback.')
        if count('retain-tenant')!=0 or req('GET','/v1/telemetry/latest?device_id='+DEVICE,tenant='retain-tenant')!=[]:
            raise ValueError('Cross-tenant synthetic telemetry leaked.')
        self.stages.append(stage)
        return {'scope':'synthetic_profile_create_http_telemetry','stage':stage,'profiles':len(self.profiles),'events':len(self.events),'full_compatibility':False,'runtime_accepted':False}
