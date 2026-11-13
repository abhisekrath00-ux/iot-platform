#!/usr/bin/env python3
"""Internal process-before-DB gate for exact running Docker peer handoff.

No owner CLI or automatic HBA opening. The host caller owns authority, stopped
writers, closed admission, exact accepted peer proofs and final runtime checks.
The gate solves dynamic IP assignment without letting the application attempt
its first DB connection before the caller has admitted that actual container.
"""
import copy
import importlib.util
import json
import os
import pathlib
import re
import stat
import subprocess
import uuid

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module

state = load('secure-state')
adapter = load('secure-application-docker')
TARGET = '/run/hexthings-startgate'
LABEL = 'hexthings.startgate'
# No eval, shell interpolation of application arguments or network operation.
# The bounded timeout fails closed. The marker survives an accepted reboot.
SCRIPT = '''token=$1
shift
n=0
while [ "$n" -lt 300 ]; do
  if [ "$(cat /run/hexthings-startgate/release 2>/dev/null)" = "$token" ]; then
    exec "$@"
  fi
  n=$((n+1))
  sleep 1
done
printf '%s\\n' 'Application admission gate timed out; maintenance remains blocked.' >&2
exit 75
'''


class StartGate:
    def __init__(self, journal, project, name, service, guard, slot='application'):
        if (not callable(guard) or not re.fullmatch(state.PROJECT, project) or name not in {'api', 'ingest', 'mcp'}
                or slot not in {'application','candidate','previous','compatibility'}):
            raise ValueError('Exact application service/project and live admission guard required.')
        if (not isinstance(service, dict) or service.get('command') != ['/bin/' + name]
                or service.get('entrypoint') not in [None, []]
                or not re.fullmatch(state.IMAGE, service.get('image', ''))):
            raise ValueError('Only exact immutable direct application binary definitions support process gating.')
        for mount in service.get('volumes', []):
            if (mount.get('type') != 'bind' or mount.get('read_only') is not True
                    or not pathlib.Path(mount.get('source', '')).is_absolute()
                    or not isinstance(mount.get('target'), str)
                    or mount['target'].startswith(TARGET)):
                raise ValueError('Only existing exact read-only file binds are supported alongside the admission gate.')
        self.journal, self.project, self.name, self.base, self.guard = journal, project, name, copy.deepcopy(service), guard
        self.slot = slot
        self.path = journal.directory / ('startgate-' + slot + '-' + name + '.json')

    def check(self):
        self.journal.locked()
        binding = state.read_private(self.journal.directory / 'maintenance.json')
        if binding.get('root') != str(self.journal.root) or self.guard() is not None:
            raise ValueError('Held matching admission and a raising live guard are required.')
        return binding

    def record(self):
        binding = self.check(); value = state.read_private(self.path)
        if (set(value) != {'version','binding','project','name','service','token','directory','stage','container','slot'}
                or value['version'] != 1 or value['binding'] != binding or value['project'] != self.project
                or value['name'] != self.name or value['service'] != self.base or value['slot'] != self.slot
                or not re.fullmatch(r'[a-f0-9]{32}', value.get('token', ''))
                or value['directory'] != str(self.journal.directory / ('startgate-' + self.slot + '-' + self.name + '-' + value['token']))
                or value['stage'] not in {'preparing','held','releasing','released'}
                or (value['container'] is not None and not re.fullmatch(r'[a-f0-9]{64}', value['container']))):
            raise ValueError('Exact durable application-gate scope required; keep admission held.')
        return value

    def directory(self, value):
        path = pathlib.Path(value['directory'])
        info = path.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o755 or info.st_uid != os.geteuid():
            raise ValueError('Exact private-parent application-gate directory required.')
        entries = list(path.iterdir())
        if any(item.name != 'release' for item in entries):
            raise ValueError('Unknown application-gate file; keep admission held.')
        if entries:
            info = entries[0].lstat()
            if (not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o444
                    or info.st_uid != os.geteuid() or entries[0].read_text() != value['token']):
                raise ValueError('Changed application-gate marker; keep admission held.')
        return path, bool(entries)

    def prepare(self):
        binding = self.check()
        if self.path.exists() or self.path.is_symlink():
            raise ValueError('Existing application gate requires exact fresh recovery, not replacement.')
        token = uuid.uuid4().hex
        value = {'version':1,'binding':binding,'project':self.project,'name':self.name,'service':self.base,
                 'token':token,'directory':str(self.journal.directory / ('startgate-' + self.slot + '-' + self.name + '-' + token)),
                 'stage':'preparing','container':None,'slot':self.slot}
        state.atomic(self.path, value)  # before mkdir; interrupted creation remains recoverable
        path = pathlib.Path(value['directory']); path.mkdir(mode=0o755); path.chmod(0o755); state.sync_directory(path.parent)
        self.directory(value); value['stage'] = 'held'; state.atomic(self.path, value)
        return self.definition()

    def recover_preparing(self):
        value = self.record()
        if value['stage'] != 'preparing' or value['container'] is not None:
            raise ValueError('Only pre-launch directory preparation can be recovered here.')
        path = pathlib.Path(value['directory'])
        if not path.exists() and not path.is_symlink():
            path.mkdir(mode=0o755); path.chmod(0o755); state.sync_directory(path.parent)
        _, released = self.directory(value)
        if released: raise ValueError('Unexpected release before application launch.')
        value['stage'] = 'held'; state.atomic(self.path, value)
        return self.definition()

    def definition(self):
        value = self.record(); _, released = self.directory(value)
        if (value['stage'] == 'preparing' or (value['stage'] == 'held' and released)
                or (value['stage'] == 'released' and not released)):
            raise ValueError('Incomplete or changed application-gate preparation.')
        result = copy.deepcopy(self.base)
        if any(key.startswith('PG') for key in result.get('environment', {})):
            raise ValueError('Application writer libpq overrides require separate review.')
        labels = result.setdefault('labels', {})
        if not isinstance(labels, dict) or LABEL in labels:
            raise ValueError('Existing gate labels cannot grant application admission.')
        labels[LABEL] = value['token']
        result['entrypoint'] = []
        result['command'] = ['/bin/sh','-c',SCRIPT,'hexthings-startgate',value['token'],*self.base['command']]
        result.setdefault('volumes', []).append({'type':'bind','source':value['directory'],'target':TARGET,'read_only':True,
                                               'bind':{'create_host_path':False}})
        return result

    def verify(self, container, image, statuses=('running',)):
        value = self.record(); definition = self.definition()
        adapter.verify_container_definition(definition, container, image)
        config = container.get('Config', {}); host = container.get('HostConfig', {}); labels = config.get('Labels', {})
        if (not re.fullmatch(r'[a-f0-9]{64}', container.get('Id', '')) or container.get('Image') != self.base['image']
                or labels.get('com.docker.compose.project') != self.project
                or labels.get('com.docker.compose.service') != self.name or labels.get(LABEL) != value['token']
                or container.get('State', {}).get('Status') not in statuses
                or container.get('State', {}).get('Paused') is not False
                or container.get('State', {}).get('Restarting') is not False
                or host.get('CapAdd') or host.get('Devices') or host.get('PidMode') in {'host'}
                or str(host.get('PidMode', '')).startswith('container:')
                or any(key.startswith('PG') for key in dict(item.split('=',1) for item in config.get('Env', [])))):
            raise ValueError('Exact running unprivileged application-gate identity required.')
        if value['container'] is not None and container['Id'] != value['container']:
            raise ValueError('Previously bound application-gate container changed.')
        expected = {(item['source'],item['target'],False) for item in definition['volumes']}
        mounts = container.get('Mounts', [])
        actual = {(item.get('Source'),item.get('Destination'),item.get('RW')) for item in mounts}
        if len(mounts) != len(expected) or actual != expected or any(item.get('Type') != 'bind' for item in mounts):
            raise ValueError('Exact read-only application-gate and original bind inventory required.')
        return value

    def bind(self, container, image):
        value = self.verify(container, image)
        if value['stage'] != 'held': raise ValueError('Only a held application can bind before peer admission.')
        value['container'] = container['Id']; state.atomic(self.path, value)

    def bind_created(self, container, image):
        # Persist immutable CID before docker start can assign a dynamic IP or
        # lose its acknowledgement. This does not admit any network endpoint.
        value = self.verify(container, image, statuses=('created',))
        if value['stage'] != 'held': raise ValueError('Only an unreleased created application can bind.')
        value['container'] = container['Id']; state.atomic(self.path, value)

    def retire(self, inspect):
        # A new candidate/previous/probe attempt needs a new nonce and mount,
        # never reuse a released marker after stopping/draining that process.
        # Caller guard must prove maintenance-only admission before this step.
        if not callable(inspect): raise ValueError('Fresh stopped exact container inspection required.')
        container, image = inspect()
        value = self.verify(container, image, statuses=('created','exited'))
        if value['container'] is None: raise ValueError('Unbound interrupted gate cannot be silently retired.')
        destination = self.path.with_name(self.path.stem + '-' + value['token'] + '-retired.json')
        if destination.exists() or destination.is_symlink(): raise ValueError('Existing retired-gate evidence cannot be replaced.')
        os.rename(self.path, destination); state.sync_directory(self.path.parent)
        # Preserve marker/directory as private-parent diagnostic evidence.
        return str(destination)

    def release(self, inspect, admit):
        # inspect returns fresh actual immutable container + image. admit must
        # establish exact IP/user HBA plus fresh positive/negative connections.
        # It runs again after a lost marker acknowledgement. Never reset marker.
        if not callable(inspect) or not callable(admit): raise ValueError('Fresh inventory and exact peer-admission callbacks required.')
        container, image = inspect(); value = self.verify(container, image)
        if value['container'] is None: raise ValueError('Bind actual gated container before peer admission.')
        if admit(container) is not None: raise ValueError('Peer admission proof must raise on failure, not return approval.')
        container, image = inspect(); value = self.verify(container, image)
        if value['stage'] not in {'held','releasing','released'}: raise ValueError('Application gate is not ready for release.')
        path, present = self.directory(value)
        if value['stage'] == 'held' and present: raise ValueError('Unrecorded application release refused.')
        if not present:
            value['stage'] = 'releasing'; state.atomic(self.path, value)
            # Exclusive creation never follows or replaces an unknown file.
            fd = os.open(path / 'release', os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o444)
            with os.fdopen(fd, 'w') as output:
                output.write(value['token']); output.flush(); os.fsync(output.fileno())
            state.sync_directory(path)
        value['stage'] = 'released'; state.atomic(self.path, value)
        self.verify(*inspect())
