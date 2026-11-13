#!/usr/bin/env python3
"""Pure regression guards for the synthetic Docker adapter. No Docker effects."""
import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('adapter',pathlib.Path(__file__).with_name('secure-disposable-docker.py'))
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)

class Tests(unittest.TestCase):
    def test_prior_dependency_must_be_unchanged_private_regular_file(self):
        for change in ['unchanged','changed','shared','symlink','missing','incomplete']:
            with self.subTest(change=change),tempfile.TemporaryDirectory() as td:
                root=pathlib.Path(td);old=root/'previous.json';old.write_text('{}');old.chmod(0o600)
                dep=root/'broker.conf';dep.write_text('synthetic');dep.chmod(0o644)
                digest=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
                value={'previous':str(old),'previous_sha256':digest(old),'previous_files':{str(old):digest(old),str(dep):digest(dep)},'previous_images':{'api':'sha256:'+'a'*64}}
                effect=object.__new__(module.DisposableDocker)
                effect.call=lambda args:json.dumps([{'Id':args[-1]}])
                if change=='changed':dep.write_text('tampered')
                if change=='shared':dep.chmod(0o666)
                if change=='missing':dep.unlink()
                if change=='symlink':
                    dep.unlink();dep.symlink_to(old)
                if change=='incomplete':value['previous_files']={}
                if change=='unchanged':effect.verify_recovery_inputs(value)
                else:
                    with self.assertRaises((ValueError,OSError)):effect.verify_recovery_inputs(value)

    def test_missing_prior_image_refuses_recovery(self):
        with tempfile.TemporaryDirectory() as td:
            old=pathlib.Path(td)/'previous.json';old.write_text('{}');old.chmod(0o600)
            digest=hashlib.sha256(old.read_bytes()).hexdigest()
            value={'previous':str(old),'previous_sha256':digest,'previous_files':{str(old):digest},'previous_images':{'api':'sha256:'+'a'*64}}
            effect=object.__new__(module.DisposableDocker);effect.call=lambda args:'[]'
            with self.assertRaises(ValueError):effect.verify_recovery_inputs(value)

    def test_timeout_or_tls_failure_cannot_count_as_candidate_acceptance(self):
        effect=object.__new__(module.DisposableDocker);effect.accepted_candidate=False
        with tempfile.TemporaryDirectory() as td:
            config=pathlib.Path(td)/'candidate.json';config.write_text('{"services":{"api":{"image":"sha256:fixture"}}}')
            effect.compose=lambda *args:'synthetic-container'
            effect.call=lambda args:'[]'
            from unittest.mock import patch
            with patch.object(module.time,'monotonic',side_effect=[0,100]):
                with self.assertRaises(ValueError):effect.accept({'candidate':str(config)},'candidate')
            self.assertFalse(effect.accepted_candidate)

if __name__=='__main__':unittest.main()
