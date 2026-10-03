import { useEffect, useMemo, useState } from 'react';
import Empty from '../components/Empty';
import { api, Device, LatestPoint } from '../lib/api';
import { correlation, normalise, Norm, Sample } from '../lib/explorer';

interface Row { t: string; avg: number; }
interface Sel { device: string; point: string; }
const COLORS = ['#4f8cff', '#f59e0b', '#10b981', '#ef4444', '#a855f7', '#14b8a6'];
const key = (s: Sel) => `${s.device}/${s.point}`;

/** Overlay up to six signals from any devices, with optional normalisation so signals with different
 *  units share one axis, and a correlation table. Correlated does not mean caused. */
export default function Explorer() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [dev, setDev] = useState('');
  const [pts, setPts] = useState<LatestPoint[]>([]);
  const [sel, setSel] = useState<Sel[]>([]);
  const [days, setDays] = useState(7);
  const [norm, setNorm] = useState<Norm>('minmax');
  const [data, setData] = useState<Record<string, Sample[]>>({});
  const [err, setErr] = useState('');

  useEffect(() => { api<Device[]>('/v1/devices').then(setDevices).catch(e => setErr(String(e))); }, []);
  useEffect(() => { if (dev) api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(dev)}`).then(setPts).catch(() => setPts([])); else setPts([]); }, [dev]);
  useEffect(() => {
    const to = new Date(), from = new Date(to.getTime() - days * 86400000);
    const bucket = days > 14 ? 'day' : 'hour';
    Promise.all(sel.map(s => api<Row[]>(`/v1/telemetry/rollup?device_id=${encodeURIComponent(s.device)}&point_id=${encodeURIComponent(s.point)}&from=${from.toISOString()}&to=${to.toISOString()}&bucket=${bucket}`)
      .then(rows => [key(s), rows.map(r => ({ t: new Date(r.t).getTime(), v: r.avg }))] as const).catch(() => [key(s), []] as const)))
      .then(entries => setData(Object.fromEntries(entries)));
  }, [sel, days]);

  const add = (p: string) => { if (sel.length < 6 && !sel.some(s => s.device === dev && s.point === p)) setSel([...sel, { device: dev, point: p }]); };
  const series = useMemo(() => sel.map((s, i) => ({ s, color: COLORS[i], raw: data[key(s)] ?? [], shown: normalise(data[key(s)] ?? [], norm) })), [sel, data, norm]);
  const all = series.flatMap(x => x.shown);
  const W = 720, H = 260, P = 34;
  const t0 = Math.min(...all.map(p => p.t)), t1 = Math.max(...all.map(p => p.t));
  const mn = Math.min(...all.map(p => p.v)), mx = Math.max(...all.map(p => p.v));
  const X = (t: number) => P + ((t - t0) / ((t1 - t0) || 1)) * (W - P - 8);
  const Y = (v: number) => H - P - ((v - mn) / ((mx - mn) || 1)) * (H - P - 10);
  const label = (s: Sel) => `${devices.find(d => d.id === s.device)?.name || s.device} · ${s.point}`;

  return (
    <>
      <h1>Explorer</h1>
      {err && <p className="muted">{err}</p>}
      <div className="card" style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        <select aria-label="Device" value={dev} onChange={e => setDev(e.target.value)} style={{ width: 'auto' }}>
          <option value="">choose device</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}
        </select>
        {pts.map(p => <button key={p.point_id} className="ghost" onClick={() => add(p.point_id)}>+ {p.point_id}</button>)}
        <span style={{ flex: 1 }} />
        <select aria-label="Window" value={days} onChange={e => setDays(+e.target.value)} style={{ width: 'auto' }}>
          <option value={1}>24 h</option><option value={7}>7 d</option><option value={30}>30 d</option><option value={90}>90 d</option>
        </select>
        <select aria-label="Scale" value={norm} onChange={e => setNorm(e.target.value as Norm)} style={{ width: 'auto' }}>
          <option value="minmax">Scale each 0 to 1</option><option value="zscore">Z-score</option><option value="raw">Raw values, one axis</option>
        </select>
      </div>
      {sel.length === 0 ? <Empty title="Pick signals to compare" hint="Choose a device, then add up to six points from any devices. Hourly averages up to 14 days, daily averages beyond." /> : (
        <div className="card">
          <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', marginBottom: 6 }}>
            {series.map(x => <span key={key(x.s)} style={{ fontSize: 13 }}><span style={{ color: x.color }}>●</span> {label(x.s)} <button className="ghost" aria-label={`Remove ${label(x.s)}`} onClick={() => setSel(sel.filter(q => key(q) !== key(x.s)))}>✕</button></span>)}
          </div>
          {all.length === 0 ? <div className="muted">No data in this window.</div> : (
            <svg role="img" aria-label={`Overlay of ${sel.length} signals`} width="100%" viewBox={`0 0 ${W} ${H}`} style={{ display: 'block' }}>
              <line x1={P} y1={H - P} x2={W - 8} y2={H - P} stroke="currentColor" opacity="0.25" />
              <line x1={P} y1={10} x2={P} y2={H - P} stroke="currentColor" opacity="0.25" />
              <text x={4} y={14} fontSize="10" fill="currentColor" opacity="0.7">{Number(mx.toFixed(2))}</text>
              <text x={4} y={H - P} fontSize="10" fill="currentColor" opacity="0.7">{Number(mn.toFixed(2))}</text>
              <text x={P} y={H - 10} fontSize="10" fill="currentColor" opacity="0.7">{new Date(t0).toLocaleString()}</text>
              <text x={W - 8} y={H - 10} fontSize="10" textAnchor="end" fill="currentColor" opacity="0.7">{new Date(t1).toLocaleString()}</text>
              {series.map(x => <polyline key={key(x.s)} fill="none" stroke={x.color} strokeWidth="2" points={x.shown.map(p => `${X(p.t)},${Y(p.v)}`).join(' ')} />)}
            </svg>
          )}
          {norm === 'raw' && <div className="muted" style={{ fontSize: 12 }}>Raw values share one axis, so signals with different units are hard to compare. Use a scaled view for shape.</div>}
          {sel.length > 1 && (
            <table style={{ marginTop: 10 }}>
              <thead><tr><th>Pair</th><th>Correlation (Pearson r)</th><th>Shared samples</th></tr></thead>
              <tbody>{series.flatMap((a, i) => series.slice(i + 1).map(b => { const c = correlation(a.raw, b.raw); return (
                <tr key={key(a.s) + key(b.s)}><td>{label(a.s)} vs {label(b.s)}</td><td>{c ? c.r.toFixed(2) : 'not enough data'}</td><td>{c?.n ?? '-'}</td></tr>); }))}</tbody>
            </table>
          )}
          <p className="muted" style={{ fontSize: 12 }}>Statistical, computed in your browser from the averages shown. Correlated with, not caused by: two signals moving together does not mean one drives the other.</p>
        </div>
      )}
    </>
  );
}
