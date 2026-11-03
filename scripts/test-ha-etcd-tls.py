#!/usr/bin/env python3
"""Disposable loopback transport check of generated etcd flags, not a deployment test."""
import base64
import json
import os
from pathlib import Path
import shlex
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
import urllib.error

import yaml

ROOT = Path(__file__).resolve().parents[1]
BINARY = os.environ.get('ETCD_BINARY')
if not BINARY:
    raise SystemExit('Set ETCD_BINARY to a trusted etcd binary; this test downloads nothing.')


def run(*args):
    return subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def context(root, identity='client', ca='ca.crt'):
    ctx = ssl.create_default_context(cafile=str(root / ca))
    ctx.verify_flags |= ssl.VERIFY_X509_STRICT
    if identity:
        ctx.load_cert_chain(str(root / (identity + '.crt')), str(root / (identity + '.key')))
    return ctx


def request(url, ctx, data=None):
    handler = urllib.request.HTTPSHandler(context=ctx)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), handler)
    body = None if data is None else json.dumps(data).encode()
    req = urllib.request.Request(url, data=body, headers={'Content-Type': 'application/json'})
    with opener.open(req, timeout=2) as reply:
        assert reply.status == 200
        return json.load(reply)


def rejected(url, ctx, data=None):
    try:
        request(url, ctx, data)
    except urllib.error.HTTPError as error:
        raise AssertionError('TLS accepted an HTTP request that should be rejected') from error
    except OSError:
        return
    raise AssertionError('Unexpected TLS acceptance')


