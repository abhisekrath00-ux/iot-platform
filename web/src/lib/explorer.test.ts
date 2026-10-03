import { describe, expect, it } from 'vitest';
import { correlation, normalise, stats } from './explorer';

const s = (vs: number[]) => vs.map((v, i) => ({ t: i, v }));

describe('explorer', () => {
  it('min-max scales to 0..1 and handles constants', () => {
    expect(normalise(s([2, 4, 6]), 'minmax').map(p => p.v)).toEqual([0, 0.5, 1]);
    expect(normalise(s([3, 3]), 'minmax').map(p => p.v)).toEqual([0.5, 0.5]);
  });
  it('z-score has mean 0', () => {
    const z = normalise(s([1, 2, 3, 4, 5]), 'zscore').map(p => p.v);
    expect(Math.abs(z.reduce((a, b) => a + b, 0))).toBeLessThan(1e-9);
    expect(normalise(s([7, 7, 7]), 'zscore').every(p => p.v === 0)).toBe(true);
  });
  it('correlation of linear series is +/-1', () => {
    expect(correlation(s([1, 2, 3, 4, 5]), s([2, 4, 6, 8, 10]))!.r).toBeCloseTo(1);
    expect(correlation(s([1, 2, 3, 4, 5]), s([10, 8, 6, 4, 2]))!.r).toBeCloseTo(-1);
  });
  it('refuses thin or constant data and uses shared timestamps only', () => {
    expect(correlation(s([1, 2, 3]), s([1, 2, 3]))).toBeNull();
    expect(correlation(s([1, 2, 3, 4, 5]), s([4, 4, 4, 4, 4]))).toBeNull();
    const a = Array.from({ length: 10 }, (_, t) => ({ t, v: t }));
    const b = Array.from({ length: 10 }, (_, k) => ({ t: k + 5, v: (k + 5) * 2 }));
    expect(correlation(a, b)!.n).toBe(5);
  });
  it('describes a series and fits a daily slope', () => {
    const day = 86400000;
    const st = stats([0, 1, 2, 3].map(i => ({ t: i * day, v: 10 + 2 * i })))!;
    expect(st.min).toBe(10); expect(st.max).toBe(16); expect(st.avg).toBe(13);
    expect(st.slopePerDay).toBeCloseTo(2); expect(st.changePct).toBeCloseTo(60);
    expect(stats([])).toBeNull();
    expect(stats([{ t: 0, v: 0 }, { t: 1, v: 1 }])!.slopePerDay).toBeNull();
    expect(stats([{ t: 0, v: 0 }, { t: 1, v: 5 }, { t: 2, v: 6 }])!.changePct).toBeNull();
  });
});
