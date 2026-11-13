#!/usr/bin/env python3
"""Separate reviewed application/schema transaction, not transport activation.

Internal orchestration only. The production Docker adapter and local schema
operator are separate; no owner/menu caller or implicit authority is supplied.
No database restore is ever performed here.
"""
import copy
import importlib.util
import json
import pathlib

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module

runtime = load('secure-runtime')
readiness = load('secure-readiness')
transport = load('secure-transaction')
ADDED = ['0090_profile_connection_defaults.sql', '0091_edge_identity_migrations.sql',
         '0092_edge_serial_port_inventory.sql', '0093_edge_pairing_proofs.sql']


def pair(previous, candidate, current_images, proposed_images):
    """Application-only change: exact same services/config, writer images only.

    No ports, routes, trust, mounts, commands, env or broker change may hide in
    this transaction. Every active writer must use the same new server bundle;
    inactive profiles stay inactive. Local availability is the adapter's check.
    """
    old, disabled = runtime.freeze(previous, current_images)
    new, new_disabled = runtime.freeze(candidate, proposed_images)
    roles = readiness.classify(previous, current_images)
    if set(old['services']) != set(new['services']) or disabled != new_disabled:
        raise ValueError('Application upgrade cannot add/remove or activate services.')
    writers = roles['writers']
    if not {'api', 'ingest'} <= set(writers):
        raise ValueError('Supported API and ingest writers required.')
    target = new['services']['api']['image']
    if target == old['services']['api']['image']:
        raise ValueError('Application upgrade requires an explicit different local API image.')
    normalized = copy.deepcopy(new)
    for name in old['services']:
        if name in writers:
            if new['services'][name]['image'] != target:
                raise ValueError('Every active writer must use the exact new server image.')
            normalized['services'][name]['image'] = old['services'][name]['image']
    if old != normalized:
        raise ValueError('Only exact API/ingest/MCP writer images may change in application upgrade.')
    return {'previous': old, 'candidate': new, 'writers': writers,
            'disabled_profiles': disabled}


def check_schema(value):
    if (not isinstance(value, dict) or value.get('version') != 1
            or value.get('status') not in {'exact_reviewed_baseline_89', 'exact_reviewed_additive_93'}
            or value.get('runtime_accepted') is not False):
        raise ValueError('Schema result is unknown or partial; keep writers stopped for explicit recovery.')
    return value['status']


class ApplicationTransaction:
    """Distinct scope from TLS. Adapter owns every effect and live fact.

    authorize verifies trusted target, downtime and app-upgrade authority, never
    receipt booleans. revalidate binds exact containers/images/config/dependencies
    and reviewed migration bytes. verify_writers_stopped checks all writers,
    including MCP. apply_reviewed_schema invokes only the exact reviewed engine.
    inspect_reviewed_schema must establish 89 or 93, not just a count. Acceptance
    must cover exact image/health and retained identity/source through the real
    API, plus current operator readiness. Recovery on 93 additionally requires
    explicit prior-image compatibility validation; filenames are not proof.
    """
    def __init__(self, journal, effects):
        self.journal, self.effects = journal, effects

    def activate(self, project, previous, candidate, images, receipt):
        self.journal.locked()
        old = json.loads(pathlib.Path(previous).read_text())
        new = json.loads(pathlib.Path(candidate).read_text())
        plan = pair(old, new, images, {name: item.get('image') for name, item in new['services'].items()})
        if old != plan['previous'] or new != plan['candidate']:
            raise ValueError('Application transaction requires private immutable runtime snapshots.')
        self.effects.authorize('application-upgrade', project, previous, candidate)
        self.effects.revalidate(project, receipt)
        backup = transport.check_backup(self.effects.backup(project, previous))
        value = self.journal.begin(project, previous, candidate, images, receipt,
                                   kind='application_bootstrap')
        value = self.journal.record_backup(backup)
        try:
            value = self.journal.transition('changing')
            self.effects.stop_writers(value)
            self.effects.verify_writers_stopped(value)
            # Mark before invoking SQL. A lost process/commit acknowledgement
            # must force schema inspection instead of assuming it rolled back.
            value = self.journal.record_schema_attempt(ADDED)
            self.effects.apply_reviewed_schema(value)
            schema = self.effects.inspect_reviewed_schema(value)
            if check_schema(schema) != 'exact_reviewed_additive_93':
                raise ValueError('Reviewed additive schema not committed; no candidate started.')
            value = self.journal.record_schema_result(schema)
            self.effects.start_candidate(value)
            self.effects.accept(value, 'candidate')
            self.journal.transition('verified')
            self.journal.commit()
            return {'state': 'committed', 'scope': 'application_bootstrap',
                    'transport_activated': False, 'source_reading_proven': False}
        except Exception as failure:
            try:
                self.journal.transition('rollback_required')
                self._rollback(self.journal.read())
            except Exception as rollback_failure:
                raise RuntimeError('Application upgrade failed; recovery is unverified. Keep normal menu actions blocked. No database restore attempted.') from rollback_failure
            raise RuntimeError('Application upgrade failed; exact previous runtime verified. Additive schema may remain. No database restore or source-reading claim.') from failure

    def recover(self):
        self.journal.locked()
        value = self.journal.read()
        if value.get('kind') != 'application_bootstrap':
            raise ValueError('Not an application upgrade journal; use its matching recovery workflow.')
        if value['phase'] in {'committed', 'restored'}:
            return {'state': value['phase'], 'changed': False}
        self.effects.authorize('application-recover', value['project'], value['previous'], value['candidate'])
        self.effects.verify_recovery_inputs(value)
        self._rollback(value)
        return {'state': 'restored', 'changed': True, 'transport_activated': False,
                'source_reading_proven': False}

    def _rollback(self, value):
        self.effects.verify_recovery_inputs(value)
        if value['phase'] == 'prepared':
            self.effects.accept(value, 'previous')
        else:
            transport.check_backup(value['backup'])
            self.effects.stop_candidate(value)
            self.effects.verify_writers_stopped(value)
            schema = check_schema(self.effects.inspect_reviewed_schema(value))
            if schema == 'exact_reviewed_additive_93':
                self.effects.verify_previous_schema_compatibility(value)
            self.effects.restore_previous(value)
            self.effects.accept(value, 'previous')
        if value['phase'] != 'rolled_back':
            self.journal.finish_rollback()
        self.journal.select_restored()
