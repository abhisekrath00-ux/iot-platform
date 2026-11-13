#!/usr/bin/env python3
"""Exclusive database-admission component, not a complete maintenance provider.

Closes one exact database to ALL new sessions, including superusers, and drains
existing sessions. Requires the caller's already-held deployment lock and fresh
scope reviewer. It deliberately allows no application/migration exceptions.
Consequently it cannot yet be plugged into Coordinator: that caller's Docker
adapters open fresh target-database connections. No CLI or owner activation.
"""
import dataclasses
import importlib.util
import json
import pathlib
import re
import subprocess

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('fence_state', HERE / 'secure-state.py')
state = importlib.util.module_from_spec(spec)
spec.loader.exec_module(state)


def literal(value):
    if not isinstance(value, str) or not value or '\x00' in value:
        raise ValueError('Exact nonempty database identifier required.')
    return "E'" + value.replace("\\", "\\\\").replace("'", "''") + "'"


def identifier(value):
    literal(value)
    return '"' + value.replace('"', '""') + '"'


class DockerCatalog:
    """Admin channel on postgres database in one immutable running container.

    Discovery, matching this container to the reviewed installation, and owner
    authority belong to the host caller. No ambient/default Docker target.
    """
    def __init__(self, container, user, database, run=None):
        if not re.fullmatch(r'[a-f0-9]{64}', container):
            raise ValueError('Exact PostgreSQL container ID required.')
        literal(user)
        literal(database)
        if database in {'postgres', 'template0', 'template1'}:
            raise ValueError('A separate application database is required.')
        self.container, self.user, self.database = container, user, database
        self.run = run or subprocess.run

    def sql(self, statement):
        command = ['docker', 'exec', '-i', self.container, 'psql', '-X', '-q', '-A', '-t',
                   '-v', 'ON_ERROR_STOP=1', '-U', self.user, '-d', 'postgres']
        try:
            result = self.run(command, input=statement, text=True, capture_output=True,
                              timeout=20, check=True)
            return json.loads(result.stdout.strip())
        except (subprocess.SubprocessError, ValueError, OSError) as exc:
            # Do not leak SQL errors, authentication context or arbitrary output.
            raise ValueError('PostgreSQL maintenance catalog check failed; keep the fence.') from None

    def inspect(self):
        db = literal(self.database)
        return self.sql(f"""SELECT json_build_object(
          'cluster', (SELECT system_identifier::text FROM pg_control_system()),
          'database', d.datname, 'oid', d.oid::text, 'owner', d.datdba::text,
          'allow', d.datallowconn, 'template', d.datistemplate,
          'admin', current_setting('is_superuser') = 'on',
          'recovery', pg_is_in_recovery(),
          'sessions', (SELECT count(*) FROM pg_stat_activity WHERE datid=d.oid),
          'prepared', (SELECT count(*) FROM pg_prepared_xacts WHERE database=d.datname),
          'slots', (SELECT count(*) FROM pg_replication_slots WHERE database=d.datname),
          'subscriptions', (SELECT count(*) FROM pg_subscription WHERE subdbid=d.oid))
          FROM pg_database d WHERE d.datname={db};""")

    def mutate(self, statement, expected):
        if self.database != expected['database']:
            raise ValueError('Catalog channel database differs from the fence identity.')
        # Serialize catalog identity validation and mutation. Without this lock,
        # a database dropped/recreated between inspect() and ALTER could turn an
        # exact-name action into an effect on a different database.
        conditions = " AND ".join([
            "d.datname=" + literal(expected['database']),
            "d.oid::text=" + literal(expected['oid']),
            "d.datdba::text=" + literal(expected['owner']),
            "(SELECT system_identifier::text FROM pg_control_system())=" + literal(expected['cluster'])])
        body = ("BEGIN IF NOT EXISTS(SELECT FROM pg_database d WHERE " + conditions +
                ") THEN RAISE EXCEPTION 'Database identity changed'; END IF; END")
        tag = '$hexthings_fence$'
        while tag in body:
            tag = tag[:-1] + 'x$'
        return self.sql("BEGIN; SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='15s'; "
                        "LOCK TABLE pg_catalog.pg_database IN SHARE ROW EXCLUSIVE MODE; DO " +
                        tag + body + tag + '; ' + statement + '; COMMIT;')

    def allow(self, enabled, expected):
        if type(enabled) is not bool:
            raise ValueError('Explicit admission state required.')
        return self.mutate('ALTER DATABASE ' + identifier(self.database) +
                           (' ALLOW_CONNECTIONS true;' if enabled else ' ALLOW_CONNECTIONS false;') +
                           " SELECT json_build_object('changed', true)", expected)

    def drain(self, expected):
        result = self.mutate("SELECT json_build_object('terminated', coalesce(json_agg("
                           "pg_terminate_backend(pid, 5000)), '[]'::json)) FROM pg_stat_activity "
                           'WHERE datid=(SELECT oid FROM pg_database WHERE datname=' +
                           literal(self.database) + ')', expected)
        if (not isinstance(result, dict) or set(result) != {'terminated'}
                or not isinstance(result['terminated'], list)
                or any(type(item) is not bool for item in result['terminated'])):
            raise ValueError('Exact database drain response required; keep admission closed.')
        if False in result['terminated']:
            # pg_stat_activity is a snapshot: a backend can exit after being
            # listed and before pg_terminate_backend runs, which returns false.
            # Never call that a successful termination. A separate catalog
            # query must prove the exact still-closed database has no sessions.
            # Empty result means nothing remains to drain, not a synthetic ack.
            live = checked_database(self.inspect(), expected)
            if live['allow'] or live['sessions']:
                raise ValueError('Database drain was not freshly exclusive; keep admission closed.')
            return {'terminated': []}
        return result



