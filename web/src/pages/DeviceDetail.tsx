import { chart as ct } from '../lib/theme';
import { formatValue } from '../lib/format';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { LineChart, Line, XAxis, YAxis, Tooltip, ResponsiveContainer, CartesianGrid } from 'recharts';
import { api, LatestPoint } from '../lib/api';
import DeviceTwin, { Health } from '../components/DeviceTwin';
import ForecastCard from '../components/ForecastCard';
import DeviceAttributes, { AttrValue } from '../components/DeviceAttributes';

interface SeriesPoint { t: string; v: number; quality?: string; aggregated?: boolean; }
const RANGES: [string, number][] = [['24 hours', 24], ['7 days', 168], ['30 days', 720], ['90 days', 2160], ['1 year', 8760]];

export default function DeviceDetail() {
  const { id } = useParams();
  const [latest, setLatest] = useState<LatestPoint[]>([]);
  const [point, setPoint] = useState('');
  const [series, setSeries] = useState<SeriesPoint[]>([]);
  const [hours, setHours] = useState(24);
  const [anom, setAnom] = useState<{ samples: number; enough_data: boolean; min_samples: number; anomalies: { t: number; value: number; score: number; median: number }[] } | null>(null);
  const [err, setErr] = useState('');
  const [health, setHealth] = useState<Health | null>(null);
  const [shadow, setShadow] = useState<{ reported: Record<string, { stale: boolean; age_seconds: number }>; attributes: Record<string, AttrValue> } | null>(null);
  const [meta, setMeta] = useState<{ name: string; profile: string } | null>(null);

  useEffect(() => {
    if (!id) return;
    const load = () => api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${id}`)
      .then(d => { setLatest(d); if (!point && d.length) setPoint(d[0].point_id); })
      .catch(e => setErr(String(e)));
    const loadHealth = () => api<Health>(`/v1/devices/${id}/health`).then(setHealth).catch(() => undefined);
    api<{ id: string; name: string; profile: string }[]>(`/v1/devices?q=${encodeURIComponent(id)}`).then(d => { const m = d.find(x => x.id === id); if (m) setMeta(m); }).catch(() => undefined);
    const loadShadow = () => api<NonNullable<typeof shadow>>(`/v1/devices/${id}/shadow`).then(setShadow).catch(() => undefined);
    load(); loadHealth(); loadShadow();
    const t = setInterval(() => { load(); loadHealth(); loadShadow(); }, 10000);
    return () => clearInterval(t);
  }, [id]);

  useEffect(() => {
    if (!id || !point) return;
    api<SeriesPoint[]>(`/v1/telemetry/series?device_id=${id}&point_id=${point}&hours=${hours}`)
      .then(setSeries).catch(e => setErr(String(e)));
    api<NonNullable<typeof anom>>(`/v1/telemetry/anomalies?device_id=${id}&point_id=${point}`)
      .then(setAnom).catch(() => setAnom(null));
  }, [id, point, hours]);

  const chart = series.map(p => ({ t: hours > 48 ? new Date(p.t).toLocaleDateString() + (hours <= 336 ? ' ' + new Date(p.t).getHours() + 'h' : '') : new Date(p.t).toLocaleTimeString(), v: p.v }));

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
            {shadow?.reported[p.point_id]?.stale && <span className="pill warn" style={{ marginLeft: 6 }} title="Older than three polling intervals">stale</span>}
            <div className="muted" style={{ fontSize: 12 }}>{new Date(p.observed_at).toLocaleString()}</div>
          </div>
        ))}
      </div>
      {id && shadow && <DeviceAttributes deviceId={id} attributes={shadow.attributes} onSaved={() => api<NonNullable<typeof shadow>>(`/v1/devices/${id}/shadow`).then(setShadow).catch(() => undefined)} />}
      {latest.length > 0 && (
        <>
          <div style={{ margin: '18px 0 8px' }}>
            <label>Point</label>
            <select value={point} onChange={e => setPoint(e.target.value)} style={{ maxWidth: 220 }}>
              {latest.map(p => <option key={p.point_id} value={p.point_id}>{p.point_id}</option>)}
            </select>
            <select value={hours} onChange={e => setHours(+e.target.value)} aria-label="Time range" style={{ maxWidth: 140, marginLeft: 8 }}>
              {RANGES.map(([l, h]) => <option key={h} value={h}>{l}</option>)}
            </select>
            {hours > 48 && <span className="muted" style={{ marginLeft: 8 }}>averages per {hours > 336 ? 'day' : 'hour'}, from rollups where raw data was purged</span>}
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
          {id && point && <ForecastCard device={id} point={point} />}
          {anom && (
            <div className="card" style={{ marginTop: 14 }} role="region" aria-label="Anomalies">
              <b>Anomalies (24h)</b>
              {!anom.enough_data
                ? <p className="muted">Needs at least {anom.min_samples} measured samples; has {anom.samples}.</p>
                : anom.anomalies.length === 0
                  ? <p className="muted">No outliers in {anom.samples} samples.</p>
                  : <table style={{ marginTop: 8 }}><thead><tr><th>Time</th><th>Value</th><th>Median</th><th>Score</th></tr></thead><tbody>
                      {anom.anomalies.slice(0, 20).map(a => <tr key={a.t}><td>{new Date(a.t * 1000).toLocaleString()}</td><td>{formatValue(a.value)}</td><td>{formatValue(a.median)}</td><td>{a.score}</td></tr>)}
                    </tbody></table>}
              <p className="muted" style={{ fontSize: 12 }}>Robust median/MAD score, flagged above 3.5. Explains outliers; it does not predict failures.</p>
            </div>
          )}
        </>
      )}
    </>
  );
}
