#!/usr/bin/env python3
"""Real nginx routing of /readyz to an intentionally unhealthy local API double.
NGINX_BINARY=/path/to/nginx python3 scripts/test-nginx-readiness.py
Only bind, API address and static root are rewritten for a non-root local test.
"""
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time

binary = os.environ.get('NGINX_BINARY') or shutil.which('nginx')
if not binary:
    raise SystemExit('NOT VERIFIED: set NGINX_BINARY to a real nginx executable')
root = Path(__file__).resolve().parent.parent


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.server.paths.append(self.path)
        self.send_response(self.server.status)
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(b'not ready' if self.server.status == 503 else b'ok')


backend = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
backend.status, backend.paths = 503, []
threading.Thread(target=backend.serve_forever, daemon=True).start()
proc = None
try:
    with tempfile.TemporaryDirectory(prefix='hexthings-nginx-ready-') as tmp:
        temp = Path(tmp)
        (temp / 'index.html').write_text('SPA only')
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        server = (root / 'web/nginx.conf').read_text().replace('listen 80;', f'listen 127.0.0.1:{port};')
        server = server.replace('root /usr/share/nginx/html;', f'root {tmp};')
        server = server.replace('http://api:8000', f'http://127.0.0.1:{backend.server_port}')
        config = temp / 'nginx.conf'
        config.write_text(f'pid {tmp}/nginx.pid; error_log {tmp}/error.log; events {{}} http {{ client_body_temp_path {tmp}/body; proxy_temp_path {tmp}/proxy; fastcgi_temp_path {tmp}/fastcgi; uwsgi_temp_path {tmp}/uwsgi; scgi_temp_path {tmp}/scgi; access_log off; {server} }}')
        subprocess.run([binary, '-t', '-p', tmp, '-c', str(config)], check=True)
        proc = subprocess.Popen([binary, '-p', tmp, '-c', str(config), '-g', 'daemon off;'])

        def get(path):
            c = http.client.HTTPConnection('127.0.0.1', port, timeout=2)
            try:
                c.request('GET', path)
                r = c.getresponse()
                return r.status, r.read().decode(), r.getheader('Cache-Control')
            finally:
                c.close()

        end = time.monotonic() + 5
        while True:
            try:
                response = get('/readyz')
                break
            except OSError:
                if time.monotonic() >= end:
                    raise
                time.sleep(.05)
        assert response == (503, 'not ready', 'no-store'), response
        assert backend.paths[-1] == '/readyz', backend.paths
        print('PASS: /readyz preserves actual backend 503, body and no-store, not SPA fallback')
        backend.status = 200
        assert get('/readyz') == (200, 'ok', 'no-store')
        assert get('/readyz?check=1') == (200, 'ok', 'no-store')
        assert backend.paths[-1] == '/readyz?check=1'
        assert get('/dashboard')[:2] == (200, 'SPA only')
        print('PASS: recovery, readiness query path and unrelated SPA route')
        print('ALL PASSED: local nginx only; not Docker image or cross-node routing proof')
finally:
    if proc:
        proc.terminate()
        proc.wait(timeout=5)
    backend.shutdown()
    backend.server_close()
