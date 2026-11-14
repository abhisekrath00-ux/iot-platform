import { describe, expect, it } from 'vitest';
import { parseAny, parseCsv, parseJson, toCsv, validateMap, templatePoints } from './registerMap';

describe('register map import', () => {
  it('round-trips the MX300 template through CSV with no issues', () => {
    const pts = templatePoints('selec-mx300');
    expect(validateMap(pts)).toEqual([]);
    const back = parseCsv(toCsv(pts));
    expect(back.issues).toEqual([]);
    expect(back.points).toEqual(pts);
  });
  it('accepts free column order, defaults and quoted units', () => {
    const r = parseCsv('register,id,unit,type\n0,voltage,"V, rms",f32\n2,current,A,f32\n');
    expect(r.issues).toEqual([]);
    expect(r.points[0]).toMatchObject({ id: 'voltage', register: 0, unit: 'V, rms', func: 4, scale: 1, word_order: 'abcd' });
  });
  it('flags duplicate ids, overlaps, bad types and ranges with row numbers', () => {
    const r = parseCsv('id,register,type,min,max\na,0,f32,0,10\na,1,u16,0,10\nb,5,i64,0,10\nc,6,u16,5,5\n');
    const t = r.issues.map((i) => `${i.row}:${i.text}`).join('\n');
    expect(t).toContain('2:a: duplicate id');
    expect(t).toContain('overlap a');
    expect(t).toContain('3:b: type must be');
    expect(t).toContain('4:c: max must be greater than min');
  });
  it('requires id and register columns', () => {
    expect(parseCsv('name,addr\nx,1\n').issues[0].text).toContain('"id"');
    expect(parseCsv('').issues[0].text).toContain('empty');
  });
  it('reads JSON arrays and objects with points, and rejects junk', () => {
    expect(parseJson('[{"id":"t","register":1,"type":"i16","min":-40,"max":150}]').issues).toEqual([]);
    expect(parseAny('{"points":[{"id":"t","register":1,"min":0,"max":9}]}').points).toHaveLength(1);
    expect(parseJson('nope').issues[0].text).toContain('Not valid JSON');
    expect(parseJson('{"a":1}').issues[0].text).toContain('Expected');
  });
  it('rejects bool on holding registers and coils with numeric types', () => {
    const r = validateMap([
      { id: 'a', register: 0, func: 3, type: 'bool', word_order: 'abcd', scale: 1, unit: '', min: 0, max: 1 },
      { id: 'b', register: 0, func: 1, type: 'u16', word_order: 'abcd', scale: 1, unit: '', min: 0, max: 1 },
    ]);
    expect(r.length).toBe(2);
  });
});
it('rejects malformed JSON rows with row numbers instead of throwing or dropping them',()=>{
 for(const row of ['null','12','"text"','[]','true']){const r=parseJson(`[{"id":"ok","register":1},${row}]`);expect(r.points).toEqual([]);expect(r.issues[0]).toMatchObject({row:2});expect(r.issues[0].text).toContain('point object')}
});

it('refuses unsupported f64 imports without returning a replacement map',()=>{
 for(const text of ['[{"id":"energy","register":0,"type":"f64"}]','id,register,type\nenergy,0,f64\n']){const r=parseAny(text);expect(r.points).toEqual([]);expect(r.issues.map(x=>x.text).join(' ')).toContain('type must be');}
});

const explorerTemplate=()=>({format:'hexthings-explorer-template',version:1,name:'Synthetic temperature',driver_profile:'modbus-tcp',manual_reference:'SYNTHETIC fixture, p2',connection_defaults:{},points:[{id:'temperature',name:'Temperature',description:'Register map: SYNTHETIC fixture, p2',register:0,func:3,type:'f32',word_order:'abcd',scale:1,unit:'C',min:-40,max:150}]});
it('loads portable explorer metadata and preserves friendly names/manual provenance in draft points',()=>{
 const r=parseAny(JSON.stringify(explorerTemplate()));expect(r.issues).toEqual([]);expect(r.profile).toEqual({name:'Synthetic temperature',driver_profile:'modbus-tcp'});expect(r.points[0]).toMatchObject({name:'Temperature',description:'Register map: SYNTHETIC fixture, p2',register:0,func:3,type:'f32',scale:1});
});
it('rejects explorer identity/connection/write fields, incomplete numeric metadata and unsupported versions without replacement points',()=>{
 const bad=[{...explorerTemplate(),version:2},{...explorerTemplate(),driver_profile:'opcua'},{...explorerTemplate(),name:'<x>'},{...explorerTemplate(),manual_reference:''},{...explorerTemplate(),gateway_id:'other'},{...explorerTemplate(),writes:[{register:0,value:1}]},{...explorerTemplate(),connection_defaults:{port:'COM3'}},{...explorerTemplate(),points:[{...explorerTemplate().points[0],register:'0'}]},{...explorerTemplate(),points:[{...explorerTemplate().points[0],writes:[]}]},{...explorerTemplate(),points:[{...explorerTemplate().points[0],name:'<img>'}]},{...explorerTemplate(),points:[{...explorerTemplate().points[0],type:'f64'}]}];
 for(const v of bad){const r=parseAny(JSON.stringify(v));expect(r.issues.length).toBeGreaterThan(0);expect(r.points).toEqual([]);expect(r.profile).toBeUndefined();}
});
it('preserves optional friendly names in ordinary profile JSON and rejects nontext provenance',()=>{
 expect(parseJson('[{"id":"p","register":0,"name":"Pressure","description":"Manual p2"}]').points[0]).toMatchObject({name:'Pressure',description:'Manual p2'});
 expect(parseJson('[{"id":"p","register":0,"description":{"text":"bad"}}]').points).toEqual([]);
});

it('refuses invalid portable point maps or inconsistent manual provenance without replacing the draft',()=>{
 const doc=explorerTemplate();for(const points of [[doc.points[0],doc.points[0]],[{...doc.points[0],description:'Different manual'}],[{...doc.points[0],scale:0}],[{...doc.points[0],max:-41}]]){const r=parseAny(JSON.stringify({...doc,points}));expect(r.points).toEqual([]);expect(r.profile).toBeUndefined();expect(r.issues.length).toBeGreaterThan(0);}
 expect(parseAny(' '.repeat(262145)).points).toEqual([]);expect(parseAny(' '.repeat(262145)).issues[0].text).toContain('256 KiB');
});
