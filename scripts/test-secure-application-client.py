#!/usr/bin/env python3
"""Real local HTTPS/HTTP transport contract; no owner services or credentials."""
import copy
import http.server
import importlib.util
import json
import os
import pathlib
import ssl
import subprocess
import tempfile
import threading
import time
from unittest import mock
import unittest

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('client',HERE/'secure-application-client.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
spec=importlib.util.spec_from_file_location('fixtures',HERE/'test-secure-application-reads.py');f=importlib.util.module_from_spec(spec);spec.loader.exec_module(f)

class Tests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp=tempfile.TemporaryDirectory();cls.root=pathlib.Path(cls.temp.name)
        subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(cls.root/'key'),'-out',str(cls.root/'ca'),'-days','1','-subj','/CN=synthetic-client','-addext','subjectAltName=IP:127.0.0.1'],capture_output=True,check=True)
    @classmethod
    def tearDownClass(cls):cls.temp.cleanup()
    def setUp(self):
        self.session=self.root/'session';self.session.write_text('synthetic-session-only');self.session.chmod(0o600)
        self.rows=f.Tests().rows();self.identity={'tenant_id':'t','user_id':'u','role':'admin','via_key':False}
        self.mode=None;self.requests=[];owner=self
        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                owner.requests.append((self.path,self.headers.get('Authorization')))
                if owner.mode=='slow':time.sleep(.4)
                if owner.mode=='rolechange'and len(owner.requests)>1:owner.identity['role']='viewer'
                value=owner.identity if self.path=='/v1/me'else owner.rows[self.path]
                if owner.mode=='redirect':
                    self.send_response(302);self.send_header('Location','http://127.0.0.1:1/secret');self.end_headers();return
                raw=json.dumps(value).encode()
                if owner.mode=='duplicate':raw=b'{"tenant_id":"t","tenant_id":"other"}'
                if owner.mode=='huge':raw=b' '*2048
                if owner.mode=='nonfinite':raw=b'{"value":NaN}'
                self.send_response(200);self.send_header('Content-Type','application/json'if owner.mode!='html'else'text/html');self.send_header('Content-Length',str(len(raw)));self.end_headers()
                try:self.wfile.write(raw)
                except (OSError,ssl.SSLError):pass
            def log_message(self,*args):pass
        self.handler=Handler
        self.server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
        context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(self.root/'ca',self.root/'key');self.server.socket=context.wrap_socket(self.server.socket,server_side=True)
        self.thread=threading.Thread(target=self.server.serve_forever,daemon=True);self.thread.start();self.addCleanup(self.server.server_close);self.addCleanup(self.server.shutdown)
    def client(self,**kwargs):return m.Client('https://127.0.0.1:'+str(self.server.server_port),self.session,'t','u',ca_file=self.root/'ca',**kwargs)
    def test_actual_tls_snapshot_compare_bound_identity(self):
        client=self.client();before=client.snapshot();self.assertFalse(client.compare(before)['full_compatibility'])
        self.assertEqual(len(self.requests),10);self.assertTrue(all(token=='Bearer synthetic-session-only'for _,token in self.requests))
        self.rows['/v1/devices'][0]['name']='changed'
        with self.assertRaises(ValueError):client.compare(before)
    def test_wrong_tenant_role_key_scope_password_refused_before_inventory(self):
        for change in [{'tenant_id':'other'},{'user_id':'other'},{'role':'viewer'},{'via_key':True},{'customer_id':'scoped'},{'denied':[]},{'must_change_credentials':True}]:
            with self.subTest(change=change):
                self.identity=dict(tenant_id='t',user_id='u',role='admin',via_key=False);self.identity.update(change);self.requests=[]
                with self.assertRaises(ValueError):self.client().snapshot()
                self.assertEqual([p for p,_ in self.requests],['/v1/me'])
    def test_redirect_duplicate_encoding_length_json_refused(self):
        for mode in ['redirect','duplicate','huge','nonfinite','html']:
            with self.subTest(mode=mode),self.assertRaises(ValueError):self.mode=mode;self.client(max_bytes=1024).snapshot()
    def test_ca_hostname_and_nonlocal_http_refused(self):
        with self.assertRaises(ValueError):m.Client('https://127.0.0.1',self.session,'t','u')
        for origin in ['http://localhost:80','http://192.168.1.2','https://user:pass@127.0.0.1','https://127.0.0.1/path','https://127.0.0.1?x=1']:
            with self.subTest(origin=origin),self.assertRaises(ValueError):m.Client(origin,self.session,'t','u',allow_loopback_http=True)
        client=self.client();client.host='localhost'
        with self.assertRaises(ValueError):client.snapshot()
    def test_session_private_regular_owned_singlelink_only(self):
        client=self.client();self.session.chmod(0o644)
        with self.assertRaises(ValueError):client.snapshot()
        self.session.chmod(0o600);link=self.root/'link';link.symlink_to(self.session)
        try:
            with self.assertRaises(OSError):m.session_file(link)
        finally:link.unlink()
        alias=self.root/'hardlink';os.link(self.session,alias)
        try:
            with self.assertRaises(ValueError):m.session_file(self.session)
        finally:alias.unlink()
        self.session.write_text('bad\r\nAuthorization: other')
        with self.assertRaises(ValueError):client.snapshot()
    def test_explicit_numeric_loopback_no_proxy(self):
        plain=http.server.ThreadingHTTPServer(('127.0.0.1',0),self.handler)
        thread=threading.Thread(target=plain.serve_forever,daemon=True);thread.start()
        self.addCleanup(plain.server_close);self.addCleanup(plain.shutdown)
        origin='http://127.0.0.1:'+str(plain.server_port)
        with self.assertRaises(ValueError):m.Client(origin,self.session,'t','u')
        with mock.patch.dict(os.environ,{'HTTP_PROXY':'http://127.0.0.1:1','HTTPS_PROXY':'http://127.0.0.1:1','ALL_PROXY':'http://127.0.0.1:1'}):
            m.Client(origin,self.session,'t','u',allow_loopback_http=True).snapshot()
    def test_deadline_and_role_change_no_snapshot(self):
        self.mode='slow';started=time.monotonic()
        with self.assertRaises(ValueError):self.client(timeout=.1).snapshot()
        self.assertLess(time.monotonic()-started,.35)
        self.mode='rolechange';self.requests=[]
        with self.assertRaises(ValueError):self.client().snapshot()
    def test_http10_close_does_not_reuse_borrowed_response_socket(self):
        # Model Python versions that invalidate the connection's socket object
        # at getresponse() for HTTP/1.0, while the response file still reads.
        # Deadline operations must use an independently held raw descriptor.
        original=m.http.client.HTTPSConnection.request
        class Borrowed:
            def __init__(self,sock):self.sock=sock
            def __getattr__(self,key):return getattr(self.sock,key)
            def settimeout(self,*args):raise OSError(9,'synthetic closed borrowed descriptor')
        def request(connection,*args,**kwargs):
            result=original(connection,*args,**kwargs);connection.sock=Borrowed(connection.sock);return result
        with mock.patch.object(m.http.client.HTTPSConnection,'request',request):
            self.client().snapshot()
    def test_unlisted_endpoint_never_sends(self):
        with self.assertRaises(ValueError):self.client().get('/v1/telemetry/ingest')
        self.assertEqual(self.requests,[])

if __name__=='__main__':unittest.main()
