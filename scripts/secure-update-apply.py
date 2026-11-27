#!/usr/bin/env python3
"""Interactive apply/recover caller for one exact reviewed server update plan.

Wiring only. Every check is an existing reviewed module: the read-only plan
review, the read-only interrupted-state review, the retained read-only client,
the production application provider and the execution Coordinator. This script
adds no credential, no Docker discovery beyond the exact reviewed plan, no
schema replay and no fallback. Fresh TLS activation is out of scope and stays
with the separate secure setup path.

Rollback never replays the database: the additive reviewed 93 ledger stays and
the previous image must pass the explicit compatibility proof. After the first
successful apply, the legacy Upgrade entry permanently refuses this
installation because the journal-managed selection takes over.
"""
import argparse
import importlib.util
import json
import pathlib
import re
import subprocess
import sys

sys.dont_write_bytecode = True
HERE = pathlib.Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


state = load('secure-state')
execution = load('secure-execution')
review = load('secure-update-review')
recovery = load('secure-recovery-review')
acceptance = load('secure-application-acceptance')
clientmod = load('secure-application-client')
production = load('secure-application-production')
fence = load('secure-postgres-fence')

PROJECT = r'[a-z0-9][a-z0-9_-]{0,62}'
ORIGIN = 'http://127.0.0.1:8000'

NON_CLAIMS = [
    'Fresh TLS activation is NOT covered; it stays with the separate secure setup path.',
    'Background, write and ingest behavior is NOT proved by the retained read-only checks.',
    'Source readings and physical devices are NOT proved.',
    'Full runtime compatibility is NOT claimed; retained-list match and live identity only.',
    'No new credentials were created; the existing session and plan secrets were never shown.',
]


def detect(root, project):
    """Map the read-only recovery review onto the exact Coordinator action."""
    value = recovery.inspect(root)
    if value.get('normal_actions_blocked') is not True:
        return 'activate', value
    if value.get('scope') != 'application_bootstrap':
        raise ValueError('Interrupted state is not an application update; this caller cannot recover it. No lab fallback.')
    if value.get('project') != project:
        raise ValueError('Interrupted maintenance belongs to a different project. Keep it blocked; no application change.')
    return 'recover', value


def limited_behavior(client):
    """Acknowledged bounded scope: live identity re-verified inside acceptance.

    RuntimeAcceptance re-compares the full retained list around this callback.
    Background and write behavior stay outside this proof and are printed as
    non-claims. Failures raise; nothing here ever returns an approval flag.
    """
    def check(value, slot):
        client.identity()
        return None
    return check


def inspected(runner, container):
    result = runner(['docker', 'inspect', container], capture_output=True, text=True, timeout=15)
    if result.returncode != 0:
        raise ValueError('Exact reviewed container inspection failed; no application change.')
    values = json.loads(result.stdout)
    if not isinstance(values, list) or len(values) != 1:
        raise ValueError('One exact reviewed container required.')
    return values[0]


