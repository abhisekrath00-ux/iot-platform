#!/usr/bin/env python3
import copy
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('gate',HERE/'secure-application-startgate.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class Loss(BaseException):pass

class Tests(unittest.TestCase):
    def gate(self,root,journal):
        m.state.atomic(journal.directory/'maintenance.json',{'root':str(root),'receipt_sha256':'a'*64})
        return m.StartGate(journal,'fixture','api',{'image':'sha256:'+'a'*64,'command':['/bin/api'],
                          'environment':{'DATABASE_URL':'postgres://fixture'}},lambda:None)
    def actual(self,gate):
        definition=gate.definition(); image={'Config':{'Env':['PATH=/bin'],'Cmd':[],'Entrypoint':[]}}
        value={'Id':'b'*64,'Image':definition['image'],'State':{'Status':'running','Paused':False,'Restarting':False},
               'Config':{'Cmd':definition['command'],'Entrypoint':[],'Env':['PATH=/bin','DATABASE_URL=postgres://fixture'],
                         'Labels':{'com.docker.compose.project':'fixture','com.docker.compose.service':'api',**definition['labels']}},
               'HostConfig':{'Privileged':False,'NetworkMode':'fixture_default'},
               'Mounts':[{'Type':'bind','Source':definition['volumes'][0]['source'],'Destination':m.TARGET,'RW':False}]}
        return value,image
    def test_exact_process_gate_and_live_admission_before_release(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            gate=self.gate(pathlib.Path(td),journal);definition=gate.prepare();actual,image=self.actual(gate)
            self.assertEqual(definition['command'][-1],'/bin/api');self.assertEqual(gate.record()['stage'],'held')
            self.assertNotIn('release',[x.name for x in pathlib.Path(gate.record()['directory']).iterdir()])
            gate.bind(actual,image);proof=[]
            def admit(item):
                self.assertEqual(item['Id'],'b'*64);self.assertEqual(gate.record()['stage'],'held');proof.append('fresh exact IP/user/deny proof')
            gate.release(lambda:(actual,image),admit)
            self.assertEqual(len(proof),1);self.assertEqual(gate.record()['stage'],'released')
            self.assertEqual((pathlib.Path(gate.record()['directory'])/'release').read_text(),gate.record()['token'])
    def test_changed_scope_command_environment_mount_identity_and_guard_refuse(self):
        for problem in ['image','container','labels','command','env','mount-rw','extra-mount','caps','running','guard','scope']:
            with self.subTest(problem=problem),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                gate=self.gate(pathlib.Path(td),journal);gate.prepare();actual,image=self.actual(gate);gate.bind(actual,image)
                if problem=='image':actual['Image']='sha256:'+'c'*64
                if problem=='container':actual['Id']='c'*64
                if problem=='labels':actual['Config']['Labels'][m.LABEL]='wrong'
                if problem=='command':actual['Config']['Cmd']=['/bin/api']
                if problem=='env':actual['Config']['Env'].append('PGHOST=elsewhere')
                if problem=='mount-rw':actual['Mounts'][0]['RW']=True
                if problem=='extra-mount':actual['Mounts'].append({'Type':'bind','Source':'/tmp','Destination':'/outside','RW':False})
                if problem=='caps':actual['HostConfig']['CapAdd']=['SYS_ADMIN']
                if problem=='running':actual['State']['Status']='exited'
                if problem=='guard':gate.guard=lambda:True
                if problem=='scope':m.state.atomic(journal.directory/'maintenance.json',{'root':td,'receipt_sha256':'wrong'})
                with self.assertRaises(ValueError):gate.release(lambda:(actual,image),lambda _:None)
                self.assertFalse(any((x/'release').exists()for x in journal.directory.glob('startgate-application-api-*')if x.is_dir()))
    def test_admission_failure_and_changed_marker_never_launch(self):
        for problem in ['proof','true','unknown','symlink','contents','missing-released']:
            with self.subTest(problem=problem),tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
                gate=self.gate(pathlib.Path(td),journal);gate.prepare();actual,image=self.actual(gate);gate.bind(actual,image)
                directory=pathlib.Path(gate.record()['directory'])
                if problem=='unknown':(directory/'outside').write_text('unknown')
                if problem=='symlink':(directory/'release').symlink_to(directory/'missing')
                if problem=='contents':(directory/'release').write_text('wrong');(directory/'release').chmod(0o444)
                if problem=='missing-released':value=gate.record();value['stage']='released';m.state.atomic(gate.path,value)
                def admit(_):
                    if problem=='proof':raise ValueError('negative connection not denied')
                    if problem=='true':return True
                with self.assertRaises(ValueError):gate.release(lambda:(actual,image),admit)
    def test_durable_marker_lost_ack_recovers_with_fresh_peer_proofs_not_reset(self):
        with tempfile.TemporaryDirectory()as td:
            with m.state.Journal(td)as journal:
                gate=self.gate(pathlib.Path(td),journal);gate.prepare();actual,image=self.actual(gate);gate.bind(actual,image)
                ordinary=m.state.atomic
                def lost(path,value):
                    if path==gate.path and value.get('stage')=='released':raise Loss()
                    ordinary(path,value)
                m.state.atomic=lost
                try:
                    with self.assertRaises(Loss):gate.release(lambda:(actual,image),lambda _:None)
                finally:m.state.atomic=ordinary
                self.assertEqual(gate.record()['stage'],'releasing');token=gate.record()['token']
            with m.state.Journal(td)as journal:
                fresh=m.StartGate(journal,'fixture','api',gate.base,lambda:None);calls=[]
                fresh.release(lambda:(actual,image),lambda _:calls.append('fresh'))
                self.assertEqual(calls,['fresh']);self.assertEqual(fresh.record()['stage'],'released')
                self.assertEqual((pathlib.Path(fresh.record()['directory'])/'release').read_text(),token)
    def test_preparing_recovery_only_and_no_scope_or_process_replacement(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            gate=self.gate(pathlib.Path(td),journal);gate.prepare();value=gate.record();value['stage']='preparing';m.state.atomic(gate.path,value)
            gate.recover_preparing();self.assertEqual(gate.record()['stage'],'held')
            with self.assertRaises(ValueError):gate.prepare()
            with self.assertRaises(ValueError):gate.recover_preparing()
    def test_created_identity_durable_before_start_and_stopped_retirement_new_nonce(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            gate=self.gate(pathlib.Path(td),journal);gate.prepare();actual,image=self.actual(gate)
            actual['State']['Status']='created';gate.bind_created(actual,image)
            self.assertEqual(gate.record()['container'],'b'*64)
            with self.assertRaises(ValueError):gate.release(lambda:(actual,image),lambda _:None)
            actual['State']['Status']='running';gate.release(lambda:(actual,image),lambda _:None)
            old=gate.record();actual['State']['Status']='exited'
            evidence=gate.retire(lambda:(actual,image))
            self.assertTrue(pathlib.Path(evidence).is_file());self.assertFalse(gate.path.exists())
            self.assertTrue((pathlib.Path(old['directory'])/'release').is_file())
            gate.prepare();self.assertNotEqual(gate.record()['token'],old['token'])
            self.assertFalse((pathlib.Path(gate.record()['directory'])/'release').exists())
    def test_candidate_previous_and_compatibility_slots_do_not_share_markers(self):
        with tempfile.TemporaryDirectory()as td,m.state.Journal(td)as journal:
            initial=self.gate(pathlib.Path(td),journal);tokens=[]
            for slot in ['candidate','previous','compatibility']:
                gate=m.StartGate(journal,'fixture','api',initial.base,lambda:None,slot=slot)
                gate.prepare();tokens.append(gate.record()['token'])
                self.assertIn(slot,gate.path.name)
                wrong=m.StartGate(journal,'fixture','api',initial.base,lambda:None,slot='application')
                wrong.path=gate.path
                with self.assertRaises(ValueError):wrong.record()
            self.assertEqual(len(set(tokens)),3)

    def test_shell_does_not_exec_before_exact_readonly_marker_and_preserves_arguments(self):
        with tempfile.TemporaryDirectory()as td:
            root=pathlib.Path(td);marker=root/'release';out=root/'output'
            # Test the exact shell logic with only its mount path replaced.
            script=m.SCRIPT.replace(m.TARGET,td)
            process=subprocess.Popen(['/bin/sh','-c',script,'hexthings-startgate','a'*32,'/bin/sh','-c','printf %s "$1" > "$2"','arg0',"literal '$() value",str(out)])
            try:
                with self.assertRaises(subprocess.TimeoutExpired):process.wait(timeout=.12)
                self.assertFalse(out.exists());marker.write_text('a'*32);marker.chmod(0o444)
                self.assertEqual(process.wait(timeout=4),0);self.assertEqual(out.read_text(),"literal '$() value")
            finally:
                if process.poll()is None:process.kill();process.wait()

if __name__=='__main__':unittest.main()
