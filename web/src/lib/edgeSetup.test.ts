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
  it('siemens-pac4200 registers match manual A5E02316180B-03 tables A-1/A-14 (docs/devices/siemens-pac4200.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'siemens-pac4200')!;
    expect(t.link).toBe('tcp'); // base unit Ethernet, Modbus TCP (same offsets over RTU via PAC RS485 module)
    expect(t.func).toBe(4); // FC 03 and 04 both stated; template uses 04
    expect(t.wordOrderUnverified).toBe(true); // word order is the ONE unstated convention
    expect(t.pduBaseUnverified).toBeUndefined(); // addressing proven by Siemens frame examples
    expect(t.funcUnverified).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('va_n').register).toBe(1); // offset 1, Float, V
    expect(pt('p_total').register).toBe(65); // offset 65, Float, W
    expect(pt('freq').register).toBe(55); // offset 55, Float, Hz
    expect(pt('thd_v_ab').register).toBe(43); // offset 43, THD Voltage a-b (line-to-line)
    expect(pt('energy_import_t1').register).toBe(2801); // Float, Wh (float energy table A-14)
    expect(pt('apparent_t2').register).toBe(2819); // Float, VAh
    expect(t.points.every((p) => (p.type ?? t.type) === 'f32')).toBe(true); // all Float, 2 registers
    // no duplicate or overlapping 2-register spans
    const starts = t.points.map((x) => x.register);
    expect(new Set(starts).size).toBe(starts.length);
    for (const r of starts) expect(t.points.every((q) => q.register === r || q.register >= r + 2 || q.register + 1 < r)).toBe(true);
  });
  it('janitza-umg96rm-e registers match the official Modbus address list (docs/devices/janitza-umg96rm-e.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'janitza-umg96rm-e')!;
    expect(t.link).toBe('tcp'); // base unit Ethernet, Modbus TCP (same addresses over RTU on RS485)
    expect(t.func).toBe(4); // FC 03 and 04 both stated for the slave; template uses 04
    expect(t.word_order).toBe('abcd'); // vendor-stated: listed addresses answer big-endian (+32768 for little-endian)
    expect(t.wordOrderUnverified).toBeUndefined(); // byte order IS documented
    expect(t.pduBaseUnverified).toBeUndefined(); // operating manual prints the same values under a "Modbus address" column
    expect(t.funcUnverified).toBeUndefined();
    expect(t.provisional).toBeUndefined();
    expect(t.stopBits).toBe(2); // vendor transfer parameters: 8 data bits, no parity, 2 stop bits (internal UART)
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('va_n').register).toBe(19000); // Voltage L1-N
    expect(pt('vc_a').register).toBe(19010); // Voltage L1-L3 (doc's L3-L1 naming)
    expect(pt('ia').register).toBe(19012);
    expect(pt('i_n_vec').register).toBe(19018); // "Vector sum; IN=I1+I2+I3" - named as a vector sum, not a neutral current
    expect(pt('p_sum').register).toBe(19026); // "Sum; Psum3=P1+P2+P3" - named sum, not total/avg
    expect(pt('freq').register).toBe(19050); // Measured frequency
    expect(pt('rotation_field').register).toBe(19052);
    expect(pt('energy_total').register).toBe(19060); // Real energy L1..L3
    expect(pt('energy_consumed').register).toBe(19068);
    expect(pt('energy_delivered').register).toBe(19076);
    expect(pt('energy_reactive_cap').register).toBe(19108);
    expect(pt('thd_v_a').register).toBe(19110); // THD U L1-N
    expect(pt('thd_i_c').register).toBe(19120);
    expect(t.points.every((p) => (p.type ?? t.type) === 'f32')).toBe(true); // all Float, 2 registers
    // no duplicate or overlapping 2-register spans
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const y = deviceYaml({ deviceId: 'umg-1', templateId: 'janitza-umg96rm-e', link: 'tcp', host: '10.0.0.5', tcpPort: 502, address: 1, intervalSec: 5 });
    expect(y).toContain('register: 19000'); expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
    const y2 = deviceYaml({ deviceId: 'umg-1', templateId: 'janitza-umg96rm-e', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', stopBits: 2, address: 1, intervalSec: 5 });
    expect(y2).toContain('stop_bits: 2'); // the operator's serial choice reaches the YAML verbatim
    expect(y2).not.toContain('stop_bits: 0');
  });
  it('stop bits are modeled: template presets, operator choice emitted, invalid values rejected', () => {
    const jan = EDGE_TEMPLATES.find((x) => x.id === 'janitza-umg96rm-e')!;
    expect(jan.stopBits).toBe(2);
    // a template without vendor-documented stop bits leaves the field undefined - the form defaults to 1
    expect(EDGE_TEMPLATES.find((x) => x.id === 'eastron-sdm630')!.stopBits).toBeUndefined();
    const base = { deviceId: 'm1', templateId: 'eastron-sdm630', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, parity: 'none' as const, address: 1, intervalSec: 10 };
    expect(deviceYaml({ ...base, stopBits: 1 })).toContain('stop_bits: 1');
    expect(deviceYaml({ ...base, stopBits: 2 })).toContain('stop_bits: 2');
    expect(deviceYaml(base)).toContain('stop_bits: 0'); // legacy default = 1 stop bit in the agent
    expect(validateDevice({ ...base, stopBits: 3 as 1 }).some((e) => e.includes('Stop bits'))).toBe(true);
  });
  it('circutor-cvm-c10 registers match the official manual M001B01-03 tables 19/20 (docs/devices/circutor-cvm-c10.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'circutor-cvm-c10')!;
    expect(t.link).toBe('rtu');
    expect(t.func).toBe(4); // FC 03 and 04 both stated for these variables; template uses 04
    expect(t.type).toBe('u32');
    expect(t.word_order).toBe('abcd'); // proven by the manual's own frame example: 0000 084D = VL1 x10 = 212.5 V
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined(); // the example reads listed address 0x0000 unmodified
    expect(t.funcUnverified).toBeUndefined();
    expect(t.provisional).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('va_n').register).toBe(0x00);
    expect(pt('va_n').scale).toBe(0.1); // V x 10
    expect(pt('ia').register).toBe(0x02);
    expect(pt('ia').scale).toBe(0.001); // mA
    expect(pt('sa').register).toBe(0x0A);
    expect(pt('s_total').register).toBe(0x36); // apparent three-phase "kVA III"
    expect(pt('freq').register).toBe(0x3C);
    expect(pt('freq').scale).toBe(0.01); // Hz x 100
    expect(pt('va_b').register).toBe(0x3E);
    expect(pt('i_n').register).toBe(0x44); // measured neutral current, mA
    expect(pt('thd_i_c').register).toBe(0x50);
    expect(pt('energy_consumed').register).toBe(0xDE); // Wh, Total column
    expect(pt('energy_generated').register).toBe(0xF2);
    expect(pt('reactive_cap_generated').register).toBe(0xFA);
    expect(pt('apparent_generated').register).toBe(0xFE);
    // signed quantities are excluded: no W, var, PF or cosphi points (wire encoding of negatives unstated)
    expect(t.points.some((p) => p.unit === 'W' || p.unit === 'var')).toBe(false);
    expect(t.points.some((p) => p.id.startsWith('pf') || p.id.startsWith('cosphi'))).toBe(false);
    // no duplicate or overlapping 2-register spans
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const y = deviceYaml({ deviceId: 'cvm-1', templateId: 'circutor-cvm-c10', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', address: 10, intervalSec: 5 });
    expect(y).toContain('register: 62'); // 0x3E = 62 (L1-L2 voltage): hex addresses land as decimal in YAML
    expect(y).toContain('scale: 0.1'); expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
  });
  it('eaton-iq250-260 registers match the official IB02601006E map (docs/devices/eaton-iq250-260-pxm2000.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'eaton-iq250-260')!;
    expect(t.link).toBe('rtu'); // same holding-register map applies over TCP with the Ethernet option
    expect(t.func).toBe(3); // the doc's B-3 note: holding registers only
    expect(t.type).toBe('f32');
    expect(t.word_order).toBe('abcd'); // B-1: lower-addressed register is the high-order half; B-2 worked example C4E1 1DB9 = -1800.929
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined(); // B-3: hex column is the wire address (serial number hex 0008 = SCADA 40009 - 40001)
    expect(t.funcUnverified).toBeUndefined();
    expect(t.provisional).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('va_n').register).toBe(0x03E7); // decimal 1000-1001 in the doc's 1-based column
    expect(pt('vc_a').register).toBe(0x03F1);
    expect(pt('ia').register).toBe(0x03F3);
    expect(pt('p_total').register).toBe(0x03F9);
    expect(pt('pf_total').register).toBe(0x03FF);
    expect(pt('freq').register).toBe(0x0401);
    expect(pt('freq').max).toBe(65); // doc range 0 to 65.00
    expect(pt('i_n').register).toBe(0x0403);
    expect(pt('p_a').register).toBe(0x0405);
    expect(pt('pf_c').register).toBe(0x041B);
    // energy counters are documented SINT32 (signedness vendor-stated), read raw per the programmed energy format
    expect(pt('energy_received').register).toBe(0x05DB);
    expect(pt('energy_received').type).toBe('i32');
    expect(pt('vah_total').register).toBe(0x05EB);
    expect(pt('varh_total').type).toBe('i32');
    // readings floats have no per-point type override (fall back to template f32)
    expect(pt('va_n').type).toBeUndefined();
    // no duplicate or overlapping 2-register spans
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const y = deviceYaml({ deviceId: 'iq-1', templateId: 'eaton-iq250-260', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', address: 1, intervalSec: 5 });
    expect(y).toContain('register: 999'); // 0x03E7 = 999 (Volts A-N): hex wire addresses land as decimal in YAML
    expect(y).toContain('func: 3'); expect(y).toContain('type: f32'); expect(y).toContain('type: i32'); expect(y).toContain('word_order: abcd');
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
  it('inepro-pro380 registers match the official manual V2.18-8 A3.2 (docs/devices/inepro-pro380.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'inepro-pro380')!;
    expect(t.func).toBe(3); // FC printed per row
    expect(t.word_order).toBe('abcd'); // "Float ABCD" printed per row
    expect(t.wordOrderUnverified).toBeUndefined(); // all conventions vendor-stated
    const reg = (id: string) => t.points.find((p) => p.id === id)?.register;
    expect(reg('v1n')).toBe(0x5002); expect(reg('v2n')).toBe(0x5004); expect(reg('v3n')).toBe(0x5006);
    expect(reg('freq')).toBe(0x5008);
    expect(reg('i1')).toBe(0x500C); expect(reg('i2')).toBe(0x500E); expect(reg('i3')).toBe(0x5010);
    expect(reg('p_total')).toBe(0x5012); expect(reg('p1')).toBe(0x5014); expect(reg('p2')).toBe(0x5016); expect(reg('p3')).toBe(0x5018);
    expect(reg('q_total')).toBe(0x501A); expect(reg('s_total')).toBe(0x5022); expect(reg('pf_total')).toBe(0x502A);
    expect(reg('kwh_total')).toBe(0x6000); expect(reg('kwh_import')).toBe(0x600C); expect(reg('kwh_export')).toBe(0x6018);
    // the undefined starred aggregates are excluded
    expect(t.points.some((p) => p.register === 0x5000 || p.register === 0x500A)).toBe(false);
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const y = deviceYaml({ deviceId: 'pro-1', templateId: 'inepro-pro380', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5 });
    expect(y).toContain('register: 20488'); expect(y).toContain('func: 3'); expect(y).toContain('word_order: abcd');
  });
  it('eastron-sdm72d-m registers match the manufacturer manual V1.4 part 3 (docs/devices/eastron-sdm72d-m.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'eastron-sdm72d-m')!;
    expect(t.func).toBe(4); // vendor-stated for all input registers
    expect(t.wordOrderUnverified).toBe(true); // vendor: byte order "normal or reverse", automatic
    const reg = (id: string) => t.points.find((p) => p.id === id)?.register;
    expect(reg('p1')).toBe(0x000C); expect(reg('p2')).toBe(0x000E); expect(reg('p3')).toBe(0x0010);
    expect(reg('p_total')).toBe(0x0034);
    expect(reg('kwh_import')).toBe(0x0048); expect(reg('kwh_export')).toBe(0x004A); expect(reg('kwh_total')).toBe(0x0156);
    expect(reg('p_import')).toBe(0x0500); expect(reg('p_export')).toBe(0x0502);
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const base72 = { deviceId: 'sdm72-1', templateId: 'eastron-sdm72d-m', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base72).join(' ')).toContain('Word order');
    const y = deviceYaml({ ...base72, wordOrder: 'dcba' });
    expect(y).toContain('word_order: dcba'); expect(y).toContain('func: 4'); expect(y).toContain('register: 52');
  });
  it('accuenergy-acuvim2 registers match the official workbook v1.10 (docs/devices/accuenergy-acuvim2.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'accuenergy-acuvim2')!;
    expect(t.func).toBe(3); // sheet headers "03H Read"
    expect(t.wordOrderUnverified).toBe(true); // float word order not stated (only dword layout is)
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('freq').register).toBe(0x4000);
    expect(pt('v1n').register).toBe(0x4002); expect(pt('v_avg_ll').register).toBe(0x4010);
    expect(pt('i1').register).toBe(0x4012); expect(pt('i_n').register).toBe(0x401A);
    expect(pt('p1').register).toBe(0x401C); expect(pt('p_total').register).toBe(0x4022);
    expect(pt('q_total').register).toBe(0x402A); expect(pt('s_total').register).toBe(0x4032); expect(pt('pf_total').register).toBe(0x403A);
    // energy is UINT32 scaled /10, not float
    expect(pt('kwh_import').register).toBe(0x4048);
    expect(pt('kwh_import').type).toBe('u32'); expect(pt('kwh_import').scale).toBe(0.1);
    const floats = t.points.filter((p) => p.id !== 'kwh_import');
    const used = new Set<number>();
    for (const p of floats) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const baseA = { deviceId: 'acu-1', templateId: 'accuenergy-acuvim2', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(baseA).join(' ')).toContain('Word order');
    const y = deviceYaml({ ...baseA, wordOrder: 'abcd' });
    expect(y).toContain('func: 3'); expect(y).toContain('register: 16384'); expect(y).toContain('type: u32'); expect(y).toContain('scale: 0.1');
  });
  it('eastron-sdm72ct-m registers match the vendor protocol V1.1 (docs/devices/eastron-sdm72ct-m.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'eastron-sdm72ct-m')!;
    expect(t.func).toBe(4); // vendor-stated for all input registers
    expect(t.wordOrderUnverified).toBe(true); // protocol doc silent; sibling manual: order varies per unit
    const reg = (id: string) => t.points.find((p) => p.id === id)?.register;
    expect(reg('v1n')).toBe(0); // 30001
    expect(reg('i1')).toBe(6); // 30007
    expect(reg('p1')).toBe(12); // 30013
    expect(reg('pf1')).toBe(30); // 30031
    expect(reg('p_total')).toBe(52); // 30053
    expect(reg('pf_total')).toBe(62); // 30063
    expect(reg('freq')).toBe(70); // 30071
    expect(reg('kwh_import')).toBe(72); // 30073
    expect(reg('v12')).toBe(200); // 30201
    expect(reg('i_neutral')).toBe(224); // 30225
    expect(reg('kwh_total')).toBe(342); // 30343
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const b = { deviceId: 'ct-1', templateId: 'eastron-sdm72ct-m', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(b).join(' ')).toContain('Word order');
    expect(deviceYaml({ ...b, wordOrder: 'abcd' })).toContain('func: 4');
  });
  it('carlo-gavazzi-em24-e1 registers match EM24_E1_CP (docs/devices/carlo-gavazzi-em24-e1.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'carlo-gavazzi-em24-e1')!;
    expect(t.func).toBe(4); // 03/04 vendor-stated as equivalent
    expect(t.word_order).toBe('cdab'); // vendor: 32-bit word order LSW->MSW
    expect(t.wordOrderUnverified).toBeUndefined(); // all conventions vendor-stated
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(0x0000); expect(pt('v31').register).toBe(0x000A);
    expect(pt('i1').register).toBe(0x000C); expect(pt('i1').scale).toBe(0.001);
    expect(pt('p1').register).toBe(0x0012); expect(pt('p1').scale).toBe(0.1);
    expect(pt('q_total').register).toBe(0x002C);
    expect(pt('pf1').register).toBe(0x002E); expect(pt('pf1').type).toBe('i16'); expect(pt('pf_total').register).toBe(0x0031);
    expect(pt('freq').register).toBe(0x0033); expect(pt('freq').type).toBe('u16');
    expect(pt('kwh_import').register).toBe(0x0034); expect(pt('kwh_import').scale).toBe(0.1);
    expect(pt('kwh_export').register).toBe(0x004E); expect(pt('kvarh_export').register).toBe(0x0050);
    // i32 points span 2 registers without overlap; i16/u16 span 1
    const used = new Set<number>();
    for (const p of t.points) {
      const n = (p.type ?? t.type) === 'i32' ? 2 : 1;
      for (let k = 0; k < n; k++) { expect(used.has(p.register + k)).toBe(false); used.add(p.register + k); }
    }
    const y = deviceYaml({ deviceId: 'em24-1', templateId: 'carlo-gavazzi-em24-e1', link: 'tcp', host: '192.168.1.70', tcpPort: 502, address: 1, intervalSec: 5 });
    expect(y).toContain('func: 4'); expect(y).toContain('word_order: cdab'); expect(y).toContain('register: 51'); expect(y).toContain('type: u16'); expect(y).toContain('scale: 0.001');
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
