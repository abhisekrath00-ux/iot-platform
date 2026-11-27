#!/usr/bin/env python3
"""Native disposable acceptance of the real reviewed apply/recover menu caller.

Drives the actual scripts/secure-update-apply.py through a real PTY against a
fresh labelled disposable Docker project: real reviewed plan preparation, real
production provider, real retained-data client and real Coordinator. Synthetic
identity/session/data only; never an owner machine, owner credential, published
port beyond loopback, or production deployment. This is not owner activation,
fresh TLS, Windows, full background compatibility or physical device proof.

--self-test is local only and never touches Docker.
"""
import argparse
import base64
import hashlib
import hmac
import importlib.util
import json
import os
import pathlib
import pty
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import time

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


m = load('secure-application-provider')
fixture = load('test-secure-application-native')
preparemod = load('secure-update-prepare')
cleanup = load('real-application-fixture-cleanup')

OLD_COMMIT = 'bfe5d89b46324a566fd089b6c83ab9c111dc9231'
ROOT_PREFIX = 'hexthings-application-ci-'
PROJECT_PREFIX = 'hexthings-app-ci-'
API_PORT = 8000  # The reviewed caller's fixed loopback origin.
PASSWORD = 'synthetic-ci-only'  # Synthetic fixture value, never an owner secret.
TENANT = 'retain-tenant'
USER = 'retain-user'
SECRET = fixture.FIXTURE_JWT
DEADLINE = 900


def stage(name, **fields):
    print('APPLY_NATIVE_STAGE=' + json.dumps({'stage': name, **fields}, sort_keys=True), flush=True)


def session_token():
    """Long-lived synthetic admin JWT; probes mint their own short-lived ones."""
    def enc(value):
        return base64.urlsafe_b64encode(json.dumps(value, separators=(',', ':')).encode()).rstrip(b'=')
    unsigned = (enc({'alg': 'HS256', 'typ': 'JWT'})
                + b'.' + enc({'sub': USER, 'tenant_id': TENANT, 'role': 'admin',
                              'iat': int(time.time()) - 1, 'exp': int(time.time()) + 7200}))
    signature = base64.urlsafe_b64encode(hmac.new(SECRET.encode(), unsigned, hashlib.sha256).digest()).rstrip(b'=')
    return (unsigned + b'.' + signature).decode()


def caller_command(root, project, receipt):
    return [sys.executable, str(HERE / 'secure-update-apply.py'),
            '--root', str(root), '--project', project, '--receipt', str(receipt)]


def pty_run(root, project, receipt, answers, kill_phase=None, deadline=DEADLINE, command=None):
    """Run the real reviewed caller on a real PTY, answering exact prompts.

    answers: ordered (prompt substring, response) pairs. Only prompt substrings
    are matched; echoed answers never retrigger. kill_phase: SIGKILL the process
    group the first time the durable journal reaches that exact phase.
    """
    journal = pathlib.Path(root) / '.secure-deployment' / 'journal.json'
    master, slave = pty.openpty()
    proc = subprocess.Popen(command or caller_command(root, project, receipt),
                            stdin=slave, stdout=slave, stderr=slave,
                            cwd=str(root), close_fds=True, start_new_session=True)
    os.close(slave)
    output = bytearray()
    pending = list(answers)
    killed = False
    eof = False
    end = time.monotonic() + deadline
    try:
        while True:
            if time.monotonic() > end:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except (ProcessLookupError, PermissionError):
                    pass
                raise ValueError('Reviewed caller exceeded the fixture deadline.')
            if eof and proc.poll() is not None:
                break
            ready, _, _ = select.select([master], [], [], 0.05)
            if ready and not eof:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    chunk = b''
                if chunk:
                    output += chunk
                else:
                    eof = True
            text = output.decode(errors='replace')
            if pending and pending[0][0] in text:
                os.write(master, (pending.pop(0)[1] + '\n').encode())
            if kill_phase and not killed and proc.poll() is None:
                try:
                    phase = json.loads(journal.read_text()).get('phase')
                except (OSError, ValueError):
                    phase = None
                if phase == kill_phase:
                    os.killpg(proc.pid, signal.SIGKILL)
                    killed = True
    finally:
        os.close(master)
    return proc.wait(), output.decode(errors='replace')


