#!/usr/bin/env python3
"""Read-only exact prepared application plan review, never update authority.

No Docker, image extraction, session/secret access, schema operator, callback,
service change or journal creation. Unknown live/package facts remain unknown.
"""
import argparse
import importlib.util
import pathlib
import sys
import json
sys.dont_write_bytecode=True
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('application_adapter',HERE/'secure-application-docker.py')
adapter=importlib.util.module_from_spec(spec);spec.loader.exec_module(adapter)
state=adapter.state

def inspect(root,project,receipt):
    root=pathlib.Path(root).resolve();receipt=pathlib.Path(receipt).absolute()
    before=state.digest(receipt)
    checked=adapter.inspect_plan(root,project,receipt)
    # Output is an allowlist: resolved environment, bind paths, database users,
    # connection strings and private certificate/session bytes are never emitted.
    previous,candidate=checked['previous']['services'],checked['candidate']['services']
    images=[{'service':name,'previous':previous[name]['image'],'candidate':candidate[name]['image']}for name in sorted(previous)]
    state.check_manifest(checked['plan']['files'])
    if state.digest(receipt)!=before:raise ValueError('Plan changed during review.')
    return {'version':1,'review_only':True,'plan_files_verified':True,'project':project,
            'receipt_sha256':before,'images':images,'writers':checked['writers'],
            'changes_allowed':False,'live_runtime_verified':False,'dashboard_package_verified':False,
            'ready_to_update':False,'remaining_checks':[
                'Verify the current installation, local images and retained configuration live.',
                'Review candidate provenance and a maintenance window for this exact plan.',
                'Verify backup and recovery plus protection from reconnecting outside writers.',
                'Prove real runtime/source/ingest/background behavior and previous-image compatibility.',
                'Verify the matching server build and same-server dashboard EXE version/checksum.',
                'Complete fresh Windows install/pair/source/test/save/apply/recent-reading checks.'
            ]}

def display(value):
    lines=['Server update review. No files or services changed.',
           'Prepared file checks passed. This is NOT approval or readiness to update.',
           'Project: '+value['project'],'Plan SHA256: '+value['receipt_sha256'],
           'Prepared immutable images (not checked live):']
    for row in value['images']:
        lines+=['  '+row['service']+': '+('unchanged'if row['previous']==row['candidate']else 'planned change'),
                '    old '+row['previous'],'    new '+row['candidate']]
    lines+=['Writers: '+', '.join(value['writers']),
            'Dashboard EXE and current runtime: NOT verified.',
            'Production update execution is not available from this review.',
            'Before an update:']+['  - '+item for item in value['remaining_checks']]
    return '\n'.join(lines)

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',required=True);parser.add_argument('--project',required=True)
    parser.add_argument('--receipt',required=True);parser.add_argument('--json',action='store_true');args=parser.parse_args()
    try:
        value=inspect(args.root,args.project,args.receipt)
        print(json.dumps(value,sort_keys=True)if args.json else display(value));return 0
    except (OSError,ValueError,KeyError,TypeError):
        print('Update review unavailable: exact private application plan could not be verified. Nothing changed. Prepare/review the correct plan; do not run a legacy upgrade as fallback.',file=sys.stderr);return 1
if __name__=='__main__':sys.exit(main())
