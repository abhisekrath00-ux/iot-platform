import { describe, expect, it } from 'vitest';
import { buildOee } from './oee';

describe('buildOee', () => {
  it('builds the three-factor expression', () => {
    expect(buildOee({ run: 'l1.run', planned: 'l1.planned', total: 'l1.total', good: 'l1.good', cycle: 0.5 }))
      .toBe('({l1.run} / {l1.planned}) * ({l1.total} * 0.5 / {l1.run}) * ({l1.good} / {l1.total})');
  });
  it('rejects bad references and cycle times', () => {
    expect(() => buildOee({ run: 'bad ref', planned: 'a.b', total: 'a.c', good: 'a.d', cycle: 1 })).toThrow();
    expect(() => buildOee({ run: 'a.a', planned: 'a.b', total: 'a.c', good: 'a.d', cycle: 0 })).toThrow();
    expect(() => buildOee({ run: 'a.a', planned: 'a.b', total: 'a.c', good: 'a.d', cycle: NaN })).toThrow();
  });
  it('matches the arithmetic: 400 of 480 min, 700 parts at 0.5 min, 665 good', () => {
    const run = 400, planned = 480, total = 700, good = 665, cycle = 0.5;
    const v = (run / planned) * (total * cycle / run) * (good / total);
    expect(v).toBeCloseTo(0.6927, 3);
  });
});
