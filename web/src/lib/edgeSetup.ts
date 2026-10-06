// Edge setup helpers: built-in templates and the device block of edge-agent.yaml.
// Template numbers come from docs/devices/*.md (never run on real hardware).

export interface TplPoint { id: string; register: number; unit: string; min: number; max: number; }
export interface EdgeTemplate { id: string; name: string; link: 'rtu' | 'tcp'; func: number; type: string; word_order: string; points: TplPoint[]; note: string; }

const p = (id: string, register: number, unit: string, min: number, max: number): TplPoint => ({ id, register, unit, min, max });

export const EDGE_TEMPLATES: EdgeTemplate[] = [
  {
    id: 'selec-mx300', name: 'Selec MX300-1-C-CE (single-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer leaflet. Factory serial 9600 8N1, address 1. Power units unverified. Never run on a real meter.',
    points: [p('voltage', 0, 'V', 0, 600000), p('current', 2, 'A', 0, 10000), p('active_power', 4, 'kW', -1e6, 1e6), p('reactive_power', 6, 'kvar', -1e6, 1e6), p('apparent_power', 8, 'kVA', 0, 1e6), p('power_factor', 10, '', -1, 1), p('frequency', 12, 'Hz', 0, 65)],
  },
  {
    id: 'selec-mfm383a', name: 'Selec MFM383A-C (three-phase meter)', link: 'rtu', func: 4, type: 'f32', word_order: 'abcd',
    note: 'From the manufacturer manual. Never run on a real meter.',
    points: [p('v1n', 0, 'V', 0, 100000), p('v2n', 2, 'V', 0, 100000), p('v3n', 4, 'V', 0, 100000), p('i1', 16, 'A', 0, 100000), p('i2', 18, 'A', 0, 100000), p('i3', 20, 'A', 0, 100000), p('kw_total', 42, 'kW', -1e7, 1e7), p('pf_avg', 54, '', -1, 1), p('freq', 56, 'Hz', 0, 65), p('kwh', 58, 'kWh', 0, 1e12)],
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
    L.push(`      - {id: ${x.id}, register: ${x.register}, func: ${t.func}, type: ${t.type}, word_order: ${t.word_order}, scale: 1, unit: "${x.unit}", min: ${x.min}, max: ${x.max}}`);
  }
  return L.join('\n') + '\n';
}
