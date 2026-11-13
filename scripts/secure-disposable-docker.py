#!/usr/bin/env python3
"""Disposable native-runner effect adapter, never an owner activation command.

Only synthetic projects in a fresh temporary fixture are accepted. This is not
a production operator interface and has no CLI. It exercises the transaction
engine against real Docker without acquiring owner-host mutation authority.
"""
import hashlib
import json
import os
import pathlib
import socket
import ssl
import subprocess
import tempfile
import time


class DisposableDocker:
    def __init__(self, root, bundle, project, gid, fail_accept=False):
        root, bundle = pathlib.Path(root).resolve(), pathlib.Path(bundle).resolve()
        if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_OS') != 'Linux'
                or not project.startswith('hexthings-secure-ci-')
                or not root.name.startswith('hexthings-compose-ci-') or root.parent != pathlib.Path('/tmp')
                or bundle.parent != root or os.geteuid() != 0):
            raise ValueError('Disposable standard Linux runner and fresh synthetic fixture required.')
        self.root, self.bundle, self.project, self.gid = root, bundle, project, gid
        self.fail_accept = fail_accept
        self.accepted_candidate = False
        self.input = json.loads((bundle / 'prepare-inputs.json').read_text())
        self.original = bundle / 'previous-compose.json'

    def call(self, args, binary=False, input=None):
        result = subprocess.run(args, cwd=self.root, input=input, capture_output=True, text=not binary)
        if result.returncode:
            # Never print resolved Compose, credentials, DB archive bytes or
            # private TLS material. Fixture logs remain separately inspectable.
            raise ValueError('Disposable Docker effect failed: ' + ' '.join(args[:3]))
        return result.stdout

    def compose(self, config, *args, binary=False, input=None):
        return self.call(['docker', 'compose', '-p', self.project, '-f', str(config), *args], binary=binary, input=input)

    def authorize(self, action, project, previous, candidate):
        if project != self.project or pathlib.Path(previous).parent != self.bundle or pathlib.Path(candidate).parent != self.bundle:
            raise ValueError('Synthetic transaction target changed.')
        if action not in {'activate', 'recover'}:
            raise ValueError('Unexpected synthetic transaction action.')

    def revalidate(self, project, receipt):
        value = json.loads(pathlib.Path(receipt).read_text())
        current = json.loads(self.compose(self.original, 'config', '--format', 'json'))
        prior = json.loads(self.original.read_text())
        if current != prior or project != self.project:
            raise ValueError('Fixture current Compose changed after preflight.')
        ids = self.compose(self.original, 'ps', '-q').split()
        containers = json.loads(self.call(['docker', 'inspect', *ids]))
        found = {}
        for container in containers:
            name = container['Config']['Labels']['com.docker.compose.service']
            if container['Config']['Labels']['com.docker.compose.project'] != self.project or container['State']['Status'] != 'running':
                raise ValueError('Fixture project/container changed after preflight.')
            found[name] = {'image': container['Image'], 'container_id': container['Id']}
        if found != value['current_services']:
            raise ValueError('Fixture image/container identity changed after preflight.')

    def backup(self, project, previous):
        raw = self.compose(previous, 'exec', '-T', 'postgres', 'pg_dump', '-U', 'ci', '-Fc', 'ci', binary=True)
        self.compose(previous, 'exec', '-T', 'postgres', 'pg_restore', '-l', binary=True, input=raw)
        fd, name = tempfile.mkstemp(prefix='backup-', suffix='.dump', dir=self.bundle)
        path = pathlib.Path(name)
        with os.fdopen(fd, 'wb') as output:
            output.write(raw); output.flush(); os.fsync(output.fileno())
        return {'path': str(path), 'sha256': hashlib.sha256(raw).hexdigest(), 'verified_archive': True}

    def stop_writers(self, value):
        # This fixture is deliberately bounded. A production adapter must
        # classify all writers, including MCP/background/custom services.
        services = set(json.loads(pathlib.Path(value['previous']).read_text())['services'])
        if services != {'api', 'ingest', 'postgres', 'mosquitto', 'web'}:
            raise ValueError('Disposable adapter refuses unclassified services.')
        self.compose(value['previous'], 'stop', 'api', 'ingest')

    def export_acl(self, value):
        # Reuse exact existing image in the immutable prior snapshot. API is
        # stopped, so use a one-shot operator from that image, never candidate.
        acl = self.compose(value['previous'], 'run', '--rm', '--no-deps', '--pull', 'never', '--entrypoint', '/bin/tenantctl', 'api', 'broker-acl-export')
        if not acl.startswith('# hexmon broker ACL'):
            raise ValueError('No complete current-image certificate ACL snapshot.')
        return acl

    def stage_acl(self, value, acl):
        target = self.bundle / 'acl' / 'acl'
        tmp = target.with_name('acl.next')
        fd = os.open(tmp, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o640)
        with os.fdopen(fd, 'w') as output:
            output.write(acl); output.flush(); os.fsync(output.fileno())
        os.chown(tmp, -1, self.gid); os.chmod(tmp, 0o640); os.replace(tmp, target)
        fd = os.open(target.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)

    def start_candidate(self, value):
        self.compose(value['candidate'], 'up', '-d', '--no-deps', '--no-build', '--pull', 'never', 'mosquitto', 'api', 'ingest', 'web', 'secure-web')

    def verify_recovery_inputs(self, value):
        old = pathlib.Path(value['previous'])
        if hashlib.sha256(old.read_bytes()).hexdigest() != value['previous_sha256']:
            raise ValueError('Prior immutable runtime config changed.')
        manifest=value.get('previous_files',{})
        if manifest.get(str(old))!=value['previous_sha256']:
            raise ValueError('Prior runtime dependency manifest is incomplete.')
        for name,want in manifest.items():
            path=pathlib.Path(name)
            if path.is_symlink() or not path.is_file() or path.stat().st_mode&0o022 or hashlib.sha256(path.read_bytes()).hexdigest()!=want:
                raise ValueError('Prior runtime read-only dependency changed; refuse rollback effects.')
        for image in sorted(set(value['previous_images'].values())):
            found = json.loads(self.call(['docker', 'image', 'inspect', image]))
            if len(found) != 1 or found[0]['Id'] != image:
                raise ValueError('Prior immutable image no longer available.')
        # No pg_restore here: app/schema images never change in transport work.

    def stop_candidate(self, value):
        self.compose(value['candidate'], 'stop', 'secure-web', 'api', 'ingest', 'mosquitto', 'web')
        self.compose(value['candidate'], 'rm', '-f', 'secure-web')

    def restore_previous(self, value):
        self.compose(value['previous'], 'up', '-d', '--no-deps', '--no-build', '--pull', 'never', 'mosquitto', 'api', 'ingest', 'web')

    def mqtt_connect(self, anonymous=False):
        certs = pathlib.Path(self.input['certs'])
        context = ssl.create_default_context(cafile=str(certs / 'broker-ca.crt'))
        if not anonymous:
            context.load_cert_chain(certs / 'api.crt', certs / 'api.key')
        with socket.create_connection((self.input['bind'], 8883), timeout=3) as plain:
            with context.wrap_socket(plain, server_hostname=self.input['host']) as secure:
                client = b'fixture-transport-acceptance'
                payload = b'\x00\x04MQTT\x04\x02\x00\x0a' + len(client).to_bytes(2, 'big') + client
                secure.sendall(b'\x10' + bytes([len(payload)]) + payload)
                reply=secure.recv(4)
                if reply==b'\x20\x02\x00\x00':return True
                if anonymous and reply in [b'\x20\x02\x00\x04',b'\x20\x02\x00\x05']:return False
                raise ValueError('MQTT CONNECT result is incomplete or unverifiable.')

    def accept(self, value, target):
        config = value[target]
        expected = json.loads(pathlib.Path(config).read_text())['services']
        until = time.monotonic() + 45
        while True:
            try:
                ids = self.compose(config, 'ps', '-q').split()
                containers = json.loads(self.call(['docker', 'inspect', *ids]))
                found = {item['Config']['Labels']['com.docker.compose.service']: item for item in containers}
                if len(found)!=len(containers) or any(item['Config']['Labels'].get('com.docker.compose.project')!=self.project for item in containers):
                    raise ValueError('Unexpected replica or project during acceptance.')
                if set(found) != set(expected):
                    raise ValueError('Runtime service selection mismatch.')
                for name, service in expected.items():
                    item = found[name]
                    if item['Image'] != service['image'] or item['State']['Status'] != 'running' or item['State'].get('Health', {}).get('Status', 'healthy') != 'healthy':
                        raise ValueError('Runtime image/health not accepted.')
                self.call(['curl', '--fail', '--silent', '--show-error', '--max-time', '3', 'http://127.0.0.1:18100/healthz'])
                if target == 'candidate':
                    self.call(['curl', '--noproxy', '*', '--fail', '--silent', '--show-error', '--max-time', '3', '--cacert', str(pathlib.Path(self.input['certs']) / 'https-ca.crt'), '--resolve', self.input['host'] + ':8443:' + self.input['bind'], 'https://' + self.input['host'] + ':8443/'])
                    if not self.mqtt_connect():
                        raise ValueError('Authenticated broker CONNECT not accepted.')
                    try:
                        anonymous = self.mqtt_connect(anonymous=True)
                    except ssl.SSLError as error:
                        if 'CERTIFICATE_REQUIRED' not in str(error).upper():raise ValueError('Anonymous refusal was not a verified missing-certificate TLS alert.') from error
                        anonymous = False
                    if anonymous:
                        raise ValueError('Anonymous broker connection accepted.')
                break
            except (ValueError, OSError, ssl.SSLError):
                if time.monotonic() >= until:
                    raise ValueError('Disposable runtime acceptance timed out.')
                time.sleep(.5)
        if target == 'candidate':
            self.accepted_candidate = True
        if target == 'candidate' and self.fail_accept:
            raise ValueError('Deliberate post-start fixture failure to exercise real rollback.')
