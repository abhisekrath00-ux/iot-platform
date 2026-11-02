#!/usr/bin/env python3
"""Real loopback HTTP tests; never probes external infrastructure."""
import argparse
import importlib.util
import json
from pathlib import Path
import socket
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

spec = importlib.util.spec_from_file_location('probe', Path(__file__).with_name('check-ha-http.py'))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        self.server.calls.append((self.command, self.path))
        status = self.server.status
        if status == 'stall':
            time.sleep(.3)
            return
        if status == 'trickle':
            try:
                for byte in b'HTTP/1.1 200 OK\r\nX-Slow: abcdefghijklmnopqrstuvwxyz\r\n\r\n':
                    self.connection.sendall(bytes([byte]))
                    time.sleep(.02)
            except OSError:
                pass
            return
        if status == 'malformed':
            self.connection.sendall(b'not HTTP\r\n\r\n')
            return
        self.send_response(status)
        self.send_header('Location', '/must-not-follow')
        self.send_header('Content-Length', '1000000000')
        self.end_headers() # no body: status-only probe must not wait for it


class Tests(unittest.TestCase):
    def setUp(self):
        self.servers = []
        self.selected = []
        for i in range(11):
            server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            server.daemon_threads = True
            server.status = 200 if i < 8 or i == 8 else 503
            server.calls = []
            threading.Thread(target=server.serve_forever, kwargs={'poll_interval': .01}, daemon=True).start()
            self.servers.append(server)
            self.selected.append((str(i), '127.0.0.1', server.server_port, '/primary' if i >= 8 else '/readyz', 'primary' if i >= 8 else 'ready'))

    def tearDown(self):
        for server in self.servers:
            server.shutdown()
            server.server_close()

    def test_success_and_contract(self):
        result = probe.check(self.selected, .2)
        self.assertTrue(result['checkpoint_passed'])
        self.assertEqual(result['primary_targets'], ['8'])
        for i, server in enumerate(self.servers):
            self.assertEqual(server.calls, [('GET', '/primary' if i >= 8 else '/readyz')])
        self.assertNotIn('Location', json.dumps(result))

    def test_readiness_and_primary_failures(self):
        for status in [302, 500, 503]:
            self.servers[0].status = status
            self.assertFalse(probe.check(self.selected, .2)['checkpoint_passed'])
        self.servers[0].status = 200
        self.servers[9].status = 200
        self.assertFalse(probe.check(self.selected, .2)['checkpoint_passed'])
        self.servers[9].status = 503
        self.servers[8].status = 503
        self.assertFalse(probe.check(self.selected, .2)['checkpoint_passed'])
        self.servers[8].status = 200
        self.servers[9].status = 401
        self.assertFalse(probe.check(self.selected, .2)['checkpoint_passed'])

    def test_deadline_malformed_and_refused(self):
        self.servers[0].status = 'stall'
        start = time.monotonic()
        result = probe.check(self.selected, .05)
        self.assertFalse(result['checkpoint_passed'])
        self.assertLess(time.monotonic() - start, 1)
        self.assertEqual(result['results'][0]['outcome'], 'unavailable')
        self.servers[0].status = 'trickle'
        start = time.monotonic()
        self.assertFalse(probe.check(self.selected, .1)['checkpoint_passed'])
        self.assertLess(time.monotonic() - start, .8)
        self.servers[0].status = 'malformed' 
        self.assertFalse(probe.check(self.selected, .1)['checkpoint_passed'])
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        self.selected[0] = ('refused', '127.0.0.1', port, '/readyz', 'ready')
        self.assertFalse(probe.check(self.selected, .1)['checkpoint_passed'])

    def test_inputs_and_generated_ports(self):
        nodes = probe.node_list('a=10.0.0.11,b=10.0.0.12,c=10.0.0.13')
        t = probe.targets(nodes, '10.0.0.10')
        self.assertEqual(len(t), 11)
        self.assertEqual({x[2] for x in t}, {8001, 8081, 8008, 8000, 8080})
        self.assertNotIn(('vip/primary', '10.0.0.10', 8008, '/primary', 'primary'), t)
        for bad in ['a=10.0.0.1,b=10.0.0.2', 'a=10.0.0.1,a=10.0.0.2,c=10.0.0.3', 'a=host,b=10.0.0.2,c=10.0.0.3']:
            with self.assertRaises(argparse.ArgumentTypeError):
                probe.node_list(bad)
        for timeout in [0, -1, 11, float('nan')]:
            with self.assertRaises(ValueError):
                probe.check(self.selected, timeout)


if __name__ == '__main__':
    unittest.main()
