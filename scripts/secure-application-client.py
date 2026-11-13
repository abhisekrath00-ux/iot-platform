#!/usr/bin/env python3
"""Read-only authenticated retained-list transport, never activation authority.

Existing session material stays in a private caller-supplied file. No login,
token creation/renewal, proxy, redirect, cookie store, write endpoint or CLI.
TLS uses explicit CA trust and hostname verification. Numeric loopback HTTP is
available only by explicit opt-in for an already-local old API. Live unrestricted
admin identity is required before and after each complete inventory, not decoded
JWT claims. Full runtime/behavior compatibility remains a separate verifier.
"""
import http.client
import importlib.util
import ipaddress
import json
import os
import pathlib
import ssl
import socket
import threading
import stat
import time
from urllib.parse import urlsplit

spec=importlib.util.spec_from_file_location('retained_reads',pathlib.Path(__file__).with_name('secure-application-reads.py'))
reads=importlib.util.module_from_spec(spec);spec.loader.exec_module(reads)


def unique_object(pairs):
    value={}
    for key,item in pairs:
        if key in value:raise ValueError('Duplicate response JSON field.')
        value[key]=item
    return value


def session_file(path):
    # O_NOFOLLOW plus fstat avoids a final-path symlink/check-read race. The
    # caller owns the private parent directory and its access boundary.
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,'rb')as file:
        info=os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode)or info.st_mode&0o077 or info.st_uid!=os.geteuid()or info.st_nlink!=1:
            raise ValueError('Existing session requires an owned single-link private regular file.')
        raw=file.read(16385)
    if not raw or len(raw)>16384:raise ValueError('Invalid existing private session file.')
    try:value=raw.decode('ascii').strip()
    except UnicodeError:raise ValueError('Invalid existing private session file.')
    if not value or any(ord(c)<33 or ord(c)>126 for c in value):raise ValueError('Invalid existing private session file.')
    return value


class Client:
    def __init__(self,origin,session,tenant,user,ca_file=None,allow_loopback_http=False,timeout=5,max_bytes=4*1024*1024):
        parsed=urlsplit(origin)
        if (parsed.scheme not in {'http','https'}or not parsed.hostname or parsed.username is not None
                or parsed.password is not None or parsed.path not in {'','/'}or parsed.query or parsed.fragment
                or any(c.isspace()for c in origin)or '\\'in origin):
            raise ValueError('One exact API origin without credentials/path/query required.')
        try:port=parsed.port
        except ValueError:raise ValueError('Invalid API port.')
        if port is not None and not 1<=port<=65535:raise ValueError('Invalid API port.')
        if not isinstance(tenant,str)or not tenant or not isinstance(user,str)or not user:
            raise ValueError('Exact expected tenant/user binding required.')
        if type(timeout)not in {int,float}or not 0<timeout<=30 or type(max_bytes)is not int or not 1024<=max_bytes<=16*1024*1024:
            raise ValueError('Bounded request time and response size required.')
        self.context=None
        if parsed.scheme=='https':
            if ca_file is None:raise ValueError('Explicit existing HTTPS CA file required.')
            self.context=ssl.create_default_context(cafile=str(ca_file))
            self.context.minimum_version=ssl.TLSVersion.TLSv1_2
        else:
            try:local=ipaddress.ip_address(parsed.hostname).is_loopback
            except ValueError:local=False
            if allow_loopback_http is not True or not local or ca_file is not None:
                raise ValueError('Plain HTTP requires explicitly selected numeric loopback, never LAN/DNS fallback.')
        self.host,self.port,self.scheme=parsed.hostname,port,parsed.scheme
        self.session,self.tenant,self.user=pathlib.Path(session),tenant,user
        self.timeout,self.max_bytes=timeout,max_bytes

    def get(self,path):
        if path not in {*reads.PATHS,'/v1/me'}:raise ValueError('Only fixed read-only acceptance endpoints supported.')
        token=session_file(self.session)
        connection=(http.client.HTTPSConnection(self.host,self.port,timeout=self.timeout,context=self.context)
                    if self.scheme=='https'else http.client.HTTPConnection(self.host,self.port,timeout=self.timeout))
        started=time.monotonic();timer=None;sock=None
        try:
            connection.request('GET',path,headers={'Authorization':'Bearer '+token,'Accept':'application/json','Accept-Encoding':'identity','Cache-Control':'no-cache','Connection':'close'})
            # HTTP/1.0 getresponse may close/detach the borrowed socket object
            # while its response file is alive. Own a raw duplicate for timeout
            # and deadline shutdown only, never TLS reads or credential writes.
            sock=socket.socket(fileno=os.dup(connection.sock.fileno()))
            def expire():
                try:sock.shutdown(socket.SHUT_RDWR)
                except OSError:pass
            remaining=self.timeout-(time.monotonic()-started)
            if remaining<=0:raise ValueError('Authenticated read deadline exceeded.')
            timer=threading.Timer(remaining,expire);timer.daemon=True;timer.start()
            response=connection.getresponse()
            if response.status!=200:raise ValueError('Authenticated read refused; no retained-data acceptance.')
            if response.getheader('Content-Encoding','identity').lower()!='identity':raise ValueError('Encoded response not accepted.')
            if response.getheader('Content-Type','').split(';')[0].strip().lower()!='application/json':raise ValueError('JSON response required.')
            lengths=response.headers.get_all('Content-Length',[])
            if len(lengths)>1 or lengths and (not lengths[0].isdigit()or int(lengths[0])>self.max_bytes):raise ValueError('Invalid/oversized response length.')
            raw=bytearray()
            while True:
                remaining=self.timeout-(time.monotonic()-started)
                if remaining<=0:raise ValueError('Authenticated read deadline exceeded.')
                sock.settimeout(remaining)
                piece=response.read1(min(65536,self.max_bytes+1-len(raw)))
                if not piece:break
                raw.extend(piece)
                if len(raw)>self.max_bytes:raise ValueError('Oversized authenticated response.')
            if lengths and len(raw)!=int(lengths[0]):raise ValueError('Incomplete authenticated response.')
            return json.loads(raw.decode('utf-8'),object_pairs_hook=unique_object,parse_constant=lambda _:(_ for _ in ()).throw(ValueError('Non-finite response JSON.')))
        except (OSError,http.client.HTTPException,UnicodeError,json.JSONDecodeError)as error:
            # Do not expose upstream bodies/URLs/session material in exceptions.
            raise ValueError('Authenticated API transport failed; no acceptance.')from None
        finally:
            if timer:timer.cancel()
            connection.close()
            if sock:sock.close()

    def identity(self):
        value=self.get('/v1/me')
        if (not isinstance(value,dict)or value.get('tenant_id')!=self.tenant or value.get('user_id')!=self.user
                or value.get('role')!='admin'or value.get('via_key')is not False
                or value.get('must_change_credentials',False)is not False
                or any(key in value for key in ['customer_id','customer_name','denied'])):
            raise ValueError('Live unrestricted expected administrator identity required; token claims are not proof.')
        return {key:value[key]for key in ['tenant_id','user_id','role','via_key']}

    def snapshot(self):
        identity=self.identity()
        records={path:self.get(path)for path in reads.PATHS}
        if self.identity()!=identity:raise ValueError('Identity changed during retained inventory.')
        return reads.snapshot(records)

    def compare(self,before):
        after=self.snapshot()
        return reads.compare(before,{path:list(rows.values())for path,rows in after['records'].items()})
