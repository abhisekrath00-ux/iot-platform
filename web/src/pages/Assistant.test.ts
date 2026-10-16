import { describe, expect, it } from 'vitest';
import { normalizeReply, type Reply } from './Assistant';

describe('normalizeReply', () => {
  it('turns null plan, trace and pending into empty arrays (a model that never called set_plan)', () => {
    const r = normalizeReply({ reply: 'ok', plan: null, trace: null, pending: null } as unknown as Reply);
    expect(r.plan).toEqual([]);
    expect(r.trace).toEqual([]);
    expect(r.pending).toEqual([]);
    expect(r.plan.length).toBe(0);
  });
  it('keeps real values', () => {
    const r = normalizeReply({ reply: 'x', plan: ['a'], trace: [{ tool: 't', detail: 'd', status: 'ok' }], pending: [] } as unknown as Reply);
    expect(r.plan).toEqual(['a']);
    expect(r.trace).toHaveLength(1);
  });
  it('handles missing fields', () => {
    const r = normalizeReply({ reply: 'x' } as unknown as Reply);
    expect(r.plan).toEqual([]);
  });
});
