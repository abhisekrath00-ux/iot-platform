#!/usr/bin/env python3
"""Explicit application runtime acceptance callback, not an activation entry.

Exact Docker runtime, live retained API identity/data and candidate read-only
operator checks complement, never replace, the mandatory caller behavior proof.
Previous-image full compatibility remains a separate Coordinator callback. No
login, image pull/build, service restart, schema apply or owner authority here.
"""
import copy
import importlib.util
import json
import pathlib

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module);return module

adaptermod=load('secure-application-docker')
reads=load('secure-application-reads')

class RuntimeAcceptance:
    def __init__(self,adapter,client,baseline,behavior):
        if not callable(behavior)or not callable(getattr(client,'compare',None)):
            raise ValueError('Live read transport and explicit additional behavior verifier required.')
        # Revalidate snapshot shape rather than accept an arbitrary receipt or
        # already-approved flag as a source of runtime authority.
        if not isinstance(baseline,dict)or 'records'not in baseline:raise ValueError('Exact retained baseline required.')
        records={path:list(rows.values())for path,rows in baseline['records'].items()}
        reads.compare(baseline,records)
        self.adapter,self.client,self.baseline,self.behavior=adapter,client,copy.deepcopy(baseline),behavior

    def inventory(self,path):
        found=self.adapter.checked_containers(path,True)
        return {name:{'container_id':item['Id'],'image':item['Image']}for name,item in found.items()}

    def __call__(self,value,slot):
        if slot not in {'candidate','previous'}:raise ValueError('Exact application runtime slot required.')
        path=pathlib.Path(value[slot])
        expected=self.adapter.candidate if slot=='candidate'else self.adapter.previous
        if path!=expected:raise ValueError('Runtime acceptance path differs from exact adapter plan.')
        before=self.inventory(path)
        proof=self.client.compare(self.baseline)
        if (not isinstance(proof,dict)or proof.get('matched')is not True or proof.get('full_compatibility')is not False
                or proof.get('runtime_accepted')is not False):raise ValueError('Exact limited retained match required, not runtime approval.')
        if slot=='candidate':
            # Execute only the checked already-running immutable API CID,
            # never Compose run or a candidate sidecar with normal DB ingress.
            api=before['api']['container_id']
            def operator(binary,action):
                return json.loads(self.adapter.call(['docker','exec',api,'/bin/'+binary,action]))
            capability=operator('tenantctl','setup-capabilities')
            adaptermod.readiness.verify_capabilities(capability)
            schema=operator('tenantctl','setup-readiness')
            if (not isinstance(schema,dict)or set(schema)!={'version','status','packaged_count','applied_count','pending_migrations','unknown_migrations','deployment_approved'}
                    or schema['version']!=1 or schema['status']!='migration_ledger_current'
                    or schema['deployment_approved']is not False
                    or type(schema['applied_count'])is not int or schema['applied_count']!=93
                    or type(schema['packaged_count'])is not int or schema['packaged_count']!=93
                    or schema['pending_migrations']!=[]or schema['unknown_migrations']!=[]):
                raise ValueError('Running candidate read-only operator readiness is not exact reviewed93.')
            if adaptermod.app.check_schema(operator('setupctl','inspect-reviewed'))!='exact_reviewed_additive_93':
                raise ValueError('Candidate schema objects/history are not exactly reviewed93.')
        # A required separate callback covers source config, current operations,
        # ingest/background behavior and any owner-specific acceptance scope.
        # It must raise on missing evidence, never return True as an approval.
        if self.behavior(value,slot)is not None:raise ValueError('Behavior verifier must raise, not return approval.')
        if self.client.compare(self.baseline)!=proof:raise ValueError('Retained verification scope changed during behavior proof.')
        if self.inventory(path)!=before:raise ValueError('Runtime identities/images changed during acceptance.')


class PreviousCompatibility:
    """Dedicated old-image grouped-runtime proof, never inferred from ledger.

    Requires the actual provider's separate compatibility gates and an explicit
    behavior verifier on the upgraded schema. This does not grant owner scope
    or cover behavior the caller has not proved. No candidate operator assumed.
    """
    def __init__(self,adapter,group,client,baseline,behavior):
        if not callable(behavior)or not callable(getattr(group,'gate',None)):
            raise ValueError('Dedicated compatibility group and behavior proof required.')
        if not getattr(adapter,'writers',None):raise ValueError('Exact nonempty previous writer set required.')
        self.adapter,self.group=adapter,group
        def previous_behavior(value,slot):
            if slot!='previous':raise ValueError('Previous compatibility only.')
            if behavior(value)is not None:raise ValueError('Compatibility behavior must raise, not return approval.')
        self.runtime=RuntimeAcceptance(adapter,client,baseline,previous_behavior)

    def gates(self,value):
        if pathlib.Path(value['previous'])!=self.adapter.previous:
            raise ValueError('Exact previous-image compatibility target required.')
        found=self.adapter.checked_containers(value['previous'],True)
        ids={}
        for name in self.adapter.writers:
            gate=self.group.gate(self.adapter,name,'compatibility')
            record=gate.record()
            if record['container']!=found[name]['Id']or record['stage']!='released':
                raise ValueError('Dedicated previous-image released compatibility group required.')
            ids[name]=found[name]['Id']
        return ids

    def __call__(self,value):
        before=self.gates(value)
        self.runtime(value,'previous')
        if self.gates(value)!=before:
            raise ValueError('Previous-image compatibility runtime changed during proof.')