def provider_inputs(root, project, receipt, runner):
    """Resolve production provider inputs from the exact reviewed plan only.

    The Postgres password is read from the private resolved plan and only ever
    placed inside the bounded connection request. It is never printed, logged,
    returned or stored outside provider construction.
    """
    plan = state.read_private(receipt)
    if plan.get('root') != str(root) or plan.get('project') != project:
        raise ValueError('Reviewed installation differs from the plan.')
    services = plan.get('current_services')
    database = plan.get('database')
    if (not isinstance(services, dict) or not isinstance(database, dict)
            or not re.fullmatch(r'[a-f0-9]{64}', str(services.get('postgres', {}).get('container_id', '')))
            or not re.fullmatch(r'[a-f0-9]{64}', str(services.get('web', {}).get('container_id', '')))):
        raise ValueError('Exact reviewed current service containers required.')
    user, name = database.get('user'), database.get('name')
    if not isinstance(user, str) or not user or not isinstance(name, str) or not name:
        raise ValueError('Exact reviewed database user/name required.')
    previous = state.read_private(receipt.parent / 'previous-runtime.json')
    environment = previous.get('services', {}).get('postgres', {}).get('environment', {})
    password = environment.get('POSTGRES_PASSWORD') if isinstance(environment, dict) else None
    if not isinstance(password, str) or not password or '\x00' in password:
        raise ValueError('Exact private Postgres password unavailable in the reviewed plan.')
    web = inspected(runner, services['web']['container_id'])
    pg = inspected(runner, services['postgres']['container_id'])
    shared = set(web.get('NetworkSettings', {}).get('Networks', {})) & set(pg.get('NetworkSettings', {}).get('Networks', {}))
    if len(shared) != 1:
        raise ValueError('Exactly one shared private network between web and Postgres is required.')
    network = next(iter(shared))
    webnet = web['NetworkSettings']['Networks'][network]
    pgnet = pg['NetworkSettings']['Networks'][network]
    endpoint = {'container': web['Id'], 'image': web['Image'], 'started': web['State']['StartedAt'],
                'project': project, 'service': 'web', 'network': network,
                'network_id': webnet['NetworkID'], 'address': webnet['IPAddress']}
    request = {'version': 1, 'transport': 'private_tcp_plain', 'host': pgnet['IPAddress'], 'port': 5432,
               'database': name, 'user': user, 'password': password}
    catalog = fence.DockerCatalog(services['postgres']['container_id'], user, name)
    return catalog, endpoint, request


def reviewer_for(root, project, receipt):
    plan = state.read_private(receipt)
    previous = str(receipt.parent / 'previous-runtime.json')
    candidate = str(receipt.parent / 'candidate-runtime.json')

    def reviewer(target):
        if (target.root != str(root) or target.project != project
                or target.scope != 'application_bootstrap'
                or target.previous != previous or target.candidate != candidate
                or target.receipt_sha256 != state.digest(receipt)):
            raise ValueError('Maintenance scope differs from the exact reviewed target.')
    return reviewer


def execute(root, project, receipt, action, session, tenant, user, *,
            provider_class=None, client_class=None, runner=None, behavior=None):
    """Verify the plan live, then run the Coordinator with the production provider."""
    provider_class = provider_class or production.Provider
    client_class = client_class or clientmod.Client
    runner = runner or subprocess.run
    root = pathlib.Path(root).resolve()
    receipt = pathlib.Path(receipt).absolute()
    if not re.fullmatch(PROJECT, project):
        raise ValueError('Explicit existing Compose project name required.')
    if action not in {'activate', 'recover'}:
        raise ValueError('Explicit activation or recovery action required.')
    bundle = receipt.parent
    if bundle.is_symlink() or not bundle.is_dir() or bundle.stat().st_mode & 0o077:
        raise ValueError('Reviewed plan must stay in a private bundle; refuse lab fallback.')
    # Live read-only plan verification: changed or missing receipt refuses here.
    review.inspect(root, project, receipt)
    # The existing session must be an owned single-link private regular file.
    clientmod.session_file(session)
    client = client_class(ORIGIN, session, tenant, user, allow_loopback_http=True)
    # Baseline is captured live BEFORE any maintenance admission.
    baseline = client.snapshot()
    catalog, endpoint, request = provider_inputs(root, project, receipt, runner)
    provider = provider_class(catalog, reviewer_for(root, project, receipt), endpoint, request, runner)
    active = [provider]
    check = behavior or limited_behavior(client)

    def accepted(value, slot):
        acceptance.RuntimeAcceptance(active[0].adapter, client, baseline, check)(value, slot)

    def compatible(value):
        acceptance.PreviousCompatibility(active[0].adapter, active[0].group, client, baseline,
                                         lambda previous: check(previous, 'previous'))(value)

    caller = execution.Coordinator(provider, accepted, compatible, provider.factory)
    return caller.run(root, project, receipt, action)


