#!/usr/bin/env python3
"""Execution/recovery caller for explicitly reviewed single-node transactions.

Not a default menu activator. A host-specific provider must verify current owner
scope, prevent outside writers reconnecting, and implement live acceptance and
prior-image compatibility. No receipt, checkbox or provider default grants these.
The caller retains a durable maintenance block across process loss/release errors.
"""
import dataclasses
import importlib.util
import pathlib

HERE = pathlib.Path(__file__).resolve().parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


application = load('secure-application-docker')
transport = load('secure-transport-docker')
state = application.state


@dataclasses.dataclass(frozen=True)
class Target:
    root: str
    project: str
    scope: str
    action: str
    previous: str
    candidate: str
    receipt_sha256: str


def checked_callback(callback, *args):
    # Failures must raise. In particular False must never silently mean success.
    if callback(*args) is not None:
        raise ValueError('Verifier must raise on failure, not return an approval flag.')


class GuardedEffects:
    """Fence checks surround every adapter entry, including rollback/acceptance."""
    def __init__(self, effects, check):
        self.effects, self.check = effects, check

    def __getattr__(self, name):
        member = getattr(self.effects, name)
        if not callable(member):
            raise AttributeError(name)
        def guarded(*args, **kwargs):
            self.check()
            result = member(*args, **kwargs)
            self.check()
            return result
        return guarded


class GuardedJournal:
    def __init__(self, journal, check):
        self.journal, self.check = journal, check

    def __getattr__(self, name):
        return getattr(self.journal, name)

    def begin(self, *args, **kwargs):
        self.check()
        self.journal.begin(*args, **kwargs)
        value = self.journal.read()
        value['maintenance_state'] = 'held'
        state.atomic(self.journal.directory / 'journal.json', value)
        return value

    def commit(self):
        self.check()
        self.journal.commit()

    def select_restored(self):
        self.check()
        self.journal.select_restored()


