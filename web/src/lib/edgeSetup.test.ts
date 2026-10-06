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
  it('every template point has a validation range', () => {
    for (const t of EDGE_TEMPLATES) for (const p of t.points) expect(p.max).toBeGreaterThan(p.min);
  });
});
