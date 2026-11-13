#!/usr/bin/env python3
"""Internal grouped startup handoff for the application Docker adapter.

Requires explicit held-admission, exact-peer-admission and connection-proof
callbacks. Not a host provider and never enabled by discovery or menu startup.
Transient gated Compose definitions do not replace the final selected runtime.
"""
import copy
import hashlib
import importlib.util
import json
import pathlib

HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('group_gates',HERE/'secure-application-startgate.py')
gates=importlib.util.module_from_spec(spec);spec.loader.exec_module(gates)


class Group:
    def __init__(self,journal,guard,admit,prove):
        if not all(callable(x)for x in [guard,admit,prove]):
            raise ValueError('Explicit admission, exact grouped peer handoff and fresh connection proofs required.')
        self.journal,self.guard,self.admit,self.prove=journal,guard,admit,prove

    def check(self):
        self.journal.locked()
        if self.guard()is not None:raise ValueError('Live admission guard must raise, not return approval.')

    def gate(self,adapter,name,slot):
        return gates.StartGate(self.journal,adapter.project,name,(adapter.new if slot=='candidate'else adapter.old)['services'][name],self.guard,slot=slot)

    def inspect(self,adapter,path):
        self.check();values=adapter.containers(path);self.check()
        found={}
        for item in values:
            labels=item.get('Config',{}).get('Labels',{});name=labels.get('com.docker.compose.service')
            if labels.get('com.docker.compose.project')!=adapter.project or name in found:
                raise ValueError('Exact single-instance application group inventory required.')
            found[name]=item
        if set(found)!=set(adapter.old['services']):raise ValueError('Grouped service inventory changed.')
        return found

    def metadata(self,adapter,image):
        self.check();values=json.loads(adapter.call(['docker','image','inspect',image]));self.check()
        if len(values)!=1 or values[0].get('Id')!=image:raise ValueError('Exact immutable application image unavailable.')
        return values[0]

    def verify(self,adapter,service,item,image):
        # Used only when an actual container carries this narrowly scoped gate.
        # Ungated definitions retain the original adapter's strict verifier.
        self.check();token=item.get('Config',{}).get('Labels',{}).get(gates.LABEL)
        if not token:raise ValueError('A grouped-gate verifier cannot approve an ungated definition.')
        name=item.get('Config',{}).get('Labels',{}).get('com.docker.compose.service')
        if name not in adapter.writers:raise ValueError('Only expected application writers can have startup gates.')
        matches=[]
        for slot in ['candidate','previous','compatibility']:
            gate=self.gate(adapter,name,slot)
            if gate.path.exists()and gate.record()['token']==token:
                if gate.base!=service:raise ValueError('Gate belongs to a different exact original service definition.')
                gate.verify(item,image,statuses=('created','exited','running'));matches.append(gate)
        if len(matches)!=1:raise ValueError('Exact unique recorded application gate required.')

    def start(self,adapter,value,slot):
        self.check()
        if slot not in {'candidate','previous','compatibility'}:raise ValueError('Exact candidate/previous application group required.')
        config=value['previous'if slot=='compatibility'else slot]
        base=gates.state.read_private(config);expected=adapter.new if slot=='candidate'else adapter.old
        if base!=expected or pathlib.Path(adapter.root)!=self.journal.root:
            raise ValueError('Exact frozen application group and held root required.')
        recipe=copy.deepcopy(base);group={}
        # Reusing a released marker would race the first DB connection. Retire
        # only a freshly verified stopped container under the caller's closed
        # admission guard; preserve every old marker as diagnostic evidence.
        found=self.inspect(adapter,config)
        for name in adapter.writers:
            gate=self.gate(adapter,name,slot)
            if gate.path.exists():
                record=gate.record()
                if record['stage']=='preparing':gate.recover_preparing()
                elif record['stage']!='held':
                    actual=found[name];image=self.metadata(adapter,actual['Image'])
                    gate.retire(lambda actual=actual,image=image:(actual,image));gate.prepare()
            else:gate.prepare()
            group[name]=gate;recipe['services'][name]=gate.definition()
            # Compose interpolates dollar expressions even in array commands.
            # Escape only the serialized recipe; actual Docker Cmd must still
            # equal the original literal gate script verified by StartGate.
            recipe['services'][name]['command'][2]=recipe['services'][name]['command'][2].replace('$','$$')
        raw=json.dumps(recipe,sort_keys=True,separators=(',',':')).encode()
        effective=self.journal.directory/('startgroup-'+slot+'-'+hashlib.sha256(raw).hexdigest()+'.json')
        if effective.exists():
            if gates.state.read_private(effective)!=recipe:raise ValueError('Durable gated Compose recipe changed.')
        else:gates.state.atomic(effective,recipe)
        self.check()
        adapter.compose(effective,'up','--no-start','--no-deps','--no-build','--pull','never',*adapter.writers)
        self.check();found=self.inspect(adapter,effective)
        for name,gate in group.items():
            actual=found[name];image=self.metadata(adapter,actual['Image'])
            if actual.get('State',{}).get('Status')=='created':gate.bind_created(actual,image)
            else:gate.bind(actual,image)  # exact held running endpoint after a lost start acknowledgement
        self.check();adapter.compose(effective,'start',*adapter.writers);self.check()
        found=self.inspect(adapter,effective)
        for name,gate in group.items():gate.bind(found[name],self.metadata(adapter,found[name]['Image']))
        def peers():
            values=self.inspect(adapter,effective)
            for name,gate in group.items():gate.verify(values[name],self.metadata(adapter,values[name]['Image']))
            return {name:values[name]for name in group}
        if self.admit(peers())is not None:raise ValueError('Grouped exact-peer handoff must raise, not return approval.')
        for name,gate in group.items():
            def inspected(name=name):
                values=peers();return values[name],self.metadata(adapter,values[name]['Image'])
            def proof(_):
                if self.prove(peers())is not None:raise ValueError('Fresh grouped peer proofs must raise, not return approval.')
            gate.release(inspected,proof)
        self.check()

    def normalize(self,adapter,value,slot,settled,prove_original_admission,accept):
        # Only after durable transaction settlement and restoration of original
        # HBA. No more schema effects are permitted. Keep admission marker until
        # direct original definitions and real final runtime are accepted.
        if not all(callable(x)for x in [prove_original_admission,accept]):raise ValueError('Original admission and final runtime proofs required.')
        self.check()
        if settled not in {'committed','restored'}or slot!=('candidate'if settled=='committed'else'previous'):
            raise ValueError('Exact durably settled final application target required.')
        record=self.journal.read()
        if (record.get('phase')!=settled or record.get('kind')!='application_bootstrap' or record.get('project')!=adapter.project
                or record.get('previous')!=value['previous']or record.get('candidate')!=value['candidate']):
            raise ValueError('Durable settled transaction required before direct-runtime normalization.')
        gates.state.check_manifest(record.get('files'))
        for key in ['previous','candidate']:
            if record.get(key+'_sha256')!=gates.state.digest(value[key]):raise ValueError('Final frozen application inputs changed.')
        if prove_original_admission()is not None:raise ValueError('Fresh exact original admission proof required.')
        # Persist before recreate. A fresh settled-release recovery can repeat
        # exact Compose reconciliation and acceptance, never schema application.
        marker=self.journal.directory/'startgroup-normalize.json'
        operation={'version':1,'binding':gates.state.read_private(self.journal.directory/'maintenance.json'),
                   'phase':settled,'slot':slot,'config':value[slot],'sha256':gates.state.digest(value[slot]),'stage':'reconciling'}
        if marker.exists():
            prior=gates.state.read_private(marker)
            if any(prior.get(key)!=operation[key]for key in operation if key!='stage'):
                raise ValueError('Different final-runtime normalization is already recorded.')
        gates.state.atomic(marker,operation)
        adapter.compose(value[slot],'up','-d','--no-deps','--no-build','--pull','never',*adapter.writers)
        if prove_original_admission()is not None:raise ValueError('Original admission changed during final-runtime reconciliation.')
        # Standard verifier rejects any remaining shell wrapper or extra env.
        found=self.inspect(adapter,value[slot])
        for name in adapter.writers:
            item=found[name]
            gates.adapter.verify_container_definition((adapter.new if slot=='candidate'else adapter.old)['services'][name],item,self.metadata(adapter,item['Image']))
            if item.get('State',{}).get('Status')!='running':raise ValueError('Final direct application is not running.')
            original=(adapter.new if slot=='candidate'else adapter.old)['services'][name]
            mounts=item.get('Mounts',[])
            expected={(v['source'],v['target'],False)for v in original.get('volumes',[])}
            actual={(v.get('Source'),v.get('Destination'),v.get('RW'))for v in mounts}
            if (gates.LABEL in item.get('Config',{}).get('Labels',{})or len(mounts)!=len(expected)or actual!=expected
                    or any(v.get('Type')!='bind'for v in mounts)):
                raise ValueError('Transient application-gate mount/label remains in final direct runtime.')
        if accept(value,slot)is not None:raise ValueError('Final live acceptance must raise, not return approval.')
        operation['stage']='accepted';gates.state.atomic(marker,operation);self.check()