def checked_database(live, expected=None):
    if (not isinstance(live, dict) or live.get('admin') is not True
            or live.get('recovery') is not False or live.get('template') is not False
            or live.get('database') in {'postgres', 'template0', 'template1'}
            or type(live.get('allow')) is not bool
            or any(type(live.get(key)) is not int or live[key] < 0
                   for key in ['sessions', 'prepared', 'slots', 'subscriptions'])
            or any(not isinstance(live.get(key), str) or not live[key]
                   for key in ['cluster', 'database', 'oid', 'owner'])):
        raise ValueError('Exact writable primary database/admin identity required.')
    if expected and {key: live[key] for key in ['cluster', 'database', 'oid', 'owner']} != expected:
        raise ValueError('PostgreSQL cluster/database/owner changed; keep maintenance blocked.')
    if any(live[key] for key in ['prepared', 'slots', 'subscriptions']):
        raise ValueError('Prepared or replication writers require separate maintenance support.')
    return live

@dataclasses.dataclass(frozen=True)
class Ticket:
    binding: tuple
    token: object


class DatabaseFence:
    """A held lock is required for the complete lifecycle, including recovery.

    Persist original admission before ALTER, and releasing before reopening.
    A new invocation may recover an interrupted closing/held/releasing fence,
    but a released record is never silently reused for a new transaction.
    A stage record proves no approval. reviewer(target) must raise on failure.
    """
    def __init__(self, journal, catalog, reviewer):
        if not callable(reviewer):
            raise ValueError('Fresh target/downtime scope reviewer required.')
        self.journal, self.catalog, self.reviewer = journal, catalog, reviewer
        self.path = journal.directory / 'postgres-fence.json'
        self.ticket = None

    def review(self, target):
        self.journal.locked()
        if pathlib.Path(target.root).resolve() != self.journal.root:
            raise ValueError('Database fence belongs to a different installation.')
        admission = state.read_private(self.journal.directory / 'maintenance.json')
        if any(admission.get(key) != item for key, item in self.binding(target).items()):
            raise ValueError('Coordinator admission marker differs from this database fence.')
        if self.reviewer(target) is not None:
            raise ValueError('Scope reviewer must raise on failure, not return approval.')

    @staticmethod
    def binding(target):
        return {key: getattr(target, key) for key in
                ['root', 'project', 'scope', 'previous', 'candidate', 'receipt_sha256']}

    @staticmethod
    def identity(live):
        return {key: live[key] for key in ['cluster', 'database', 'oid', 'owner']}

    def inspect(self, expected=None):
        live = self.catalog.inspect()
        return checked_database(live, expected)

    def record(self, target):
        value = state.read_private(self.path)
        if (set(value) != {'version', 'binding', 'identity', 'original_allow', 'stage'}
                or value['version'] != 1 or value['binding'] != self.binding(target)
                or value['original_allow'] is not True
                or not isinstance(value['identity'], dict)
                or set(value['identity']) != {'cluster', 'database', 'oid', 'owner'}
                or value['stage'] not in {'closing', 'held', 'releasing', 'released'}):
            raise ValueError('Database fence record differs from the exact reviewed target.')
        return value

    def enter(self, target):
        self.review(target)
        exists = self.path.exists() or self.path.is_symlink()
        if exists:
            value = self.record(target)
            if target.action != 'recover' or value['stage'] == 'released':
                raise ValueError('Existing database fence needs explicit unfinished recovery.')
            live = self.inspect(value['identity'])
            if value['stage'] == 'held' and live['allow']:
                raise ValueError('Held database fence was reopened outside this session.')
        else:
            if target.action != 'activate':
                raise ValueError('No database fence record to recover.')
            live = self.inspect()
            if not live['allow']:
                raise ValueError('Database was already closed; do not assume permission to reopen it.')
            value = {'version': 1, 'binding': self.binding(target),
                     'identity': self.identity(live), 'original_allow': live['allow'], 'stage': 'closing'}
            state.atomic(self.path, value)
        # The durable intent exists even if ALTER succeeds but its acknowledgement
        # is lost. Do not replace it with a new baseline during recovery.
        self.catalog.allow(False, value['identity'])
        live = self.inspect(value['identity'])
        if live['allow']:
            raise ValueError('PostgreSQL admission did not close.')
        result = self.catalog.drain(value['identity'])
        if (not isinstance(result, dict) or set(result) != {'terminated'}
                or not isinstance(result['terminated'], list)
                or any(item is not True for item in result['terminated'])):
            raise ValueError('Existing database sessions did not all terminate.')
        live = self.inspect(value['identity'])
        if live['allow'] or live['sessions']:
            raise ValueError('Database is not exclusive; keep maintenance blocked.')
        value['stage'] = 'held'
        state.atomic(self.path, value)
        self.ticket = Ticket(tuple(sorted(self.binding(target).items())), object())
        self.check(self.ticket, target)
        return self.ticket

    def check(self, ticket, target):
        self.review(target)
        if ticket is None or ticket is not self.ticket or ticket.binding != tuple(sorted(self.binding(target).items())):
            raise ValueError('Fresh opaque database-fence session required.')
        value = self.record(target)
        if value['stage'] != 'held':
            raise ValueError('Database fence is not durably held.')
        live = self.inspect(value['identity'])
        if live['allow'] or live['sessions']:
            raise ValueError('Exclusive database fence lost; refuse further effects.')

    def release(self, ticket, target):
        self.check(ticket, target)
        value = self.record(target)
        value['stage'] = 'releasing'
        state.atomic(self.path, value)
        self.catalog.allow(value['original_allow'], value['identity'])
        live = self.inspect(value['identity'])
        if live['allow'] is not value['original_allow']:
            raise ValueError('Original database admission was not restored.')
        value['stage'] = 'released'
        state.atomic(self.path, value)
        self.ticket = None
        # Keep the released evidence. An enclosing settled transaction, not this
        # component, owns archival/removal and ordinary-selector unblocking.

    def retain(self, ticket, target):
        self.journal.locked()
        # Never reopen as error cleanup. The durable intent is sufficient to
        # discover an entry failure before a ticket was returned.
        if self.path.exists() or self.path.is_symlink():
            self.record(target)
