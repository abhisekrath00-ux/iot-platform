#!/usr/bin/env python3
"""Concrete provider's durable HBA/catalog composition, fake Docker only."""
import copy
import dataclasses
import importlib.util
import pathlib
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

m=load('secure-application-provider');hostfixture=load('test-secure-postgres-hba-docker');catalogfixture=load('test-secure-postgres-fence')
class Loss(BaseException):pass

class Tests(unittest.TestCase):
    def fixture(self,root,journal,action='activate',catalog=None,runner=None):
        host,raw,pg,peer,binding=hostfixture.Tests().fixture(root)
        catalog=catalog or catalogfixture.Catalog();catalog.container=raw.container;catalog.user=raw.user;catalog.database=raw.database
        catalog.value['database']=raw.database;catalog.sql=raw.sql
        path=root/'plan';path.mkdir(mode=0o700,exist_ok=True)
        old={'services':{'postgres':{'image':pg['Image'],'environment':{'POSTGRES_USER':raw.user,'POSTGRES_DB':raw.database}}}}
        for name in ['api','ingest','mcp']:old['services'][name]={'environment':{'DATABASE_URL':'postgres://admin:x@postgres:5432/application?sslmode=disable'},'image':'sha256:'+'e'*64,'command':['/bin/'+name]}
        previous=path/'previous-runtime.json';candidate=path/'candidate-runtime.json';receipt=path/'preflight.json'
        for config in [previous,candidate]:m.state.atomic(config,old)
        plan={'current_services':{'postgres':{'container_id':pg['Id'],'image':pg['Image']}}};m.state.atomic(receipt,plan)
        target=m.execution.Target(str(root),'fixture','application_bootstrap',action,str(previous),str(candidate),m.state.digest(receipt))
        calls=[]
        def connections(phase,peers,raw):
            calls.append((phase,copy.deepcopy(peers)))
            if phase=='exclusive':self.assertFalse(catalog.value['allow'])
        provider=m.Provider(catalog,lambda _:None,connections,runner or host.run)
        provider.bind_journal(journal,target)
        m.state.atomic(journal.directory/'maintenance.json',{'version':1,**provider.binding(),'receipt':str(receipt)})
        return provider,target,catalog,calls,pg,peer

    def test_empty_peer_closing_intent_precedes_hba_reload_and_lost_ack(self):
        for lost in [False,True]:
            with self.subTest(lost=lost),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal);p.enter(t)
                value=p.record();p.save(value,'peers',[{'container':'b'*64,'network':'fixture_default','address':'172.20.0.4','user':c.user,'ssl':False}])
                seen=[]
                def handoff(peers):
                    self.assertEqual(peers,[]);self.assertEqual(p.record()['stage'],'closing');self.assertEqual(p.record()['peers'],[])
                    seen.append(True)
                    if lost:raise Loss('lost HBA handoff acknowledgement')
                p.barrier.handoff=handoff
                if lost:
                    with self.assertRaises(Loss):p.close_local()
                    self.assertEqual(p.record()['stage'],'closing');self.assertEqual(p.record()['peers'],[])
                else:p.close_local()
                self.assertEqual(seen,[True])

    def test_exclusive_callback_runs_between_drain_and_reopen(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal);seen=[]
            original=p.connections
            def check(phase,peers,raw):
                if phase=='exclusive':
                    self.assertEqual(c.calls,[('allow',False),('drain',)])
                    p.connection_guard(phase,raw);seen.append(phase)
                    self.assertEqual(p.record()['stage'],'closing');self.assertEqual(peers,[])
                original(phase,peers,raw)
            p.connections=check;p.enter(t)
            self.assertEqual(seen,['exclusive']);self.assertTrue(c.value['allow'])
    def test_failed_closed_proof_keeps_admission_closed_for_fresh_recovery(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td)
            with m.state.Journal(root)as journal:
                p,t,c,calls,pg,peer=self.fixture(root,journal)
                def refuse(phase,peers,raw):
                    if phase=='exclusive':raise ValueError('Local closed proof missing')
                p.connections=refuse
                with self.assertRaises(ValueError):p.enter(t)
                self.assertFalse(c.value['allow']);self.assertEqual(p.record()['stage'],'closing')
                self.assertEqual(c.calls,[('allow',False),('drain',)])
                with self.assertRaises(ValueError):m.state.selection(root)
            with m.state.Journal(root)as journal:
                raw=(root/'pg_hba.conf').read_bytes();fresh,t,c,calls,pg,peer=self.fixture(root,journal,'recover',c)
                (root/'pg_hba.conf').write_bytes(raw);fresh.enter(t)
                self.assertTrue(c.value['allow']);self.assertEqual(fresh.record()['stage'],'local')
    def test_transition_guard_rejects_marker_receipt_hba_and_catalog_drift(self):
        for damage in ['marker','receipt','bytes','oid','sessions','approval']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal)
                def check(phase,peers,raw):
                    if phase!='exclusive':return
                    if damage=='marker':
                        v=m.state.read_private(journal.directory/'maintenance.json');v['project']='other';m.state.atomic(journal.directory/'maintenance.json',v)
                    if damage=='receipt':m.state.atomic(pathlib.Path(t.previous).parent/'preflight.json',{'changed':True})
                    if damage=='bytes':raw+=b'#different'
                    if damage=='oid':c.value['oid']='changed'
                    if damage=='sessions':c.value['sessions']=1
                    if damage=='approval':p.reviewer=lambda _:True
                    p.connection_guard(phase,raw)
                p.connections=check
                with self.assertRaises(ValueError):p.enter(t)
                self.assertFalse(c.value['allow']);self.assertNotIn(('allow',True),c.calls)

    def test_exclusive_diagnostic_identifies_closed_session_failure_without_weakening_guard(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal)
            def check(phase,peers,raw):
                if phase!='exclusive':return
                for damage,want in [('sessions','zero-sessions'),('allow','database-closed'),('stage','closing-stage'),('peers','empty-peers')]:
                    saved=dict(c.value);record=p.record();original=copy.deepcopy(record)
                    if damage=='sessions':c.value['sessions']=1
                    if damage=='allow':c.value['allow']=True
                    if damage=='stage':p.save(record,'local',[])
                    if damage=='peers':p.save(record,'closing',[{'untrusted':'never-print-private'}])
                    with self.assertRaises(ValueError)as error:p.connection_guard(phase,raw)
                    self.assertTrue(str(error.exception).endswith('Failed checks: '+want))
                    self.assertNotIn('never-print-private',str(error.exception))
                    c.value=saved;m.state.atomic(p.path,original)
                p.connection_guard(phase,raw)
            p.connections=check;p.enter(t)
            self.assertTrue(c.value['allow'])

    def test_actual_hba_and_catalog_components_close_drain_reopen_local_with_marker_held(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal)
            ticket=p.enter(t);p.check(ticket,t)
            self.assertEqual(c.calls,[('allow',False),('drain',),('allow',True)])
            self.assertEqual(p.record()['stage'],'local');self.assertTrue(c.value['allow'])
            self.assertEqual(p.host.path.read_bytes(),m.hostmod.hba.render(hostfixture.BASE,c.database,c.user,[]))
            with self.assertRaises(ValueError):m.state.selection(td)
            with self.assertRaises(ValueError):p.check(object(),t)
            self.assertEqual(p.record()['original_allow'],True)

    def test_lost_catalog_close_or_reopen_ack_recovers_original_identity_not_new_baseline(self):
        for enabled in [False,True]:
            with self.subTest(enabled=enabled),tempfile.TemporaryDirectory()as td:
                root=pathlib.Path(td)
                with m.state.Journal(root)as journal:
                    p,t,c,calls,pg,peer=self.fixture(root,journal);c.crash=enabled
                    with self.assertRaises(SystemExit):p.enter(t)
                    self.assertEqual(p.record()['stage'],'closing')
                with m.state.Journal(root)as journal:
                    # fixture helper resets host bytes, so preserve the actual
                    # interrupted file and catalog identity across reconstruction.
                    raw=(root/'pg_hba.conf').read_bytes();c.crash=None
                    fresh,t,c,calls,pg,peer=self.fixture(root,journal,'recover',c)
                    (root/'pg_hba.conf').write_bytes(raw)
                    ticket=fresh.enter(t);fresh.check(ticket,t)
                    self.assertTrue(c.value['allow']);self.assertEqual(fresh.record()['identity']['oid'],'22')
                    self.assertEqual(bytes.fromhex(fresh.barrier.read()['original']),hostfixture.BASE)
                    self.assertEqual(fresh.record()['stage'],'local')

    def test_changed_database_or_host_and_wrong_catalog_never_write(self):
        for damage in ['oid','host','database']:
            with self.subTest(damage=damage),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal);ticket=p.enter(t)
                before=p.host.path.read_bytes();effects=list(c.calls)
                if damage=='oid':c.value['oid']='99'
                elif damage=='host':pg['Image']='sha256:'+'9'*64
                else:c.value['database']='different'
                with self.assertRaises(ValueError):p.check(ticket,t)
                self.assertEqual(c.calls,effects);self.assertEqual(p.host.path.read_bytes(),before)

    def test_settled_release_recovery_restores_original_without_sql_or_closing_again(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td)
            with m.state.Journal(root)as journal:
                p,t,c,calls,pg,peer=self.fixture(root,journal);ticket=p.enter(t)
                final={'kind':'application_bootstrap','phase':'committed',**{k:v for k,v in p.binding().items()if k!='scope'}}
                # This is deliberately a mocked journal reader, not native
                # Coordinator settlement. The native test covers the real one.
                journal.read=lambda:final
                p.barrier.reload=lambda raw:(_ for _ in ()).throw(Loss())if raw==hostfixture.BASE else None
                p.save(p.record(),'restoring')
                with self.assertRaises(Loss):p.barrier.recover_release()
                effects=list(c.calls)
            with m.state.Journal(root)as journal:
                fresh,t,c,calls,pg,peer=self.fixture(root,journal,'recover',c)
                m.state.atomic(journal.directory/'journal.json',{'exists':'fixture-only'})
                journal.read=lambda:final
                ticket=fresh.enter(t);fresh.check(ticket,t)
                self.assertEqual(c.calls,effects)
                self.assertEqual(fresh.host.path.read_bytes(),hostfixture.BASE)
                self.assertEqual(fresh.record()['stage'],'original')
                self.assertEqual(fresh.barrier.read()['stage'],'released')

    def test_settled_hba_release_requires_exact_journal_binding_even_when_phase_matches(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal);p.enter(t)
            original=p.host.path.read_bytes()
            for key in ['project','previous','candidate','receipt_sha256','kind']:
                final={'kind':'application_bootstrap','phase':'committed',**{k:v for k,v in p.binding().items()if k!='scope'}}
                final[key]='wrong'
                journal.read=lambda:final
                with self.assertRaises(ValueError):p.barrier.recover_release()
                self.assertEqual(p.host.path.read_bytes(),original)

    def test_nearby_preflight_cannot_choose_catalog_for_a_different_receipt(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal)
            receipt=pathlib.Path(t.previous).parent/'preflight.json'
            value=m.state.read_private(receipt);value['unreviewed']='changed';m.state.atomic(receipt,value)
            fresh=m.Provider(c,lambda _:None,lambda *a:None,p.host.run)
            before=p.host.path.read_bytes()
            with self.assertRaises(ValueError):fresh.bind_journal(journal,t)
            self.assertEqual(p.host.path.read_bytes(),before);self.assertEqual(c.calls,[])

    def test_missing_scope_probes_and_unsettled_release_recovery_refuse(self):
        with self.assertRaises(ValueError):m.Provider(object(),None,lambda *a:None)
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            p,t,c,calls,pg,peer=self.fixture(pathlib.Path(td),journal);p.enter(t)
            journal.read=lambda:{'kind':'application_bootstrap','phase':'changing'}
            before=p.host.path.read_bytes()
            with self.assertRaises(ValueError):p.barrier.recover_release()
            self.assertEqual(p.host.path.read_bytes(),before)

if __name__=='__main__':unittest.main()
