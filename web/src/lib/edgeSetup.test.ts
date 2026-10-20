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
  it('abb-m4m registers match the official ABB manual + table (docs/devices/abb-m4m.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'abb-m4m')!;
    expect(t.func).toBe(3); // holding registers, NOT FC 04
    expect(t.word_order).toBe('abcd');
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(23298); // 0x5B02, the official manual's worked example
    expect(pt('v1n').scale).toBe(0.1);
    expect(pt('i1').register).toBe(23312); // 0x5B10, x0.01 A
    expect(pt('p_total').register).toBe(23322); // 0x5B1A, INT32 x0.01 W
    expect(pt('p_total').type).toBe('i32');
    expect(pt('freq').register).toBe(23346); // 0x5B32, UINT16 x0.01 Hz
    expect(pt('freq').type).toBe('u16');
    expect(pt('pf_total').register).toBe(23360); // 0x5B40, INT16 x0.001
    expect(t.points.every((p) => p.register < 20480 || p.register > 21800)).toBe(true); // UINT64 energy counter range excluded
    // word order is NOT documented by ABB: no YAML without an explicit installer choice
    expect(t.wordOrderUnverified).toBe(true);
    const base = { deviceId: 'm4m-1', templateId: 'abb-m4m', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order');
    expect(deviceYaml({ ...base, wordOrder: 'cdab' })).toContain('word_order: cdab');
    expect(deviceYaml({ ...base, wordOrder: 'abcd' })).toContain('word_order: abcd');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });
  it('schneider-pm53xx registers match the official register list v2011_v2021_R01 (docs/devices/schneider-pm53xx.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'schneider-pm53xx')!;
    expect(t.func).toBe(3); // holding registers (FC not stated by Schneider; FC 03 chosen, gated in the provisional text)
    expect(t.type).toBe('f32');
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('i1').register).toBe(3000); // Current A
    expect(pt('i_avg').register).toBe(3010); // Current Avg
    expect(pt('v12').register).toBe(3020); // Voltage A-B
    expect(pt('v_avg_ll').register).toBe(3026); // Voltage L-L Avg
    expect(pt('v1n').register).toBe(3028); // Voltage A-N
    expect(pt('v_avg_ln').register).toBe(3036); // Voltage L-N Avg
    expect(pt('p_total').register).toBe(3060); // Active Power Total
    expect(pt('q_total').register).toBe(3068); // Reactive Power Total
    expect(pt('s_total').register).toBe(3076); // Apparent Power Total
    expect(pt('pf_total').register).toBe(3084); // PF Total, 4Q_FP_PF encoding
    expect(pt('pf_total').max).toBe(2); // Schneider 4Q range, NOT plain -1..1
    expect(pt('freq').register).toBe(3110); // Frequency (also in the user manual table)
    expect(pt('kwh_import').register).toBe(2700); // Active Energy Delivered, FLOAT32 kWh (INT64 Wh excluded)
    expect(t.points.every((p) => p.register < 3204 || p.register > 3251)).toBe(true); // INT64 energy range excluded
    // word order AND register base are NOT documented by Schneider: no YAML without explicit choices
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBe(true);
    expect(t.funcUnverified).toBe(true);
    const base = { deviceId: 'pm-1', templateId: 'schneider-pm53xx', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order');
    expect(validateDevice({ ...base, wordOrder: 'abcd' }).join(' ')).toContain('Register base');
    expect(validateDevice({ ...base, wordOrder: 'abcd', pduBase: 'listed' as const }).join(' ')).toContain('Read function');
    const listed = deviceYaml({ ...base, wordOrder: 'abcd', pduBase: 'listed' as const, readFunc: 3 as const });
    expect(listed).toContain('register: 3110'); // frequency verbatim
    expect(listed).toContain('word_order: abcd');
    expect(listed).toContain('func: 3');
    const fc4 = deviceYaml({ ...base, wordOrder: 'abcd', pduBase: 'listed' as const, readFunc: 4 as const });
    expect(fc4).toContain('func: 4');
    expect(fc4).not.toContain('func: 3');
    const minus1 = deviceYaml({ ...base, wordOrder: 'abcd', pduBase: 'minus1' as const, readFunc: 3 as const });
    expect(minus1).toContain('register: 3109'); // frequency minus one
    expect(minus1).toContain('register: 2999'); // current A minus one
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
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
  it('carlo-gavazzi-em340 registers match EM300 series communication protocol CP (docs/devices/carlo-gavazzi-em340.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'carlo-gavazzi-em340')!;
    expect(t.word_order).toBe('cdab'); // LSW->MSW per the protocol doc
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(0x0000); // INT32, Volt*10
    expect(pt('v1n').scale).toBe(0.1);
    expect(pt('i1').register).toBe(0x000C); // INT32, Ampere*1000
    expect(pt('i1').scale).toBe(0.001);
    expect(pt('p1').register).toBe(0x0012); // INT32, Watt*10
    expect(pt('pf1').register).toBe(0x002E); // INT16, PF*1000
    expect(pt('pf1').type).toBe('i16');
    expect(pt('freq').register).toBe(0x0033); // INT16, Hz*10
    expect(pt('freq').type).toBe('i16');
    expect(pt('kwh_import').register).toBe(0x0034); // INT32, kWh*10
    expect(pt('kwh_export').register).toBe(0x004E);
    expect(pt('kvarh_export').register).toBe(0x0050);
  });
  it('provisional templates block config until the assumption is confirmed', () => {
    const d: DeviceInput = { ...base, templateId: 'siemens-pac3200', link: 'tcp', host: '192.168.1.60', tcpPort: 502 };
    const errs = validateDevice(d);
    expect(errs.some((e) => e.startsWith('Provisional template:'))).toBe(true);
    expect(() => deviceYaml(d)).toThrow();
    const ok = validateDevice({ ...d, provisionalConfirmed: true });
    expect(ok).toEqual([]);
    expect(deviceYaml({ ...d, provisionalConfirmed: true })).toContain('type: f64');
  });
  it('non-provisional templates need no confirmation', () => {
    expect(validateDevice({ ...base, provisionalConfirmed: false })).toEqual([]);
  });
  it('every template point has a validation range', () => {
    for (const t of EDGE_TEMPLATES) for (const p of t.points) expect(p.max).toBeGreaterThan(p.min);
  });
});
