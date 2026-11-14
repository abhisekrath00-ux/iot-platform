import {expect,test} from 'vitest';
import {decodeRegister,labelledPoint,newRegisterLabel} from './registerPreview';
test('decoder mirrors edge orders, signed and float representations',()=>{
 expect(decodeRegister([0x42c8,0],0,'f32','abcd')).toBe(100);
 expect(decodeRegister([0,0x42c8],0,'f32','cdab')).toBe(100);
 expect(decodeRegister([0xc842,0],0,'f32','badc')).toBe(100);
 expect(decodeRegister([0,0xc842],0,'f32','dcba')).toBe(100);
 expect(decodeRegister([65535],0,'i16','abcd')).toBe(-1);
 expect(decodeRegister([65535,65535],0,'i32','abcd')).toBe(-1);
 expect(decodeRegister([1,2],0,'u32','abcd')).toBe(65538);
 expect(decodeRegister([1],0,'bool','abcd')).toBe(1);
});
test('incomplete, invalid, non-finite and unsupported decoders cannot validate a point',()=>{
 for(const x of [decodeRegister([1],0,'f32','abcd'),decodeRegister([1,2],0,'f32',''),decodeRegister([65536],0,'u16','abcd'),decodeRegister([2],0,'bool','abcd'),decodeRegister([0x7f80,0],0,'f32','abcd')])expect(Number.isFinite(x)).toBe(false);
 const p=labelledPoint(newRegisterLabel(),10,3);expect(p.id).toBe('');expect(Number.isFinite(p.min)).toBe(false);expect(Number.isFinite(p.max)).toBe(false);
});
