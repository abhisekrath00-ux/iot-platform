#!/usr/bin/env python3
"""Pure synthetic simulator/scope guards, no Docker/network/owner effects."""
import importlib.util
import pathlib
import struct
import unittest
from unittest.mock import patch
p=pathlib.Path(__file__).with_name('test-secure-source-native.py');s=importlib.util.spec_from_file_location('fixture',p);m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class Tests(unittest.TestCase):
    def args(self):return ['/tmp/hexthings-compose-ci-test','/tmp/hexthings-compose-ci-test/prepared','hexthings-secure-ci-test','image','hexthings-ci.invalid','192.168.1.20',1000,'synthetic']
    def test_non_ci_refuses_before_any_effect(self):
        with patch.dict(m.os.environ,{'GITHUB_ACTIONS':'false'}),patch.object(m.subprocess,'run')as run,patch.object(m.socket,'socket')as sock:
            with self.assertRaises(ValueError):m.source_apply(*self.args())
            run.assert_not_called();sock.assert_not_called()
    def test_wrong_scope_refuses_before_any_effect(self):
        for field,value in [(0,'/owner'),(1,'/outside'),(2,'owner'),(4,'owner.example'),(5,'127.0.0.1'),(5,'8.8.8.8')]:
            args=self.args();args[field]=value
            with self.subTest(field=field,value=value),patch.dict(m.os.environ,{'GITHUB_ACTIONS':'true','RUNNER_OS':'Linux'}),patch.object(m.os,'geteuid',return_value=0),patch.object(m.subprocess,'run')as run,patch.object(m.socket,'socket')as sock:
                with self.assertRaises(ValueError):m.source_apply(*args)
                run.assert_not_called();sock.assert_not_called()
    def test_returned_ids_must_be_canonical_uuids_before_sql(self):
        value='123e4567-e89b-12d3-a456-426614174000'
        self.assertEqual(m.exact_uuid(value),value)
        for bad in [None,'',"x';DROP TABLE devices;",value.upper(),value.replace('-','')]:
            with self.subTest(value=bad),self.assertRaises((ValueError,AttributeError)):m.exact_uuid(bad)
    def test_only_exact_read_request(self):
        fields=[42,0,6,1,3,10,1]
        self.assertEqual(m.synthetic_response(struct.pack('>HHHBBHH',*fields)),struct.pack('>HHHBBBH',42,0,5,1,3,2,110))
        for index,value in [(1,1),(2,7),(3,2),(4,6),(4,16),(5,11),(6,2)]:
            bad=fields.copy();bad[index]=value
            with self.subTest(index=index),self.assertRaises(ValueError):m.synthetic_response(struct.pack('>HHHBBHH',*bad))
        with self.assertRaises(ValueError):m.synthetic_response(b'')
if __name__=='__main__':unittest.main()
