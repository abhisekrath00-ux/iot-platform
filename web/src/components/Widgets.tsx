import { useEffect, useState } from 'react';
import { api, LatestPoint } from '../lib/api';
import { formatValue } from '../lib/format';
import { Widget, thresholdState, fraction, freshness } from '../lib/widgets';

const STATE_VAR: Record<string, string> = { ok: 'var(--ok, #248a3d)', warn: 'var(--warn, #b25000)', bad: 'var(--bad, #d70015)', none: 'var(--muted, #6e6e73)' };

/** Polls the latest values of one device; shared by the live widgets. */
export function useLatest(deviceId: string, everyMs = 10000) {
  const [pts, setPts] = useState<LatestPoint[]>([]);
  const [err, setErr] = useState('');
  useEffect(() => {
    let alive = true;
    const load = () => api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(deviceId)}`)
      .then(d => { if (alive) { setPts(d); setErr(''); } })
      .catch(e => { if (alive) setErr(String(e)); });
    load();
    const t = setInterval(load, everyMs);
    return () => { alive = false; clearInterval(t); };
  }, [deviceId, everyMs]);
  return { pts, err };
}

export function GaugeWidget({ w }: { w: Widget }) {
  const { pts, err } = useLatest(w.device_id);
  const p = pts.find(x => x.point_id === w.point_id);
  const min = w.min ?? 0, max = w.max ?? 100;
  const f = p ? fraction(p.value, min, max) : 0;
  const color = STATE_VAR[p ? thresholdState(p.value, w.warn, w.crit) : 'none'];
  // half-circle arc, r=50, length = pi*r
  const len = Math.PI * 50;
  return (
    <div className="card" style={{ textAlign: 'center' }}>
      <div className="muted">{w.title}</div>
      {err && <div className="muted" style={{ fontSize: 12 }}>{err}</div>}
      <svg viewBox="0 0 120 70" width="100%" style={{ maxWidth: 220 }} role="img" aria-label={`${w.title} gauge`}>
        <path d="M10 60 A50 50 0 0 1 110 60" fill="none" stroke="currentColor" strokeOpacity="0.12" strokeWidth="10" strokeLinecap="round" />
        <path d="M10 60 A50 50 0 0 1 110 60" fill="none" stroke={color} strokeWidth="10" strokeLinecap="round"
          strokeDasharray={`${len * f} ${len}`} style={{ transition: 'stroke-dasharray .4s' }} />
      </svg>
      <div className="kpi" style={{ marginTop: -26 }}>{p ? formatValue(p.value) : '-'} <small>{p?.unit}</small></div>
      <div className="muted" style={{ fontSize: 12 }}>{formatValue(min)} to {formatValue(max)}</div>
    </div>
  );
}

export function BarWidget({ w }: { w: Widget }) {
  const { pts, err } = useLatest(w.device_id);
  const max = w.max ?? Math.max(1, ...pts.map(p => Math.abs(p.value)));
  return (
    <div className="card" style={{ minWidth: 0 }}>
      <div className="muted" style={{ marginBottom: 8 }}>{w.title}</div>
      {err && <div className="muted" style={{ fontSize: 12 }}>{err}</div>}
      {pts.map(p => (
        <div key={p.point_id} style={{ marginBottom: 8 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}>
            <span>{p.point_id}</span><span className="muted">{formatValue(p.value)} {p.unit}</span>
          </div>
          <div style={{ height: 8, borderRadius: 4, background: 'rgba(128,128,128,.15)' }}>
            <div style={{ height: 8, borderRadius: 4, width: `${fraction(Math.abs(p.value), 0, max) * 100}%`, background: STATE_VAR[thresholdState(p.value, w.warn, w.crit) === 'none' ? 'ok' : thresholdState(p.value, w.warn, w.crit)] }} />
          </div>
        </div>
      ))}
      {pts.length === 0 && !err && <div className="muted">no data yet</div>}
    </div>
  );
}

export function StatusWidget({ w }: { w: Widget }) {
  const { pts, err } = useLatest(w.device_id, 5000);
  const newest = pts.map(p => p.observed_at).sort().pop();
  const st = freshness(newest, Date.now(), w.stale_seconds);
  return (
    <div className="card" style={{ minWidth: 0 }}>
      <div className="muted">{w.title}</div>
      {err && <div className="muted" style={{ fontSize: 12 }}>{err}</div>}
      <div style={{ fontSize: 22, fontWeight: 650, marginTop: 6 }}>
        <span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 5, marginRight: 8, background: STATE_VAR[st === 'online' ? 'ok' : 'bad'] }} />
        {st === 'online' ? 'Online' : 'Offline'}
      </div>
      <div className="muted" style={{ fontSize: 12 }}>{newest ? `last data ${new Date(newest).toLocaleString()}` : 'no data yet'} · {pts.length} points</div>
    </div>
  );
}
