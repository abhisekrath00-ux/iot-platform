#!/usr/bin/env python3
"""Exact Linux Docker-volume binding for the internal PostgreSQL HBA barrier.

No discovery/default container, owner CLI, or automatic provider activation.
Only a running, unprivileged local Postgres container with one writable local
Docker volume containing the server-reported HBA path is supported. Host file
identity is checked against PostgreSQL's own bytes before use. Actual peers and
positive/negative fresh connection proofs remain required from the host caller.
"""
import importlib.util
import json
import pathlib
import posixpath
import re
import subprocess
import time

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    value = importlib.util.module_from_spec(spec); spec.loader.exec_module(value); return value

hba = load('secure-postgres-hba')
fence = load('secure-postgres-fence')


class DockerHost:
    def __init__(self, catalog, project, runner=None):
        if not isinstance(catalog, fence.DockerCatalog):
            # Modules are often independently loaded by the Python adapters.
            if not all(hasattr(catalog, key) for key in ['container', 'user', 'database', 'sql']):
                raise ValueError('Explicit immutable PostgreSQL catalog channel required.')
        if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}', project):
            raise ValueError('Exact Compose project required.')
        self.catalog, self.project = catalog, project
        self.run = runner or subprocess.run
        self.binding = None
        self.path = None

    def docker(self, args):
        try:
            result = self.run(['docker', *args], text=True, capture_output=True, timeout=20, check=True)
            return json.loads(result.stdout)
        except (subprocess.SubprocessError, ValueError, OSError):
            raise ValueError('Docker maintenance inventory unavailable; keep admission blocked.') from None

    def server(self):
        return self.catalog.sql("SELECT json_build_object('path', current_setting('hba_file'), "
                                "'bytes', encode(pg_read_binary_file(current_setting('hba_file')), 'hex'), "
                                "'loaded', pg_conf_load_time()::text, "
                                "'errors', (SELECT count(*) FROM pg_hba_file_rules WHERE error IS NOT NULL))")

    def inspect(self):
        values = self.docker(['inspect', self.catalog.container])
        if not isinstance(values, list) or len(values) != 1:
            raise ValueError('One exact PostgreSQL container required.')
        value = values[0]
        config = value.get('Config', {}); host = value.get('HostConfig', {}); running = value.get('State', {})
        labels = config.get('Labels', {})
        if (value.get('Id') != self.catalog.container or running.get('Status') != 'running'
                or running.get('Paused') is not False or running.get('Restarting') is not False
                or host.get('Privileged') is not False or host.get('NetworkMode') == 'host'
                or host.get('CapAdd') or host.get('Devices')
                or (host.get('PidMode') == 'host' or str(host.get('PidMode', '')).startswith('container:'))
                or labels.get('com.docker.compose.project') != self.project
                or labels.get('com.docker.compose.service') != 'postgres'
                or not re.fullmatch(r'sha256:[a-f0-9]{64}', value.get('Image', ''))):
            raise ValueError('Exact unprivileged running Compose PostgreSQL identity required.')
        server = self.server()
        path = server.get('path')
        if (not isinstance(path, str) or not path.startswith('/') or posixpath.normpath(path) != path
                or pathlib.PurePosixPath(path).name != 'pg_hba.conf' or server.get('errors') != 0):
            raise ValueError('Canonical server HBA path without parse errors required.')
        mounts = []
        for mount in value.get('Mounts', []):
            dest = mount.get('Destination', '').rstrip('/')
            if dest and path.startswith(dest + '/'):
                mounts.append(mount)
        if len(mounts) != 1:
            raise ValueError('One unambiguous directory-mounted HBA volume required.')
        mount = mounts[0]
        if mount.get('Type') != 'volume' or mount.get('Driver') != 'local' or mount.get('RW') is not True:
            raise ValueError('Writable local Docker volume required; bind/remote/file mounts unsupported.')
        source = pathlib.Path(mount.get('Source', ''))
        if not source.is_absolute() or source.resolve() != source or not source.is_dir():
            raise ValueError('Canonical accessible local Docker volume source required.')
        relative = pathlib.PurePosixPath(path).relative_to(mount['Destination'])
        mapped = source.joinpath(*relative.parts)
        if mapped.resolve() != mapped or not mapped.is_file() or mapped.is_symlink():
            raise ValueError('Mapped host-auth file is missing, redirected or inaccessible.')
        try: database_bytes = bytes.fromhex(server['bytes'])
        except (ValueError, TypeError, KeyError): raise ValueError('Server host-auth bytes unavailable.') from None
        if mapped.read_bytes() != database_bytes:
            raise ValueError('Host volume and PostgreSQL disagree on host-auth bytes.')
        identity = {'container': value['Id'], 'image': value['Image'], 'started': running.get('StartedAt'),
                    'mount': {key: mount.get(key) for key in ['Type', 'Name', 'Source', 'Destination', 'Driver', 'RW']},
                    'path': path, 'host_path': str(mapped), 'project': self.project}
        if not identity['started']:
            raise ValueError('Exact PostgreSQL start identity required.')
        return identity, mapped, server

    def bind(self):
        identity, path, _ = self.inspect()
        if self.binding is not None:
            raise ValueError('Host inventory binding is immutable for this invocation.')
        self.binding, self.path = identity, path
        return path

    def check(self):
        if self.binding is None:
            raise ValueError('Bind exact Docker host volume first.')
        identity, path, _ = self.inspect()
        if identity != self.binding or path != self.path:
            raise ValueError('Docker container/start/image/volume/path changed; keep admission blocked.')

    def verify_peers(self, peers):
        """Exact immutable container IDs and network IPs, not names or DNS.

        peers include the HBA tuple plus container and network. The caller must
        independently bind these to the accepted candidate/previous service set.
        Docker identity alone does not grant an unrelated container permission.
        """
        self.check()
        if not isinstance(peers, list) or len(peers) > 16:
            raise ValueError('Bounded exact accepted peer inventory required.')
        if not peers: return
        for peer in peers:
            if (not isinstance(peer, dict) or set(peer) != {'container', 'network', 'address', 'user', 'ssl'}
                    or not re.fullmatch(r'[a-f0-9]{64}', peer.get('container', ''))):
                raise ValueError('Exact accepted container/network peer binding required.')
        ids = [self.catalog.container, *[peer['container'] for peer in peers]]
        if len(set(ids)) != len(ids): raise ValueError('Unique exact peer containers required.')
        # One fresh Docker snapshot for the entire group, including the already
        # server/volume-bound PG. Never cache across guards or external effects.
        values = self.docker(['inspect', *ids])
        if not isinstance(values,list) or len(values)!=len(ids):
            raise ValueError('Complete exact PostgreSQL and peer snapshot required.')
        found = {item.get('Id'):item for item in values}
        if len(found)!=len(ids) or set(found)!=set(ids):
            raise ValueError('Duplicate, missing or unrelated container in peer snapshot.')
        pg = found[self.catalog.container]
        if (pg.get('Image')!=self.binding['image']
                or pg.get('State',{}).get('StartedAt')!=self.binding['started']
                or pg.get('State',{}).get('Status')!='running'):
            raise ValueError('Bound PostgreSQL changed during grouped peer snapshot.')
        for peer in peers:
            item = found[peer['container']]; host = item.get('HostConfig', {}); labels = item.get('Config', {}).get('Labels', {})
            if (item.get('Id') != peer['container'] or item.get('State', {}).get('Status') != 'running'
                    or host.get('Privileged') is not False or host.get('NetworkMode') == 'host'
                    or host.get('CapAdd') or host.get('Devices')
                    or labels.get('com.docker.compose.project') != self.project
                    or labels.get('com.docker.compose.service') not in {'api', 'ingest', 'mcp'}):
                raise ValueError('Accepted application peer identity is not exact/running/unprivileged.')
            networks = item.get('NetworkSettings', {}).get('Networks', {})
            pg_networks = pg.get('NetworkSettings', {}).get('Networks', {})
            if (peer['network'] not in networks or peer['network'] not in pg_networks
                    or networks[peer['network']].get('IPAddress') != peer['address']
                    or not networks[peer['network']].get('NetworkID')
                    or networks[peer['network']].get('NetworkID') != pg_networks[peer['network']].get('NetworkID')):
                raise ValueError('Accepted application peer address/network changed.')

    def reload_and_verify(self, expected, prove_connections):
        if not callable(prove_connections):
            raise ValueError('Fresh positive/negative connection proofs required.')
        self.check(); before = self.server()
        if before['bytes'] != expected.hex():
            raise ValueError('Server does not see the staged exact host-auth bytes.')
        answer = self.catalog.sql("SELECT json_build_object('reload',pg_reload_conf())")
        if answer != {'reload': True}:
            raise ValueError('Host-auth reload was not acknowledged.')
        until = time.monotonic() + 5
        while True:
            self.check(); after = self.server()
            if after['bytes'] != expected.hex() or after['errors']:
                raise ValueError('Host-auth changed or contains parse errors after reload.')
            if after['loaded'] != before['loaded']: break
            if time.monotonic() >= until:
                raise ValueError('PostgreSQL did not observe the host-auth reload.')
            time.sleep(.05)
        if prove_connections(expected) is not None:
            raise ValueError('Fresh connection proof must raise on failure, not return approval.')
        self.check()
