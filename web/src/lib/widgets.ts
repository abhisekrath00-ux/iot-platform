// Pure widget logic: kept out of components so it is unit-tested.

export type WidgetType = 'kpi' | 'timeseries' | 'gauge' | 'bar' | 'status';

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
