// Edge setup helpers: built-in templates and the device block of edge-agent.yaml.
// Template numbers come from docs/devices/*.md (never run on real hardware).

export interface TplPoint { id: string; register: number; unit: string; min: number; max: number; scale?: number; type?: string; }
export interface EdgeTemplate { id: string; name: string; link: 'rtu' | 'tcp'; func: number; type: string; word_order: string; points: TplPoint[]; note: string; }

const p = (id: string, register: number, unit: string, min: number, max: number): TplPoint => ({ id, register, unit, min, max });
// ps adds an explicit scale and/or type for scaled-integer maps (e.g. Socomec U32 in V 10e-2);
// omitted fields fall back to scale 1 and the template type, as before.
const ps = (id: string, register: number, unit: string, min: number, max: number, scale: number, type?: string): TplPoint => ({ id, register, unit, min, max, scale, type });

export const EDGE_TEMPLATES: EdgeTemplate[] = [
  {
    id: 'selec-mx300', name: 'Selec MX300-1-C-CE (single-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer leaflet. Factory serial 9600 8N1, address 1. Power units unverified. Never run on a real meter.',
    points: [p('voltage', 0, 'V', 0, 600000), p('current', 2, 'A', 0, 10000), p('active_power', 4, 'kW', -1e6, 1e6), ps('reactive_power', 6, 'kvar', -1e6, 1e6), ps('apparent_power', 8, 'kVA', 0, 1e6), p('power_factor', 10, '', -1, 1), p('frequency', 12, 'Hz', 0, 65)],
  },
  {
    id: 'selec-mfm383a', name: 'Selec MFM383A-C (three-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer manual. Never run on a real meter.',
    points: [p('v1n', 0, 'V', 0, 100000), p('v2n', 2, 'V', 0, 100000), p('v3n', 4, 'V', 0, 100000), p('i1', 16, 'A', 0, 100000), p('i2', 18, 'A', 0, 100000), p('i3', 20, 'A', 0, 100000), p('kw_total', 42, 'kW', -1e7, 1e7), p('pf_avg', 54, '', -1, 1), p('freq', 56, 'Hz', 0, 65), p('kwh', 58, 'kWh', 0, 1e12)],
  },
  {
    id: 'eastron-sdm630', name: 'Eastron SDM630 Modbus (three-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the official SDM630Modbus Protocol V1.8 PDF (docs/devices/eastron-sdm630.md). Factory serial 9600 8N1, address 1. Units as printed (W/VA/VAr). PF sign = current direction. Read-only; never run on a real meter.',
    points: [
      p('v1n', 0, 'V', 0, 1000), p('v2n', 2, 'V', 0, 1000), p('v3n', 4, 'V', 0, 1000),
      p('i1', 6, 'A', 0, 10000), p('i2', 8, 'A', 0, 10000), p('i3', 10, 'A', 0, 10000),
      p('w1', 12, 'W', -1e8, 1e8), p('w2', 14, 'W', -1e8, 1e8), p('w3', 16, 'W', -1e8, 1e8),
      p('va1', 18, 'VA', 0, 1e8), p('va2', 20, 'VA', 0, 1e8), p('va3', 22, 'VA', 0, 1e8),
      p('var1', 24, 'VAr', -1e8, 1e8), p('var2', 26, 'VAr', -1e8, 1e8), p('var3', 28, 'VAr', -1e8, 1e8),
      p('pf1', 30, '', -1, 1), p('pf2', 32, '', -1, 1), p('pf3', 34, '', -1, 1),
      p('v_avg_ln', 42, 'V', 0, 1000), p('i_avg', 46, 'A', 0, 10000), p('i_sum', 48, 'A', 0, 30000),
      p('w_total', 52, 'W', -1e8, 1e8), p('va_total', 56, 'VA', 0, 1e8), p('var_total', 60, 'VAr', -1e8, 1e8),
      p('pf_total', 62, '', -1, 1), p('freq', 70, 'Hz', 0, 70),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12),
      p('kvarh_import', 76, 'kVArh', 0, 1e12), p('kvarh_export', 78, 'kVArh', 0, 1e12),
      p('kvah_total', 80, 'kVAh', 0, 1e12), p('ah_total', 82, 'Ah', 0, 1e12), p('w_demand', 84, 'W', -1e8, 1e8),
      p('v12', 200, 'V', 0, 1000), p('v23', 202, 'V', 0, 1000), p('v31', 204, 'V', 0, 1000), p('v_avg_ll', 206, 'V', 0, 1000),
      p('i_neutral', 224, 'A', 0, 10000),
      p('thd_v1n', 234, '%', 0, 100), p('thd_v2n', 236, '%', 0, 100), p('thd_v3n', 238, '%', 0, 100), p('thd_i1', 240, '%', 0, 100),
    ],
  },
  {
    id: 'eastron-sdm120', name: 'Eastron SDM120 Modbus (single-phase DIN-rail meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the official SDM120Modbus Protocol V2.4 PDF (docs/devices/eastron-sdm120.md). Factory serial 2400 8N1 (NOT 9600 like the SDM630), address 1, no broadcast. Units as printed. Read-only; never run on a real meter.',
    points: [
      p('voltage', 0, 'V', 0, 1000), p('current', 6, 'A', 0, 1000),
      p('active_power', 12, 'W', -1e7, 1e7), ps('apparent_power', 18, 'VA', 0, 1e7), ps('reactive_power', 24, 'VAr', -1e7, 1e7),
      p('power_factor', 30, '', -1, 1), p('frequency', 70, 'Hz', 0, 70),
      p('kwh_import', 72, 'kWh', 0, 1e12), p('kwh_export', 74, 'kWh', 0, 1e12),
      p('kvarh_import', 76, 'kvarh', 0, 1e12), p('kvarh_export', 78, 'kvarh', 0, 1e12),
      p('w_demand', 84, 'W', 0, 1e7), p('max_w_demand', 86, 'W', 0, 1e7),
      p('i_demand', 258, 'A', 0, 1000), p('max_i_demand', 264, 'A', 0, 1000),
      p('kwh_total', 342, 'kWh', 0, 1e12), p('kvarh_total', 344, 'kvarh', 0, 1e12),
    ],
  },
  {
    id: 'socomec-diris-a40', name: 'Socomec DIRIS A-40, load 1 (three-phase panel meter)', link: 'rtu', func: 4, type: 'u32', word_order: 'abcd',
    note: 'From the official DIRIS A-40 Modbus communication table CMT-1.7.4B (docs/devices/socomec-diris-a40.md). Reads load-1 instantaneous values only; scaled integers (V 10e-2, mA, mHz, PF /1000) carry their scale. Energy counters and loads 2-3 not in this template yet. Read-only; never run on a real meter.',
    points: [
      ps('v_sys_ln', 18436, 'V', 0, 1000, 0.01), ps('v_sys_ll', 18438, 'V', 0, 1000, 0.01),
      ps('i_sys', 18440, 'A', 0, 100000, 0.001), ps('freq', 18442, 'Hz', 0, 70, 0.001),
      ps('v1n', 18444, 'V', 0, 1000, 0.01), ps('v2n', 18446, 'V', 0, 1000, 0.01), ps('v3n', 18448, 'V', 0, 1000, 0.01),
      ps('v12', 18452, 'V', 0, 1000, 0.01), ps('v23', 18454, 'V', 0, 1000, 0.01), ps('v31', 18456, 'V', 0, 1000, 0.01),
      ps('i1', 18458, 'A', 0, 100000, 0.001), ps('i2', 18460, 'A', 0, 100000, 0.001), ps('i3', 18462, 'A', 0, 100000, 0.001), ps('i_n', 18464, 'A', 0, 100000, 0.001),
      ps('s_nom', 18474, 'VA', 0, 1e9, 1),
      ps('p_total', 18476, 'W', -1e9, 1e9, 1, 'i32'), ps('q_total', 18478, 'var', -1e9, 1e9, 1, 'i32'), ps('s_total', 18484, 'VA', 0, 1e9, 1),
      ps('pf_total', 18486, '', -1, 1, 0.001, 'i16'),
      ps('p1', 18488, 'W', -1e9, 1e9, 1, 'i32'), ps('p2', 18490, 'W', -1e9, 1e9, 1, 'i32'), ps('p3', 18492, 'W', -1e9, 1e9, 1, 'i32'),
      ps('q1', 18494, 'var', -1e9, 1e9, 1, 'i32'), ps('q2', 18496, 'var', -1e9, 1e9, 1, 'i32'), ps('q3', 18498, 'var', -1e9, 1e9, 1, 'i32'),
      ps('s1', 18512, 'VA', 0, 1e9, 1), ps('s2', 18514, 'VA', 0, 1e9, 1), ps('s3', 18516, 'VA', 0, 1e9, 1),
      ps('pf1', 18518, '', -1, 1, 0.001, 'i16'), ps('pf2', 18519, '', -1, 1, 0.001, 'i16'), ps('pf3', 18520, '', -1, 1, 0.001, 'i16'),
    ],
  },
  {
    id: 'siemens-pac3200', name: 'Siemens SENTRON PAC3200 (three-phase power monitoring, Modbus TCP)', link: 'tcp', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the official SENTRON PAC3200 manual A5E01168664B-04, table 3-6 (docs/devices/siemens-pac3200.md). Register numbers are the manual\'s 1-based offsets. Instantaneous values are Float; energy counters are Double (f64, 4 registers) - word order for doubles is not stated in the manual, standard big-endian assumed and flagged in the doc. Read-only: the writable counter resets and 50xxx config registers are excluded. Min/max history offsets 75-211 not in this template. Never run on a real meter.',
    points: [
      p('va_n', 1, 'V', 0, 1000), p('vb_n', 3, 'V', 0, 1000), p('vc_n', 5, 'V', 0, 1000),
      p('va_b', 7, 'V', 0, 1000), p('vb_c', 9, 'V', 0, 1000), p('vc_a', 11, 'V', 0, 1000),
      p('ia', 13, 'A', 0, 100000), p('ib', 15, 'A', 0, 100000), p('ic', 17, 'A', 0, 100000),
      p('sa', 19, 'VA', 0, 1e9), p('sb', 21, 'VA', 0, 1e9), p('sc', 23, 'VA', 0, 1e9),
      p('pa', 25, 'W', -1e9, 1e9), p('pb', 27, 'W', -1e9, 1e9), p('pc', 29, 'W', -1e9, 1e9),
      p('qa', 31, 'var', -1e9, 1e9), p('qb', 33, 'var', -1e9, 1e9), p('qc', 35, 'var', -1e9, 1e9),
      p('pfa', 37, '', 0, 1), p('pfb', 39, '', 0, 1), p('pfc', 41, '', 0, 1),
      p('thd_va', 43, '%', 0, 100), p('thd_vb', 45, '%', 0, 100), p('thd_vc', 47, '%', 0, 100),
      p('thd_ia', 49, '%', 0, 100), p('thd_ib', 51, '%', 0, 100), p('thd_ic', 53, '%', 0, 100),
      p('freq', 55, 'Hz', 45, 65),
      p('avg_vpn', 57, 'V', 0, 1000), p('avg_vpp', 59, 'V', 0, 1000), p('avg_i', 61, 'A', 0, 100000),
      p('s_total', 63, 'VA', 0, 1e9), p('p_total', 65, 'W', -1e9, 1e9), p('q_total', 67, 'var', -1e9, 1e9), p('pf_total', 69, '', 0, 1),
      p('unbal_v', 71, '%', 0, 100), p('unbal_i', 73, '%', 0, 200),
      ps('work_hours', 213, 's', 0, 999999999, 1, 'u32'), ps('universal_counter', 215, '', 0, 999999999, 1, 'u32'),
      ps('energy_import_t1', 801, 'Wh', 0, 1e12, 1, 'f64'), ps('energy_import_t2', 805, 'Wh', 0, 1e12, 1, 'f64'),
      ps('energy_export_t1', 809, 'Wh', 0, 1e12, 1, 'f64'), ps('energy_export_t2', 813, 'Wh', 0, 1e12, 1, 'f64'),
      ps('reactive_import_t1', 817, 'varh', 0, 1e12, 1, 'f64'), ps('reactive_import_t2', 821, 'varh', 0, 1e12, 1, 'f64'),
      ps('reactive_export_t1', 825, 'varh', 0, 1e12, 1, 'f64'), ps('reactive_export_t2', 829, 'varh', 0, 1e12, 1, 'f64'),
      ps('apparent_t1', 833, 'VAh', 0, 1e12, 1, 'f64'), ps('apparent_t2', 837, 'VAh', 0, 1e12, 1, 'f64'),
    ],
  },
];


