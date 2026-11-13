#!/usr/bin/env python3
"""Explicit Linux single-node application host provider, no owner/menu entry.

Composes actual Docker/volume HBA binding, exclusive catalog drain, local static
schema tools, grouped gated runtime and settled direct normalization. A fresh
scope reviewer and connection/runtime/compatibility probes are mandatory. These
remain caller responsibilities; this provider never grants owner authority or
claims that retained-list reads alone prove full compatibility. Privileged host
administrators and PostgreSQL superusers via the preserved local admin channel
remain outside the supported concurrent-writer boundary.
"""
import dataclasses
import importlib.util
import pathlib
from urllib.parse import urlsplit, parse_qs

HERE = pathlib.Path(__file__).resolve().parent

def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module

execution = load('secure-execution')
application = execution.application
state = execution.state
fence = load('secure-postgres-fence')
hostmod = load('secure-postgres-hba-docker')
operators = load('secure-postgres-operator')
groups = load('secure-application-startgroup')

@dataclasses.dataclass(frozen=True)
class Session:
    binding: tuple
    nonce: object

class Provider:
    def __init__(self, catalog, reviewer, connections, runner=None):
        if not all(callable(x) for x in [reviewer, connections]):
            raise ValueError('Fresh owner scope and actual connection proofs required.')
        self.catalog, self.reviewer, self.connections, self.runner = catalog, reviewer, connections, runner
        self.journal = self.target = self.ticket = self.adapter = None
        self.host = self.barrier = self.operator = self.group = None
        self.peer_values = []

    def bind_journal(self, journal, target):
        journal.locked()
        if self.journal is not None:
            raise ValueError('One invocation per provider; recovery requires a fresh instance.')
        if target.scope != 'application_bootstrap' or pathlib.Path(target.root) != journal.root:
            raise ValueError('Only the exact reviewed application transaction is supported.')
        self.journal, self.target = journal, target
        self.path = journal.directory / 'application-provider.json'
        self.host = hostmod.DockerHost(self.catalog, target.project, self.runner)
        self.host.bind()
        old = state.read_private(target.previous)
        user, database, _ = application.database_target(old)
        receipt_path = pathlib.Path(target.previous).parent / 'preflight.json'
        if state.digest(receipt_path) != target.receipt_sha256:
            raise ValueError('Provider supports only the exact reviewed preflight receipt, not a nearby plan.')
        receipt = state.read_private(receipt_path)
        expected = receipt['current_services']['postgres']
        if (user != self.catalog.user or database != self.catalog.database
                or expected.get('container_id') != self.catalog.container
                or expected.get('image') != self.host.binding['image']
                or old['services']['postgres']['image'] != self.host.binding['image']):
            raise ValueError('Catalog/host must match exact reviewed PostgreSQL CID/image/database/admin.')

    def review(self, target):
        if self.journal is None or target != self.target:
            raise ValueError('Provider must bind the exact Coordinator-held journal first.')
        self.journal.locked(); self.host.check()
        execution.checked_callback(self.reviewer, target)

    def binding(self):
        return fence.DatabaseFence.binding(self.target)

    def record(self):
        value = state.read_private(self.path)
        if (set(value) != {'version','binding','host','identity','original_allow','stage','peers'}
                or value['version'] != 1 or value['binding'] != self.binding()
                or value['host'] != self.host.binding or value['original_allow'] is not True
                or value['stage'] not in {'preparing','closing','local','peers','restoring','original','normalized','released'}
                or not isinstance(value['peers'], list)):
            raise ValueError('Exact durable application host-provider record required.')
        self.database.inspect(value['identity'])
        return value

    def save(self, value, stage, peers=None):
        value['stage'] = stage
        if peers is not None: value['peers'] = peers
        state.atomic(self.path, value)

    def reload(self, expected):
        original = bytes.fromhex(self.barrier.read()['original'])
        phase = 'original' if expected == original else 'restricted'
        if not self.catalog.inspect()['allow']: phase = 'exclusive'
        # Tuple expectations are recovered from exact HBA bytes, not a loose
        # provider-stage flag. This also covers a lost handoff acknowledgement.
        peers = []
        if phase == 'restricted':
            for rule in hostmod.hba.rules(expected.split(b'# Original host authentication follows unchanged',1)[0]):
                if rule['database'] == self.catalog.database and rule['kind'] in {'hostssl','hostnossl'} and rule['method'] == 'scram-sha-256':
                    peers.append({'address':str(rule['network'].network_address),'user':rule['user'],'ssl':rule['kind']=='hostssl'})
        self.host.reload_and_verify(expected, lambda raw: execution.checked_callback(self.connections, phase, peers, raw))

    def connection_guard(self,phase,raw):
        """Transition-only proof boundary, never normal runtime admission.

        Does not call guard/check/reload or a connection callback recursively.
        Deliberately supports held intermediate preparing/closing/restoring states
        while retaining scope, marker/receipt, exact host/database/HBA identity.
        """
        if phase not in {'original','restricted','exclusive'}or not isinstance(raw,bytes):
            raise ValueError('Explicit connection phase and exact HBA bytes required.')
        self.review(self.target)
        marker=state.read_private(self.journal.directory/'maintenance.json')
        if (any(marker.get(k)!=v for k,v in self.binding().items())
                or state.digest(pathlib.Path(self.target.previous).parent/'preflight.json')!=self.target.receipt_sha256):
            raise ValueError('Exact held transition marker and reviewed receipt required.')
        value=self.record();live=self.database.inspect(value['identity'])
        record=self.barrier.read();actual,_=self.barrier.checked()
        original=bytes.fromhex(record['original'])
        if actual!=raw or raw not in {bytes.fromhex(record['current']),bytes.fromhex(record['pending'])}:
            raise ValueError('Transition proof differs from exact held HBA intent.')
        if phase=='exclusive':
            if (value['stage']!='closing'or value['peers']or live['allow']or live['sessions']
                    or raw!=hostmod.hba.render(original,self.catalog.database,self.catalog.user,[])):
                raise ValueError('Exact closed zero-session database behind local-only HBA required.')
        elif not live['allow']or (phase=='original')!=(raw==original):
            raise ValueError('Connection phase differs from live database/HBA admission.')

    def enter(self, target):
        self.review(target)
        self.database = fence.DatabaseFence(self.journal, self.catalog, self.reviewer)
        exists = self.path.exists() or self.path.is_symlink()
        if exists:
            value = self.record()
            if target.action != 'recover': raise ValueError('Existing host-provider session needs explicit recovery.')
        else:
            if target.action != 'activate': raise ValueError('No durable host-provider session to recover.')
            live = self.database.inspect()
            if live['allow'] is not True: raise ValueError('Database was already closed; cannot invent reopening authority.')
            value = {'version':1,'binding':self.binding(),'host':self.host.binding,
                     'identity':self.database.identity(live),'original_allow':True,'stage':'preparing','peers':[]}
            state.atomic(self.path, value)
        self.barrier = hostmod.hba.Barrier(self.journal,self.host.path,self.catalog.database,self.catalog.user,self.host.check,self.reload)
        final = self.journal.read() if (self.journal.directory/'journal.json').exists() else None
        if final and final['phase'] in {'committed','restored'}:
            # Settled recovery never closes/reopens a fresh SQL attempt. Restore
            # the retained exact baseline and finish direct runtime acceptance.
            self.save(value,'restoring')
            live = self.database.inspect(value['identity'])
            if not live['allow']: raise ValueError('Settled database admission unexpectedly closed; separate support required.')
            self.barrier.recover_release()
            self.save(value,'original',[])
        else:
            self.barrier.enter(recovery=self.barrier.record_path.exists())
            self.close_local(value)
        self.ticket = Session(tuple(sorted(self.binding().items())),object())
        self.check(self.ticket,target)
        return self.ticket

    def close_local(self, value=None):
        self.review(self.target)
        value = value or self.record()
        # Persist the exact empty-peer closing intent before HBA replacement.
        # The connection callback validates durable tuples during reload; a lost
        # handoff acknowledgement must recover this intent, not stale peers.
        self.save(value,'closing',[])
        self.barrier.handoff([])
        # Every ALTER is identity-checked by the catalog transaction. Durable
        # intent and original_allow survive any lost ALTER/drain acknowledgement.
        self.catalog.allow(False,value['identity'])
        live = self.database.inspect(value['identity'])
        if live['allow']: raise ValueError('Database did not close before drain.')
        result = self.catalog.drain(value['identity'])
        if (not isinstance(result,dict) or set(result)!={'terminated'} or not isinstance(result['terminated'],list)
                or any(item is not True for item in result['terminated'])):
            raise ValueError('Database sessions were not all drained.')
        live = self.database.inspect(value['identity'])
        if live['allow'] or live['sessions']: raise ValueError('Exclusive drain not verified.')
        self.host.check()
        # Exercise the closed state before reopen. HBA outside denial alone is
        # not a database-closed connection proof. A failure retains closing and
        # datallowconn=false for explicit fresh recovery, never error cleanup.
        raw=self.host.path.read_bytes()
        self.connection_guard('exclusive',raw)
        execution.checked_callback(self.connections,'exclusive',[],raw)
        self.connection_guard('exclusive',raw)
        self.catalog.allow(value['original_allow'],value['identity'])
        if not self.database.inspect(value['identity'])['allow']: raise ValueError('Controlled local database channel did not reopen.')
        self.save(value,'local',[])
        self.barrier.check()

    def original(self, prove=True):
        self.review(self.target); value = self.record()
        record = self.barrier.read(); live,_ = self.barrier.checked()
        if (record['stage'] != 'released' or live != bytes.fromhex(record['original'])
                or not self.database.inspect(value['identity'])['allow']):
            raise ValueError('Original database/HBA admission is not freshly verified.')
        if prove: self.reload(live)

    def check(self,ticket,target):
        self.review(target)
        if ticket is None or ticket is not self.ticket or ticket.binding != tuple(sorted(self.binding().items())):
            raise ValueError('Fresh opaque application host-provider session required.')
        value = self.record()
        if value['stage'] in {'original','normalized','released'}:
            self.original(False); return
        if value['stage'] not in {'local','peers'}: raise ValueError('Host-provider transition is incomplete; recover explicitly.')
        if not self.database.inspect(value['identity'])['allow']: raise ValueError('Controlled database admission is closed unexpectedly.')
        hba_record = self.barrier.read()
        live,_ = self.barrier.checked()
        if hba_record['stage'] != 'held' or live != bytes.fromhex(hba_record['current']):
            raise ValueError('Exact held host-auth state required.')
        expected = hostmod.hba.render(bytes.fromhex(self.barrier.read()['original']),self.catalog.database,self.catalog.user,
                                     [{k:p[k] for k in ['address','user','ssl']} for p in value['peers']])
        if self.host.path.read_bytes() != expected: raise ValueError('Host-provider HBA/peer record diverged.')
        self.host.verify_peers(value['peers'])

    def guard(self):
        self.check(self.ticket,self.target)

    def peers(self, values):
        if self.adapter is None or set(values) != set(self.adapter.writers):
            raise ValueError('Exact full application writer set required for peer admission.')
        pg = self.host.docker(['inspect',self.catalog.container])[0]['NetworkSettings']['Networks']
        peers = []
        for name,item in values.items():
            networks = item.get('NetworkSettings',{}).get('Networks',{})
            common = set(networks) & set(pg)
            if len(common)!=1: raise ValueError('One unambiguous shared database network required.')
            network = next(iter(common))
            query = parse_qs(urlsplit(self.adapter.old['services'][name]['environment']['DATABASE_URL']).query)
            mode = query.get('sslmode',['prefer'])
            if mode not in [['disable'],['require'],['verify-ca'],['verify-full']]:
                raise ValueError('An exact SSL class is required; no prefer/allow admission guessing.')
            peers.append({'container':item['Id'],'network':network,'address':networks[network]['IPAddress'],
                          'user':self.catalog.user,'ssl':mode!=['disable']})
        self.host.verify_peers(peers)
        return peers

    def admit(self, values):
        self.guard(); peers = self.peers(values)
        # The group has bound all created CIDs before starting and freshly
        # verified every held gate. Persist the exact peer set before HBA write.
        value = self.record(); self.save(value,'closing',peers)
        self.barrier.handoff([{k:p[k] for k in ['address','user','ssl']} for p in peers])
        self.save(value,'peers',peers); self.guard()

    def prove(self, values):
        self.guard()
        if self.peers(values) != self.record()['peers']: raise ValueError('Grouped admitted endpoints changed.')
        self.reload(self.host.path.read_bytes())

    def factory(self,target,receipt,authorize,accept,compatible):
        self.guard()
        self.group = groups.Group(self.journal,self.guard,self.admit,self.prove)
        self.group.before_stop = self.close_local
        def operator(value, action):
            self.close_local()
            if self.operator is None:
                self.operator = operators.LocalOperator(self.journal,self.catalog,self.adapter.new['services']['api']['image'],self.guard,self.runner)
                if self.operator.path.exists():
                    staged = self.operator.record()
                    if staged['stage'] in {'creating','copying','removed'}: self.operator.recover_staging()
                if not self.operator.path.exists(): self.operator.prepare()
            return self.operator(value,action)
        def accepted(value,slot):
            if self.record()['stage']=='local':
                # Pre-change recovery can have original direct writers. Exact
                # adapter inspection plus explicit full-set handoff precede reads.
                found = self.adapter.checked_containers(value[slot],False)
                self.admit({name:found[name] for name in self.adapter.writers})
            execution.checked_callback(accept,value,slot)
        def compatibility(value):
            self.close_local()
            self.group.start(self.adapter,value,'compatibility')
            execution.checked_callback(compatible,value)
            self.close_local()
            self.adapter.compose(value['previous'],'stop',*self.adapter.writers)
            self.adapter.verify_writers_stopped(value)
            self.close_local()
        kwargs = {'maintenance_operator':operator,'runtime_group':self.group}
        if self.runner is not None: kwargs['execute']=self.runner
        self.adapter = application.DockerApplication(target.root,target.project,receipt,authorize,accepted,compatibility,**kwargs)
        if self.catalog.user!=self.adapter.user or self.catalog.database!=self.adapter.db:
            raise ValueError('Provider catalog differs from reviewed application database/admin.')
        return self.adapter

    def release(self,ticket,target):
        self.check(ticket,target)
        value = self.record(); final = self.journal.read() if (self.journal.directory/'journal.json').exists() else None
        if not final:
            if target.action != 'recover' or self.adapter is None:
                raise ValueError('Only explicit pre-change recovery can release without a journal.')
            found = self.adapter.checked_containers(target.previous,True)
            current = {name:{'image':item['Image'],'container_id':item['Id']} for name,item in found.items()}
            if current != self.adapter.plan['current_services']:
                raise ValueError('Pre-change recovery requires exact unchanged original service identities.')
            self.barrier.release(); self.save(value,'original',[]); self.original()
            self.save(value,'released',[]); return
        if final['phase'] not in {'committed','restored'}:
            raise ValueError('Concrete provider requires a durably settled application before release.')
        self.save(value,'restoring')
        self.barrier.recover_release()
        self.save(value,'original',[])
        slot = 'candidate' if final['phase']=='committed' else 'previous'
        if self.adapter is None: raise ValueError('Final actual application adapter unavailable.')
        self.group.normalize(self.adapter,final,slot,final['phase'],self.original,self.adapter.accept)
        self.save(value,'normalized',[])
        self.original()
        self.save(value,'released',[])

    def retain(self,ticket,target):
        # Never release or run SQL as error cleanup. A failed entry may have no
        # ticket; immutable durable records identify the exact recovery target.
        self.review(target)
        if self.path.exists(): self.record()
