#!/usr/bin/env python3
"""Exercise generated HTTP checks with a real local HAProxy, not a cluster.

HAPROXY_BINARY=/path/to/haproxy python3 scripts/test-ha-http-routing.py
Requires Python 3, bash and a locally installed HAProxy. No Docker or remote host.
Only listen/backend addresses change for loopback; generated checks/timings stay intact.
"""
import http.client
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parent.parent
BINARY = os.environ.get('HAPROXY_BINARY') or shutil.which('haproxy')
if not BINARY:
    raise SystemExit('NOT VERIFIED: set HAPROXY_BINARY to a real HAProxy executable')


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


class Backend(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, name, status):
        self.name, self.status, self.posts = name, status, 0
        super().__init__(('127.0.0.1', 0), Handler)


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):
        pass

    def answer(self, status, body):
        self.send_response(status)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Connection', 'close')
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass # active health checks can close after reading only headers

    def do_GET(self):
        if self.path == '/readyz':
            self.answer(self.server.status, b'health')
        elif self.path == '/primary':
            self.answer(200, b'primary')
        else:
            self.answer(200, self.server.name.encode())

    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length', '0')))
        self.server.posts += 1
        # An application failure must not trigger a replay on a different API.
        self.answer(503, self.server.name.encode())


def request(p, method='GET'):
    c = http.client.HTTPConnection('127.0.0.1', p, timeout=2)
    try:
        c.request(method, '/write' if method == 'POST' else '/probe',
                  body=b'marker' if method == 'POST' else None,
                  headers={'Connection': 'close'})
        r = c.getresponse()
        return r.status, r.read().decode()
    finally:
        c.close()


def await_members(p, expected, message):
    end = time.monotonic() + 25
    last = None
    while time.monotonic() < end:
        try:
            replies = [request(p) for _ in range(12)]
            last = replies
            if {body for code, body in replies if code == 200} == expected and all(code == 200 for code, _ in replies):
                print('PASS:', message, flush=True)
                return
        except (OSError, http.client.HTTPException) as e:
            last = str(e)
        time.sleep(.2)
    raise AssertionError((message, last))


def await_unavailable(p):
    end = time.monotonic() + 25
    while time.monotonic() < end:
        try:
            code, _ = request(p)
            if code == 200:
                time.sleep(.2)
                continue
            assert code == 503, code
        except (OSError, http.client.HTTPException):
            # Generated mode is TCP: no fabricated HTTP response when all are down.
            pass
        print('PASS: all unhealthy fails without a successful backend response', flush=True)
        return
    raise AssertionError('all unhealthy still serving')


servers = [Backend('a', 200), Backend('b', 503), Backend('c', 302)]
proc = None
try:
    for s in servers:
        threading.Thread(target=s.serve_forever, daemon=True).start()
    with tempfile.TemporaryDirectory(prefix='hexthings-ha-http-') as tmp:
        out = Path(tmp) / 'render'
        subprocess.run(['bash', str(ROOT / 'scripts/install-ha.sh'), 'render', '--nodes',
                        'a=10.0.0.11,b=10.0.0.12,c=10.0.0.13', '--vip', '10.0.0.10', '--out', str(out)], check=True)
        config = (out / 'a/haproxy.cfg').read_text()
        binds = {old: port() for old in (5000, 8080, 8000, 1883)}
        for old, new in binds.items():
            config = config.replace(f'bind *:{old}\n', f'bind 127.0.0.1:{new}\n')
        for i, s in enumerate(servers):
            config = re.sub(rf'10\.0\.0\.{11+i}:\d+', f'127.0.0.1:{s.server_port}', config)
        # Patroni's separate health port is remapped too, not its health path.
        config = re.sub(r'(server ([abc]) [^\n]+) check port 8008',
                        lambda m: m[1] + ' check port ' + str(servers[ord(m[2])-ord('a')].server_port), config)
        path = Path(tmp) / 'haproxy.cfg'
        path.write_text(config)
        subprocess.run([BINARY, '-c', '-f', str(path)], check=True)
        with open(Path(tmp) / 'haproxy.log', 'w+') as log:
            proc = subprocess.Popen([BINARY, '-db', '-f', str(path)], stdout=log, stderr=log)
            try:
                for p in (binds[8080], binds[8000]):
                    await_members(p, {'a'}, '503 and redirect backends excluded with TCP ports still open')
                before = sum(s.posts for s in servers)
                code, body = request(binds[8000], 'POST')
                assert (code, body) == (503, 'a'), (code, body)
                assert sum(s.posts for s in servers) == before + 1
                print('PASS: failing POST forwarded once, not replayed', flush=True)
                servers[1].status = 200
                for p in (binds[8080], binds[8000]):
                    await_members(p, {'a', 'b'}, 'recovered backend re-enters round-robin')
                servers[0].status, servers[2].status = 503, 500
                for p in (binds[8080], binds[8000]):
                    await_members(p, {'b'}, 'new 503 and 500 failures excluded')
                servers[1].status = 503
                for p in (binds[8080], binds[8000]):
                    await_unavailable(p)
                servers[2].status = 200
                for p in (binds[8080], binds[8000]):
                    await_members(p, {'c'}, 'service resumes after all-down condition')
                assert proc.poll() is None, 'HAProxy exited during exercise'
            except Exception:
                log.flush()
                log.seek(0)
                print(log.read())
                raise
    print('ALL PASSED: local HAProxy HTTP routing only; no Docker, VIP, TLS, database or node failover claim')
finally:
    if proc:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
    for s in servers:
        s.shutdown()
        s.server_close()
