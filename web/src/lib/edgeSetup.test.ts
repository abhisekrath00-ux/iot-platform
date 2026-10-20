import { describe, expect, it } from 'vitest';
import { deviceYaml, validateDevice, EDGE_TEMPLATES, DeviceInput } from './edgeSetup';

const base: DeviceInput = { deviceId: 'meter-1', templateId: 'selec-mx300', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', address: 1, intervalSec: 10 };

describe('edge setup', () => {
  it('generates the MX300 device block', () => {
    const y = deviceYaml(base);
    expect(y).toContain('profile: modbus-generic');
    expect(y).toContain('{id: voltage, register: 0, func: 4, type: f32, word_order: abcd');
    expect(y).toContain('{id: frequency, register: 12');
    expect(y.match(/- \{id:/g)?.length).toBe(7);
  });
  it('supports modbus tcp', () => {
    const y = deviceYaml({ ...base, link: 'tcp', host: '192.168.1.50', tcpPort: 502 });
    expect(y).toContain('profile: modbus-tcp');
    expect(y).toContain('host: 192.168.1.50');
  });
  it('rejects bad input instead of emitting a file', () => {
    expect(validateDevice({ ...base, address: 0 })).not.toEqual([]);
    expect(validateDevice({ ...base, deviceId: 'Bad Id!' })).not.toEqual([]);
    expect(validateDevice({ ...base, port: 'ttyUSB0' })).not.toEqual([]);
    expect(validateDevice({ ...base, baud: 1234 })).not.toEqual([]);
    expect(() => deviceYaml({ ...base, intervalSec: 0 })).toThrow();
  });
  it('eastron-sdm630 registers match the official protocol PDF (docs/devices/eastron-sdm630.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'eastron-sdm630')!;
    expect(t.func).toBe(4);
    expect(t.word_order).toBe('abcd');
    const reg = (id: string) => t.points.find((p) => p.id === id)?.register;
    expect(reg('v1n')).toBe(0); // 30001
    expect(reg('i1')).toBe(6); // 30007
    expect(reg('w_total')).toBe(52); // 30053
    expect(reg('freq')).toBe(70); // 30071
    expect(reg('kwh_import')).toBe(72); // 30073
    expect(reg('v12')).toBe(200); // 30201
    expect(reg('i_neutral')).toBe(224); // 30225
    expect(reg('thd_i1')).toBe(240); // 30241
    // no overlapping registers (each f32 point spans 2)
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
  });
  it('socomec-diris-a40 registers match communication table CMT-1.7.4B (docs/devices/socomec-diris-a40.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'socomec-diris-a40')!;
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(18444); // 0x480C, U32, V 10e-2
    expect(pt('v1n').scale).toBe(0.01);
    expect(pt('i1').register).toBe(18458); // 0x481A, mA
    expect(pt('i1').scale).toBe(0.001);
    expect(pt('freq').register).toBe(18442); // 0x480A, mHz
    expect(pt('p_total').register).toBe(18476); // 0x482C, S32 W
    expect(pt('p_total').type).toBe('i32');
    expect(pt('pf1').register).toBe(18518); // 0x4856, S16 /1000
    expect(pt('pf1').type).toBe('i16');
    expect(pt('pf1').scale).toBe(0.001);
  });
  it('siemens-pac3200 registers match manual A5E01168664B-04 table 3-6 (docs/devices/siemens-pac3200.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'siemens-pac3200')!;
    expect(t.link).toBe('tcp'); // base unit Ethernet, Modbus TCP
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('va_n').register).toBe(1); // offset 1, Float, V
    expect(pt('p_total').register).toBe(65); // offset 65, Float, W
    expect(pt('freq').register).toBe(55); // offset 55, Float, Hz
    expect(pt('work_hours').register).toBe(213); // offset 213, unsigned long
    expect(pt('work_hours').type).toBe('u32');
    expect(pt('energy_import_t1').register).toBe(801); // offset 801, Double, Wh
    expect(pt('energy_import_t1').type).toBe('f64');
    expect(pt('apparent_t2').register).toBe(837); // offset 837, Double, VAh
    expect(pt('apparent_t2').type).toBe('f64');
    // no gaps or overlaps across the f64 spans
    const spans = t.points.map((x) => [x.register, x.register + ((x.type ?? t.type) === 'f64' ? 4 : 2) - 1]);
    for (const [a] of spans) for (const [b] of spans) if (a !== b) expect(spans.filter((s) => s[0] === a).length).toBe(1);
  });
  it('every template point has a validation range', () => {
    for (const t of EDGE_TEMPLATES) for (const p of t.points) expect(p.max).toBeGreaterThan(p.min);
  });
});
