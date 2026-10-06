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
    const r = parseCsv('id,register,type,min,max\na,0,f32,0,10\na,1,u16,0,10\nb,5,f64,0,10\nc,6,u16,5,5\n');
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