export interface DeviceInput {
  deviceId: string; templateId: string; link: 'rtu' | 'tcp';
  port?: string; baud?: number; parity?: 'none' | 'even' | 'odd'; host?: string; tcpPort?: number; address: number; intervalSec: number;
}

export function validateDevice(d: DeviceInput): string[] {
  const e: string[] = [];
  if (!/^[a-z0-9][a-z0-9_-]{0,39}$/.test(d.deviceId)) e.push('Device id: lowercase letters, digits, - and _ only (max 40).');
  if (!EDGE_TEMPLATES.some((t) => t.id === d.templateId)) e.push('Pick a template.');
  if (!Number.isInteger(d.address) || d.address < 1 || d.address > 247) e.push('Modbus address must be 1-247.');
  if (!(d.intervalSec >= 1 && d.intervalSec <= 3600)) e.push('Read interval must be 1-3600 seconds.');
  if (d.link === 'rtu') {
    if (!d.port || !/^(COM\d{1,3}|\/dev\/[A-Za-z0-9._\/-]+)$/.test(d.port)) e.push('Serial port looks wrong (COM3 or /dev/ttyUSB0).');
    if (![1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200].includes(d.baud ?? 0)) e.push('Pick a standard baud rate.');
  } else {
    if (!d.host || !/^[A-Za-z0-9.-]+$/.test(d.host)) e.push('Host or IP looks wrong.');
    if (!d.tcpPort || d.tcpPort < 1 || d.tcpPort > 65535) e.push('TCP port must be 1-65535.');
  }
  return e;
}

/** The `devices:` block for edge-agent.yaml. Throws on invalid input so a bad file is never offered. */
export function deviceYaml(d: DeviceInput): string {
  const errs = validateDevice(d);
  if (errs.length) throw new Error(errs.join(' '));
  const t = EDGE_TEMPLATES.find((x) => x.id === d.templateId)!;
  const L: string[] = ['devices:', `  - id: ${d.deviceId}`];
  if (d.link === 'rtu') {
    L.push('    profile: modbus-generic', `    port: ${d.port}`, `    baud: ${d.baud}`, '    data_bits: 8', '    stop_bits: 0', `    parity: ${d.parity ?? 'none'}`);
  } else {
    L.push('    profile: modbus-tcp', `    host: ${d.host}`);
  }
  L.push(`    address: ${d.address}`, `    interval: ${d.intervalSec}s`, '    points:');
  for (const x of t.points) {
    L.push(`      - {id: ${x.id}, register: ${x.register}, func: ${t.func}, type: ${x.type ?? t.type}, word_order: ${t.word_order}, scale: ${x.scale ?? 1}, unit: "${x.unit}", min: ${x.min}, max: ${x.max}}`);
  }
  return L.join('\n') + '\n';
}