def prompt(text):
    try:
        return input(text).strip()
    except (EOFError, KeyboardInterrupt):
        print('')
        return None


def describe_failure(action, error):
    """Failure line safe to print without resolved environment, session
    material or connection details.

    ValueError refusals raised anywhere in the reviewed transaction path are
    fixed literals naming only the refused invariant; five audited sites append
    machine identifiers only (a field/type name, fixed diagnostic codes or a
    service name), never a path, an environment value or session material, so
    the exact refusal reason is safe to show. Every other error type stays a
    bare type name because its message can carry host details.
    """
    if isinstance(error, ValueError):
        return ('Reviewed update ' + action + ' failed or was refused: '
                + type(error).__name__ + ': ' + str(error)
                + ' Maintenance protection is retained; use this same entry to recover. '
                'No legacy fallback exists.')
    return ('Reviewed update ' + action + ' failed or was refused: ' + type(error).__name__ + '. '
            'Maintenance protection is retained; use this same entry to recover. '
            'No legacy fallback exists.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--project', required=True)
    parser.add_argument('--receipt', required=True)
    args = parser.parse_args()
    if not sys.stdin.isatty():
        print('Reviewed update apply needs an interactive terminal for the typed confirmation. '
              'Nothing changed.', file=sys.stderr)
        return 1
    try:
        root = pathlib.Path(args.root).resolve()
        receipt = pathlib.Path(args.receipt).absolute()
        plan = review.inspect(root, args.project, receipt)
        action, interrupted = detect(root, args.project)
    except (OSError, ValueError, KeyError, TypeError):
        print('Reviewed update apply unavailable: the exact private plan or interrupted state could '
              'not be verified. Nothing changed. Do not run a legacy upgrade as fallback.', file=sys.stderr)
        return 1
    print('Reviewed server update ' + ('RECOVERY of an interrupted maintenance session.' if action == 'recover'
                                       else 'apply of the exact prepared plan.'))
    print('Project: ' + args.project)
    print('Plan SHA256: ' + plan['receipt_sha256'])
    for row in plan['images']:
        print('  ' + row['service'] + ': ' + ('unchanged' if row['previous'] == row['candidate'] else 'planned change'))
    if action == 'recover':
        print('Interrupted state: ' + interrupted.get('state', 'unknown').replace('_', ' '))
        print('Normal setup actions stay blocked until recovery settles.')
    print('This stops application writers during a maintenance window and proves retained data live.')
    print('After the first successful secure update, the legacy Upgrade entry permanently refuses')
    print('this installation; only this journal-managed path can change it afterwards.')
    print('Not covered:')
    for item in NON_CLAIMS:
        print('  - ' + item)
    session = prompt('  Existing private admin session file path: ')
    if session is None:
        print('Cancelled. Nothing changed.')
        return 0
    tenant = prompt('  Expected tenant ID: ')
    if tenant is None:
        print('Cancelled. Nothing changed.')
        return 0
    user = prompt('  Expected admin user ID: ')
    if user is None:
        print('Cancelled. Nothing changed.')
        return 0
    phrase = 'RECOVER' if action == 'recover' else 'APPLY'
    answer = prompt('  Type ' + phrase + ' to start the reviewed maintenance window, or Enter to cancel: ')
    if answer is None or answer != phrase:
        print('Cancelled. Nothing changed.')
        return 0
    try:
        result = execute(root, args.project, receipt, action, session, tenant, user)
    except (OSError, ValueError, KeyError, TypeError, RuntimeError) as error:
        print(describe_failure(action, error), file=sys.stderr)
        return 1
    print('Reviewed update ' + action + ' settled: ' + str(result.get('state', 'unknown')).replace('_', ' ') + '.')
    if action == 'activate':
        print('The legacy Upgrade entry now permanently refuses this installation.')
    print('Reminder, still NOT covered:')
    for item in NON_CLAIMS:
        print('  - ' + item)
    return 0


if __name__ == '__main__':
    sys.exit(main())
