#!/usr/bin/env python3
"""Post-pairing fixture refuses every non-CI/owner-shaped invocation."""
import importlib.util
import pathlib
import unittest
from unittest.mock import patch
p=pathlib.Path(__file__).with_name('test-secure-pairing-native.py');s=importlib.util.spec_from_file_location('fixture',p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class Tests(unittest.TestCase):
    def test_non_ci_refuses_before_any_effect(self):
        with patch.dict(m.os.environ,{'GITHUB_ACTIONS':'false'}),patch.object(m.subprocess,'run')as run:
            with self.assertRaises(ValueError):m.post_pair('/owner','/owner/prepared','owner','owner:image','owner.example','192.168.1.20',1000)
            run.assert_not_called()
    def test_wrong_project_root_or_hostname_refuse_even_on_runner(self):
        base=['/tmp/hexthings-compose-ci-test','/tmp/hexthings-compose-ci-test/prepared','hexthings-secure-ci-test','image','hexthings-ci.invalid','192.168.1.20',1000]
        for field,value in [(0,'/owner'),(1,'/outside'),(2,'owner'),(4,'owner.example')]:
            args=base.copy();args[field]=value
            with self.subTest(field=field),patch.dict(m.os.environ,{'GITHUB_ACTIONS':'true','RUNNER_OS':'Linux'}),patch.object(m.os,'geteuid',return_value=0),patch.object(m.subprocess,'run')as run:
                with self.assertRaises(ValueError):m.post_pair(*args)
                run.assert_not_called()
if __name__=='__main__':unittest.main()
