import { describe, expect, it, vi, beforeEach } from 'vitest';
vi.mock('../lib/api', () => ({ api: vi.fn() }));
import { api } from '../lib/api';
const apiMock = api as unknown as ReturnType<typeof vi.fn>;
import { WIZARDS } from './ChatWizards';

const byId = (id: string) => {
  const w = WIZARDS.find(x => x.id === id);
  if (!w) throw new Error('missing wizard ' + id);
  return w;
};

describe('guided chat templates', () => {
  it('covers the requested template set', () => {
    for (const id of ['add-device', 'alert-rule', 'add-channel', 'energy-meter', 'ack-alert', 'invite-user', 'meter-watch']) byId(id);
  });
  it('energy meter guidance names the model and honesty labels without any API call', async () => {
    const w = byId('energy-meter');
    const msg = await w.run({ template: 'eastron-sdm630' });
    expect(msg).toContain('SDM630');
    expect(msg.toLowerCase()).toContain('not run on real hardware');
    const prov = await w.run({ template: 'siemens-pac3200' });
    expect(prov).toContain('PROVISIONAL');
  });
  it('telegram channels route to pairing instead of writing', async () => {
    const w = byId('add-channel');
    const msg = await w.run({ type: 'telegram', target: '-1001234567890' });
    expect(msg).toContain('pairing');
  });
  it('channel and invite summaries name destination and consequence', () => {
    const w = byId('add-channel');
    const cs = w.summary({ type: 'email', target: 'a@b.c' });
    expect(cs).toContain('a@b.c');
    expect(cs).toContain('Nothing is delivered');
    const inv = byId('invite-user');
    const is = inv.summary({ email: 'a@b.c', role: 'viewer' });
    expect(is).toContain('a@b.c');
    expect(is).toContain('invitation email');
  });
  it('validates numbers and emails', () => {
    const rule = byId('alert-rule');
    const thr = rule.steps.find(s => s.key === 'threshold');
    expect(thr?.validate?.('abc')).toBeTruthy();
    expect(thr?.validate?.('42')).toBeNull();
    const inv = byId('invite-user');
    const em = inv.steps.find(s => s.key === 'email');
    expect(em?.validate?.('nope')).toBeTruthy();
    expect(em?.validate?.('a@b.co')).toBeNull();
  });
});


describe('meter-watch pack', () => {
  beforeEach(() => apiMock.mockReset());
  const answers = { device_id: 'd1', device_id_label: 'Meter 1', voltage_point: 'va_n', voltage_point_label: 'va_n', v_high: '253', v_low: '207', freq_watch: 'no', i_limit: '0', severity: 'warning' };
  it('summary lists the voltage band and skip behavior', () => {
    const w = byId('meter-watch');
    const s = w.summary(answers);
    expect(s).toContain('2 warning rules');
    expect(s).toContain('va_n > 253 V');
    expect(s).toContain('va_n < 207 V');
    expect(s).not.toContain('frequency');
    expect(s).not.toContain('current >');
    const s2 = w.summary({ ...answers, freq_watch: 'yes', i_limit: '80' });
    expect(s2).toContain('4 warning rules');
    expect(s2).toContain('49.5-50.5 Hz');
    expect(s2).toContain('current > 80 A');
  });
  it('EN 50160 defaults are named in the prompts, not hidden', () => {
    const w = byId('meter-watch');
    expect(w.steps.find(s => s.key === 'v_high')?.prompt).toContain('253');
    expect(w.steps.find(s => s.key === 'v_low')?.prompt).toContain('207');
    expect(w.steps.find(s => s.key === 'freq_watch')?.prompt).toContain('49.5-50.5');
  });
  it('i_limit accepts 0 (skip) or a plain number, rejects text', () => {
    const v = byId('meter-watch').steps.find(s => s.key === 'i_limit')?.validate!;
    expect(v('0')).toBeNull();
    expect(v('63')).toBeNull();
    expect(v('many')).toBeTruthy();
  });
  it('run creates two voltage rules with per-rule idempotency keys when extras are skipped', async () => {
    apiMock.mockResolvedValue([]);
    const w = byId('meter-watch');
    const msg = await w.run(answers, { idempotencyKey: 'run-1' });
    expect(msg).toContain('voltage > 253 V');
    const posts = apiMock.mock.calls.filter(c => c[0] === '/v1/rules');
    expect(posts.length).toBe(2);
    const keys = posts.map(c => c[1].headers['Idempotency-Key']);
    expect(keys).toEqual(['run-1-vhigh', 'run-1-vlow']);
    const body = JSON.parse(posts[0][1].body);
    expect(body.enabled).toBe(true);
    expect(body.definition).toMatchObject({ device_id: 'd1', point_id: 'va_n', op: '>', threshold: 253, severity: 'warning' });
  });
  it('run adds frequency rules only when a freq point reports', async () => {
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'freq' }] : []);
    const w = byId('meter-watch');
    const msg = await w.run({ ...answers, freq_watch: 'yes' }, { idempotencyKey: 'run-2' });
    expect(msg).toContain('49.5-50.5');
    expect(apiMock.mock.calls.filter(c => c[0] === '/v1/rules').length).toBe(4);
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'va_n' }] : []);
    const msg2 = await w.run({ ...answers, freq_watch: 'yes' }, { idempotencyKey: 'run-3' });
    expect(msg2).toContain('Frequency watch SKIPPED');
    expect(apiMock.mock.calls.filter(c => c[0] === '/v1/rules').length).toBe(6); // 4 + 2 voltage-only
  });
  it('run adds overcurrent rules for each current point and skips cleanly when none report', async () => {
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'ia' }, { point_id: 'ib' }, { point_id: 'va_n' }] : []);
    const w = byId('meter-watch');
    const msg = await w.run({ ...answers, i_limit: '80' }, { idempotencyKey: 'run-4' });
    expect(msg).toContain('current > 80 A on ia, ib');
    const posts = apiMock.mock.calls.filter(c => c[0] === '/v1/rules');
    expect(posts.length).toBe(4); // 2 voltage + 2 current
    expect(posts[2][1].headers['Idempotency-Key']).toBe('run-4-i-ia');
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'va_n' }] : []);
    const msg2 = await w.run({ ...answers, i_limit: '80' }, { idempotencyKey: 'run-5' });
    expect(msg2).toContain('Overload watch SKIPPED');
  });
});

describe('wizard role gating metadata', () => {
  it('write wizards declare the base roles the server requires', () => {
    expect(byId('invite-user').roles).toEqual(['admin']);
    expect(byId('add-channel').roles).toEqual(['admin']);
    expect(byId('ack-alert').roles).toEqual(['admin', 'operator']);
    expect(byId('alert-rule').roles).toEqual(['admin', 'operator']);
    expect(byId('meter-watch').roles).toEqual(['admin', 'operator']);
    expect(byId('add-device').roles).toEqual(['admin', 'operator', 'installer']);
  });
  it('the energy-meter guide writes nothing, so it stays open to every role', () => {
    expect(byId('energy-meter').roles).toBeUndefined();
  });
});
