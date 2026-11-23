#!/usr/bin/env python3
"""Explicit disposable Docker image compatibility, not owner update acceptance.

Images must be built separately from fixed complete revisions. Never pulls or
builds an app image, changes a workflow, touches Compose/owner volumes, enables
Apply candidate policy, or calls a device executor. --self-test is local only.
"""
import argparse
import base64
import hashlib
import hmac
import http.client
import ipaddress
import json
import os
from pathlib import Path
import re
import socket
import struct
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def run(*args, stdin=None):
    result = subprocess.run(args, input=stdin, capture_output=True, timeout=120)
    if result.returncode:
        raise RuntimeError('fixture command failed: '+args[0]+' '+args[1]+': '+result.stderr.decode()[-4000:])
    return result.stdout.decode().strip()


def mqtt_string(text):
    data = text.encode()
    return struct.pack('!H', len(data)) + data


def send(sock, kind, body):
    n = len(body)
    encoded = b''
    while True:
        b = n % 128
        n //= 128
        encoded += bytes([b | 128 if n else b])
        if not n:
            break
    sock.sendall(bytes([kind]) + encoded + body)


def packet(sock):
    def exact(n):
        b = b''
        while len(b) < n:
            data = sock.recv(n-len(b))
            if not data:
                raise RuntimeError('MQTT connection closed')
            b += data
        return b
    kind = exact(1)[0]
    length, mul = 0, 1
    for _ in range(4):
        b = exact(1)[0]
        length += (b & 127) * mul
        if b < 128:
            break
        mul *= 128
    else:
        raise RuntimeError('invalid MQTT length')
    if length > 1024 * 1024:
        raise RuntimeError('oversized fixture message')
    return kind, exact(length)


def jwt(secret):
    def b64(data):
        return base64.urlsafe_b64encode(data).rstrip(b'=')
    head = b64(b'{"alg":"HS256","typ":"JWT"}')
    body = b64(json.dumps({'tenant_id': 'image-fixture', 'role': 'admin',
                           'sub': 'image-user', 'iat': int(time.time())-1,
                           'exp': int(time.time())+3300}).encode())
    signed = head+b'.'+body
    return (signed+b'.'+b64(hmac.new(secret.encode(), signed, hashlib.sha256).digest())).decode()


def image_id(value):
    # Exact local image IDs only. No mutable app tags or implicit app pulls.
    if not re.fullmatch(r'sha256:[a-f0-9]{64}', value):
        raise ValueError('requires exact locally built image ID')
    return value


def internal_endpoint(info, network, number):
    # Linux hosted runner can reach its internal bridge directly. Do not add a
    # second egress-enabled network just to make published ports work.
    networks = info['NetworkSettings']['Networks']
    if set(networks) != {network} or info['HostConfig']['PortBindings']:
        raise RuntimeError('fixture must use only its internal network and no published ports')
    entry = networks[network]
    address = ipaddress.ip_address(entry['IPAddress'])
    if address.version != 4 or not address.is_private or address.is_loopback or address.is_unspecified or entry.get('Gateway'):
        raise RuntimeError('fixture requires private internal IPv4 and no default gateway')
    return str(address), number


