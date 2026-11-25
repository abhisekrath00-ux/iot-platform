#!/usr/bin/env python3
"""Prepare a private application update plan from exact already-local images.

Only explicit read-only Docker inspection/config validation is used. Private
plan files contain resolved secrets. Never share them. No pull, build, run,
service change, schema operator, authority callback or activation is offered.
"""
import argparse
import importlib.util
import pathlib
import subprocess
import sys
sys.dont_write_bytecode=True
HERE=pathlib.Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('update_review',HERE/'secure-update-review.py')
review=importlib.util.module_from_spec(spec);spec.loader.exec_module(review)

def read_only_command(args,**kwargs):
    return subprocess.run(args,timeout=15,**kwargs)

def prepare(root,project,image,bundle,execute=read_only_command):
    # The adapter refuses an existing/uncertain output. Never delete partial
    # diagnostics, recreate a receipt, or retry an external operation here.
    review.adapter.prepare(root,project,image,bundle,execute)
    value=review.inspect(root,project,pathlib.Path(bundle).absolute()/'preflight.json')
    return value

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root',required=True);parser.add_argument('--project',required=True)
    parser.add_argument('--image',required=True);parser.add_argument('--bundle',required=True)
    args=parser.parse_args()
    try:
        value=prepare(args.root,args.project,args.image,args.bundle)
        print('Private update plan created. The bundle contains resolved secrets: do not share it.')
        print(review.display(value));return 0
    except (OSError,ValueError,KeyError,TypeError,subprocess.SubprocessError):
        print('Update preparation could not be confirmed. No service or schema change was requested. Preserve any partial bundle for review; do not remove it to retry, run a legacy upgrade, or treat this as update approval.',file=sys.stderr)
        return 1
if __name__=='__main__':sys.exit(main())
