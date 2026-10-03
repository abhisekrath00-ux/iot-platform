// Pure widget logic: kept out of components so it is unit-tested.

export type WidgetType = 'kpi' | 'timeseries' | 'gauge' | 'bar' | 'status' | 'table' | 'stat' | 'indicator' | 'alarms' | 'note';

export interface Widget {
  id: string;
  type: WidgetType;
  title: string;
  device_id: string;
  point_id?: string;
  // optional per-widget configuration
  min?: number;
  max?: number;
  warn?: number; // value at or above this turns amber
  crit?: number; // value at or above this turns red
  stale_seconds?: number; // status widget: older than this counts as offline
  text?: string; // note widget body (plain text, rendered as text, never HTML)
  span?: number; // grid columns 1-4 (default depends on type)
}

export type State = 'ok' | 'warn' | 'bad' | 'none';

/** Threshold state for a value. crit wins over warn; NaN or no thresholds -> none/ok. */
export function thresholdState(v: number, warn?: number, crit?: number): State {
  if (!Number.isFinite(v)) return 'none';
  if (crit !== undefined && v >= crit) return 'bad';
  if (warn !== undefined && v >= warn) return 'warn';
  return 'ok';
}

/** Position of v in [min,max] as 0..1, clamped. Degenerate ranges give 0. */
export function fraction(v: number, min: number, max: number): number {
  if (!Number.isFinite(v) || !(max > min)) return 0;
  return Math.min(1, Math.max(0, (v - min) / (max - min)));
}

/** Online/offline from last observation age. Missing timestamp is offline. */
export function freshness(observedAt: string | undefined, nowMs: number, staleSeconds = 120): 'online' | 'offline' {
  if (!observedAt) return 'offline';
  const t = Date.parse(observedAt);
  if (Number.isNaN(t)) return 'offline';
  return (nowMs - t) / 1000 <= staleSeconds ? 'online' : 'offline';
}

/** Parse an optional numeric form field: '' -> undefined, junk -> undefined. */
export function optNum(s: string): number | undefined {
  if (s.trim() === '') return undefined;
  const n = Number(s);
  return Number.isFinite(n) ? n : undefined;
}

/** Grid columns a widget occupies: explicit span (clamped 1-4) or a per-type default. */
export function spanOf(w: Pick<Widget, 'type' | 'span'>): number {
  if (w.span !== undefined && Number.isFinite(w.span)) return Math.min(4, Math.max(1, Math.round(w.span)));
  return w.type === 'bar' || w.type === 'timeseries' || w.type === 'table' ? 2 : 1;
}

/** Move the item at `from` so it lands at index `to`. Out-of-range input returns the list unchanged (as a copy). */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  const out = list.slice();
  if (from < 0 || from >= out.length || to < 0 || to >= out.length || from === to) return out;
  const [it] = out.splice(from, 1);
  out.splice(to, 0, it);
  return out;
}

/** Summary of a series: last/min/max/avg and change from first to last. Empty or non-finite input gives null. */
export function summarize(vals: number[]): { last: number; min: number; max: number; avg: number; delta: number; n: number } | null {
  const v = vals.filter(Number.isFinite);
  if (v.length === 0) return null;
  let min = v[0], max = v[0], sum = 0;
  for (const x of v) { if (x < min) min = x; if (x > max) max = x; sum += x; }
  return { last: v[v.length - 1], min, max, avg: sum / v.length, delta: v[v.length - 1] - v[0], n: v.length };
}

/** Indicator lamp: on when value >= onAt (default 1, so 0/1 flags work). NaN is off. */
export function indicatorOn(v: number, onAt = 1): boolean {
  return Number.isFinite(v) && v >= onAt;
}
