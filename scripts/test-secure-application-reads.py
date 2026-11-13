#!/usr/bin/env python3
"""Pure retained-list comparison guards, no network or owner effects."""
import copy
import importlib.util
import pathlib
import unittest
p=pathlib.Path(__file__).with_name('secure-application-reads.py')
s=importlib.util.spec_from_file_location('reads',p);reads=importlib.util.module_from_spec(s);s.loader.exec_module(reads)
class Tests(unittest.TestCase):
    def rows(self):
        return {'/v1/gateways':[dict(id='g',serial='serial',status='active',kind='edge',site_id='s')],
                '/v1/profiles':[dict(id='p',name='Source',driver_profile='modbus-generic',points=[{'id':'volts','address':42,'scale':1}],created_at='fixed')],
                '/v1/devices':[dict(id='d',profile='modbus-generic',name='Meter',gateway_id='g',created_at='fixed',tags=[],asset_id=None,lat_point=None,lon_point=None)]}
    def test_reviewed_added_default_and_order_only(self):
        old=self.rows();new=copy.deepcopy(old);new['/v1/profiles'][0]['connection_defaults']={}
        self.assertFalse(reads.compare(reads.snapshot(old),new)['full_compatibility'])
        old['/v1/gateways'].append(dict(old['/v1/gateways'][0],id='g2'))
        new=copy.deepcopy(old);new['/v1/gateways'].reverse()
        reads.compare(reads.snapshot(old),new)
    def test_exact_values_not_substring_or_counts(self):
        baseline=reads.snapshot(self.rows())
        for path,key,value in [('/v1/gateways','serial','serial-BAD'),('/v1/profiles','points',[]),('/v1/profiles','connection_defaults',{'port':'other'}),('/v1/devices','gateway_id','other'),('/v1/devices','tags',['changed'])]:
            rows=self.rows();rows[path][0][key]=value
            with self.subTest(path=path,key=key),self.assertRaises(ValueError):reads.compare(baseline,rows)
    def test_missing_extra_duplicate_unknown_capped_refuse(self):
        for damage in ['missing-path','missing-field','extra-field','duplicate','capped','not-list','bad-points']:
            rows=self.rows()
            if damage=='missing-path':del rows['/v1/devices']
            if damage=='missing-field':del rows['/v1/devices'][0]['gateway_id']
            if damage=='extra-field':rows['/v1/devices'][0]['unknown']='x'
            if damage=='duplicate':rows['/v1/devices']*=2
            if damage=='capped':rows['/v1/gateways']=[dict(rows['/v1/gateways'][0],id=str(i))for i in range(500)]
            if damage=='not-list':rows['/v1/profiles']={}
            if damage=='bad-points':rows['/v1/profiles'][0]['points']={}
            with self.subTest(damage=damage),self.assertRaises(ValueError):reads.snapshot(rows)
    def test_snapshot_deep_copy_and_not_an_approval(self):
        rows=self.rows();before=reads.snapshot(rows);rows['/v1/devices'][0]['name']='changed'
        with self.assertRaises(ValueError):reads.compare(before,rows)
        before['runtime_accepted']=True
        with self.assertRaises(ValueError):reads.compare(before,self.rows())
if __name__=='__main__':unittest.main()
