#!/usr/bin/env python3
"""Inspect current single-node setup readiness, never bootstrap or activate.

No certificate input, image pull/build/run, deployment file, DB mutation or
service restart. Only the already-running API's local operator is executed.
Output contains no resolved environment, credentials, certificate or ACL text.
"""
import argparse
import importlib.util
import json
import pathlib
import re
import subprocess
import sys

HERE=pathlib.Path(__file__).resolve().parent

def load(name):
    spec=importlib.util.spec_from_file_location(name,HERE/(name+'.py'))
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    return module

preflight=load('secure-preflight')
runtime=load('secure-runtime')
REQUIRED_PROTOCOLS={'certificate_acl_snapshot_v1','read_only_setup_readiness_v1'}
ROLES={'api':'database_writer','ingest':'database_writer','mcp':'database_writer',
       'postgres':'data_store','mosquitto':'broker','elasticsearch':'data_store',
       'redis':'data_store','web':'web','ai-runtime':'local_model'}


def classify(config,images):
    frozen,disabled=runtime.freeze(config,images)
    result={}
    for name,service in frozen['services'].items():
        if name not in ROLES:
            raise ValueError('Unclassified active service; setup readiness cannot cover every writer.')
        role=ROLES[name]
        if role=='database_writer':
            if service.get('command')!=['/bin/'+name] or not service.get('environment',{}).get('DATABASE_URL'):
                raise ValueError('A database writer differs from the supported single-node service command/environment.')
        elif service.get('environment',{}).get('DATABASE_URL'):
            raise ValueError('Unexpected database access on a non-writer service; classify it before migration.')
        if service.get('privileged') or service.get('network_mode')=='host':
            raise ValueError('Privileged/host-network services need separate deployment review.')
        result[name]=role
    return {'roles':result,'writers':sorted(name for name,role in result.items() if role=='database_writer'),'disabled_profiles':disabled}


def verify_capabilities(value):
    if (not isinstance(value,dict) or value.get('version')!=1
            or value.get('activates_deployment') is not False
            or not isinstance(value.get('protocols'),list)
            or not all(isinstance(item,str) for item in value['protocols'])
            or not REQUIRED_PROTOCOLS<=set(value['protocols'])):
        raise ValueError('Current API operator has no verified setup protocol. Application bootstrap is separate; no candidate operator or SQL fallback was run.')


def inspect(root,project,execute=subprocess.run,selection_reader=None):
    root=pathlib.Path(root).resolve()
    if not re.fullmatch(r'[a-z0-9][a-z0-9_-]{0,62}',project):
        raise ValueError('Explicit existing Compose project name required.')
    # Resolve an already-committed/restored selection, never lab fallback when
    # a pending or damaged journal exists. Inspection creates no journal.
    state=load('secure-state')
    selected=(selection_reader or state.selection)(root)
    if selected and selected['project']!=project:
        raise ValueError('Requested project differs from verified deployment selection.')
    compose=['docker','compose','-p',project]
    if selected:compose+=['-f',selected['config']]
    def call(args):
        value=execute(args,cwd=root,capture_output=True,text=True)
        if value.returncode:
            raise ValueError('Current read-only inspection/operator failed; no readiness proved. No bootstrap, image pull or candidate operator attempted.')
        return value.stdout
    config=json.loads(call(compose+['config','--format','json']))
    ids=call(compose+['ps','-q']).split()
    if not ids:raise ValueError('No running existing Compose project found.')
    containers=json.loads(call(['docker','inspect',*ids]))
    current=preflight.validate_current(config,config,containers,project)
    images={name:item['image'] for name,item in current.items()}
    classes=classify(config,images)
    capability=json.loads(call(compose+['exec','-T','api','/bin/tenantctl','setup-capabilities']))
    verify_capabilities(capability)
    schema=json.loads(call(compose+['exec','-T','api','/bin/tenantctl','setup-readiness']))
    if (not isinstance(schema,dict) or schema.get('version')!=1
            or schema.get('deployment_approved') is not False
            or schema.get('status') not in {'migration_ledger_current','application_bootstrap_required'}
            or not isinstance(schema.get('pending_migrations'),list)
            or not isinstance(schema.get('unknown_migrations'),list)
            or any(not isinstance(schema.get(k),int) or schema[k]<0 for k in ['applied_count','packaged_count'])):
        raise ValueError('Current schema inspection is unverifiable; no readiness proved.')
    if (schema['status']=='migration_ledger_current' and
            (schema['pending_migrations'] or schema['unknown_migrations'] or schema['packaged_count']==0 or schema['packaged_count']!=schema['applied_count'])):
        raise ValueError('Current schema inspection contradicts its status; no readiness proved.')
    return {'version':1,'status':schema['status'],'project':project,
            'current_services':current,**classes,
            'schema':schema,'transport_activation_allowed':False,
            'limitations':['Only current operator capability and migration ledger inspected.',
                'Not trust, issuer compatibility, route, downtime approval, full ACL export or physical source evidence.',
                'Application bootstrap, activation/recovery and secure Upgrade are separate unfinished workflows.']}


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',required=True);parser.add_argument('--project',required=True);parser.add_argument('--json',action='store_true')
    args=parser.parse_args()
    try:
        # Serialize with existing menu/transaction operations without creating
        # secure state on an ordinary installation just to inspect it.
        state=load('secure-state');directory=pathlib.Path(args.root)/'.secure-deployment'
        if directory.exists() or directory.is_symlink():
            with state.Journal(args.root):result=inspect(args.root,args.project)
        else:result=inspect(args.root,args.project)
        if args.json:print(json.dumps(result,indent=2))
        else:
            print('Read-only setup check. No installation or service changed.')
            print('Current migration ledger: '+result['status'].replace('_',' '))
            print('Database writers that must be paused together: '+', '.join(result['writers']))
            print('Services inspected: '+str(len(result['current_services'])))
            if result['schema']['pending_migrations'] or result['schema']['unknown_migrations']:
                print('Application/schema update needs its own backup, recovery and approval before TLS migration.')
            print('This does not activate secure setup or approve route, trust, downtime or device access.')
    except (ValueError,OSError,KeyError,TypeError,subprocess.SubprocessError) as error:
        print(str(error),file=sys.stderr);sys.exit(1)
