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
  const answers = { device_id: 'd1', device_id_label: 'Meter 1', nominal: '230ln-50', nominal_label: '230 V phase-to-neutral, 50 Hz (public LV grid: EU, India, ...)', voltage_point: 'va_n', voltage_point_label: 'va_n', v_high: '253', v_low: '207', freq_watch: 'no', i_limit: '0', severity: 'warning' };
  it('summary names the nominal, the rules and the enabled-immediately disclosure', () => {
    const w = byId('meter-watch');
    const s = w.summary(answers);
    expect(s).toContain('Nominal system: 230 V phase-to-neutral, 50 Hz');
    expect(s).toContain('Create warning rules on Meter 1:');
    expect(s).toContain('va_n > 253 V');
    expect(s).toContain('va_n < 207 V');
    expect(s).toContain('ENABLED the moment they are created');
    expect(s).toContain('already enabled in Settings can receive these alerts immediately');
    const s2 = w.summary({ ...answers, freq_watch: 'yes', i_limit: '80' });
    expect(s2).toContain('Create warning rules on Meter 1:');
    expect(s2).toContain('49.5-50.5 Hz');
    expect(s2).toContain('every reported current point > 80 A');
  });
  it('nominal choice includes non-230V and non-50Hz systems, and band hints are not framed as protection', () => {
    const w = byId('meter-watch');
    const nom = w.steps.find(s => s.key === 'nominal')!;
    const vals = (nom.options ?? []).map(o => o.value);
    expect(vals).toEqual(['230ln-50', '400ll-50', '277ln-60', '120-60', 'other']);
    const vh = w.steps.find(s => s.key === 'v_high')!.prompt;
    expect(vh).toContain('253');
    expect(vh).toContain('NOT an equipment-protection or compliance limit');
    expect(w.steps.find(s => s.key === 'v_low')!.prompt).toContain('207');
    expect(w.steps.find(s => s.key === 'freq_watch')!.prompt).toContain('49.5-50.5');
  });
  it('voltage point options are filtered to points the device reports in volts', async () => {
    apiMock.mockResolvedValue([{ point_id: 'va_n', value: 231.5, unit: 'V' }, { point_id: 'ia', value: 12, unit: 'A' }, { point_id: 'volts_total', value: 9, unit: '' }]);
    const opts = await byId('meter-watch').steps.find(s => s.key === 'voltage_point')!.loadOptions!({ device_id: 'd1' });
    expect(opts.map(o => o.value)).toEqual(['va_n']); // unit-validated, never name-inferred
    expect(opts[0].label).toContain('231.5 V');
  });
  it('i_limit accepts 0 (skip) or a plain number, rejects text', () => {
    const v = byId('meter-watch').steps.find(s => s.key === 'i_limit')?.validate!;
    expect(v('0')).toBeNull();
    expect(v('63')).toBeNull();
    expect(v('many')).toBeTruthy();
  });
  it('run refuses an inverted band before creating anything', async () => {
    await expect(byId('meter-watch').run({ ...answers, v_low: '999' }, { idempotencyKey: 'r0' })).rejects.toThrow('Nothing was created');
    expect(apiMock.mock.calls.filter(c => c[0] === '/v1/rules').length).toBe(0);
  });
  it('run creates two voltage rules with per-rule idempotency keys when extras are skipped', async () => {
    apiMock.mockResolvedValue([]);
    const msg = await byId('meter-watch').run(answers, { idempotencyKey: 'run-1' });
    expect(msg).toContain('Created: voltage > 253 V; voltage < 207 V.');
    expect(msg).toContain('All enabled now');
    const posts = apiMock.mock.calls.filter(c => c[0] === '/v1/rules');
    expect(posts.length).toBe(2);
    expect(posts.map(c => c[1].headers['Idempotency-Key'])).toEqual(['run-1-vhigh', 'run-1-vlow']);
    expect(JSON.parse(posts[0][1].body)).toMatchObject({ enabled: true, definition: { device_id: 'd1', point_id: 'va_n', op: '>', threshold: 253, severity: 'warning' } });
  });
  it('frequency watch keys off the Hz unit, not the point name', async () => {
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'f50', unit: 'Hz' }] : []);
    const msg = await byId('meter-watch').run({ ...answers, freq_watch: 'yes' }, { idempotencyKey: 'run-2' });
    expect(msg).toContain('frequency > 50.5 Hz');
    expect(apiMock.mock.calls.filter(c => c[0] === '/v1/rules').length).toBe(4);
    apiMock.mockReset();
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'freq', unit: '' }] : []);
    const msg2 = await byId('meter-watch').run({ ...answers, freq_watch: 'yes' }, { idempotencyKey: 'run-3' });
    expect(msg2).toContain('Skipped: frequency watch');
    expect(apiMock.mock.calls.filter(c => c[0] === '/v1/rules').length).toBe(2); // voltage only
  });
  it('overload keys off the A unit and skips cleanly when nothing reports amps', async () => {
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'ia', unit: 'A' }, { point_id: 'ib', unit: 'A' }, { point_id: 'va_n', unit: 'V' }] : []);
    const msg = await byId('meter-watch').run({ ...answers, i_limit: '80' }, { idempotencyKey: 'run-4' });
    expect(msg).toContain('ia > 80 A');
    expect(msg).toContain('ib > 80 A');
    const posts = apiMock.mock.calls.filter(c => c[0] === '/v1/rules');
    expect(posts.length).toBe(4);
    expect(posts[2][1].headers['Idempotency-Key']).toBe('run-4-i-ia');
    apiMock.mockReset();
    apiMock.mockImplementation(async (path?: string) => String(path).includes('telemetry') ? [{ point_id: 'va_n', unit: 'V' }] : []);
    const msg2 = await byId('meter-watch').run({ ...answers, i_limit: '80' }, { idempotencyKey: 'run-5' });
    expect(msg2).toContain('Skipped: overload watch');
  });
  it('partial failure throws a full created/failed tally, and a Confirm retry replays with the same keys', async () => {
    let calls = 0;
    apiMock.mockImplementation(async (path?: string) => {
      if (String(path) === '/v1/rules') { calls++; if (calls === 2) throw new Error('500 boom'); }
      return [];
    });
    const w = byId('meter-watch');
    await expect(w.run(answers, { idempotencyKey: 'run-6' })).rejects.toThrow('Created: voltage > 253 V. FAILED: voltage < 207 V');
    // Confirm stays available on 5xx; a retry re-sends BOTH rules under the same per-rule keys -
    // the server replays the already-created one and writes only the missing one.
    apiMock.mockClear();
    apiMock.mockResolvedValue([]);
    const msg = await w.run(answers, { idempotencyKey: 'run-6' });
    expect(msg).toContain('Created: voltage > 253 V; voltage < 207 V.');
    const keys = apiMock.mock.calls.filter(c => c[0] === '/v1/rules').map(c => c[1].headers['Idempotency-Key']);
    expect(keys).toEqual(['run-6-vhigh', 'run-6-vlow']); // retry replays the SAME per-rule keys, no new writes for already-created rules
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
