#!/usr/bin/env python3
"""Bounded real Patroni transport discovery rejection, not daemon startup."""
import copy
import importlib.metadata
import io
import json
import logging
import socket
from pathlib import Path
import sys
import subprocess

import etcd
from patroni.dcs.etcd import DnsCachingResolver
from patroni.dcs.etcd3 import PatroniEtcd3Client

assert importlib.metadata.version('patroni') == '4.1.5'
config_path = Path(sys.argv[1])
config = json.loads(config_path.read_text())
assert config['scope'] == 'disposable-patroni-fixture'
assert config['namespace'] == '/fixture/' and config['patronictl'] is True
settings = config['etcd3']
assert settings['protocol'] == 'https'
hosts = settings['hosts'].split(',')
assert len(hosts) == 3 and all(host.startswith('127.0.0.1:') and host.split(':')[1].isdigit() for host in hosts)
root = config_path.parent
assert all(Path(settings[k]).parent == root for k in ('cacert', 'cert', 'key'))
# Same cert/key and cacert mapping used by AbstractEtcd.get_etcd_client.
# Construct its actual transport directly: get_dcs retries startup forever on
# unavailable endpoints, so a forced subprocess timeout would not prove TLS.
base = dict(protocol='https', hosts=['https://' + host for host in hosts], retry_timeout=3,
            ca_cert=settings['cacert'], cert=(settings['cert'], settings['key']))


def transport(params):
    return PatroniEtcd3Client(params, DnsCachingResolver())


control = transport(copy.deepcopy(base))
assert len(control.machines) == 3
control.http.clear()
for label in ('missing-client', 'untrusted-client', 'untrusted-server', 'wrong-server-san', 'expired-client', 'wrong-client-purpose'):
    params = copy.deepcopy(base)
    if label == 'missing-client':
        params.pop('cert')
    elif label == 'untrusted-client':
        params['cert'] = (str(root / 'rogue.crt'), str(root / 'rogue.key'))
    elif label == 'wrong-client-purpose':
        certificate = str(root / 'server-only.crt')
        params['cert'] = (certificate, str(root / 'server-only.key'))
        trusted = subprocess.run(['openssl', 'verify', '-CAfile', settings['cacert'],
                                  '-purpose', 'sslserver', certificate], capture_output=True, text=True)
        assert trusted.returncode == 0, trusted.stdout + trusted.stderr
        wrong = subprocess.run(['openssl', 'verify', '-CAfile', settings['cacert'],
                                '-purpose', 'sslclient', certificate], capture_output=True, text=True)
        diagnostic = wrong.stdout + wrong.stderr
        assert wrong.returncode != 0 and any(reason in diagnostic for reason in
                                           ('unsuitable certificate purpose', 'unsupported certificate purpose')), diagnostic
    elif label == 'expired-client':
        params['cert'] = (str(root / 'expired-client.crt'), settings['key'])
        # Prove expiry independently; the chain and purpose verify when time checking alone is disabled.
        verified = subprocess.run(['openssl', 'verify', '-no_check_time', '-CAfile',
                                   settings['cacert'], '-purpose', 'sslclient', str(root / 'expired-client.crt')],
                                  capture_output=True, text=True)
        assert verified.returncode == 0, verified.stdout + verified.stderr
        expired = subprocess.run(['openssl', 'verify', '-CAfile', settings['cacert'],
                                  str(root / 'expired-client.crt')], capture_output=True, text=True)
        assert expired.returncode != 0 and 'certificate has expired' in expired.stdout + expired.stderr
    elif label == 'wrong-server-san':
        # The real loopback listener remains reachable through this alias, but
        # certificates contain IP:127.0.0.1 only, never DNS:localhost.
        assert all(item[4][0] in ('127.0.0.1', '::1') for item in socket.getaddrinfo('localhost', None))
        params['hosts'] = [url.replace('127.0.0.1', 'localhost') for url in base['hosts']]
        for host in hosts:
            with socket.create_connection(('localhost', int(host.split(':')[1])), timeout=2):
                pass
    else:
        params['ca_cert'] = str(root / 'rogue-ca.crt')
    captured = io.StringIO()
    handler = logging.StreamHandler(captured)
    logger = logging.getLogger('patroni.dcs.etcd')
    logger.addHandler(handler)
    try:
        try:
            unexpected = transport(params)
        except etcd.EtcdException as error:
            assert type(error) is etcd.EtcdException
            # Reject connection-refused/timeouts/other initialization failures as
            # evidence: actual Patroni diagnostics must identify an SSL error.
            evidence = captured.getvalue()
            failures = evidence.splitlines()
            assert len(failures) == 3 and all('SSLError' in line for line in failures), \
                'Each endpoint must fail in the TLS handshake: ' + evidence
            expected = [url.split('://')[1] for url in params['hosts']]
            assert all(host in evidence for host in expected)
            if label == 'wrong-server-san':
                assert all('CERTIFICATE_VERIFY_FAILED' in line and
                           ('Hostname mismatch' in line or 'not valid for' in line or "doesn't match" in line)
                           for line in failures), 'Expected explicit hostname mismatch on every endpoint: ' + evidence
            assert not any(term in evidence for term in ('Connection refused', 'Read timed out', 'ConnectTimeoutError'))
            assert any(term in evidence for term in ('CERTIFICATE_VERIFY_FAILED', 'TLSV13_ALERT_CERTIFICATE_REQUIRED',
                                                     'SSLV3_ALERT_BAD_CERTIFICATE', 'TLSV1_ALERT_UNKNOWN_CA', 'SSLV3_ALERT_CERTIFICATE_EXPIRED', 'SSLEOFError')), \
                'No recognized TLS handshake rejection: ' + evidence
        else:
            unexpected.http.clear()
            raise AssertionError('Patroni accepted invalid TLS identity: ' + label)
    finally:
        logger.removeHandler(handler)
    print('PASS: actual Patroni transport rejects ' + label + ' with TLS handshake diagnostic', flush=True)
# A fresh good-identity discovery after negatives excludes an unavailable
# cluster being mistaken for authentication rejection.
control = transport(copy.deepcopy(base))
assert len(control.machines) == 3
control.http.clear()
print('PASS: actual Patroni transport good-identity discovery before/after TLS negatives', flush=True)