with tempfile.TemporaryDirectory(prefix='hex-etcd-tls-') as temp:
    root = Path(temp)
    processes = []
    logs = []
    try:
        # Synthetic test PKI only, removed with the temporary directory.
        for ca in ('ca', 'rogue-ca'):
            run('openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                '-subj', '/CN=disposable-' + ca,
                '-addext', 'basicConstraints=critical,CA:TRUE',
                '-addext', 'keyUsage=critical,keyCertSign,cRLSign', '-keyout', str(root / (ca + '.key')),
                '-out', str(root / (ca + '.crt')))
        for identity in ('a', 'b', 'c', 'client', 'rogue'):
            ca = 'rogue-ca' if identity == 'rogue' else 'ca'
            ext = root / 'extensions'
            ext.write_text('basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\n'
                           'subjectAltName=IP:127.0.0.1\nextendedKeyUsage=' +
                           ('clientAuth' if identity in ('client', 'rogue') else 'serverAuth,clientAuth') + '\n')
            run('openssl', 'req', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=fixture-' + identity,
                '-keyout', str(root / (identity + '.key')), '-out', str(root / (identity + '.csr')))
            run('openssl', 'x509', '-req', '-in', str(root / (identity + '.csr')), '-CA', str(root / (ca + '.crt')),
                '-CAkey', str(root / (ca + '.key')), '-CAcreateserial', '-days', '1', '-extfile', str(ext),
                '-out', str(root / (identity + '.crt')))
        output = root / 'render'
        run('bash', str(ROOT / 'scripts/install-ha.sh'), 'render', '--nodes',
            'a=10.0.0.11,b=10.0.0.12,c=10.0.0.13', '--vip', '10.0.0.10',
            '--etcd-mtls', '--out', str(output))
        ports = [port() for _ in range(6)]
        assert len(set(ports)) == 6, 'Ephemeral port collision; rerun'
        commands = []
        for index, name in enumerate('abc'):
            compose = yaml.safe_load((output / name / 'docker-compose.yml').read_text())
            command = compose['services']['etcd']['command']
            # Keep generated transport/auth flags; only relocate endpoints, data and mounts.
            for j in range(3):
                command = command.replace(f'10.0.0.{11+j}:2379', f'127.0.0.1:{ports[j*2]}')
                command = command.replace(f'10.0.0.{11+j}:2380', f'127.0.0.1:{ports[j*2+1]}')
            command = command.replace('/etcd-data', str(root / ('data-' + name)))
            for filename in ('ca.crt', 'member.crt', 'member.key'):
                local = filename.replace('member', name)
                command = command.replace('/run/etcd-tls/' + filename, str(root / local))
            args = shlex.split(command)
            args[0] = BINARY
            commands.append(args)
            log = open(root / (name + '.log'), 'w+')
            logs.append(log)
            processes.append(subprocess.Popen(args, stdout=log, stderr=log))
        ctx = context(root)
        urls = [f'https://127.0.0.1:{ports[i*2]}' for i in range(3)]
        deadline = time.monotonic() + 25
        while True:
            try:
                health = [request(url + '/health', ctx) for url in urls]
                assert all(item.get('health') == 'true' for item in health)
                break
            except (OSError, ValueError, AssertionError) as error:
                if any(p.poll() is not None for p in processes) or time.monotonic() > deadline:
                    print('Disposable fixture health failure:', type(error).__name__, str(error), flush=True)
                    for name, proc, log in zip('abc', processes, logs):
                        log.flush()
                        log.seek(0)
                        print('Fixture member', name, 'exit', proc.poll(), flush=True)
                        print(''.join(log.readlines()[-40:]), flush=True)
                    raise AssertionError('TLS cluster did not become healthy') from None
                time.sleep(0.2)
        key = base64.b64encode(b'fixture-key').decode()
        value = base64.b64encode(b'fixture-value').decode()
        request(urls[0] + '/v3/kv/put', ctx, dict(key=key, value=value))
        for url in urls:
            found = request(url + '/v3/kv/range', ctx, dict(key=key))
            assert found['kvs'][0]['value'] == value
            members = request(url + '/v3/cluster/member/list', ctx, {})['members']
            assert len(members) == 3
            assert all(u.startswith('https://') for m in members for u in m['peerURLs'] + m['clientURLs'])
            # Check the gateway as well as generic HTTPS; no certificate must not grant reads.
            for bad in (context(root, None), context(root, 'rogue')):
                rejected(url + '/v3/kv/range', bad, dict(key=key))
            rejected(url + '/v3/kv/range', context(root, ca='rogue-ca.crt'), dict(key=key))
        patroni_python = os.environ.get('PATRONI_PYTHON')
        if patroni_python:
            # Exercise Patroni's real DCS implementation, not a replacement HTTP client.
            config = yaml.safe_load((output / 'a' / 'patroni.yml').read_text())
            config['etcd3']['hosts'] = ','.join('127.0.0.1:' + str(ports[i*2]) for i in range(3))
            for field, filename in [('cacert', 'ca.crt'), ('cert', 'client.crt'), ('key', 'client.key')]:
                config['etcd3'][field] = str(root / filename)
            config.update(scope='disposable-patroni-fixture', namespace='/fixture/', patronictl=True,
                          ttl=30, loop_wait=10, retry_timeout=3)
            config_path = root / 'patroni-fixture.json'
            config_path.write_text(json.dumps(config))
            subprocess.run([patroni_python, str(ROOT / 'scripts/test-patroni-dcs.py'),
                            str(config_path)], check=True, timeout=90)
        for i in range(3):
            for bad in (context(root, None), context(root, 'rogue')):
                rejected(f'https://127.0.0.1:{ports[i*2+1]}/members', bad)
        # Hostname verification stays on; correct CA alone does not authorize the wrong SAN.
        try:
            with socket.create_connection(('127.0.0.1', ports[0]), timeout=2) as sock:
                with ctx.wrap_socket(sock, server_hostname='127.0.0.2'):
                    pass
        except ssl.SSLCertVerificationError:
            pass
        else:
            raise AssertionError('Wrong server SAN accepted')
        print('PASS: generated TLS flags form three local members; authenticated gateway writes/readback on all members')
        print('PASS: no-client/untrusted-client/untrusted-server/wrong-SAN and unauthenticated peer connections rejected')
        for proc in processes:
            proc.terminate()
        for proc in processes:
            proc.wait(timeout=8)
        processes.clear()
        # Missing client-listener certificate can leave etcd retrying, not exiting.
        # Assert fail-closed transport, not process health or restart behavior.
        broken = commands[0].copy()
        broken[broken.index('--data-dir') + 1] = str(root / 'missing-data')
        index = broken.index('--cert-file=' + str(root / 'a.crt'))
        broken[index] = '--cert-file=' + str(root / 'absent.crt')
        proc = subprocess.Popen(broken, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        processes.append(proc)
        for _ in range(3):
            time.sleep(0.5)
            rejected(urls[0] + '/health', ctx)
        try:
            request(urls[0].replace('https://', 'http://') + '/health', ctx)
        except (OSError, ValueError):
            pass
        else:
            raise AssertionError('Missing certificate enabled plaintext')
        print('PASS: missing member certificate serves neither authenticated health nor plaintext health')
    finally:
        for proc in processes:
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
        for log in logs:
            log.close()
