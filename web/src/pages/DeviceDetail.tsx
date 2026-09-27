import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api, LatestPoint } from '../lib/api';

interface SeriesPoint { t: string; v: number; quality: string; }

export default function DeviceDetail() {
  const { id } = useParams();
  const [latest, setLatest] = useState<LatestPoint[]>([]);
  const [point, setPoint] = useState('');
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!id) return;
    const load = () => api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${id}`)
      .then(d => { setLatest(d); if (!point && d.length) setPoint(d[0].point_id); })
      .catch(e => setErr(String(e)));
    load();
    const t = setInterval(load, 10000);
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
      <h1>Device {id}</h1>
      {err && <p className="muted">{err}</p>}
      <div className="cards">
        {latest.map(p => (
          <div className="card" key={p.point_id}>
            <div className="muted">{p.point_id}</div>
            <div className="kpi">{p.value} <small>{p.unit}</small></div>
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
                <CartesianGrid stroke="#232b35" />
                <XAxis dataKey="t" stroke="#9aa7b4" fontSize={11} />
                <YAxis stroke="#9aa7b4" fontSize={11} />
                <Tooltip contentStyle={{ background: '#171c23', border: '1px solid #2a3340' }} />
                <Line type="monotone" dataKey="v" stroke="#3b82f6" dot={false} strokeWidth={2} />
              </LineChart>
            </ResponsiveContainer>
          </div>
        </>
      )}
    </>
  );
}
