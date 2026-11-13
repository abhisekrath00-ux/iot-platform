#!/usr/bin/env python3
"""Recovery review contracts: private synthetic files, no Docker or owner host."""
import copy
import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('review', HERE / 'secure-recovery-review.py')
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
IMAGE = 'sha256:' + 'a' * 64


class Tests(unittest.TestCase):
    def fixture(self, root, kind='transport_only', phase='prepared'):
        bundle = root / 'plan'
        bundle.mkdir(mode=0o700)
        services = {name: {'image': IMAGE} for name in ['api', 'ingest', 'mcp', 'mosquitto', 'web', 'postgres']}
        old, new, receipt = [bundle / name for name in ['previous-runtime.json', 'candidate-runtime.json', 'preflight.json']]
        m.state.atomic(old, {'services': services})
        m.state.atomic(new, {'services': services, 'fixture': 'new'})
        files = {str(path): m.state.digest(path) for path in [old, new]}
        m.state.atomic(receipt, {'version': 1, 'root': str(root), 'project': 'fixture',
                              'status': 'preflight_only', 'activation_allowed': False,
                              'files': files, 'previous_files': {str(old): files[str(old)]},
                              'current_services': {name: {'image': IMAGE} for name in services}})
        with m.state.Journal(root) as journal:
            journal.begin('fixture', old, new, {name: IMAGE for name in services}, receipt, kind)
            backup = bundle / 'backup.dump'
            backup.write_bytes(b'synthetic archive, not a live pg_dump')
            backup.chmod(0o600)
            journal.record_backup({'verified_archive': True, 'path': str(backup), 'sha256': m.state.digest(backup)})
            if phase != 'prepared':
                journal.transition('changing')
            if phase in {'verified', 'committed'}:
                journal.transition('verified')
            if phase == 'rollback_required':
                journal.transition('rollback_required')
            if phase in {'rolled_back', 'restored'}:
                journal.finish_rollback()
            if phase == 'restored':
                journal.select_restored()
            if phase == 'committed':
                journal.commit()
        return old, new, backup

    def snapshot(self, root):
        return {str(p.relative_to(root)): (p.read_bytes(), p.stat().st_mode, p.stat().st_mtime_ns)
                for p in root.rglob('*') if p.is_file()}

    def test_missing_or_empty_state_is_read_only(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            self.assertEqual(m.inspect(root)['state'], 'not_started')
            self.assertEqual(list(root.iterdir()), [])
            with m.state.Journal(root):
                pass
            before = self.snapshot(root)
            self.assertEqual(m.inspect(root)['state'], 'no_transaction')
            self.assertEqual(self.snapshot(root), before)

    def test_every_interrupted_phase_and_kind_remains_blocked_and_unchanged(self):
        for kind in ['transport_only', 'application_bootstrap']:
            for phase in ['prepared', 'changing', 'verified', 'rollback_required', 'rolled_back']:
                with self.subTest(kind=kind, phase=phase), tempfile.TemporaryDirectory() as td:
                    root = pathlib.Path(td)
                    self.fixture(root, kind, phase)
                    before = self.snapshot(root)
                    value = m.inspect(root)
                    self.assertEqual(value['state'], phase)
                    self.assertEqual(value['scope'], kind)
                    for name in ['normal_actions_blocked', 'previous_files_verified', 'previous_image_ids_verified']:
                        self.assertIs(value[name], True)
                    for name in ['changes_allowed', 'live_runtime_verified', 'local_images_verified', 'source_reading_proven', 'recovery_execution_available']:
                        self.assertIs(value[name], False)
                    self.assertEqual(self.snapshot(root), before)
                    with self.assertRaises(ValueError):
                        m.state.selection(root)
                    checks = ' '.join(value['next_checks'])
                    self.assertIn('outside database writers', checks)
                    self.assertEqual('current schema' in checks, kind == 'application_bootstrap' and phase != 'prepared')

    def test_settled_file_selection_is_not_runtime_or_reading_acceptance(self):
        for phase in ['committed', 'restored']:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                self.fixture(root, phase=phase)
                before = self.snapshot(root)
                value = m.inspect(root)
                self.assertFalse(value['normal_actions_blocked'])
                self.assertTrue(value['selection_files_verified'])
                self.assertFalse(value['live_runtime_verified'])
                self.assertFalse(value['source_reading_proven'])
                self.assertEqual(self.snapshot(root), before)

    def test_damaged_prior_inputs_backup_or_record_refuse_review(self):
        for damage in ['previous', 'backup', 'backup-mode', 'manifest', 'images', 'kind', 'phase', 'project', 'selector-only']:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                old, new, backup = self.fixture(root, phase='changing')
                journal = root / '.secure-deployment/journal.json'
                value = m.state.read_private(journal)
                if damage == 'previous': old.write_text('damaged')
                if damage == 'backup': backup.write_bytes(b'changed')
                if damage == 'backup-mode': backup.chmod(0o644)
                if damage == 'manifest': value['previous_files'] = {}
                if damage == 'images': value['previous_images']['api'] = 'sha256:' + 'b' * 64
                if damage == 'kind': value['kind'] = 'new-unknown-operation'
                if damage == 'phase': value['phase'] = 'assumed-good'
                if damage == 'project': value['project'] = 'fixture\nsecret'
                m.state.atomic(journal, value)
                if damage == 'selector-only':
                    journal.unlink()
                    m.state.atomic(root / '.secure-deployment/selected.json', {})
                before = self.snapshot(root)
                with self.assertRaises(ValueError): m.inspect(root)
                self.assertEqual(self.snapshot(root), before)

    def test_transport_review_can_report_damaged_candidate_but_app_cannot(self):
        for kind in ['transport_only', 'application_bootstrap']:
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                old, new, backup = self.fixture(root, kind, 'changing')
                new.write_text('changed candidate')
                if kind == 'application_bootstrap':
                    with self.assertRaises(ValueError): m.inspect(root)
                else:
                    value = m.inspect(root)
                    self.assertFalse(value['candidate_files_verified'])
                    self.assertTrue(value['normal_actions_blocked'])

    def test_lock_and_symlink_fail_without_repair_creation_or_docker(self):
        for damage in ['held', 'missing-lock', 'lock-mode', 'lock-link', 'directory-link']:
            with self.subTest(damage=damage), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                self.fixture(root)
                directory = root / '.secure-deployment'
                lock = directory / 'lock'
                if damage == 'held':
                    with m.state.Journal(root):
                        with self.assertRaises(ValueError): m.inspect(root)
                    continue
                if damage == 'missing-lock': lock.unlink()
                if damage == 'lock-mode': lock.chmod(0o644)
                if damage == 'lock-link':
                    lock.unlink()
                    lock.symlink_to(root / 'plan/backup.dump')
                if damage == 'directory-link':
                    directory.rename(root / 'other-state')
                    directory.symlink_to(root / 'other-state')
                before = self.snapshot(root)
                with self.assertRaises((ValueError, OSError)): m.inspect(root)
                self.assertEqual(self.snapshot(root), before)

    def test_cli_redacts_errors_and_has_no_execution_mode(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            self.fixture(root)
            journal = root / '.secure-deployment/journal.json'
            journal.write_text('{"secret":"never-print-fixture-secret",invalid}')
            before = self.snapshot(root)
            for flags in [[], ['--activate'], ['--recover'], ['--yes']]:
                proc = subprocess.run(['python3', str(HERE / 'secure-recovery-review.py'), '--root', str(root), *flags], text=True, capture_output=True)
                self.assertNotEqual(proc.returncode, 0)
                self.assertNotIn('never-print-fixture-secret', proc.stdout + proc.stderr)
            self.assertEqual(self.snapshot(root), before)

    def test_setup_command_routes_review_without_docker_or_legacy_fallback(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            scripts = root / 'scripts'
            scripts.mkdir()
            for name in ['setup.sh', 'hx-ui.sh', 'secure-recovery-review.py', 'secure-state.py', 'secure-transaction.py']:
                shutil.copy2(HERE / name, scripts / name)
            self.fixture(root, 'application_bootstrap', 'changing')
            bin = root / 'bin'
            bin.mkdir()
            docker = bin / 'docker'
            docker.write_text('#!/bin/sh\necho unexpected-docker >&2\nexit 88\n')
            docker.chmod(0o700)
            before = self.snapshot(root)
            proc = subprocess.run(['bash', str(scripts / 'setup.sh'), 'secure-recovery-review'], text=True, capture_output=True,
                                  env=dict(os.environ, PATH=str(bin) + ':' + os.environ['PATH']))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn('Normal setup actions remain blocked.', proc.stdout)
            self.assertIn('No files or services changed.', proc.stdout)
            self.assertNotIn('unexpected-docker', proc.stdout + proc.stderr)
            self.assertEqual(self.snapshot(root), before)

    def test_settled_runtime_with_unreleased_maintenance_stays_blocked(self):
        for phase in ['committed', 'restored']:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as td:
                root = pathlib.Path(td)
                self.fixture(root, phase=phase)
                path = root / '.secure-deployment/journal.json'
                value = m.state.read_private(path)
                value['maintenance_state'] = 'held'
                m.state.atomic(path, value)
                before = self.snapshot(root)
                result = m.inspect(root)
                self.assertEqual(result['state'], 'maintenance_release_pending')
                self.assertTrue(result['normal_actions_blocked'])
                self.assertIn('settled runtime', ' '.join(result['next_checks']))
                with self.assertRaises(ValueError): m.state.selection(root)
                self.assertEqual(self.snapshot(root), before)

    def test_terminal_output_stays_readable_and_honest_at_80_columns(self):
        with tempfile.TemporaryDirectory() as td:
            root = pathlib.Path(td)
            self.fixture(root, 'application_bootstrap', 'changing')
            text = m.display(m.inspect(root))
            self.assertLessEqual(max(map(len, text.splitlines())), 74)
            self.assertIn('does not run recovery or approve downtime', text)
            self.assertIn('Live runtime is NOT verified', text)
            self.assertIn('Recovery execution is not available', text)
            self.assertIn('No database restore', text)


if __name__ == '__main__':
    unittest.main()
