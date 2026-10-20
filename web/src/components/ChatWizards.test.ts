import { describe, expect, it } from 'vitest';
import { WIZARDS } from './ChatWizards';

const byId = (id: string) => {
  const w = WIZARDS.find(x => x.id === id);
  if (!w) throw new Error('missing wizard ' + id);
  return w;
};

describe('guided chat templates', () => {
  it('covers the requested template set', () => {
    for (const id of ['add-device', 'alert-rule', 'add-channel', 'energy-meter', 'ack-alert', 'invite-user']) byId(id);
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

describe('wizard role gating metadata', () => {
  it('write wizards declare the base roles the server requires', () => {
    expect(byId('invite-user').roles).toEqual(['admin']);
    expect(byId('add-channel').roles).toEqual(['admin']);
    expect(byId('ack-alert').roles).toEqual(['admin', 'operator']);
    expect(byId('alert-rule').roles).toEqual(['admin', 'operator']);
    expect(byId('add-device').roles).toEqual(['admin', 'operator', 'installer']);
  });
  it('the energy-meter guide writes nothing, so it stays open to every role', () => {
    expect(byId('energy-meter').roles).toBeUndefined();
  });
});