def self_test():
    for bad in ('latest', 'image:tag', '', 'sha256:bad'):
        try:
            image_id(bad)
        except ValueError:
            continue
        raise AssertionError('mutable image admitted')
    image_id('sha256:'+'a'*64)
    info = {'NetworkSettings': {'Networks': {'private': {'IPAddress': '172.22.0.2', 'Gateway': ''}}},
            'HostConfig': {'PortBindings': {}}}
    assert internal_endpoint(info, 'private', 8000) == ('172.22.0.2', 8000)
    for change in ('other-network', 'binding', 'public', 'loopback', 'gateway'):
        bad = json.loads(json.dumps(info))
        if change == 'other-network': bad['NetworkSettings']['Networks']['egress'] = {}
        if change == 'binding': bad['HostConfig']['PortBindings'] = {'8000/tcp': []}
        if change == 'public': bad['NetworkSettings']['Networks']['private']['IPAddress'] = '8.8.8.8'
        if change == 'loopback': bad['NetworkSettings']['Networks']['private']['IPAddress'] = '127.0.0.1'
        if change == 'gateway': bad['NetworkSettings']['Networks']['private']['Gateway'] = '172.22.0.1'
        try:
            internal_endpoint(bad, 'private', 8000)
        except RuntimeError:
            continue
        raise AssertionError('unsafe fixture network accepted: '+change)
    assert mqtt_string('test') == b'\x00\x04test'
    a, b = socket.socketpair()
    try:
        for data in (b'', b'x'*128, b'y'*16384):
            send(a, 0x30, data)
            assert packet(b) == (0x30, data)
    finally:
        a.close()
        b.close()
    # Execute the actual unchanged legacy updater's secure-selection refusal.
    # This is not successful native upgrade/rollback acceptance.
    with tempfile.TemporaryDirectory(prefix='secure-updater-refusal-') as tmp:
        root = Path(tmp)
        (root/'scripts').mkdir()
        (root/'scripts/upgrade.sh').write_bytes((ROOT/'scripts/upgrade.sh').read_bytes())
        (root/'.secure-deployment').write_text('synthetic-selection')
        before = sorted(str(p.relative_to(root)) for p in root.rglob('*'))
        for option in ('--yes', '--rollback'):
            result = subprocess.run(['bash', str(root/'scripts/upgrade.sh'), option],
                                    capture_output=True, timeout=5)
            assert result.returncode != 0 and b'Secure server upgrade is unavailable' in result.stderr
            assert sorted(str(p.relative_to(root)) for p in root.rglob('*')) == before
    print('SELF_TEST_ONLY: image argument/internal-only endpoint/MQTT framing and actual secure-selector legacy-updater refusal; no Docker image or successful updater acceptance')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--self-test', action='store_true')
    parser.add_argument('--old-image')
    parser.add_argument('--new-image')
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return
    if os.environ.get('TEST_APPLY_DISPOSABLE_IMAGES') != '1':
        raise SystemExit('explicit TEST_APPLY_DISPOSABLE_IMAGES=1 required')
    old, new = image_id(args.old_image or ''), image_id(args.new_image or '')
    for image in (old, new):
        assert run('docker', 'image', 'inspect', '--format', '{{.Id}}', image) == image
    prefix = 'hexthings-image-test-'+uuid.uuid4().hex[:16]
    network = prefix+'-net'
    created, volumes, results = [], [], []
    def container(name, *parameters):
        name = prefix+'-'+name
        run('docker', 'run', '-d', '--name', name, '--label', prefix,
            '--network', network, *parameters)
        created.append(name)
        return name
    def remove(name):
        run('docker', 'rm', '-f', name)
        created.remove(name)
    def pg(name, sql):
        return run('docker', 'exec', '-i', name, 'psql', '-X', '-U',
                   'fixture', '-d', 'fixture', '-At', '-v', 'ON_ERROR_STOP=1', stdin=sql.encode())
    def endpoint(name, number):
        info = json.loads(run('docker', 'inspect', name))[0]
        return internal_endpoint(info, network, number)
    try:
        run('docker', 'network', 'create', '--internal', '--label', prefix, network)
        assert json.loads(run('docker', 'network', 'inspect', network))[0]['Internal'] is True
        with tempfile.TemporaryDirectory(prefix=prefix) as tmp:
            config = Path(tmp)/'mosquitto.conf'
            config.write_text('listener 1883\nallow_anonymous true\npersistence false\n')
            broker = container('broker', '-v',
                               str(config)+':/mosquitto/config/mosquitto.conf:ro',
                               'eclipse-mosquitto:2')
            broker_endpoint = endpoint(broker, 1883)
            for direction, first, second in [('old-to-new', old, new), ('new-to-old', new, old)]:
                for guarded in (False, True):
                    tag = direction+('-guarded' if guarded else '-baseline')
                    volume = prefix+'-'+tag
                    run('docker', 'volume', 'create', '--label', prefix, volume)
                    volumes.append(volume)
                    db = container(tag+'-db', '-e', 'POSTGRES_USER=fixture', '-e',
                                   'POSTGRES_PASSWORD=synthetic-only', '-e', 'POSTGRES_DB=fixture',
                                   '-v', volume+':/var/lib/postgresql/data', 'postgres:16-alpine')
                    for _ in range(100):
                        ready = subprocess.run(['docker', 'exec', db, 'pg_isready', '-U', 'fixture'], capture_output=True)
                        if ready.returncode == 0:
                            break
                        time.sleep(.2)
                    else:
                        raise RuntimeError('disposable database not ready')
                    secret = 'synthetic-'+uuid.uuid4().hex
                    token = jwt(secret)
                    def start(image, suffix):
                        app = container(tag+suffix, '--pull=never', '--read-only', '--tmpfs', '/tmp',
                                        '--cap-drop=ALL', '--security-opt=no-new-privileges:true',
                                        '-e', 'API_PORT=8000',
                                        '-e', 'DATABASE_URL=postgres://fixture:synthetic-only@'+db+':5432/fixture?sslmode=disable',
                                        '-e', 'MIGRATIONS_DIR=/migrations', '-e', 'JWT_SIGNING_SECRET='+secret,
                                        '-e', 'FLEET_SIGNING_KEY='+base64.b64encode(bytes(32)).decode(),
                                        '-e', 'MQTT_HOST='+broker, '-e', 'MQTT_PORT=1883', '-e', 'MQTT_TLS=false',
                                        '-e', 'CACHE_BACKEND=off', '-e', 'OAUTH_STATE_BACKEND=memory',
                                        image, '/bin/api')
                        address = endpoint(app, 8000)
                        for _ in range(150):
                            try:
                                c = http.client.HTTPConnection(*address, timeout=1)
                                c.request('GET', '/healthz')
                                res = c.getresponse(); res.read(); c.close()
                                if res.status == 200:
                                    return app, address
                            except OSError:
                                pass
                            time.sleep(.2)
                        raise RuntimeError('image health failed: '+run('docker', 'logs', app))
                    def request(address, method):
                        c = http.client.HTTPConnection(*address, timeout=15)
                        c.request(method, '/v1/gateways/image-gw/config-push', body='{}' if method == 'POST' else None,
                                  headers={'Authorization': 'Bearer '+token, 'Content-Type': 'application/json'})
                        res = c.getresponse(); body = res.read().decode(); c.close()
                        return res.status, body
                    app, address = start(first, '-first')
                    pg(db, "INSERT INTO tenants(id,name) VALUES('image-fixture','Synthetic'); INSERT INTO users(id,tenant_id,email,display_name,role) VALUES('image-user','image-fixture','synthetic@example.invalid','Synthetic','admin'); INSERT INTO sites(id,tenant_id,name) VALUES('image-site','image-fixture','Synthetic'); INSERT INTO gateways(id,tenant_id,site_id,serial,status) VALUES('image-gw','image-fixture','image-site','SYNTHETIC-IMAGE','active')")
                    if guarded:
                        pg(db, (ROOT/'server/internal/applyadmission/candidate.sql').read_text())
                    observer = socket.create_connection(broker_endpoint, timeout=5)
                    observer.settimeout(3)
                    try:
                        send(observer, 0x10, mqtt_string('MQTT')+b'\x04\x02\x00\x3c'+mqtt_string(tag))
                        assert packet(observer) == (0x20, b'\x00\x00')
                        send(observer, 0x82, b'\x00\x01'+mqtt_string('t/image-fixture/g/image-gw/config')+b'\x01')
                        assert packet(observer)[0] == 0x90
                        def delivery():
                            kind, body = packet(observer)
                            if kind >> 4 != 3:
                                raise RuntimeError('not a publication')
                            n = struct.unpack('!H', body[:2])[0]
                            assert body[2:2+n].decode() == 't/image-fixture/g/image-gw/config'
                            pos = 2+n
                            if (kind >> 1) & 3:
                                send(observer, 0x40, body[pos:pos+2]); pos += 2
                            value = json.loads(body[pos:])
                            assert value['tenant_id'] == 'image-fixture' and value['signature']
                            return value['push_id']
                        response = request(address, 'POST')
                        assert response[0] == 202, response
                        first_id = delivery()
                        pg(db, "UPDATE gateway_config_pushes SET created_at=now()-interval '1 day'")
                        row_before = pg(db, "SELECT row_to_json(p)::text FROM gateway_config_pushes p")
                        remove(app)
                        app, address = start(second, '-second')
                        seen, again = request(address, 'GET'), request(address, 'POST')
                        assert seen[0] == 200 and again[0] == (409 if guarded else 202), (seen, again)
                        assert json.loads(seen[1])[0]['status'] == ('sent' if guarded else 'expired')
                        ids = [first_id]
                        if guarded:
                            assert pg(db, "SELECT row_to_json(p)::text FROM gateway_config_pushes p") == row_before
                            assert pg(db, "SELECT (to_regprocedure('apply_attempt_guard_candidate()') IS NOT NULL)::int || ',' || (EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='gateway_config_pushes'::regclass AND tgname='apply_attempt_guard_candidate' AND tgenabled='O'))::int || ',' || (EXISTS(SELECT 1 FROM pg_index WHERE indexrelid='apply_open_attempt_candidate'::regclass AND indisunique AND indisvalid))::int") == '1,1,1' 
                            observer.settimeout(.5)
                            try:
                                packet(observer)
                                raise AssertionError('unexpected second delivery')
                            except socket.timeout:
                                pass
                        else:
                            ids.append(delivery())
                        assert len(set(ids)) == len(ids)
                        assert pg(db, 'SELECT count(*) FROM schema_migrations') == '93'
                        results.append({'case': tag, 'first_image': first, 'second_image': second,
                                        'next_http': again[0], 'deliveries': len(ids), 'ledger': 93,
                                        'expected': 'retained database blocks resend' if guarded else 'OPEN baseline rearm'})
                    finally:
                        observer.close()
                    remove(app); remove(db)
        print(json.dumps({'results': results, 'limits': ['Actual fixed image sequential replacement, NOT native updater acceptance',
              'No enabled candidate, retained-store deployment, mTLS, mixed concurrent writers or physical executor',
              'Plaintext broker confined to disposable internal network, direct host observer, no published ports',
              'No owner environment, production Compose, existing volumes or account secrets']}, indent=2))
    except Exception:
        # All containers contain synthetic fixture values only. Preserve useful
        # runtime diagnostics before guaranteed cleanup, never print tokens.
        for name in created:
            state = subprocess.run(['docker', 'inspect', '--format', '{{json .State}}', name], capture_output=True)
            logs = subprocess.run(['docker', 'logs', '--tail', '40', name], capture_output=True)
            print('fixture failure container', name, state.stdout.decode(), logs.stdout.decode(), logs.stderr.decode())
        raise
    finally:
        for name in reversed(created):
            result = subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
            print('cleanup container', name, result.returncode)
        for name in reversed(volumes):
            result = subprocess.run(['docker', 'volume', 'rm', name], capture_output=True)
            print('cleanup volume', name, result.returncode)
        result = subprocess.run(['docker', 'network', 'rm', network], capture_output=True)
        print('cleanup network', network, result.returncode)


if __name__ == '__main__':
    main()
