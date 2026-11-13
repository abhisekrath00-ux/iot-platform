#!/usr/bin/env python3
"""Bounded transport transaction engine. Internal only, no activation CLI.

The effect adapter must own live checks and source-grounded authority. This
engine cannot infer approval from a receipt or state file. The internal
production Docker adapter and disposable fixtures supply effects; no owner
activation CLI is exposed.
"""
import hashlib
import json
import pathlib


def sha(path):
    return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()


def check_backup(info):
    if not isinstance(info, dict) or info.get('verified_archive') is not True:
        raise ValueError('A verified private database backup is required before downtime.')
    path = pathlib.Path(info['path'])
    if (not path.is_absolute() or path.is_symlink() or not path.is_file()
            or path.stat().st_mode & 0o077 or path.stat().st_size == 0
            or sha(path) != info['sha256']):
        raise ValueError('Database backup is missing, shared or changed; refuse downtime.')
    return info


class Transaction:
    """Only the adapter may perform effects, always under the journal OS lock.

    `authorize` must check current trusted authority and target outside this
    engine. `revalidate` must bind fresh exact containers/config/images to the
    receipt. `backup` must verify pg_restore archive listing. `export_acl` runs
    only the prior verified API image, with writers stopped. `accept` must
    establish runtime image/health, HTTPS and broker authentication/refusal.
    An effect failure never selects the candidate. Rollback keeps all ordinary
    menu actions blocked until exact restored runtime acceptance is complete.
    """
    def __init__(self, journal, effects):
        self.journal, self.effects = journal, effects

    def activate(self, project, previous, candidate, images, receipt):
        self.journal.locked()
        # No generic boolean or artifact can stand in for the adapter's
        # trusted-channel target/route/trust/downtime authority verification.
        self.effects.authorize('activate', project, previous, candidate)
        self.effects.revalidate(project, receipt)
        backup = check_backup(self.effects.backup(project, previous))
        value = self.journal.begin(project, previous, candidate, images, receipt)
        value=self.journal.record_backup(backup)
        try:
            self.journal.transition('changing')
            # Include every non-database writer established by the adapter,
            # not only API/ingest. Unclassified services must fail preflight.
            self.effects.stop_writers(value)
            # Export complete current ACL after writers stop. Never substitute
            # candidate app code, SQL fallback, service-only ACL or a stale file.
            acl = self.effects.export_acl(value)
            self.effects.stage_acl(value, acl)
            self.effects.start_candidate(value)
            self.effects.accept(value, 'candidate')
            self.journal.transition('verified')
            self.journal.commit()
            return {'state': 'committed', 'scope': 'transport_only', 'source_reading_proven': False}
        except Exception as failure:
            # A BaseException (power loss / killed process) is deliberately not
            # converted to success or rollback. Its durable journal blocks all
            # normal selection until explicit recovery verifies current facts.
            try:
                self.journal.transition('rollback_required')
                self._rollback(self.journal.read())
            except Exception as rollback_failure:
                raise RuntimeError('Transport change failed and rollback is not verified; keep deployment blocked and use explicit recovery.') from rollback_failure
            raise RuntimeError('Transport change failed; exact previous runtime was restored and verified. No source reading is proved.') from failure

    def recover(self):
        self.journal.locked()
        value = self.journal.read()
        if value.get('kind', 'transport_only') != 'transport_only':
            raise ValueError('Application schema journal requires separate application recovery; no transport rollback.')
        if value['phase'] in {'committed', 'restored'}:
            return {'state': value['phase'], 'changed': False}
        self.effects.authorize('recover', value['project'], value['previous'], value['candidate'])
        self.effects.verify_recovery_inputs(value)
        self._rollback(value)
        return {'state': 'rolled_back', 'changed': True, 'source_reading_proven': False}

    def _rollback(self, value):
        if value['phase']=='prepared':
            # The changing marker is written before the first service effect.
            # A crash before backup metadata persistence therefore needs only
            # exact prior-runtime verification, never a speculative restore.
            self.effects.verify_recovery_inputs(value)
            self.effects.accept(value,'previous')
            self.journal.finish_rollback()
            self.journal.select_restored()
            return
        check_backup(value['backup'])
        self.effects.verify_recovery_inputs(value)
        # The migration cannot change application images/schema, so ordinary
        # rollback restores containers/config, not the database. Replaying an
        # older DB backup could erase new telemetry. Any schema/data mismatch
        # must be a blocked incident, not an automatic pg_restore here.
        self.effects.stop_candidate(value)
        self.effects.restore_previous(value)
        self.effects.accept(value, 'previous')
        if value['phase']!='rolled_back':self.journal.finish_rollback()
        # A verified restored selector is a separate durable state operation,
        # not default Compose fallback. Adapter must fail if it cannot bind it.
        self.journal.select_restored()
