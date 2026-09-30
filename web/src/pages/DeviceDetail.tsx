import { chart as ct } from '../lib/theme';
import { formatValue } from '../lib/format';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api, LatestPoint } from '../lib/api';
import DeviceTwin, { Health } from '../components/DeviceTwin';

interface SeriesPoint { t: string; v: number; quality: string; }

export default function DeviceDetail() {
  const { id } = useParams();
  const [latest, setLatest] = useState<LatestPoint[]>([]);
  const [point, setPoint] = useState('');
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [err, setErr] = useState('');
  const [health, setHealth] = useState<Health | null>(null);
  const [meta, setMeta] = useState<{ name: string; profile: string } | null>(null);

  useEffect(() => {
    if (!id) return;
    const load = () => api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${id}`)
      .then(d => { setLatest(d); if (!point && d.length) setPoint(d[0].point_id); })
      .catch(e => setErr(String(e)));
    const loadHealth = () => api<Health>(`/v1/devices/${id}/health`).then(setHealth).catch(() => undefined);
    api<{ id: string; name: string; profile: string }[]>(`/v1/devices?q=${encodeURIComponent(id)}`).then(d => { const m = d.find(x => x.id === id); if (m) setMeta(m); }).catch(() => undefined);
    load(); loadHealth();
    const t = setInterval(() => { load(); loadHealth(); }, 10000);
    return () => clearInterval(t);
  }, [id]);

  useEffect(() => {
    if (!id || !point) return;
    api<SeriesPoint[]>(`/v1/telemetry/series?device_id=${id}&point_id=${point}`)
      .then(setSeries).catch(e => setErr(String(e)));
  }, [id, point]);

  const chart = series.map(p => ({ t: new Date(p.t).toLocaleTimeString(), v: p.v }));

  return (
    <>
      <h1>{meta?.name ?? `Device ${id}`}</h1>
      {err && <p className="muted">{err}</p>}
      <DeviceTwin name={meta?.name ?? String(id)} profile={meta?.profile} health={health} points={latest} />
      <div className="cards">
        {latest.map(p => (
          <div className="card" key={p.point_id}>
            <div className="muted">{p.point_id}</div>
            <div className="kpi">{formatValue(p.value)} <small>{p.unit}</small></div>
            <span className={`pill ${p.quality === 'measured' ? 'ok' : 'warn'}`}>{p.quality}</span>
            <div className="muted" style={{ fontSize: 12 }}>{new Date(p.observed_at).toLocaleString()}</div>
          </div>
        ))}
      </div>
      {latest.length > 0 && (
        <>
          <div style={{ margin: '18px 0 8px' }}>
            <label>Point (last 24h)</label>
            <select value={point} onChange={e => setPoint(e.target.value)} style={{ maxWidth: 220 }}>
              {latest.map(p => <option key={p.point_id} value={p.point_id}>{p.point_id}</option>)}
            </select>
          </div>
          <div style={{ width: '100%', height: 300, background: 'var(--panel)', borderRadius: 12, padding: 12 }}>
            <ResponsiveContainer>
              <LineChart data={chart}>
                <CartesianGrid stroke={ct.grid} />
                <XAxis dataKey="t" stroke={ct.axis} fontSize={11} />
                <YAxis stroke={ct.axis} fontSize={11} domain={['auto', 'auto']} tickFormatter={formatValue} width={56} />
                <Tooltip contentStyle={ct.tooltip} />
                <Line type="monotone" dataKey="v" stroke={ct.line} dot={false} strokeWidth={2} />
              </LineChart>
            </ResponsiveContainer>
          </div>
        </>
      )}
    </>
  );
}
