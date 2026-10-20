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
  it('staged-off copy is visible in the channel summary', () => {
    const w = byId('add-channel');
    expect(w.summary({ type: 'email', target: 'a@b.c' })).toContain('OFF');
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
