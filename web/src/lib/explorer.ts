// Pure helpers for the multi-signal explorer: normalisation and correlation.
export interface Sample { t: number; v: number; }
export type Norm = 'raw' | 'minmax' | 'zscore';

export function normalise(s: Sample[], mode: Norm): Sample[] {
  if (mode === 'raw' || s.length === 0) return s;
  const vs = s.map(p => p.v);
  if (mode === 'minmax') {
    const mn = Math.min(...vs), mx = Math.max(...vs), span = mx - mn;
    return s.map(p => ({ t: p.t, v: span === 0 ? 0.5 : (p.v - mn) / span }));
  }
  const mean = vs.reduce((a, b) => a + b, 0) / vs.length;
  const sd = Math.sqrt(vs.reduce((a, b) => a + (b - mean) ** 2, 0) / vs.length);
  return s.map(p => ({ t: p.t, v: sd === 0 ? 0 : (p.v - mean) / sd }));
}

/** Pearson r over the timestamps both series share. Null when fewer than 5 shared
 *  samples or either series is constant: a number from that little data would mislead. */
export function correlation(a: Sample[], b: Sample[]): { r: number; n: number } | null {
  const bm = new Map(b.map(p => [p.t, p.v]));
  const xs: number[] = [], ys: number[] = [];
  for (const p of a) { const q = bm.get(p.t); if (q !== undefined) { xs.push(p.v); ys.push(q); } }
  const n = xs.length;
  if (n < 5) return null;
  const mx = xs.reduce((s, v) => s + v, 0) / n, my = ys.reduce((s, v) => s + v, 0) / n;
  let sxy = 0, sxx = 0, syy = 0;
  for (let i = 0; i < n; i++) { sxy += (xs[i] - mx) * (ys[i] - my); sxx += (xs[i] - mx) ** 2; syy += (ys[i] - my) ** 2; }
  if (sxx === 0 || syy === 0) return null;
  return { r: sxy / Math.sqrt(sxx * syy), n };
}

export interface Stats { n: number; min: number; max: number; avg: number; first: number; last: number; changePct: number | null; slopePerDay: number | null; }

/** Plain descriptive statistics of a series. Slope is a least-squares line through the points, per day; null under 3 points. */
export function stats(s: Sample[]): Stats | null {
  if (s.length === 0) return null;
  const vs = s.map(p => p.v);
  const avg = vs.reduce((a, b) => a + b, 0) / vs.length;
  const first = s[0].v, last = s[s.length - 1].v;
  let slope: number | null = null;
  if (s.length >= 3) {
    const dayMs = 86400000, t0 = s[0].t;
    const xs = s.map(p => (p.t - t0) / dayMs), mx = xs.reduce((a, b) => a + b, 0) / xs.length;
    let sxy = 0, sxx = 0;
    xs.forEach((x, i) => { sxy += (x - mx) * (vs[i] - avg); sxx += (x - mx) ** 2; });
    slope = sxx === 0 ? null : sxy / sxx;
  }
  return { n: s.length, min: Math.min(...vs), max: Math.max(...vs), avg, first, last, changePct: first === 0 ? null : ((last - first) / Math.abs(first)) * 100, slopePerDay: slope };
}
