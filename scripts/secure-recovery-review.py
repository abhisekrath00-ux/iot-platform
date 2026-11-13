#!/usr/bin/env python3
"""Read-only owner recovery review. No Docker, signing or service changes.

Evidence is never authority. Review cannot settle the journal, select a runtime,
restore a database or claim current runtime/retained-data acceptance.
"""
import argparse
import contextlib
import fcntl
import importlib.util
import json
import os
import pathlib
import re
import stat
import sys
import textwrap

# A read-only review must not create __pycache__ in the installation.
sys.dont_write_bytecode = True
HERE = pathlib.Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


state = load('secure-state')
transaction = load('secure-transaction')


@contextlib.contextmanager
def existing_lock(directory):
    """Share the execution lock without creating a directory, file or journal."""
    if directory.is_symlink() or not directory.is_dir() or directory.stat().st_mode & 0o077:
        raise ValueError('Private deployment state is unavailable. Keep setup blocked.')
    fd = os.open(directory / 'lock', os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_nlink != 1:
            raise ValueError('Private deployment lock is invalid. Keep setup blocked.')
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            raise ValueError('Another setup operation is running. Wait before reviewing.') from error
        yield
    finally:
        os.close(fd)


def exists(path):
    return path.exists() or path.is_symlink()


def inspect(root):
    root = pathlib.Path(root).resolve()
    directory = root / '.secure-deployment'
    result = {'version': 1, 'review_only': True, 'changes_allowed': False,
              'live_runtime_verified': False, 'source_reading_proven': False,
              'normal_actions_blocked': False, 'state': 'not_started'}
    if not exists(directory):
        return result
    with existing_lock(directory):
        journal = directory / 'journal.json'
        marker = directory / 'maintenance.json'
        admission = None
        if exists(marker):
            admission = state.read_private(marker)
            if (admission.get('version') != 1 or admission.get('root') != str(root)
                    or admission.get('scope') not in {'application_bootstrap', 'transport_only'}
                    or not isinstance(admission.get('project'), str)
                    or not re.fullmatch(state.PROJECT, admission['project'])):
                raise ValueError('Invalid pending maintenance session.')
            receipt = pathlib.Path(admission['receipt'])
            state.checked_config(receipt, admission['receipt_sha256'])
        if not exists(journal):
            if exists(directory / 'selected.json'):
                raise ValueError('Selection has no transaction record. Keep setup blocked.')
            if admission:
                result.update(state='maintenance_admission_pending', normal_actions_blocked=True,
                              scope=admission['scope'], project=admission['project'],
                              next_checks=['Recover the interrupted maintenance admission first.',
                                           'Verify the exact previous runtime before releasing protection.',
                                           'Do not assume the external-writer fence was never changed.'])
            else:
                result['state'] = 'no_transaction'
            return result
        value = state.read_private(journal)
        if (value.get('version') != 1 or value.get('phase') not in state.PHASES
                or value.get('kind', 'transport_only') not in {'transport_only', 'application_bootstrap'}
                or not isinstance(value.get('project'), str)
                or not re.fullmatch(state.PROJECT, value['project'])):
            raise ValueError('Transaction record is invalid. Keep setup blocked.')
        phase = value['phase']
        kind = value.get('kind', 'transport_only')
        result.update(state=phase, scope=kind, project=value['project'])
        maintenance = 'held' if admission else value.get('maintenance_state', 'released')
        if maintenance not in {'held', 'released'}:
            raise ValueError('Unknown maintenance state. Keep setup blocked.')
        if phase in {'committed', 'restored'} and maintenance == 'released':
            state.selection(root)
            result['selection_files_verified'] = True
            result['next_checks'] = ['Check current runtime health and retained data separately.']
            return result
        result['normal_actions_blocked'] = True
        if phase in {'committed', 'restored'} and maintenance == 'held':
            result['state'] = 'maintenance_release_pending'
            result['recorded_phase'] = phase
        # Recovery must bind an exact rollback runtime and all of its retained
        # dependencies, not trust the presence of a journal or backup filename.
        previous = value.get('previous')
        if not isinstance(previous, str):
            raise ValueError('Previous runtime is missing. Keep setup blocked.')
        state.checked_config(previous, value.get('previous_sha256'))
        files = value.get('previous_files')
        state.check_manifest(files)
        if files.get(previous) != value.get('previous_sha256'):
            raise ValueError('Previous runtime manifest is incomplete. Keep setup blocked.')
        images = value.get('previous_images')
        old = state.read_private(previous)
        if (not isinstance(images, dict) or not {'api', 'ingest', 'mosquitto', 'web', 'postgres'} <= set(images)
                or any(not isinstance(image, str) or not re.fullmatch(state.IMAGE, image) for image in images.values())
                or {name: item['image'] for name, item in old['services'].items()} != images):
            raise ValueError('Previous runtime images differ from the journal. Keep setup blocked.')
        # App recovery uses the candidate schema inspector. Transport rollback
        # deliberately permits damaged candidate inputs when prior files survive.
        candidate_valid = True
        try:
            state.check_manifest(value.get('files'))
            state.checked_config(value['candidate'], value.get('candidate_sha256'))
            if value['files'].get(value['candidate']) != value.get('candidate_sha256'):
                raise ValueError('Candidate manifest is incomplete.')
        except (ValueError, OSError, KeyError, TypeError):
            candidate_valid = False
        if kind == 'application_bootstrap' and not candidate_valid:
            raise ValueError('Application recovery inspector inputs changed. Keep setup blocked.')
        backup_status = 'not_required_before_changes'
        if phase != 'prepared':
            transaction.check_backup(value.get('backup'))
            backup_status = 'private_archive_checksum_verified'
        result.update(previous_files_verified=True, previous_image_ids_verified=True,
                      local_images_verified=False, candidate_files_verified=candidate_valid,
                      backup=backup_status, recovery_execution_available=False)
        checks = ['Confirm this installation and a maintenance window before any recovery.',
                  'Prevent outside database writers from reconnecting during maintenance.',
                  'Inspect exact local images and current service definitions.',
                  'Verify restored runtime and retained data before clearing the block.']
        if kind == 'application_bootstrap' and phase != 'prepared':
            checks.insert(3, 'With all writers stopped, inspect the exact current schema.')
            checks.insert(4, 'If additive schema remains, prove previous-image compatibility.')
        if kind == 'transport_only':
            checks.insert(3, 'Verify the previous route, HTTPS trust and broker access/refusals.')
        if result['state'] == 'maintenance_release_pending':
            checks.insert(0, 'Recheck the settled runtime before releasing maintenance protection.')
        result['next_checks'] = checks
        return result


def display(value):
    lines = ['Setup recovery review. No files or services changed.',
             'This review does not run recovery or approve downtime.']
    if value['state'] in {'not_started', 'no_transaction'}:
        lines += ['No secure setup transaction to recover.',
                  'Current installation health has not been checked.']
    else:
        lines += ['Transaction: ' + value['scope'].replace('_', ' '),
                  'Recorded phase: ' + value['state'].replace('_', ' ')]
        if value['normal_actions_blocked']:
            lines += ['Normal setup actions remain blocked. Do not reinstall or restore a DB.',
                      'Live runtime is NOT verified.',
                      'Recovery execution is not available in this build.']
            if not value.get('candidate_files_verified', True):
                lines += ['Candidate files changed; only prior transport inputs passed review.']
            lines += ['Before an approved recovery:']
        else:
            lines += ['Saved selection files passed. Live runtime is NOT verified.']
        lines += ['  - ' + item for item in value['next_checks']]
    lines += ['No database restore, pairing change or physical reading is proved.']
    return '\n'.join(textwrap.fill(line, width=74, subsequent_indent='    ' if line.startswith('  - ') else '') for line in lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--json', action='store_true')
    args = parser.parse_args()
    try:
        value = inspect(args.root)
        print(json.dumps(value, indent=2) if args.json else display(value))
    except (ValueError, OSError, KeyError, TypeError, AttributeError):
        # Never echo corrupt journal contents, paths, resolved env or secrets.
        print('Recovery review could not verify private state. Keep setup blocked.\n'
              'No files or services changed. Check private recovery inputs and retry.', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