class Coordinator:
    """Provider methods: review(target), enter(target)->opaque ticket,
    check(ticket,target), release(ticket,target), retain(ticket,target).

    All except enter return None or raise. enter/check own actual prevention of
    outside writers, not just pg_stat_activity or stopped Compose containers.
    review verifies fresh trusted target/action/downtime/route/trust permission.
    accept(value,target) and compatible(value) remain mandatory live checks.
    Retain must never release protection on an unverified failure/interruption.
    """
    def __init__(self, provider, accept, compatible, factory=None):
        if not all(callable(getattr(provider, name, None)) for name in ['review', 'enter', 'check', 'release', 'retain']):
            raise ValueError('Explicit review and maintenance lifecycle provider required.')
        if not callable(accept) or not callable(compatible):
            raise ValueError('Explicit runtime and previous-image compatibility verifiers required.')
        self.provider, self.accept, self.compatible = provider, accept, compatible
        self.factory = factory or self.production

    @staticmethod
    def production(target, receipt, authorize, accept, compatible):
        if target.scope == 'application_bootstrap':
            return application.DockerApplication(target.root, target.project, receipt, authorize, accept, compatible)
        return transport.DockerTransport(target.root, target.project, receipt, authorize, accept)

    def run(self, root, project, receipt, action):
        if action not in {'activate', 'recover'}:
            raise ValueError('Explicit activation or recovery action required.')
        root = pathlib.Path(root).resolve()
        receipt = pathlib.Path(receipt).absolute()
        plan = state.read_private(receipt)
        scope = plan.get('scope', 'transport_only')
        if scope not in {'application_bootstrap', 'transport_only'}:
            raise ValueError('Unknown execution scope.')
        previous = receipt.parent / 'previous-runtime.json'
        candidate = receipt.parent / 'candidate-runtime.json'
        if plan.get('root') != str(root) or plan.get('project') != project:
            raise ValueError('Reviewed installation differs from the plan.')
        target = Target(str(root), project, scope, action, str(previous), str(candidate), state.digest(receipt))
        # The same OS lock spans review, maintenance, effects and release.
        with state.Journal(root) as journal:
            # Explicit opt-in for providers composed from durable host barriers.
            # Pass the exact already-held journal, never reacquire the OS lock.
            binder = getattr(self.provider, 'bind_journal', None)
            if binder is not None:
                if not callable(binder): raise ValueError('Provider journal binder must be callable.')
                checked_callback(binder, journal, target)
            marker = journal.directory / 'maintenance.json'
            journal_path = journal.directory / 'journal.json'
            has_journal = journal_path.exists() or journal_path.is_symlink()
            has_marker = marker.exists() or marker.is_symlink()
            binding = {'version': 1, 'root': str(root), 'project': project,
                       'scope': scope, 'previous': str(previous),
                       'candidate': str(candidate), 'receipt': str(receipt),
                       'receipt_sha256': target.receipt_sha256}
            if has_marker and state.read_private(marker) != binding:
                raise ValueError('Maintenance session does not match this reviewed target.')
            if action == 'activate' and has_marker:
                raise ValueError('An earlier maintenance attempt requires explicit recovery.')
            if action == 'recover' and not has_journal and not has_marker:
                raise ValueError('No interrupted execution or maintenance session to recover.')
            if action == 'recover' and has_journal:
                value = journal.read()
                if (value.get('kind', 'transport_only') != scope or value.get('project') != project
                        or value.get('previous') != str(previous) or value.get('candidate') != str(candidate)
                        or value.get('receipt_sha256') != target.receipt_sha256):
                    raise ValueError('Recovery does not match the exact recorded transaction.')
            # Capture the verified current selector BEFORE admission closes.
            # Only the locked adapter's pre-change inspection can use it.
            selected = state.selection(root) if action == 'activate' else None
            checked_callback(self.provider.review, target)
            # Persist admission blocking before provider.enter can establish a
            # fence or fail. Power loss here must never reopen ordinary actions.
            state.atomic(marker, binding)
            ticket = None
            def check():
                journal.locked()
                if (state.read_private(marker) != binding
                        or state.digest(receipt) != target.receipt_sha256):
                    raise ValueError('Reviewed execution plan or maintenance marker changed.')
                checked_callback(self.provider.check, ticket, target)
            def selected_for_inspection(requested_root):
                check()
                if pathlib.Path(requested_root).resolve() != root:
                    raise ValueError('Maintenance inspection target changed.')
                if journal_path.exists() or journal_path.is_symlink():
                    raise ValueError('Pre-change inspection cannot run after a transaction starts.')
                if selected:
                    state.check_manifest(selected['files'])
                    state.checked_config(selected['config'], selected['sha256'])
                return selected
            def authorize(verb, requested_project, old, new):
                expected = ('application-upgrade' if action == 'activate' else 'application-recover') if scope == 'application_bootstrap' else action
                if (verb != expected or requested_project != project or str(old) != target.previous or str(new) != target.candidate):
                    raise ValueError('Execution action or target changed.')
                check()
                checked_callback(self.provider.review, target)
            try:
                ticket = self.provider.enter(target)
                if ticket is None or isinstance(ticket, (bool, str, int, float, dict, list)):
                    raise ValueError('Live opaque maintenance session required, not a saved approval.')
                check()
                def accepted(value, destination):
                    checked_callback(self.accept, value, destination)
                def compatible(value):
                    checked_callback(self.compatible, value)
                adapter = self.factory(target, receipt, authorize, accepted, compatible)
                adapter.selection_reader = selected_for_inspection
                effects = GuardedEffects(adapter, check)
                protected = GuardedJournal(journal, check)
                engine = application.app.ApplicationTransaction(protected, effects) if scope == 'application_bootstrap' else transport.load('secure-transaction').Transaction(protected, effects)
                if action == 'recover' and not has_journal:
                    # Admission/fence may have changed, but begin() never ran,
                    # so no transaction service/schema effect was allowed.
                    authorize('application-recover' if scope == 'application_bootstrap' else 'recover', project, previous, candidate)
                    untouched = {'previous': str(previous), 'candidate': str(candidate),
                                 'files': plan['files'], 'previous_files': plan['previous_files'],
                                 'previous_images': {name: item['image'] for name, item in plan['current_services'].items()}}
                    effects.verify_recovery_inputs(untouched)
                    effects.accept(untouched, 'previous')
                    result = {'state': 'maintenance_settled_before_changes', 'changed': False, 'source_reading_proven': False}
                elif action == 'recover' and value['phase'] in {'committed', 'restored'}:
                    # Crash after commit but before release: fresh acceptance,
                    # never blind rollback or repeated schema application.
                    authorize('application-recover' if scope == 'application_bootstrap' else 'recover', project, previous, candidate)
                    effects.verify_recovery_inputs(value)
                    effects.accept(value, 'candidate' if value['phase'] == 'committed' else 'previous')
                    result = {'state': value['phase'], 'changed': False, 'source_reading_proven': False}
                elif action == 'recover':
                    result = engine.recover()
                else:
                    images = {name: item['image'] for name, item in plan['current_services'].items()}
                    result = engine.activate(project, previous, candidate, images, receipt)
                check()
                final = journal.read() if journal_path.exists() else None
                if final and final['phase'] not in {'committed', 'restored'}:
                    raise ValueError('Runtime was not durably settled. Keep maintenance protection.')
                checked_callback(self.provider.release, ticket, target)
                if final:
                    final['maintenance_state'] = 'released'
                    state.atomic(journal_path, final)
                # If interrupted between release and this removal, normal
                # selection remains blocked and fresh recovery rechecks runtime.
                state.unlink_durable(marker)
                state.selection(root)
                return result
            except BaseException:
                # enter() can fail without returning a ticket. The provider
                # must retain/recover its fence by target in that case too.
                checked_callback(self.provider.retain, ticket, target)
                raise
