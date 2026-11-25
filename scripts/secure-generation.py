#!/usr/bin/env python3
"""Durable handoff between settled application generations. No CLI or effects.

Coordinator must retain its OS lock, trusted review, and fresh maintenance checks.
An intent blocks ordinary selection until explicit, verified settlement. Evidence
is immutable and never pruned. Provider-specific state rollover is a separate
required capability, not inferred from an application receipt.
"""
import importlib.util
import pathlib
import re
import os
import tempfile

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('generation_state', HERE / 'secure-state.py')
state = importlib.util.module_from_spec(spec)
spec.loader.exec_module(state)


def exists(path):
    return path.exists() or path.is_symlink()


class Generation:
    def __init__(self, journal):
        journal.locked()
        self.journal = journal
        self.root = journal.root
        self.directory = journal.directory
        self.intent = self.directory / 'generation-intent.json'

    def guard(self):
        self.journal.locked()

    def selected(self):
        self.guard()
        if exists(self.intent):
            raise ValueError('Generation handoff needs explicit recovery; no selection.')
        return state.selection(self.root)

    @staticmethod
    def immutable(path, raw):
        # Resuming a pre-intent crash completes missing evidence, never replaces
        # it. Preserve exact bytes, including a noncanonical receipt encoding.
        if exists(path):
            state.read_private(path)
            if path.read_bytes() != raw:
                raise ValueError('Immutable generation evidence differs.')
        else:
            fd, name = tempfile.mkstemp(prefix='.evidence-', dir=path.parent)
            try:
                with os.fdopen(fd, 'wb') as stream:
                    stream.write(raw)
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(name, path)
                state.sync_directory(path.parent)
            finally:
                if os.path.exists(name):
                    os.unlink(name)

    def prepare(self, project, receipt):
        self.guard()
        selected = self.selected()
        if selected is None or selected['project'] != project:
            raise ValueError('Exact settled predecessor selection required.')
        if exists(self.directory / 'maintenance.json'):
            raise ValueError('Earlier maintenance must be recovered.')
        predecessor = self.journal.read()
        if predecessor.get('maintenance_state', 'released') != 'released':
            raise ValueError('Predecessor maintenance is not settled.')
        receipt = pathlib.Path(receipt).absolute()
        plan = state.read_private(receipt)
        if (plan.get('version') != 1 or plan.get('scope') != 'application_bootstrap'
                or plan.get('root') != str(self.root) or plan.get('project') != project
                or plan.get('status') != 'preflight_only' or plan.get('activation_allowed') is not False):
            raise ValueError('Exact application plan required, not approval.')
        state.check_manifest(plan.get('files'))
        state.check_manifest(plan.get('previous_files'))
        old = receipt.parent / 'previous-runtime.json'
        new = receipt.parent / 'candidate-runtime.json'
        for path in [old, new]:
            state.checked_config(path, plan['files'].get(str(path)))
        if plan['previous_files'].get(str(old)) != state.digest(old):
            raise ValueError('Previous runtime manifest is incomplete.')
        # The full selected manifest remains bound in the predecessor archive.
        # Snapshot paths may differ; their exact runtime content may not.
        if state.read_private(old) != state.read_private(selected['config']):
            raise ValueError('Plan prior runtime differs from selected predecessor.')
        images = {name: item['image'] for name, item in plan['current_services'].items()}
        if (not {'api', 'ingest', 'mosquitto', 'web', 'postgres'} <= set(images)
                or any(not isinstance(image, str) or not re.fullmatch(state.IMAGE, image) for image in images.values())):
            raise ValueError('Exact complete immutable existing images required.')
        oldconfig = state.read_private(old)
        if images != {name: service['image'] for name, service in oldconfig['services'].items()}:
            raise ValueError('Plan images differ from predecessor runtime.')
        anchor = state.digest(self.directory / 'journal.json')
        receipt_hash = state.digest(receipt)
        name = 'generation-' + anchor + '-' + receipt_hash
        archive = self.directory / name
        successor = {'version': 1, 'kind': 'application_bootstrap', 'phase': 'prepared',
                     'project': project, 'previous': str(old), 'previous_sha256': state.digest(old),
                     'candidate': str(new), 'candidate_sha256': state.digest(new),
                     'previous_images': images, 'files': plan['files'], 'previous_files': plan['previous_files'],
                     'receipt_sha256': receipt_hash, 'maintenance_state': 'held', 'generation': name}
        # Archive before intent. A pre-intent crash has made no runtime change;
        # activation may safely complete these same immutable files on retry.
        if not exists(archive):
            archive.mkdir(mode=0o700)
        if archive.is_symlink() or not archive.is_dir() or archive.stat().st_mode & 0o077:
            raise ValueError('Private real generation archive required.')
        state.sync_directory(self.directory)
        if exists(archive / 'settled.json'):
            raise ValueError('This generation was already settled; prepare a fresh explicit plan.')
        for filename, source in [('journal.json', self.directory / 'journal.json'),
                                 ('selected.json', self.directory / 'selected.json'), ('receipt.json', receipt)]:
            self.immutable(archive / filename, source.read_bytes())
        if exists(archive / 'successor.json'):
            if state.read_private(archive / 'successor.json') != successor:
                raise ValueError('Immutable successor differs.')
        else:
            state.atomic(archive / 'successor.json', successor)
        value = {'version': 1, 'project': project, 'root': str(self.root), 'receipt': str(receipt),
                 'receipt_sha256': receipt_hash, 'archive': str(archive), 'predecessor_journal': anchor,
                 'predecessor_selector': state.digest(self.directory / 'selected.json'),
                 'successor_sha256': state.digest(archive / 'successor.json')}
        state.atomic(self.intent, value)
        return self.inspect()

    def inspect(self):
        self.guard()
        value = state.read_private(self.intent)
        if value.get('version') != 1 or value.get('root') != str(self.root):
            raise ValueError('Invalid generation intent.')
        archive = pathlib.Path(value['archive'])
        name = 'generation-' + value['predecessor_journal'] + '-' + value['receipt_sha256']
        if (archive.parent != self.directory or archive.name != name or archive.is_symlink()
                or not archive.is_dir() or archive.stat().st_mode & 0o077):
            raise ValueError('Invalid generation archive.')
        for filename in ['journal.json', 'selected.json', 'successor.json', 'receipt.json']:
            state.read_private(archive / filename)
        if (state.digest(archive / 'journal.json') != value['predecessor_journal']
                or state.digest(archive / 'selected.json') != value['predecessor_selector']
                or state.digest(archive / 'successor.json') != value['successor_sha256']
                or state.digest(archive / 'receipt.json') != value['receipt_sha256']):
            raise ValueError('Immutable generation evidence changed.')
        state.checked_config(value['receipt'], value['receipt_sha256'])
        predecessor_selector = state.read_private(archive / 'selected.json')
        state.check_manifest(predecessor_selector['files'])
        state.checked_config(predecessor_selector['config'], predecessor_selector['sha256'])
        successor = state.read_private(archive / 'successor.json')
        state.check_manifest(successor['files'])
        state.check_manifest(successor['previous_files'])
        active = self.journal.read()
        if state.digest(self.directory / 'journal.json') == value['predecessor_journal']:
            if state.digest(self.directory / 'selected.json') != value['predecessor_selector']:
                raise ValueError('Pre-change selector changed.')
            stage = 'predecessor_held'
        else:
            fixed = {key: item for key, item in successor.items() if key not in {'phase', 'maintenance_state'}}
            if (any(active.get(key) != item for key, item in fixed.items())
                    or active.get('maintenance_state') not in {'held', 'released'}):
                raise ValueError('Active generation changed; retain block.')
            stage = 'successor_prepared' if active['phase'] == 'prepared' else 'successor_active'
        return {**value, 'state': stage}

    def predecessor_selection(self):
        value = self.inspect()
        if value['state'] != 'predecessor_held':
            raise ValueError('Pre-change inspection cannot run after successor publication.')
        return state.read_private(pathlib.Path(value['archive']) / 'selected.json')

    def publish(self, verify_maintenance):
        self.guard()
        value = self.inspect()
        if not callable(verify_maintenance):
            raise ValueError('Fresh maintenance verifier required.')
        if verify_maintenance(value) is not None:
            raise ValueError('Maintenance verifier must raise, not return an approval flag.')
        value = self.inspect()
        if value['state'] != 'predecessor_held':
            raise ValueError('Successor already published; explicit recovery required.')
        state.atomic(self.directory / 'journal.json',
                     state.read_private(pathlib.Path(value['archive']) / 'successor.json'))
        # Intent remains through backup, all effects, acceptance, and release.
        return self.inspect()

    def settle(self, verify_runtime):
        value = self.inspect()
        if not callable(verify_runtime) or verify_runtime(value) is not None:
            raise ValueError('Explicit fresh runtime/release verifier required.')
        value = self.inspect()
        active = self.journal.read()
        if active['phase'] not in {'committed', 'restored'} or active.get('maintenance_state', 'released') != 'released':
            raise ValueError('Only a settled runtime can finish a generation.')
        # Caller still owns the durable maintenance marker. Keep intent through
        # marker removal, so process loss cannot silently reuse this generation.
        marker = state.read_private(self.directory / 'maintenance.json')
        if marker.get('receipt_sha256') != value['receipt_sha256']:
            raise ValueError('Held maintenance marker differs from generation.')
        settled = {'version': 1, 'generation': pathlib.Path(value['archive']).name,
                   'receipt_sha256': value['receipt_sha256'],
                   'journal_sha256': state.digest(self.directory / 'journal.json'),
                   'selector_sha256': state.digest(self.directory / 'selected.json')}
        # Release recovery may run after this write. Evidence of an earlier
        # settlement is retained, never interpreted as fresh runtime acceptance.
        destination = pathlib.Path(value['archive']) / 'settled.json'
        if exists(destination):
            if state.read_private(destination) != settled:
                raise ValueError('Previously settled generation differs from current state.')
        else:
            state.atomic(destination, settled)
        return value

    def finish(self):
        value = self.inspect()
        active = self.journal.read()
        if (active['phase'] not in {'committed', 'restored'}
                or active.get('maintenance_state', 'released') != 'released'
                or exists(self.directory / 'maintenance.json')):
            raise ValueError('Verified release must precede final intent removal.')
        settled = state.read_private(pathlib.Path(value['archive']) / 'settled.json')
        if (settled.get('journal_sha256') != state.digest(self.directory / 'journal.json')
                or settled.get('selector_sha256') != state.digest(self.directory / 'selected.json')):
            raise ValueError('Runtime journal/selection changed after verified settlement.')
        selected = state.read_private(self.directory / 'selected.json')
        slot = 'previous' if active['phase'] == 'restored' else 'candidate'
        manifest = active['previous_files' if active['phase'] == 'restored' else 'files']
        if (selected.get('version') != 1 or selected.get('project') != active['project']
                or selected.get('config') != active[slot] or selected.get('sha256') != active[slot + '_sha256']
                or selected.get('files') != manifest):
            raise ValueError('Settled selector differs from active journal.')
        state.check_manifest(manifest)
        state.checked_config(selected['config'], selected['sha256'])
        state.unlink_durable(self.intent)
