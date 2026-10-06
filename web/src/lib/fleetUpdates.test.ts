import { describe, expect, it } from 'vitest';
import { allowedActions, parseStages, progress } from './fleetUpdates';

describe('fleet updates helpers', () => {
  it('validates stages like the server', () => {
    expect(parseStages('10, 50,100')).toEqual({ stages: [10, 50, 100], error: '' });
    expect(parseStages('100').stages).toEqual([100]);
    for (const bad of ['', 'a', '10,50', '50,10,100', '0,100', '10,10,100', '10,200', '1,2,3,4,5,6,7,8,9,10,100']) expect(parseStages(bad).error).not.toBe('');
  });
  it('offers only sensible actions', () => {
    const base = { stages: [10, 100], stage_index: 0, open: 0, failed: 0 };
    expect(allowedActions({ ...base, state: 'draft', stage_index: -1 })).toEqual(['start', 'abort']);
    expect(allowedActions({ ...base, state: 'running' })).toContain('advance');
    expect(allowedActions({ ...base, state: 'running', open: 2 })).not.toContain('advance');
    expect(allowedActions({ ...base, state: 'running', failed: 1 })).not.toContain('advance');
    expect(allowedActions({ ...base, state: 'running', stage_index: 1 })).not.toContain('advance');
    expect(allowedActions({ ...base, state: 'done' })).toEqual(['rollback']);
    expect(allowedActions({ ...base, state: 'aborted' })).toEqual([]);
  });
  it('computes progress', () => {
    expect(progress({ acked: 3, failed: 1, open: 0 })).toEqual({ total: 4, pct: 75 });
    expect(progress({ acked: 0, failed: 0, open: 0 }).pct).toBe(0);
  });
});
