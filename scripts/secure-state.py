#!/usr/bin/env python3
"""Private durable deployment journal and verified Compose selector.

No Docker calls and no activation CLI. An operational executor must still own
approval, current-state checks, service change, rollback and runtime acceptance.
A journal or preflight receipt is evidence, never permission to change services.
"""
import fcntl
import hashlib
import json
import os
import pathlib
import re
import tempfile

PHASES = {'prepared', 'changing', 'verified', 'rollback_required', 'rolled_back', 'committed', 'restored'}
PROJECT = r'[a-z0-9][a-z0-9_-]{0,62}'
IMAGE = r'sha256:[a-f0-9]{64}'


def digest(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def atomic(path, value):
    path = pathlib.Path(path)
    fd, tmp = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(value, output, sort_keys=True, indent=2)
            output.flush()
            os.fsync(output.fileno())
        os.replace(tmp, path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def unlink_durable(path):
    path = pathlib.Path(path)
    if path.exists() or path.is_symlink():
        path.unlink()
    sync_directory(path.parent)


def read_private(path):
    path = pathlib.Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_mode & 0o077:
        raise ValueError('Deployment state must be a regular private file; refuse lab fallback.')
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError('Invalid deployment state; refuse lab fallback.')
    return value


def checked_config(path, want):
    path = pathlib.Path(path)
    if (path.is_symlink() or not path.is_absolute() or not path.is_file()
            or path.stat().st_mode & 0o077 or digest(path) != want):
        raise ValueError('Selected configuration changed or is unavailable; refuse lab fallback.')
    return path


def check_manifest(files):
    if not isinstance(files, dict) or not files:
        raise ValueError('Exact deployment input manifest required.')
    for name, want in files.items():
        path = pathlib.Path(name)
        if (not path.is_absolute() or path.is_symlink() or not path.is_file()
                or path.stat().st_mode & 0o022 or digest(path) != want):
            raise ValueError('Deployment certificate or configuration changed; refuse lab fallback.')


class Journal:
    def __init__(self, root):
        self.root = pathlib.Path(root).resolve()
        self.directory = self.root / '.secure-deployment'
        self.fd = None

    def __enter__(self):
        self.directory.mkdir(mode=0o700, exist_ok=True)
        if self.directory.is_symlink() or self.directory.stat().st_mode & 0o077:
            raise ValueError('Private deployment state directory required.')
        sync_directory(self.root)
        self.fd = os.open(self.directory / 'lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
        try:
            fcntl.flock(self.fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            os.close(self.fd)
            self.fd = None
            raise ValueError('Another deployment operation is running.')
        return self

    def __exit__(self, *args):
        if self.fd is not None:
            fcntl.flock(self.fd, fcntl.LOCK_UN)
            os.close(self.fd)
            self.fd = None

    def locked(self):
        if self.fd is None:
            raise ValueError('Deployment journal must hold its OS lock.')

    def read(self):
        self.locked()
        value = read_private(self.directory / 'journal.json')
        if value.get('version') != 1 or value.get('phase') not in PHASES:
            raise ValueError('Invalid deployment journal; explicit recovery required.')
        return value

    def begin(self, project, previous, candidate, images, receipt, kind="transport_only"):
        self.locked()
        if kind not in {'transport_only', 'application_bootstrap'}:
            raise ValueError('Explicit supported transaction kind required.')
        for name in ['journal.json', 'selected.json']:
            path = self.directory / name
            if path.exists() or path.is_symlink():
                raise ValueError('Existing deployment state needs explicit settlement; no new activation.')
        if not re.fullmatch(PROJECT, project):
            raise ValueError('Explicit Compose project required.')
        if (not {'api', 'ingest', 'mosquitto', 'web', 'postgres'} <= set(images)
                or any(not re.fullmatch(IMAGE, item) for item in images.values())):
            raise ValueError('Exact current immutable image IDs required.')
        old, new = pathlib.Path(previous).absolute(), pathlib.Path(candidate).absolute()
        preflight = read_private(receipt)
        if (preflight.get('version') != 1 or preflight.get('status') != 'preflight_only'
                or preflight.get('activation_allowed') is not False
                or preflight.get('project') != project or preflight.get('root') != str(self.root)):
            raise ValueError('Matching private preflight evidence required. It is not approval.')
        manifest = preflight.get('files')
        check_manifest(manifest)
        if manifest.get(str(old)) != digest(old) or manifest.get(str(new)) != digest(new):
            raise ValueError('Preflight does not bind both deployment configurations.')
        recorded = {name: item['image'] for name, item in preflight['current_services'].items()}
        if recorded != images:
            raise ValueError('Rollback image IDs do not match preflight evidence.')
        checked_config(old, digest(old))
        checked_config(new, digest(new))
        value = {'version': 1, 'kind': kind, 'phase': 'prepared', 'project': project,
                 'previous': str(old), 'previous_sha256': digest(old),
                 'candidate': str(new), 'candidate_sha256': digest(new),
                 'previous_images': images, 'files': manifest, 'previous_files':preflight.get('previous_files',{}), 'receipt_sha256': digest(receipt)}
        atomic(self.directory / 'journal.json', value)
        return value

    def record_backup(self, backup):
        value=self.read()
        if value['phase']!='prepared':raise ValueError('Backup must be recorded before any service change.')
        value['backup']=backup;value['schema_upgrade']=value.get('kind')=='application_bootstrap'
        atomic(self.directory/'journal.json',value)
        return value

    def record_schema_attempt(self, migrations):
        value = self.read()
        if value.get('kind') != 'application_bootstrap' or value['phase'] != 'changing' or value.get('schema_attempted'):
            raise ValueError('Reviewed schema attempt requires a changing application transaction.')
        value['schema_attempted'] = list(migrations)
        atomic(self.directory/'journal.json', value)
        return value

    def record_schema_result(self, result):
        value = self.read()
        if value.get('kind') != 'application_bootstrap' or value['phase'] != 'changing' or not value.get('schema_attempted'):
            raise ValueError('No recorded reviewed schema attempt.')
        value['schema_result'] = result
        atomic(self.directory/'journal.json', value)
        return value

    def select_restored(self):
        # Caller must have accepted the exact previous runtime first.
        value=self.read()
        if value['phase']!='rolled_back':raise ValueError('Rollback has not been settled.')
        manifest=value.get('previous_files')
        check_manifest(manifest)
        checked_config(value['previous'],value['previous_sha256'])
        if manifest.get(value['previous'])!=value['previous_sha256']:raise ValueError('Prior runtime manifest is incomplete.')
        selected={'version':1,'project':value['project'],'config':value['previous'],'sha256':value['previous_sha256'],'files':manifest}
        # rolled_back remains blocked if interrupted before restored mark.
        atomic(self.directory/'selected.json',selected)
        value['phase']='restored';atomic(self.directory/'journal.json',value)

    def transition(self, phase):
        value = self.read()
        allowed = {'prepared': {'changing'}, 'changing': {'verified', 'rollback_required'},
                   'verified': {'rollback_required'}}
        if phase not in allowed.get(value['phase'], set()):
            raise ValueError('Invalid deployment phase transition.')
        # Failure recording must remain possible when candidate inputs were damaged.
        if phase != 'rollback_required':
            check_manifest(value['files'])
            checked_config(value['previous'], value['previous_sha256'])
            checked_config(value['candidate'], value['candidate_sha256'])
        value['phase'] = phase
        atomic(self.directory / 'journal.json', value)
        return value

    def commit(self):
        value = self.read()
        if value['phase'] != 'verified':
            raise ValueError('Runtime acceptance must pass before selecting deployment.')
        if value.get('kind') == 'application_bootstrap':
            schema = value.get('schema_result', {})
            if (not value.get('schema_attempted') or schema.get('version') != 1
                    or schema.get('status') != 'exact_reviewed_additive_93'
                    or schema.get('runtime_accepted') is not False):
                raise ValueError('Application schema result missing; no deployment selection.')
        check_manifest(value['files'])
        checked_config(value['candidate'], value['candidate_sha256'])
        selected = {'version': 1, 'project': value['project'], 'config': value['candidate'],
                    'sha256': value['candidate_sha256'], 'files': value['files']}
        # Crash after selection but before commit remains blocked by journal phase.
        atomic(self.directory / 'selected.json', selected)
        value['phase'] = 'committed'
        atomic(self.directory / 'journal.json', value)

    def finish_rollback(self):
        # An executor must check the restored runtime first. This does no restore.
        value = self.read()
        if value['phase'] not in {'prepared', 'changing', 'verified', 'rollback_required'}:
            raise ValueError('No interrupted transaction to settle.')
        checked_config(value['previous'], value['previous_sha256'])
        unlink_durable(self.directory / 'selected.json')
        value['phase'] = 'rolled_back'
        atomic(self.directory / 'journal.json', value)

    def recovery_plan(self):
        value = self.read()
        if value['phase'] in {'committed', 'restored'}:
            return {'action': 'already_' + value['phase']}
        checked_config(value['previous'], value['previous_sha256'])
        return {'action': 'explicit_rollback_required', 'project': value['project'],
                'config': value['previous'], 'images': value['previous_images'],
                'may_change_services': True}


def selection(root):
    directory = pathlib.Path(root).resolve() / '.secure-deployment'
    if not directory.exists() and not directory.is_symlink():
        return None
    if directory.is_symlink() or directory.stat().st_mode & 0o077:
        raise ValueError('Private deployment state invalid; refuse lab fallback.')
    maintenance = directory / 'maintenance.json'
    if maintenance.exists() or maintenance.is_symlink():
        raise ValueError('Maintenance session needs explicit recovery settlement. No lab fallback.')
    journal, selected = directory / 'journal.json', directory / 'selected.json'
    state = None
    if journal.exists() or journal.is_symlink():
        state = read_private(journal)
        if state.get('version') != 1 or state.get('phase') not in {'committed','restored'}:
            raise ValueError('Interrupted secure deployment requires explicit recovery. No lab fallback.')
        if state.get('maintenance_state', 'released') != 'released':
            raise ValueError('Maintenance protection has not been settled; explicit recovery required. No lab fallback.')
    if not selected.exists() and not selected.is_symlink():
        if state and state['phase'] in {'committed','restored'}:
            raise ValueError('Committed selection missing; refuse lab fallback.')
        return None
    value = read_private(selected)
    if not state or state['phase'] not in {'committed','restored'}:
        raise ValueError('Selection has no committed transaction; refuse lab fallback.')
    if (value.get('version') != 1 or not re.fullmatch(PROJECT, value.get('project', ''))
            or value['project'] != state['project']
            or value['config'] != state['previous' if state['phase']=='restored' else 'candidate']
            or value['sha256'] != state['previous_sha256' if state['phase']=='restored' else 'candidate_sha256']
            or value['files'] != state['previous_files' if state['phase']=='restored' else 'files']):
        raise ValueError('Invalid deployment selector; refuse lab fallback.')
    check_manifest(value['files'])
    checked_config(value['config'], value['sha256'])
    return value