def checked_output(text, markers):
    if PASSWORD in text or SECRET in text:
        raise ValueError('Reviewed caller output leaked fixture secret material.')
    for marker in markers:
        if marker not in text:
            raise ValueError('Reviewed caller output missing: ' + marker)


def tampered_bundle(source, destination):
    """Copy a private plan, then change one reviewed byte. Original untouched."""
    source, destination = pathlib.Path(source), pathlib.Path(destination)
    if destination.exists():
        raise ValueError('Tampered fixture bundle already exists.')
    shutil.copytree(source, destination)
    destination.chmod(0o700)
    receipt = destination / 'preflight.json'
    value = json.loads(receipt.read_text())
    if value.get('database', {}).get('name') != 'application_fixture':
        raise ValueError('Tamper fixture target missing.')
    value['database']['name'] = 'application_fixture_tampered'
    receipt.write_text(json.dumps(value, indent=1))
    receipt.chmod(0o600)
    return receipt


def fixture_main():
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_OS') != 'Linux'
            or os.geteuid() != 0):
        raise SystemExit('Disposable standard Ubuntu runner only.')
    owner = cleanup.current()
    if owner is None:
        raise SystemExit('Private cleanup ledger required before setup.')
    with tempfile.TemporaryDirectory(prefix=ROOT_PREFIX, dir='/tmp') as td:
        work = pathlib.Path(td)
        project = PROJECT_PREFIX + str(os.getpid())
        oldimage, newimage = project + ':old', project + ':new'
        variantimage, testimage = project + ':variant', project + ':operator'
        owner.register_project(project)
        owner.register_images([oldimage, newimage, variantimage, testimage])

        def call(args, **kwargs):
            creating = args[:2] in (['docker', 'build'], ['docker', 'run']) or (args[:2] == ['docker', 'compose'] and {'up', 'run'} & set(args))
            key = owner.begin('docker-create') if creating else None
            try:
                return fixture.call(args, **kwargs)
            finally:
                if key:
                    owner.complete(key)

        label = 'hexthings.preparation.owner=' + owner.data['token']
        # Exact public historical checkpoint (89 reviewed-baseline migrations,
        # no setupctl) and the current checkout (93, with setupctl).
        if subprocess.run(['git', 'cat-file', '-e', OLD_COMMIT + '^{commit}'], cwd=ROOT, capture_output=True).returncode:
            call(['git', 'fetch', '--no-tags', '--depth=1', 'origin', OLD_COMMIT], cwd=ROOT)
        archive = fixture.call(['git', 'archive', OLD_COMMIT, 'server'], cwd=ROOT, binary=True)
        oldsrc = work / 'old-source'
        oldsrc.mkdir()
        subprocess.run(['tar', '-x', '-C', str(oldsrc)], input=archive, check=True)
        old_migrations = sorted(p.name for p in (oldsrc / 'server' / 'migrations').glob('*.sql'))
        new_migrations = sorted(p.name for p in (ROOT / 'server' / 'migrations').glob('*.sql'))
        if len(old_migrations) != 89 or len(new_migrations) != 93:
            raise ValueError('Reviewed baseline/additive migration sets changed.')
        for name, source, binaries in [('old', oldsrc / 'server', ['api', 'ingest', 'mcp', 'tenantctl']),
                                       ('new', ROOT / 'server', ['api', 'ingest', 'mcp', 'tenantctl', 'setupctl'])]:
            context = work / name
            context.mkdir()
            shutil.copytree(source / 'migrations', context / 'migrations')
            for binary in binaries:
                subprocess.run(['go', 'build', '-o', str(context / binary), './cmd/' + binary],
                               cwd=source, env=dict(os.environ, CGO_ENABLED='0', GOTOOLCHAIN='auto'), check=True)
            (context / 'Dockerfile').write_text(
                'FROM alpine:3.20\nCOPY ' + ' '.join(binaries) + ' /bin/\nCOPY migrations /migrations\nWORKDIR /\n')
            call(['docker', 'build', '--label', label, '-t', oldimage if name == 'old' else newimage, context])
        # Distinct image ID over identical current layers: models a no-change
        # reviewed republish for the already-93 acknowledged no-op apply.
        variant = work / 'variant'
        variant.mkdir()
        (variant / 'Dockerfile').write_text('FROM ' + newimage + '\nLABEL hexthings.fixture.variant=republish\n')
        call(['docker', 'build', '--label', label, '-t', variantimage, variant])
        subprocess.run(['go', 'test', '-c', '-o', str(work / 'bootstrap-tests'), './internal/setupbootstrap'],
                       cwd=ROOT / 'server', env=dict(os.environ, CGO_ENABLED='0', GOTOOLCHAIN='auto'), check=True)
        shutil.copytree(ROOT / 'server' / 'migrations', work / 'migrations')
        (work / 'Dockerfile').write_text('FROM alpine:3.20\nCOPY bootstrap-tests /bootstrap-tests\n'
                                         'COPY migrations /migrations\nWORKDIR /\nENTRYPOINT ["/bootstrap-tests"]\n')
        call(['docker', 'build', '--label', label, '-t', testimage, work])
        stage('images built', old_commit=OLD_COMMIT, baseline_migrations=89, additive_migrations=93)

        db = 'postgres://ci:' + PASSWORD + '@postgres:5432/application_fixture?sslmode=disable'
        base = {'services': {
            'postgres': {'image': 'postgres:16-alpine',
                         'environment': {'POSTGRES_USER': 'ci', 'POSTGRES_PASSWORD': PASSWORD,
                                         'POSTGRES_DB': 'application_fixture'},
                         'healthcheck': {'test': ['CMD-SHELL', 'pg_isready -U ci'],
                                         'interval': '1s', 'timeout': '2s', 'retries': 30}},
            'mosquitto': {'image': 'eclipse-mosquitto:2@sha256:75d6662dee52c947dc9dd9c29f1d8d7ffef78ddaf38001caa09755681e8bbc98'},
            'web': {'image': 'nginx:1.27-alpine'}}}
        for name in ['api', 'ingest', 'mcp']:
            base['services'][name] = {'image': oldimage, 'command': ['/bin/' + name],
                                      'environment': {'DATABASE_URL': db, 'JWT_SIGNING_SECRET': SECRET,
                                                      'MQTT_TLS': 'false', 'MQTT_HOST': 'mosquitto', 'MQTT_PORT': '1883'},
                                      'depends_on': {'postgres': {'condition': 'service_healthy'}}}
        base['services']['api']['ports'] = [{'target': 8000, 'published': str(API_PORT), 'host_ip': '127.0.0.1'}]
        owner.decorate(base, project)
        basepath = work / 'docker-compose.yml'
        basepath.write_text(json.dumps(base))
        basepath.chmod(0o600)
        compose = ['docker', 'compose', '-p', project, '-f', str(basepath)]
        try:
            call(compose + ['up', '-d', '--wait', 'postgres', 'mosquitto', 'web'])
            cid = call(compose + ['ps', '-q', 'postgres']).strip()

            def local(sql):
                return call(['docker', 'exec', cid, 'psql', '-X', '-v', 'ON_ERROR_STOP=1', '-U', 'ci',
                             '-d', 'application_fixture', '-Atc', sql]).strip()

            def ledger():
                return local('SELECT filename FROM schema_migrations ORDER BY filename').splitlines()

            helper = fixture.Effects(work, project, work / 'old.json', work / 'new.json', testimage)
            helper.operator('seed89')
            call(compose + ['up', '-d', '--no-deps', 'api', 'ingest', 'mcp'])
            helper.probe(API_PORT)
            if ledger() != old_migrations:
                raise ValueError('Reviewed baseline 89 ledger not established.')
            stage('baseline89 running', identity=TENANT + '/' + USER)

            # Established journal-managed selection, modelling a settled secure
            # installation. Fixture-created, not installer proof.
            resolved = json.loads(call(compose + ['config', '--format', 'json']))
            for name, item in helper.identities(basepath).items():
                resolved['services'][name]['image'] = item['image']
            resolved, _ = m.application.app.runtime.freeze(resolved, {name: item['image'] for name, item in helper.identities(basepath).items()})
            selected = work / 'selected-old-runtime.json'
            m.state.atomic(selected, resolved)
            with m.state.Journal(work) as journal:
                files = {str(selected): m.state.digest(selected)}
                m.state.atomic(journal.directory / 'selected.json',
                               {'version': 1, 'project': project, 'config': str(selected),
                                'sha256': files[str(selected)], 'files': files})
                m.state.atomic(journal.directory / 'journal.json',
                               {'version': 1, 'kind': 'application_bootstrap', 'phase': 'committed',
                                'project': project, 'candidate': str(selected),
                                'candidate_sha256': files[str(selected)], 'files': files,
                                'maintenance_state': 'released'})
            host = m.hostmod.DockerHost(m.fence.DockerCatalog(cid, 'ci', 'application_fixture'), project)
            hba = host.bind()
            hba_baseline = hba.read_bytes()
            fd, session = tempfile.mkstemp(prefix='synthetic-apply-session-', dir=work)
            with os.fdopen(fd, 'w') as out:
                out.write(session_token())
            prompts = lambda phrase: [('session file path:', session), ('Expected tenant ID:', TENANT),
                                      ('Expected admin user ID:', USER), ('Type ' + phrase, phrase)]
            retained = helper.retained_snapshot
            new_id = json.loads(call(['docker', 'image', 'inspect', newimage]))[0]['Id']
            variant_id = json.loads(call(['docker', 'image', 'inspect', variantimage]))[0]['Id']

            # Refusals before any apply: non-TTY, blank/wrong phrase cancel and
            # a tampered reviewed receipt. None may change anything.
            before = helper.identities(basepath)
            result = subprocess.run([sys.executable, str(HERE / 'secure-update-apply.py'), '--root', str(work),
                                     '--project', project, '--receipt', str(work / 'plan' / 'preflight.json')],
                                    stdin=subprocess.DEVNULL, capture_output=True, text=True)
            if result.returncode != 1 or 'interactive terminal' not in result.stderr:
                raise ValueError('Non-TTY refusal missing.')
            checked_output(result.stdout + result.stderr, [])
            stage('refusal non-tty')
            preparemod.prepare(work, project, new_id, work / 'plan')
            receipt = work / 'plan' / 'preflight.json'
            code, out = pty_run(work, project, receipt, prompts('apply'))
            if code != 0 or 'Cancelled. Nothing changed.' not in out:
                raise ValueError('Wrong-phrase cancellation missing.')
            checked_output(out, ['apply of the exact prepared plan', 'Not covered:'])
            if helper.identities(basepath) != before or ledger() != old_migrations:
                raise ValueError('Cancelled apply changed the fixture.')
            stage('refusal wrong phrase cancelled cleanly')
            bad = tampered_bundle(work / 'plan', work / 'plan-tampered')
            code, out = pty_run(work, project, bad, prompts('APPLY'))
            if code != 1 or 'could not be verified' not in out:
                raise ValueError('Tampered reviewed receipt was not refused.')
            checked_output(out, [])
            if helper.identities(basepath) != before or ledger() != old_migrations:
                raise ValueError('Refused tampered plan changed the fixture.')
            stage('refusal tampered receipt')

            # Apply 1: exact historical 89 -> current additive 93 through the
            # real reviewed caller, production provider and Coordinator.
            code, out = pty_run(work, project, receipt, prompts('APPLY'))
            if code != 0 or 'settled: committed' not in out:
                raise ValueError('Reviewed apply did not settle: ' + out[-2000:])
            checked_output(out, ['Plan SHA256', 'legacy Upgrade entry now permanently refuses'])
            if m.state.selection(work)['config'] != str(work / 'plan' / 'candidate-runtime.json'):
                raise ValueError('Selection did not move to the reviewed candidate.')
            if ledger() != new_migrations:
                raise ValueError('Additive 93 ledger not exact after apply.')
            helper.probe(API_PORT)
            if helper.retained_snapshot != retained:
                raise ValueError('Apply lost retained inventory.')
            if hba.read_bytes() != hba_baseline:
                raise ValueError('Original HBA not restored after settled apply.')
            stage('apply1 committed', ledger=93, retained=True, hba_restored=True)

            # The legacy updater permanently refuses this journal-managed root.
            (work / 'scripts').mkdir()
            shutil.copy(ROOT / 'scripts' / 'upgrade.sh', work / 'scripts' / 'upgrade.sh')
            tree_before = sorted(str(p.relative_to(work)) for p in work.rglob('*'))
            for option in ('--yes', '--rollback'):
                refusal = subprocess.run(['bash', str(work / 'scripts' / 'upgrade.sh'), option],
                                         capture_output=True, timeout=15)
                if refusal.returncode == 0 or b'Secure server upgrade is unavailable' not in refusal.stderr:
                    raise ValueError('Legacy updater did not permanently refuse.')
            if sorted(str(p.relative_to(work)) for p in work.rglob('*')) != tree_before:
                raise ValueError('Refused legacy updater changed files.')
            stage('legacy upgrade permanently refuses')

            # The reviewed operator refuses without the exact acknowledgement.
            ackless = subprocess.run(compose + ['-f', str(work / 'plan' / 'candidate-runtime.json'),
                                                'run', '--rm', '--no-deps', '--pull', 'never',
                                                '--entrypoint', '/bin/setupctl', 'api', 'apply-reviewed'],
                                     cwd=work, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
            if ackless.returncode == 0:
                raise ValueError('Ack-less reviewed schema apply was accepted.')
            if PASSWORD in ackless.stdout + ackless.stderr:
                raise ValueError('Ack-less refusal leaked fixture material.')
            if ledger() != new_migrations:
                raise ValueError('Ack-less refusal changed the ledger.')
            stage('ack-less operator refused', ledger_unchanged=True)

            # Apply 2: republish with identical layers (distinct image ID). The
            # already-93 reviewed apply must be an acknowledged no-op.
            preparemod.prepare(work, project, variant_id, work / 'plan2')
            code, out = pty_run(work, project, work / 'plan2' / 'preflight.json', prompts('APPLY'))
            if code != 0 or 'settled: committed' not in out:
                raise ValueError('Already-93 republish apply did not settle: ' + out[-2000:])
            checked_output(out, ['Plan SHA256'])
            if ledger() != new_migrations or helper.retained_snapshot != retained:
                raise ValueError('Already-93 apply was not an acknowledged no-op.')
            if m.state.selection(work)['config'] != str(work / 'plan2' / 'candidate-runtime.json'):
                raise ValueError('Second apply selected the wrong runtime.')
            helper.probe(API_PORT)
            if hba.read_bytes() != hba_baseline:
                raise ValueError('HBA changed during already-93 apply.')
            stage('apply2 already-93 acknowledged no-op committed')

            # Apply 3: SIGKILL the caller mid-maintenance; the durable block
            # must hold, then a fresh typed RECOVER must settle consistently.
            preparemod.prepare(work, project, new_id, work / 'plan3')
            receipt3 = work / 'plan3' / 'preflight.json'
            code, out = pty_run(work, project, receipt3, prompts('APPLY'), kill_phase='changing')
            if code != -signal.SIGKILL:
                raise ValueError('Synthetic process loss did not happen.')
            checked_output(out, [])
            with m.state.Journal(work) as journal:
                held = journal.read()
                if held.get('maintenance_state') != 'held' or held.get('phase') in {'committed', 'restored'}:
                    raise ValueError('Interrupted apply did not retain the durable block.')
            try:
                m.state.selection(work)
            except ValueError:
                pass
            else:
                raise ValueError('Interrupted apply allowed ordinary selection.')
            stage('apply3 interrupted mid-maintenance; selection blocked')
            code, out = pty_run(work, project, receipt3, prompts('RECOVER'))
            if code != 0 or 'settled: ' not in out:
                raise ValueError('Typed recovery did not settle: ' + out[-2000:])
            checked_output(out, ['RECOVERY of an interrupted maintenance session'])
            settled = out.split('settled: ', 1)[1].split('.', 1)[0].strip()
            if settled not in {'restored', 'committed'}:
                raise ValueError('Unknown recovery settlement.')
            chosen = m.state.selection(work)['config']
            expect = work / 'plan3' / ('previous-runtime.json' if settled == 'restored' else 'candidate-runtime.json')
            if chosen != str(expect):
                raise ValueError('Recovery selected the wrong runtime.')
            if ledger() != new_migrations:
                raise ValueError('Recovery replayed or lost the additive ledger.')
            helper.probe(API_PORT)
            if helper.retained_snapshot != retained:
                raise ValueError('Recovery lost retained inventory.')
            if hba.read_bytes() != hba_baseline:
                raise ValueError('HBA not restored after settled recovery.')
            stage('apply3 recovery settled', settled=settled)
            print(json.dumps({'results': [
                {'case': 'refusals', 'non_tty': True, 'wrong_phrase_cancel': True, 'tampered_receipt': True},
                {'case': 'apply-89-to-93', 'settled': 'committed', 'ledger': 93, 'retained': True,
                 'legacy_upgrade_refused': True, 'ackless_operator_refused': True, 'hba_restored': True},
                {'case': 'apply-already-93-republish', 'settled': 'committed', 'acknowledged_noop': True},
                {'case': 'interrupted-apply-then-typed-recover', 'settled': settled,
                 'selection_blocked_while_held': True, 'retained': True, 'hba_restored': True}],
                'limits': ['Synthetic disposable fixture only; NOT owner activation or owner machine proof',
                           'NOT fresh TLS/transport acceptance; NOT Windows; no physical device',
                           'Limited authenticated retained-list identity proof, not full background compatibility',
                           'Simulated process loss via SIGKILL, not physical power loss']}, indent=2), flush=True)
            print('PASS native reviewed apply caller: real PTY apply of the exact published menu caller through '
                  'production provider/Coordinator; historical 89 to additive 93 commit; exact refusals '
                  '(non-TTY, wrong phrase, tampered receipt, ack-less operator); already-93 republish acknowledged '
                  'no-op; SIGKILL mid-maintenance held then typed RECOVER settled; retained identity/inventory and '
                  'original HBA restored at every settlement. Disposable synthetic fixture only.', flush=True)
        finally:
            cleanup.diagnostics(owner, compose + ['logs', '--no-color', '--tail', '20'])


def self_test():
    """Local contract checks only; never Docker, never the real caller."""
    if API_PORT != 8000 or '127.0.0.1:8000' not in (HERE / 'secure-update-apply.py').read_text().split('ORIGIN = ')[1].splitlines()[0]:
        raise AssertionError('caller origin drifted from the harness binding')
    for prefix, want in [(ROOT_PREFIX, 'hexthings-application-ci-'), (PROJECT_PREFIX, 'hexthings-app-ci-')]:
        if prefix != want:
            raise AssertionError('fixture prefix drifted from the native fixture contract')
    with tempfile.TemporaryDirectory() as td:
        root = pathlib.Path(td)
        (root / '.secure-deployment').mkdir()
        journal = root / '.secure-deployment' / 'journal.json'

        # PTY driver answers the exact caller prompts in order.
        mock = ('import sys,time\n'
                'def prompt(t):\n sys.stdout.write(t);sys.stdout.flush();return sys.stdin.readline().strip()\n'
                'a=prompt("  Existing private admin session file path: ")\n'
                'b=prompt("  Expected tenant ID: ")\n'
                'c=prompt("  Expected admin user ID: ")\n'
                'd=prompt("  Type APPLY to start the reviewed maintenance window, or Enter to cancel: ")\n'
                'sys.exit(0 if (a,b,c,d)==("sess","t1","u1","APPLY") else 7)\n')
        journal.write_text(json.dumps({'phase': 'committed'}))
        code, out = pty_run(root, 'fixture', root / 'unused', [('session file path:', 'sess'),
                                                             ('Expected tenant ID:', 't1'),
                                                             ('Expected admin user ID:', 'u1'),
                                                             ('Type APPLY', 'APPLY')],
                            deadline=30, command=[sys.executable, '-c', mock])
        if code != 0:
            raise AssertionError('PTY driver did not answer the exact prompt sequence')

        # PTY driver kills exactly at the requested durable phase.
        killer = ('import json,pathlib,time,sys\n'
                  'p=pathlib.Path(sys.argv[1])\n'
                  'p.write_text(json.dumps({"phase":"prepared"}))\n'
                  'time.sleep(0.3)\n'
                  'p.write_text(json.dumps({"phase":"changing"}))\n'
                  'time.sleep(30)\n')
        code, out = pty_run(root, 'fixture', root / 'unused', [], kill_phase='changing',
                            deadline=30, command=[sys.executable, '-c', killer, str(journal)])
        if code != -signal.SIGKILL:
            raise AssertionError('phase-triggered SIGKILL failed')

        # Tamper helper changes exactly one reviewed byte in a copy.
        plan = root / 'plan'
        plan.mkdir()
        (plan / 'preflight.json').write_text(json.dumps({'version': 1, 'database': {'name': 'application_fixture'}}))
        before = (plan / 'preflight.json').read_bytes()
        bad = tampered_bundle(plan, root / 'plan-tampered')
        if json.loads(bad.read_text())['database']['name'] != 'application_fixture_tampered':
            raise AssertionError('tamper did not change the copy')
        if (plan / 'preflight.json').read_bytes() != before:
            raise AssertionError('tamper touched the original')
        try:
            tampered_bundle(plan, root / 'plan-tampered')
        except ValueError:
            pass
        else:
            raise AssertionError('tamper destination reuse allowed')

    # The native entry point refuses without the disposable-runner markers.
    env = {k: v for k, v in os.environ.items() if k not in {'GITHUB_ACTIONS', 'RUNNER_OS', 'HEXTHINGS_CLEANUP_MANIFEST'}}
    refusal = subprocess.run([sys.executable, str(HERE / 'test-secure-update-apply-native.py')],
                             env=env, capture_output=True, text=True)
    if refusal.returncode == 0 or 'Disposable standard Ubuntu runner only' not in refusal.stderr + refusal.stdout:
        raise AssertionError('native entry ran without disposable-runner markers')
    print('SELF_TEST_ONLY: PTY prompt driving, phase-triggered SIGKILL, single-byte tamper copy and '
          'runner-marker refusal; no Docker image, real caller execution or owner acceptance', flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--self-test', action='store_true')
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return
    if (os.environ.get('GITHUB_ACTIONS') != 'true' or os.environ.get('RUNNER_OS') != 'Linux'
            or os.geteuid() != 0):
        raise SystemExit('Disposable standard Ubuntu runner only.')
    path = os.environ.get('HEXTHINGS_CLEANUP_MANIFEST')
    if not path:
        raise SystemExit('Private cleanup ledger required before setup.')
    cleanup.Owner.create(path)
    try:
        return fixture_main()
    finally:
        cleanup.Owner(path).cleanup()


if __name__ == '__main__':
    main()
