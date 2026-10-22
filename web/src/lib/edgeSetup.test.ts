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
  it('schneider-em6400ng registers match the official Public register list v1050, EM6400 NG column (docs/devices/schneider-em6400ng.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'schneider-em6400ng')!;
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
    expect(pt('freq').register).toBe(3110); // Frequency
    expect(pt('kwh_import').register).toBe(2700); // Active Energy Delivered, resettable FLOAT32 kWh (2676 = Permanent block)
    // word order AND register base AND function code are NOT documented by Schneider: no YAML without explicit choices
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBe(true);
    expect(t.funcUnverified).toBe(true);
    const base = { deviceId: 'em64-1', templateId: 'schneider-em6400ng', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order');
    expect(validateDevice({ ...base, wordOrder: 'abcd' }).join(' ')).toContain('Register base');
    expect(validateDevice({ ...base, wordOrder: 'abcd', pduBase: 'listed' as const }).join(' ')).toContain('Read function');
    const listed = deviceYaml({ ...base, wordOrder: 'abcd', pduBase: 'listed' as const, readFunc: 3 as const });
    expect(listed).toContain('register: 3110'); // frequency verbatim
    expect(listed).toContain('word_order: abcd');
    expect(listed).toContain('func: 3');
    const minus1 = deviceYaml({ ...base, wordOrder: 'abcd', pduBase: 'minus1' as const, readFunc: 4 as const });
    expect(minus1).toContain('register: 3109'); // frequency minus one
    expect(minus1).toContain('func: 4');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });


  it('schneider-iem3000 registers match the official register list DOCA0005EN-07 (docs/devices/schneider-iem3000.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'schneider-iem3000')!;
    expect(t.func).toBe(3); // FC 03 is vendor-stated (the only read FC in the doc's function list)
    expect(t.type).toBe('f32');
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('i1').register).toBe(3000); // I1: phase 1 current
    expect(pt('i_avg').register).toBe(3010); // Current Avg
    expect(pt('v12').register).toBe(3020); // Voltage L1-L2
    expect(pt('v_avg_ll').register).toBe(3026); // Voltage L-L Avg
    expect(pt('v1n').register).toBe(3028); // Voltage L1-N
    expect(pt('v_avg_ln').register).toBe(3036); // Voltage L-N Avg
    expect(pt('p_total').register).toBe(3060); // Total Active Power
    expect(pt('pf_total').register).toBe(3084); // Total Power Factor, four-quadrant encoding
    expect(pt('pf_total').min).toBe(-2); expect(pt('pf_total').max).toBe(2); // 4Q range, NOT plain -1..1
    expect(pt('freq').register).toBe(3110); // Frequency
    expect(pt('energy_import').register).toBe(45100); // Total Active Energy Import, 32-bit float block
    expect(pt('energy_import').unit).toBe('Wh'); // float block prints Wh; Int64 block at 3204 excluded (no i64 driver type)
    expect(t.points.find((p) => p.id === 'q_total')).toBeUndefined(); // 3068 not applicable on iEM3150/3250/3350: excluded
    expect(t.points.find((p) => p.id === 's_total')).toBeUndefined(); // 3076 same exclusion
    // word order is NOT documented: no YAML without an explicit choice. FC and address base ARE vendor-stated, so not gated.
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    const base = { deviceId: 'iem3-1', templateId: 'schneider-iem3000', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order');
    const listed = deviceYaml({ ...base, wordOrder: 'abcd' });
    expect(listed).toContain('register: 3110'); // frequency verbatim (listed address = on-wire address, vendor-stated)
    expect(listed).toContain('register: 45100'); // float energy verbatim
    expect(listed).toContain('word_order: abcd');
    expect(listed).toContain('func: 3');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });


  it('abb-b23-b24 registers match the official manual 2CMC485003M0201 Rev A chapter 9 (docs/devices/abb-b23-b24.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'abb-b23-b24')!;
    expect(t.func).toBe(3); // FC 03 vendor-stated (chapter 9.1.1)
    expect(t.type).toBe('u32');
    expect(t.word_order).toBe('abcd'); // vendor-stated: MSB in the first (lowest) register
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(23296); // 5B00
    expect(pt('v3n').register).toBe(23300); // 5B04
    expect(pt('v12').register).toBe(23302); // 5B06 L1-L2
    expect(pt('v23').register).toBe(23304); // 5B08 manual prints L3-L2 (see doc)
    expect(pt('v31').register).toBe(23306); // 5B0A manual prints L1-L3 (see doc)
    expect(pt('i1').register).toBe(23308); // 5B0C
    expect(pt('i_n').register).toBe(23314); // 5B12
    expect(pt('p_total').register).toBe(23316); // 5B14, signed 32-bit, 2 decimals
    expect(pt('p_total').type).toBe('i32');
    expect(pt('p_total').scale).toBe(0.01);
    expect(pt('p_total').max).toBe(2.1e7); // documented ~+/-21 MW ceiling
    expect(pt('q_total').register).toBe(23324); // 5B1C
    expect(pt('s_total').register).toBe(23332); // 5B24
    expect(pt('freq').register).toBe(23340); // 5B2C, 1 register
    expect(pt('freq').type).toBe('u16');
    expect(pt('pf_total').register).toBe(23354); // 5B3A, signed, 0.001
    expect(pt('pf_total').type).toBe('i16');
    expect(pt('pf3').register).toBe(23357); // 5B3D
    // energy accumulators excluded: 4 registers = 64-bit unsigned, driver has no u64
    expect(t.points.some((p) => p.register >= 0x5000 && p.register < 0x5B00)).toBe(false);
    // all conventions vendor-stated: nothing gated
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    const base = { deviceId: 'b24-1', templateId: 'abb-b23-b24', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base)).toEqual([]);
    const yaml = deviceYaml(base);
    expect(yaml).toContain('register: 23340'); // frequency verbatim (listed hex = on-wire address)
    expect(yaml).toContain('scale: 0.01');
    expect(yaml).toContain('type: i32');
    expect(yaml).toContain('func: 3');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });


  it('janitza-umg96rm registers match the official Modbus-address list firmware 1.14+ (docs/devices/janitza-umg96rm.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'janitza-umg96rm')!;
    expect(t.type).toBe('f32');
    expect(t.word_order).toBe('abcd'); // vendor-stated Big-Endian for listed addresses
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(19000);
    expect(pt('v31').register).toBe(19010); // manual prints L1-L3
    expect(pt('i_n').register).toBe(19018); // vector sum IN
    expect(pt('p_total').register).toBe(19026);
    expect(pt('s_total').register).toBe(19034);
    expect(pt('q_total').register).toBe(19042);
    expect(pt('pf1').register).toBe(19044); // CosPhi fundamental
    expect(pt('pf_total').register).toBe(826); // Sum CosPhisum3, Measured-values block
    expect(pt('freq').register).toBe(19050);
    expect(pt('kwh_import').register).toBe(19068); // Wh float, consumed rate 1
    expect(pt('kwh_import').scale).toBe(0.001);
    expect(pt('kwh_export').register).toBe(19076);
    expect(pt('kvah_total').register).toBe(19084);
    expect(pt('kvarh_total').register).toBe(19092);
    // base IS vendor-stated (user manual "Modbus Address" column); FC 03 vs 04 is NOT: stays gated
    expect(t.funcUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.stopBits).toBe(2); // vendor-stated serial: 8 data bits, parity none, 2 stop bits
    const missing = validateDevice({ deviceId: 'umg-1', templateId: 'janitza-umg96rm', link: 'rtu', port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true });
    expect(missing.length).toBeGreaterThan(0);
    const base = { deviceId: 'umg-1', templateId: 'janitza-umg96rm', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, readFunc: 3 as const, provisionalConfirmed: true };
    expect(validateDevice(base)).toEqual([]);
    const yaml = deviceYaml(base);
    expect(yaml).toContain('register: 19000'); // verbatim
    expect(yaml).toContain('register: 19068');
    expect(yaml).toContain('func: 3');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });


  it('kamstrup-multical-x03 registers match the official data sheet FILE100004479_C_EN_03.2025 (docs/devices/kamstrup-multical-x03.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'kamstrup-multical-x03')!;
    expect(t.type).toBe('f32');
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('flow_m3h').register).toBe(1); // printed register 1 = hex address 0000
    expect(pt('power_kw').register).toBe(5); // 0004
    expect(pt('t_flow_c').register).toBe(7); // 0006
    expect(pt('t_return_c').register).toBe(9); // 0008
    expect(pt('t_diff_k').register).toBe(15); // 000E
    expect(pt('heat_kwh').register).toBe(21); // 0014
    expect(pt('cooling_kwh').register).toBe(25); // 0018
    expect(pt('volume_m3').register).toBe(53); // 0034
    // word order, read FC and base are NOT stated in the data sheet: all three stay gated
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.funcUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBe(true);
    const missing = validateDevice({ deviceId: 'mc-1', templateId: 'kamstrup-multical-x03', link: 'rtu', port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true });
    expect(missing.length).toBeGreaterThan(0);
    // units are dynamic on this meter: a separate acknowledgement is required
    expect(t.unitsUnverified).toBe(true);
    const noUnits = { deviceId: 'mc-1', templateId: 'kamstrup-multical-x03', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, wordOrder: 'abcd' as const, readFunc: 3 as const, pduBase: 'listed' as const, provisionalConfirmed: true };
    expect(validateDevice(noUnits).some((e) => e.startsWith('Configured units:'))).toBe(true);
    const base = { ...noUnits, unitsConfirmed: true };
    expect(validateDevice(base)).toEqual([]);
    expect(deviceYaml(base)).toContain('register: 21'); // heat energy verbatim (listed register)
    const minus1 = deviceYaml({ ...base, pduBase: 'minus1' as const });
    expect(minus1).toContain('register: 20'); // 21 -> hex address 0x14, the vendor-stated correspondence
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
  it('ime-conto-d4 registers match the official protocol IDP000372 REV 1.01 (docs/devices/ime-conto-d4.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'ime-conto-d4')!;
    expect(t.func).toBe(4); // FC 04 printed per input-register row
    expect(t.type).toBe('u32');
    expect(t.wordOrderUnverified).toBe(true); // only byte order ("big endian") stated; 32-bit word order NOT stated
    expect(t.pduBaseUnverified).toBeUndefined(); // the protocol prints the on-wire "Register Address" column explicitly
    expect(t.funcUnverified).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('i1').register).toBe(20480); expect(pt('i1').scale).toBe(0.001); // 0x5000, mA
    expect(pt('i2').register).toBe(20482); expect(pt('i3').register).toBe(20484);
    expect(pt('v1n').register).toBe(20509); expect(pt('v1n').scale).toBe(0.001); // 0x501D, mV
    expect(pt('v2n').register).toBe(20511); expect(pt('v3n').register).toBe(20513);
    expect(pt('v12').register).toBe(20515); expect(pt('v23').register).toBe(20517); expect(pt('v31').register).toBe(20519);
    expect(pt('freq').register).toBe(20537); expect(pt('freq').scale).toBe(0.01); expect(pt('freq').type).toBe('u16'); // 0x5039
    expect(pt('p_total').register).toBe(20538); expect(pt('p_total').scale).toBe(0.01); expect(pt('p_total').type).toBe('i32'); // 0x503A, 0.01 kW
    expect(pt('q_total').register).toBe(20540); // 0x503C
    expect(pt('s_total').register).toBe(20544); // 0x5040
    expect(pt('pf_total').register).toBe(20548); expect(pt('pf_total').scale).toBe(0.001); expect(pt('pf_total').type).toBe('i16'); // 0x5044
    expect(pt('p1').register).toBe(20551); expect(pt('p2').register).toBe(20553); expect(pt('p3').register).toBe(20555);
    expect(pt('kwh_import').register).toBe(20592); expect(pt('kwh_import').scale).toBe(0.01); // 0x5070, 0.01 kWh
    expect(pt('kwh_export').register).toBe(20594); // 0x5072
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    const base = { deviceId: 'd4-1', templateId: 'ime-conto-d4', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 5, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order'); // no YAML until the word-order gate is chosen
    const y = deviceYaml({ ...base, wordOrder: 'abcd' });
    expect(y).toContain('register: 20537'); expect(y).toContain('register: 20592');
    expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
  });
  it('satec-pm130 registers match the official guide BG0427 Rev. A10 (docs/devices/satec-pm130.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'satec-pm130')!;
    expect(t.func).toBe(3); // FC 03; the guide documents FC 04 as equivalent
    expect(t.type).toBe('u32');
    expect(t.word_order).toBe('cdab'); // guide: "the first register contains the low-order word"
    expect(t.wordOrderUnverified).toBeUndefined(); // word order, base and FC are all vendor-stated
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    expect(t.unitsUnverified).toBe(true); // units follow the meter's resolution option (default Low: 1V/1A/1kW)
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(13952); expect(pt('v2n').register).toBe(13954); expect(pt('v3n').register).toBe(13956);
    expect(pt('i1').register).toBe(13958); expect(pt('i2').register).toBe(13960); expect(pt('i3').register).toBe(13962);
    expect(pt('p1').register).toBe(13964); expect(pt('p2').register).toBe(13966); expect(pt('p3').register).toBe(13968);
    expect(pt('p1').type).toBe('i32'); // kW rows are signed
    expect(pt('v12').register).toBe(14012); expect(pt('v23').register).toBe(14014); expect(pt('v31').register).toBe(14016);
    expect(pt('p_total').register).toBe(14336); expect(pt('p_total').type).toBe('i32');
    expect(pt('q_total').register).toBe(14338); expect(pt('s_total').register).toBe(14340);
    expect(pt('pf_total').register).toBe(14342); expect(pt('pf_total').scale).toBe(0.001); expect(pt('pf_total').type).toBe('i32');
    expect(pt('v_avg_ln').register).toBe(14356); expect(pt('v_avg_ll').register).toBe(14358); expect(pt('i_avg').register).toBe(14360);
    expect(pt('freq').register).toBe(14468); expect(pt('freq').scale).toBe(0.01);
    expect(pt('kwh_import').register).toBe(14720); expect(pt('kwh_export').register).toBe(14722);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    // units gate: no YAML until the unit confirmation is ticked
    const base = { deviceId: 'pm130-1', templateId: 'satec-pm130', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('units');
    const y = deviceYaml({ ...base, unitsConfirmed: true });
    expect(y).toContain('register: 14468'); expect(y).toContain('register: 14720');
    expect(y).toContain('func: 3'); expect(y).toContain('word_order: cdab');
  });
  it('iskra-mc7x0 registers match the official MC 7x0 manual Appendix A (docs/devices/iskra-mc7x0.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'iskra-mc7x0')!;
    expect(t.func).toBe(4); // 3xxxx input registers; the manual states memory reference 30000/40000
    expect(t.type).toBe('t5'); // Iskra decade-exponent unsigned measurement
    expect(t.word_order).toBe('abcd'); // vendor worked example: FD01 E240 = 123456 x 10^-3, MSW first
    expect(t.wordOrderUnverified).toBeUndefined(); // word order, base and FC are all vendor-stated
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    expect(t.unitsUnverified).toBeUndefined(); // readings carry their own decade exponent; units are base units
    expect(t.stopBits).toBe(2); // vendor-stated factory default
    expect(t.serial).toEqual({ baud: 115200, parity: 'none', address: 33 }); // vendor-stated factory defaults
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    // on-wire = listed Modicon address - 30001
    expect(pt('freq').register).toBe(104); // 30105
    expect(pt('v1').register).toBe(106); expect(pt('v2').register).toBe(108); expect(pt('v3').register).toBe(110);
    expect(pt('v_avg_ln').register).toBe(112);
    expect(pt('v12').register).toBe(117); expect(pt('v23').register).toBe(119); expect(pt('v31').register).toBe(121);
    expect(pt('v_avg_ll').register).toBe(123);
    expect(pt('i1').register).toBe(125); expect(pt('i2').register).toBe(127); expect(pt('i3').register).toBe(129);
    expect(pt('i_neutral').register).toBe(131); expect(pt('i_avg').register).toBe(135);
    expect(pt('p_total').register).toBe(139); expect(pt('p_total').type).toBe('t6'); // powers are signed (T6)
    expect(pt('p1').register).toBe(141); expect(pt('p2').register).toBe(143); expect(pt('p3').register).toBe(145);
    expect(pt('q_total').register).toBe(147); expect(pt('q_total').type).toBe('t6');
    expect(pt('q1').register).toBe(149); expect(pt('q2').register).toBe(151); expect(pt('q3').register).toBe(153);
    expect(pt('s_total').register).toBe(155); expect(pt('s_total').type).toBeUndefined(); // T5 template default
    expect(pt('s1').register).toBe(157); expect(pt('s2').register).toBe(159); expect(pt('s3').register).toBe(161);
    expect(pt('thd_u1').register).toBe(181); expect(pt('thd_u1').type).toBe('u16'); expect(pt('thd_u1').scale).toBe(0.01); // T16
    expect(pt('thd_u3').register).toBe(183); expect(pt('thd_i1').register).toBe(187); expect(pt('thd_i3').register).toBe(189);
    // no PF and no energy counters: T7 sign bytes and per-counter exponent registers are not
    // representable as one fixed-scale number - excluded by design, named in the doc
    expect(t.points.some((p) => p.id.startsWith('pf'))).toBe(false);
    expect(t.points.some((p) => p.id.includes('kwh'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    // no installer gates on this one: provisional confirmation alone unlocks the YAML
    const base = { deviceId: 'mc750-1', templateId: 'iskra-mc7x0', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 115200, address: 33, intervalSec: 5, provisionalConfirmed: true };
    const y = deviceYaml(base);
    expect(y).toContain('register: 104'); expect(y).toContain('register: 139');
    expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
    expect(y).toContain('type: t6'); expect(y).toContain('type: t5');
  });
  it('iskra-mc3x0 registers match the official MC 3x0 manual Appendix A, MC7x0-compatible map (docs/devices/iskra-mc3x0.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'iskra-mc3x0')!;
    expect(t.func).toBe(4); // 3xxxx input registers; manual states memory reference 30000/40000
    expect(t.type).toBe('t5');
    expect(t.word_order).toBe('abcd'); // vendor worked examples print MSW first
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    expect(t.stopBits).toBe(2);
    expect(t.serial).toEqual({ baud: 115200, parity: 'none', address: 33 });
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('freq').register).toBe(104); // 30105
    expect(pt('v1').register).toBe(106); expect(pt('v3').register).toBe(110); expect(pt('v_avg_ln').register).toBe(112);
    expect(pt('v12').register).toBe(117); expect(pt('v31').register).toBe(121); expect(pt('v_avg_ll').register).toBe(123);
    expect(pt('i1').register).toBe(125); expect(pt('i3').register).toBe(129);
    expect(pt('i_neutral').register).toBe(131); expect(pt('i_total').register).toBe(137); // 30138
    expect(pt('p_total').register).toBe(139); expect(pt('p_total').type).toBe('t6');
    expect(pt('p1').register).toBe(141); expect(pt('p3').register).toBe(145);
    expect(pt('q1').register).toBe(149); expect(pt('q3').register).toBe(153); // per-phase Q are T6
    expect(pt('s1').register).toBe(157); expect(pt('s3').register).toBe(161); // per-phase S are T5
    // q_total/s_total excluded: the manual's type codes for the two totals conflict with the
    // family type definitions (T5/T7 where T6/T5 are expected) - excluded rather than guessed
    expect(t.points.some((p) => p.id === 'q_total')).toBe(false);
    expect(t.points.some((p) => p.id === 's_total')).toBe(false);
    // THD is T1 on this model (unsigned 16-bit, whole percent) - NOT the MC 7x0's T16 0.01
    expect(pt('thd_u1').register).toBe(181); expect(pt('thd_u1').type).toBe('u16'); expect(pt('thd_u1').scale).toBe(1);
    expect(pt('thd_i3').register).toBe(189);
    expect(t.points.some((p) => p.id.startsWith('pf'))).toBe(false);
    expect(t.points.some((p) => p.id.includes('kwh'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    const base = { deviceId: 'mc350-1', templateId: 'iskra-mc3x0', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 115200, address: 33, intervalSec: 5, provisionalConfirmed: true };
    const y = deviceYaml(base);
    expect(y).toContain('register: 104'); expect(y).toContain('register: 139');
    expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
  });
  it('entes-mpr53cs registers match the official MODBUS Register Map and manual A4396 R3 (docs/devices/entes-mpr53cs.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'entes-mpr53cs')!;
    expect(t.func).toBe(3); // manual's worked read example: 01 03 00 00 00 02 reads L1 voltage
    expect(t.type).toBe('u32'); // 2 registers per value, map FORMAT column
    expect(t.stopBits).toBe(2); // manual specifications: 8 data bits, 2 stop bits
    expect(t.serial).toEqual({ baud: 9600, parity: 'none', address: 1 }); // manual factory settings
    expect(t.wordOrderUnverified).toBe(true); // word order never stated in either document
    expect(t.pduBaseUnverified).toBeUndefined(); // example reads L1 voltage at on-wire 0x0000
    expect(t.funcUnverified).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1').register).toBe(0); expect(pt('v2').register).toBe(2); expect(pt('v3').register).toBe(4);
    expect(pt('i1').register).toBe(6); expect(pt('i3').register).toBe(10); expect(pt('i_neutral').register).toBe(12);
    expect(pt('i1').scale).toBe(0.001);
    expect(pt('v12').register).toBe(14); expect(pt('v23').register).toBe(16); expect(pt('v31').register).toBe(18);
    expect(pt('p1').register).toBe(20); expect(pt('p3').register).toBe(24); expect(pt('p1').type).toBe('i32');
    expect(pt('q1').register).toBe(26); expect(pt('q3').register).toBe(30);
    expect(pt('s1').register).toBe(32); expect(pt('s3').register).toBe(36);
    expect(pt('pf1').register).toBe(38); expect(pt('pf3').register).toBe(42); expect(pt('pf1').scale).toBe(0.001);
    expect(pt('p_total_import').register).toBe(44); expect(pt('p_total_export').register).toBe(46);
    expect(pt('q_total_ind').register).toBe(48); expect(pt('q_total_cap').register).toBe(50);
    expect(pt('s_total').register).toBe(52);
    expect(pt('freq').register).toBe(58); expect(pt('freq').scale).toBe(0.01);
    expect(pt('thd_u1').register).toBe(72); expect(pt('thd_u3').register).toBe(76);
    expect(pt('thd_i1').register).toBe(78); expect(pt('thd_i3').register).toBe(82);
    // energies excluded: the map prints a 64-bit range (0-FFFFFFFFFFFFFFFF) at 2-register spacing - contradictory
    expect(t.points.some((p) => p.id.includes('kwh') || p.id.includes('energy'))).toBe(false);
    // min/max/demand, angles, average cosj pair, I/O, counters, setup registers excluded
    expect(t.points.some((p) => p.id.includes('max') || p.id.includes('min') || p.id.includes('demand') || p.id.includes('angle'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    expect(t.points.length).toBe(34);
    const base = { deviceId: 'mpr53cs-1', templateId: 'entes-mpr53cs', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true, wordOrder: 'abcd' as const };
    const y = deviceYaml(base);
    expect(y).toContain('register: 58'); expect(y).toContain('func: 3');
  });
  it('cet-pmc53m registers match the official PMC-53 manual V5.2A chapter 5 (docs/devices/cet-pmc53m.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'cet-pmc53m')!;
    expect(t.func).toBe(3); // manual: Read Holding Registers (Function Code 0x03)
    expect(t.type).toBe('u32');
    expect(t.serial).toEqual({ baud: 9600, parity: 'even', address: 100 }); // setup table defaults: 3=9600*, 2=8E1*, Unit ID Default=100
    expect(t.wordOrderUnverified).toBe(true); // word order never stated for the 5.1 registers
    expect(t.pduBaseUnverified).toBeUndefined(); // listed = on-wire, manual says no subtraction
    expect(t.funcUnverified).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1').register).toBe(40000); expect(pt('v3').register).toBe(40004); expect(pt('v_avg_ln').register).toBe(40006);
    expect(pt('v12').register).toBe(40008); expect(pt('v31').register).toBe(40012); expect(pt('v_avg_ll').register).toBe(40014);
    expect(pt('v1').scale).toBe(0.01); // Table 5-1 note 2: raw 22003 = 220.03 V
    expect(pt('i1').register).toBe(40016); expect(pt('i3').register).toBe(40020); expect(pt('i_avg').register).toBe(40022);
    expect(pt('i_neutral').register).toBe(40053);
    expect(pt('p1').register).toBe(40024); expect(pt('p_total').register).toBe(40030); expect(pt('p1').type).toBe('i32');
    expect(pt('p1').unit).toBe('kW'); // manual scales in kW/kvar/kVA, not W
    expect(pt('q1').register).toBe(40032); expect(pt('q_total').register).toBe(40038);
    expect(pt('s1').register).toBe(40040); expect(pt('s_total').register).toBe(40046);
    expect(pt('pf1').register).toBe(40048); expect(pt('pf_total').register).toBe(40051); expect(pt('pf1').type).toBe('i16');
    expect(pt('freq').register).toBe(40052); expect(pt('freq').type).toBe('u16'); expect(pt('freq').scale).toBe(0.01);
    expect(pt('kwh_import').register).toBe(40100); expect(pt('kwh_export').register).toBe(40102);
    expect(pt('kvarh_import').register).toBe(40106); expect(pt('kvarh_export').register).toBe(40108);
    expect(pt('kwh_import').scale).toBe(0.1);
    // excluded: demands/peak logs, angles, residual/temperature, unbalance, dPF, bitmaps, harmonics, setup 41000+
    expect(t.points.some((p) => p.register >= 40110 && p.register !== 40112)).toBe(false);
    expect(t.points.some((p) => p.id.includes('demand') || p.id.includes('angle') || p.id.includes('thd'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    expect(t.points.length).toBe(34);
    const base = { deviceId: 'pmc53m-1', templateId: 'cet-pmc53m', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 100, intervalSec: 5, provisionalConfirmed: true, wordOrder: 'abcd' as const };
    const y = deviceYaml(base);
    expect(y).toContain('register: 40052'); expect(y).toContain('func: 3'); expect(y).toContain('word_order: abcd');
  });
  it('lovato-dmg600 registers match the official protocol I317IGB02_22 table 2 (docs/devices/lovato-dmg600.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'lovato-dmg600')!;
    expect(t.func).toBe(4); // protocol's worked read example; table 2 header allows FC 03 and 04
    expect(t.type).toBe('u32');
    expect(t.word_order).toBe('abcd'); // worked example response 00 01 FB 00 = 129792 = 1.29792 kW, MSW first
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined(); // protocol: query address = listed minus 1 (explicit + example)
    expect(t.funcUnverified).toBeUndefined();
    expect(t.serial).toEqual({ baud: 9600, parity: 'none', address: 1 }); // manual P07 menu defaults
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    // PDU = listed hex address minus 1 (0002H -> 1)
    expect(pt('v1').register).toBe(1); expect(pt('v2').register).toBe(3); expect(pt('v3').register).toBe(5);
    expect(pt('v1').scale).toBe(0.01);
    expect(pt('i1').register).toBe(7); expect(pt('i3').register).toBe(11); expect(pt('i1').scale).toBe(0.0001);
    expect(pt('v12').register).toBe(13); expect(pt('v23').register).toBe(15); expect(pt('v31').register).toBe(17);
    expect(pt('p1').register).toBe(19); expect(pt('p3').register).toBe(23); expect(pt('p1').type).toBe('i32');
    expect(pt('p2').register).toBe(21); // worked example: L2 active power at 0016H, query 0x15
    expect(pt('q1').register).toBe(25); expect(pt('q3').register).toBe(29);
    expect(pt('s1').register).toBe(31); expect(pt('s3').register).toBe(35);
    expect(pt('pf1').register).toBe(37); expect(pt('pf3').register).toBe(41); expect(pt('pf1').scale).toBe(0.0001);
    expect(pt('freq').register).toBe(49); expect(pt('freq').scale).toBe(0.001); // Hz/1000 (Hz/100 is DMG210-only)
    expect(pt('v_avg_ln').register).toBe(51); expect(pt('v_avg_ll').register).toBe(53); expect(pt('i_avg').register).toBe(55);
    expect(pt('p_total').register).toBe(57); expect(pt('q_total').register).toBe(59); expect(pt('s_total').register).toBe(61);
    expect(pt('pf_total').register).toBe(63);
    expect(pt('i_neutral').register).toBe(71);
    // energies excluded: 32-bit rows unmapped for DMG6xx, 64-bit rows exceed driver types
    expect(t.points.some((p) => p.id.includes('kwh') || p.id.includes('energy') || p.id.includes('kvarh'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    expect(t.points.length).toBe(30);
    const base = { deviceId: 'dmg610-1', templateId: 'lovato-dmg600', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    const y = deviceYaml(base);
    expect(y).toContain('register: 21'); expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
  });
  it('eig-shark-200 registers match the official map E149701 appendix B (docs/devices/eig-shark-200.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'eig-shark-200')!;
    expect(t.func).toBe(3); // appendix B notes: function codes 0x03, 0x06, 0x10 (reads use 0x03)
    expect(t.type).toBe('f32');
    expect(t.word_order).toBe('abcd'); // B.3: lower-addressed register is the high-order half (FLOAT/SINT32)
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined(); // B.6: 40000 + listed address, drivers subtract 1 -> PDU = listed minus 1
    expect(t.funcUnverified).toBeUndefined();
    expect(t.serial).toEqual({ baud: 57600, address: 1 }); // quickstart E149703 factory initial defaults (QS-5)
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    // PDU = listed decimal address minus 1 (Volts A-N listed 1000 -> 999)
    expect(pt('v1').register).toBe(999); expect(pt('v2').register).toBe(1001); expect(pt('v3').register).toBe(1003);
    expect(pt('v12').register).toBe(1005); expect(pt('v23').register).toBe(1007); expect(pt('v31').register).toBe(1009);
    expect(pt('i1').register).toBe(1011); expect(pt('i2').register).toBe(1013); expect(pt('i3').register).toBe(1015);
    expect(pt('p_total').register).toBe(1017); expect(pt('q_total').register).toBe(1019); expect(pt('s_total').register).toBe(1021);
    expect(pt('pf_total').register).toBe(1023);
    expect(pt('freq').register).toBe(1025); expect(pt('freq').max).toBe(65); // map range 0 to 65.00 Hz
    expect(pt('i_neutral').register).toBe(1027);
    expect(pt('p1').register).toBe(1029); expect(pt('p3').register).toBe(1033);
    expect(pt('q1').register).toBe(1035); expect(pt('q3').register).toBe(1039);
    expect(pt('s1').register).toBe(1041); expect(pt('s3').register).toBe(1045);
    expect(pt('pf1').register).toBe(1047); expect(pt('pf3').register).toBe(1051);
    expect(pt('i_unbalance').register).toBe(1064); expect(pt('i_unbalance').type).toBe('u16'); expect(pt('i_unbalance').scale).toBe(0.01);
    expect(pt('thd_v1').register).toBe(5999); expect(pt('thd_i3').register).toBe(6004); expect(pt('thd_v1').scale).toBe(0.01); // 0.01% resolution
    // energies excluded: SINT32 "per energy format" - implied decimal AND unit multiplier are programmable
    expect(t.points.some((p) => p.id.includes('kwh') || p.id.includes('energy') || p.id.includes('wh_'))).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    expect(t.points.length).toBe(34);
    const base = { deviceId: 'shark200-1', templateId: 'eig-shark-200', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 57600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    const y = deviceYaml(base);
    expect(y).toContain('register: 999'); expect(y).toContain('func: 3'); expect(y).toContain('word_order: abcd');
  });
  it('chint-dtsu666 registers match the official manual ZTW0.464.0104 table 11 (docs/devices/chint-dtsu666.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'chint-dtsu666')!;
    expect(t.func).toBe(3); // manual: "read command is 03H, write command is 10H"
    expect(t.type).toBe('f32');
    expect(t.word_order).toBe('abcd'); // table 11 Note 1: IEEE754 single, ABCD (high type front, low byte behind)
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBe(true); // manual never states listed vs listed-minus-1 on the wire
    expect(t.serial).toEqual({ baud: 9600, parity: 'none', address: 1 }); // manual factory default statement
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    // listed hex addresses stored verbatim; the installer picks the base (gate)
    expect(pt('v12').register).toBe(0x2000); expect(pt('v23').register).toBe(0x2002); expect(pt('v31').register).toBe(0x2004);
    expect(pt('v1').register).toBe(0x2006); expect(pt('v2').register).toBe(0x2008); expect(pt('v3').register).toBe(0x200A);
    expect(pt('v1').scale).toBe(0.1);
    expect(pt('i1').register).toBe(0x200C); expect(pt('i2').register).toBe(0x200E); expect(pt('i3').register).toBe(0x2010);
    expect(pt('i1').scale).toBe(0.001);
    expect(pt('p_total').register).toBe(0x2012); expect(pt('p1').register).toBe(0x2014); expect(pt('p3').register).toBe(0x2018);
    expect(pt('q_total').register).toBe(0x201A); expect(pt('q1').register).toBe(0x201C); expect(pt('q3').register).toBe(0x2020);
    expect(pt('pf_total').register).toBe(0x202A); expect(pt('pf3').register).toBe(0x2030); expect(pt('pf_total').scale).toBe(0.001);
    expect(pt('freq').register).toBe(0x2044); expect(pt('freq').scale).toBe(0.01);
    expect(pt('kwh_import').register).toBe(0x101E); expect(pt('kwh_import_c').register).toBe(0x1024);
    expect(pt('kwh_import_net').register).toBe(0x1026);
    expect(pt('kwh_export').register).toBe(0x1028); expect(pt('kwh_export_c').register).toBe(0x102E);
    expect(pt('kwh_export_net').register).toBe(0x1030); expect(pt('kwh_export').scale).toBe(1);
    // apparent power/energy absent from the vendor map - never synthesized
    expect(t.points.some((p) => p.id.startsWith('s_') || p.unit === 'VA' || p.unit === 'kVAh')).toBe(false);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    expect(t.points.length).toBe(32);
    const base = { deviceId: 'dtsu666-1', templateId: 'chint-dtsu666', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).some((e) => e.startsWith('Register base:'))).toBe(true); // gate blocks until base chosen
    const yListed = deviceYaml({ ...base, pduBase: 'listed' as const });
    expect(yListed).toContain('register: 8192'); expect(yListed).toContain('func: 3'); expect(yListed).toContain('word_order: abcd');
    const yMinus1 = deviceYaml({ ...base, pduBase: 'minus1' as const });
    expect(yMinus1).toContain('register: 8191'); // listed minus 1 on the wire
  });
  it('finder-7m38 registers match the official ModBUS COMMUNICATION PROTOCOL 7M24-7M38 7M.38 table (docs/devices/finder-7m38.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'finder-7m38')!;
    expect(t.func).toBe(4); // 3xxxx input registers; protocol states memory reference 30000/40000
    expect(t.type).toBe('t5'); // Finder T5 decade-exponent unsigned measurement (same encoding as Iskra t5)
    expect(t.word_order).toBe('abcd'); // vendor worked example: FD01 E240 = 123456 x 10^-3, MSW first
    expect(t.wordOrderUnverified).toBeUndefined(); // word order, base and FC are all vendor-stated
    expect(t.pduBaseUnverified).toBeUndefined(); // worked example: listed 30107 -> on-wire 00 6B = 107 (listed - 30000)
    expect(t.funcUnverified).toBeUndefined();
    expect(t.unitsUnverified).toBeUndefined(); // readings carry their own decade exponent; units are base units
    expect(t.stopBits).toBe(2); // vendor-stated factory default: frame 8,N,2
    expect(t.serial).toEqual({ baud: 19200, parity: 'none', address: 33 }); // vendor-stated factory defaults
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    // on-wire = listed address - 30000
    expect(pt('freq').register).toBe(105); // 30105
    expect(pt('v1').register).toBe(107); expect(pt('v2').register).toBe(109); expect(pt('v3').register).toBe(111);
    expect(pt('v_avg_ln').register).toBe(113);
    expect(pt('v12').register).toBe(118); expect(pt('v23').register).toBe(120); expect(pt('v31').register).toBe(122);
    expect(pt('v_avg_ll').register).toBe(124);
    expect(pt('i1').register).toBe(126); expect(pt('i2').register).toBe(128); expect(pt('i3').register).toBe(130);
    expect(pt('i_avg').register).toBe(136); expect(pt('si').register).toBe(138);
    expect(pt('p_total').register).toBe(140); expect(pt('p_total').type).toBe('t6'); // active/reactive powers are signed (T6)
    expect(pt('p1').register).toBe(142); expect(pt('p2').register).toBe(144); expect(pt('p3').register).toBe(146);
    expect(pt('q_total').register).toBe(148); expect(pt('q_total').type).toBe('t6');
    expect(pt('q1').register).toBe(150); expect(pt('q2').register).toBe(152); expect(pt('q3').register).toBe(154);
    expect(pt('s_total').register).toBe(156); expect(pt('s_total').type).toBeUndefined(); // T5 template default
    expect(pt('s1').register).toBe(158); expect(pt('s2').register).toBe(160); expect(pt('s3').register).toBe(162);
    expect(pt('thd_v1').register).toBe(182); expect(pt('thd_v1').type).toBe('u16'); expect(pt('thd_v1').scale).toBe(0.01); // T16
    expect(pt('thd_v3').register).toBe(184); expect(pt('thd_i1').register).toBe(188); expect(pt('thd_i3').register).toBe(190);
    // no PF and no energy counters: T7 sign bytes and user-parameterisable counter roles are not
    // representable as fixed-scale numbers - excluded by design, named in the doc
    expect(t.points.some((p) => p.id.startsWith('pf'))).toBe(false);
    expect(t.points.some((p) => p.id.includes('kwh') || p.unit === 'kWh' || p.unit === 'Wh')).toBe(false);
    expect(t.points.length).toBe(32);
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
    // no installer gates on this one: provisional confirmation alone unlocks the YAML
    const base = { deviceId: '7m38-1', templateId: 'finder-7m38', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 33, stopBits: 2 as const, intervalSec: 5, provisionalConfirmed: true }; // stopBits preset from the template's vendor-stated 8,N,2 default
    const y = deviceYaml(base);
    expect(y).toContain('register: 105'); expect(y).toContain('register: 140');
    expect(y).toContain('func: 4'); expect(y).toContain('word_order: abcd');
    expect(y).toContain('type: t6'); expect(y).toContain('type: t5');
    expect(y).toContain('stop_bits: 2');
  });
  it('socomec-diris-a10 registers match the official MODBUS tables of Diris A-10 CMT_EN 2025-02 (docs/devices/socomec-diris-a10.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'socomec-diris-a10')!;
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(t.type).toBe('u32');
    // Common metrology block, addresses listed verbatim
    expect(pt('v12').register).toBe(50514); expect(pt('v12').scale).toBe(0.01); // 0xC552, U32 V 10e-2
    expect(pt('v1').register).toBe(50520); expect(pt('v3').register).toBe(50524);
    expect(pt('freq').register).toBe(50526); expect(pt('freq').scale).toBe(0.01);
    expect(pt('i1').register).toBe(50528); expect(pt('i1').scale).toBe(0.001); // mA
    expect(pt('i_n').register).toBe(50534);
    expect(pt('p_total').register).toBe(50536); expect(pt('p_total').type).toBe('i32'); expect(pt('p_total').scale).toBe(10); // S32 W 10
    expect(pt('q_total').register).toBe(50538); expect(pt('s_total').register).toBe(50540); expect(pt('s_total').type).toBeUndefined(); // U32 default
    expect(pt('pf_total').register).toBe(50542); expect(pt('pf_total').type).toBe('i32'); expect(pt('pf_total').scale).toBe(0.001); // -leading/+lagging vendor-stated
    expect(pt('p1').register).toBe(50544); expect(pt('p3').register).toBe(50548);
    expect(pt('q1').register).toBe(50550); expect(pt('q3').register).toBe(50554);
    expect(pt('s1').register).toBe(50556); expect(pt('s3').register).toBe(50560);
    expect(pt('pf1').register).toBe(50562); expect(pt('pf3').register).toBe(50566);
    // partial (resettable) energy counters, vendor naming
    expect(pt('kwh_import_partial').register).toBe(50780); expect(pt('kwh_import_partial').scale).toBe(1); // Ea+ k Wh
    expect(pt('kvarh_import_partial').register).toBe(50782); expect(pt('kvarh_import_partial').unit).toBe('kvarh'); // "varh 10^3"
    expect(pt('kvah_partial').register).toBe(50784);
    expect(pt('kwh_export_partial').register).toBe(50786); expect(pt('kvarh_export_partial').register).toBe(50788);
    // THD block, U16 "% / 10"
    expect(pt('thd_u12').register).toBe(51536); expect(pt('thd_u12').type).toBe('u16'); expect(pt('thd_u12').scale).toBe(0.1);
    expect(pt('thd_v3').register).toBe(51541); expect(pt('thd_i1').register).toBe(51542); expect(pt('thd_i3').register).toBe(51544);
    expect(t.points.length).toBe(41);
    // word order, read FC and PDU base are NOT stated in the table or the quick-start guide: all three gated
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.funcUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBe(true);
    const missing = validateDevice({ deviceId: 'a10-1', templateId: 'socomec-diris-a10', link: 'rtu', port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true });
    expect(missing.length).toBeGreaterThan(0); // no YAML until the three gates are chosen
    const base = { deviceId: 'a10-1', templateId: 'socomec-diris-a10', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, wordOrder: 'abcd' as const, readFunc: 4 as const, pduBase: 'listed' as const, provisionalConfirmed: true };
    expect(validateDevice(base)).toEqual([]);
    const y = deviceYaml(base);
    expect(y).toContain('register: 50780'); // energy import verbatim (listed address)
    expect(y).toContain('type: i32');
    const minus1 = deviceYaml({ ...base, pduBase: 'minus1' as const });
    expect(minus1).toContain('register: 50779'); // 50780 -> listed minus 1
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4, t5: 2, t6: 2 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });
  it('schneider-pm8000 registers match the official PM8000 Modbus Map workbook (docs/devices/schneider-pm8000.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'schneider-pm8000')!;
    expect(t.func).toBe(3); // FC 03 server-supported per user manual 7EN02-0336; FC 04 is client-only
    expect(t.type).toBe('f32');
    expect(t.word_order).toBe('abcd'); // manual: 32-bit data transmitted most-significant word first
    expect(t.wordOrderUnverified).toBeUndefined(); // all three conventions vendor-stated
    expect(t.pduBaseUnverified).toBeUndefined();
    expect(t.funcUnverified).toBeUndefined();
    const reg = (id: string) => t.points.find((p) => p.id === id)?.register;
    expect(reg('i1')).toBe(3000); expect(reg('i2')).toBe(3002); expect(reg('i3')).toBe(3004);
    expect(reg('i_avg')).toBe(3010); // Current Avg
    expect(reg('v12')).toBe(3020); expect(reg('v23')).toBe(3022); expect(reg('v31')).toBe(3024);
    expect(reg('v_avg_ll')).toBe(3026); // Voltage L-L Avg
    expect(reg('v1n')).toBe(3028); expect(reg('v2n')).toBe(3030); expect(reg('v3n')).toBe(3032);
    expect(reg('v_avg_ln')).toBe(3036); // Voltage L-N Avg
    expect(reg('p1')).toBe(3054); expect(reg('p2')).toBe(3056); expect(reg('p3')).toBe(3058);
    expect(reg('p_total')).toBe(3060); // Active Power Total
    expect(reg('q_total')).toBe(3068); // Reactive Power Total
    expect(reg('s_total')).toBe(3076); // Apparent Power Total
    expect(reg('pf_total')).toBe(3150); // Power Factor Total
    expect(reg('freq')).toBe(3110); // Frequency
    expect(reg('kwh_import')).toBe(2700); // kWh del
    expect(reg('kwh_export')).toBe(2702); // kWh rec
    // power is in W/VAR/VA on this meter, not kW like PM53xx
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('p_total').unit).toBe('W');
    expect(pt('q_total').unit).toBe('var');
    expect(pt('s_total').unit).toBe('VA');
    expect(pt('pf_total').min).toBe(-2); expect(pt('pf_total').max).toBe(2); // sign convention unstated: wide range admits both encodings
    const used = new Set<number>();
    for (const p of t.points) { expect(used.has(p.register) || used.has(p.register + 1)).toBe(false); used.add(p.register); used.add(p.register + 1); }
    const y = deviceYaml({ deviceId: 'pm8-1', templateId: 'schneider-pm8000', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, address: 1, intervalSec: 5, provisionalConfirmed: true });
    expect(y).toContain('register: 3110'); expect(y).toContain('register: 2700');
    expect(y).toContain('func: 3'); expect(y).toContain('word_order: abcd');
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
    // energy counters, Energy AC block (verified 2026-10-21 against the full table)
    expect(pt('kwh_import').register).toBe(19843); // 0x4D83, Total Ea+, U32 kWh
    expect(pt('kwh_import').scale).toBe(1);
    expect(pt('kwh_export').register).toBe(19846); // 0x4D86, Total Ea-
    expect(pt('kvarh_import').register).toBe(19849); // 0x4D89, Total Er+, printed "varh 10e3" = kvarh
    expect(pt('kvarh_import').unit).toBe('kvarh');
    expect(pt('kvah_total').register).toBe(19855); // 0x4D8F, Total Eap, kVAh
    // word order, read FC and PDU base are NOT stated in the table or the operating manual: all three gated
    expect(t.wordOrderUnverified).toBe(true);
    expect(t.funcUnverified).toBe(true);
    expect(t.pduBaseUnverified).toBe(true);
    const missing = validateDevice({ deviceId: 'a40-1', templateId: 'socomec-diris-a40', link: 'rtu', port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, provisionalConfirmed: true });
    expect(missing.length).toBeGreaterThan(0); // no YAML until the three gates are chosen
    const base = { deviceId: 'a40-1', templateId: 'socomec-diris-a40', link: 'rtu' as const, port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5, wordOrder: 'abcd' as const, readFunc: 4 as const, pduBase: 'listed' as const, provisionalConfirmed: true };
    expect(validateDevice(base)).toEqual([]);
    expect(deviceYaml(base)).toContain('register: 19843'); // energy import verbatim (listed address)
    const minus1 = deviceYaml({ ...base, pduBase: 'minus1' as const });
    expect(minus1).toContain('register: 19842'); // 19843 -> 0x4D82
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
  });

  it('siemens-pac2200 offsets match the official manual L1V30415167B-06 table A-3 (docs/devices/siemens-pac2200.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'siemens-pac2200')!;
    expect(t.func).toBe(4); // FC 03 and 04 both vendor-stated for all measured variables; family choice is FC 04
    expect(t.type).toBe('f32');
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('v1n').register).toBe(1); // Voltage U_L1-N
    expect(pt('v31').register).toBe(11); // Voltage U_L3-L1
    expect(pt('i1').register).toBe(13); // Current L1
    expect(pt('s1').register).toBe(19); // Apparent power L1
    expect(pt('p1').register).toBe(25); // Active power L1
    expect(pt('q3').register).toBe(35); // Reactive power L3
    expect(pt('pf1').register).toBe(37); // PF L1, documented range 0...1
    expect(pt('pf1').min).toBe(0); expect(pt('pf1').max).toBe(1);
    expect(pt('freq').register).toBe(55); // Frequency, documented range 45...65
    expect(pt('freq').min).toBe(45); expect(pt('freq').max).toBe(65);
    expect(pt('p_total').register).toBe(65); // Total active power
    expect(pt('q_total').register).toBe(67); // Total reactive power
    expect(pt('i_n').register).toBe(71); // Neutral current
    expect(pt('energy_import').register).toBe(801); // Active energy import tariff 1, Double
    expect(pt('energy_import').type).toBe('f64'); // Double = 4 registers
    expect(pt('energy_import').unit).toBe('Wh'); // as printed
    expect(pt('energy_import').max).toBe(1e12); // documented overflow 1.0e+12
    expect(pt('energy_export').register).toBe(809);
    expect(pt('varh_import').register).toBe(817);
    expect(t.points.find((p) => p.id === 'pf_total')).toBeUndefined(); // offset 69 excluded: manual leaves its range unstated
    // word order is NOT documented anywhere in the manual: no YAML without an explicit choice
    expect(t.wordOrderUnverified).toBe(true);
    const base = { deviceId: 'pac22-1', templateId: 'siemens-pac2200', link: 'tcp' as const, host: '192.168.1.10', tcpPort: 502, address: 1, intervalSec: 5, provisionalConfirmed: true };
    expect(validateDevice(base).join(' ')).toContain('Word order');
    const yaml = deviceYaml({ ...base, wordOrder: 'abcd' });
    expect(yaml).toContain('register: 55'); // frequency verbatim (listed offset = on-wire start address)
    expect(yaml).toContain('register: 801'); // Double energy verbatim
    expect(yaml).toContain('type: f64');
    expect(yaml).toContain('word_order: abcd');
    expect(yaml).toContain('func: 4');
    const widths: Record<string, number> = { u16: 1, i16: 1, u32: 2, i32: 2, f32: 2, f64: 4 };
    const used = new Set<number>();
    for (const p of t.points) { const ty = p.type ?? t.type; const w = widths[ty] ?? 2; for (let r = p.register; r < p.register + w; r++) { expect(used.has(r)).toBe(false); used.add(r); } }
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
  it('acrel-apm800 registers match the APM series manual V2.4 (docs/devices/acrel-apm800.md)', () => {
    const t = EDGE_TEMPLATES.find((x) => x.id === 'acrel-apm800')!;
    expect(t.link).toBe('rtu');
    expect(t.func).toBe(3); // the only FC the second RS485/TCP paths support
    expect(t.type).toBe('i32');
    expect(t.word_order).toBe('abcd'); // "high bits are in the front"; CN 8.3.4 IEEE-754 example 0x474B 0xAC00 = 52140 Wh
    expect(t.wordOrderUnverified).toBeUndefined();
    expect(t.pduBaseUnverified).toBeUndefined(); // 0-based table (register 0 = meter address); examples read at table numbers
    expect(t.funcUnverified).toBeUndefined();
    expect(t.provisional).toBeUndefined();
    const pt = (id: string) => t.points.find((p) => p.id === id)!;
    expect(pt('ia').register).toBe(1100);
    expect(pt('ia').scale).toBe(0.001);
    expect(pt('va_n').register).toBe(1120);
    expect(pt('va_n').scale).toBe(0.1);
    expect(pt('vc_a').register).toBe(1132);
    // primary power floats carry the scaled value: f32 with scale 0.01 (110000.0 -> 1100 W)
    expect(pt('p_total').register).toBe(1156);
    expect(pt('p_total').type).toBe('f32');
    expect(pt('p_total').scale).toBe(0.01);
    expect(pt('s_total').register).toBe(1172);
    expect(pt('freq').register).toBe(1179);
    expect(pt('freq').type).toBe('i16');
    expect(pt('pf_total').register).toBe(1183);
    expect(pt('pf_total').scale).toBe(0.001);
    // energies come from the table rows 3000-3007 (the 3050+ tariff block stays excluded)
    expect(pt('energy_imp').register).toBe(3000);
    expect(pt('energy_imp').type).toBe('f32');
    expect(pt('varh_capacitive').register).toBe(3006);
    expect(t.points.some((p) => p.register >= 3050)).toBe(false);
    // voltage/current are Int32 (fall back to template type)
    expect(pt('va_n').type).toBeUndefined();
    // no duplicate or overlapping 2-register spans (i16 points occupy 1 register - check pairs only for 32-bit points)
    const used = new Set<number>();
    for (const p of t.points) {
      const two = (p.type ?? t.type) !== 'i16';
      expect(used.has(p.register)).toBe(false);
      if (two) expect(used.has(p.register + 1)).toBe(false);
      used.add(p.register); if (two) used.add(p.register + 1);
    }
    const y = deviceYaml({ deviceId: 'apm-1', templateId: 'acrel-apm800', link: 'rtu', port: '/dev/ttyUSB0', baud: 9600, parity: 'none', address: 1, intervalSec: 5 });
    expect(y).toContain('register: 1120'); // table numbers land unmodified
    expect(y).toContain('func: 3'); expect(y).toContain('type: i32'); expect(y).toContain('type: f32'); expect(y).toContain('scale: 0.01');
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
    // the full provisional text renders once in the safety block, never repeated as an error line
    for (const e of errs) expect(e.length).toBeLessThan(200);
    // shared gate messages carry no meter-specific register examples (the old pduBase hint named another vendor's register 3110)
    const kErrs = validateDevice({ deviceId: 'x-1', templateId: 'kamstrup-multical-x03', link: 'rtu', port: '/dev/ttyUSB0', baud: 19200, address: 1, intervalSec: 5 });
    for (const e of kErrs) expect(e).not.toContain('3110');

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
