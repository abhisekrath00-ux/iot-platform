import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, AlertRow, Device, LatestPoint } from '../lib/api';
import Empty from '../components/Empty';

interface FleetStatus { gateways: number; active_gateways: number; devices: number; stale_devices: number; open_alerts: number; }

export default function Fleet() {
  const [s, setS] = useState<FleetStatus | null>(null);
  const [err, setErr] = useState('');
  const [devices, setDevices] = useState<Device[]>([]);
  const [latest, setLatest] = useState<Record<string, LatestPoint[]>>({});
  const [alerts, setAlerts] = useState<AlertRow[]>([]);
  useEffect(() => {
    const load = () => api<FleetStatus>('/v1/fleet').then(setS).catch(e => setErr(String(e)));
    const loadDevices = async () => {
      try {
        const ds = await api<Device[]>('/v1/devices');
        setDevices(ds);
        const entries = await Promise.all(ds.slice(0, 12).map(async d =>
          [d.id, await api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(d.id)}`).catch(() => [])] as const));
        setLatest(Object.fromEntries(entries));
        setAlerts((await api<AlertRow[]>('/v1/alerts')).slice(0, 5));
      } catch (e) { setErr(String(e)); }
    };
    load(); loadDevices();
    const t = setInterval(() => { load(); loadDevices(); }, 15000);
    return () => clearInterval(t);
  }, []);
  return (
    <>
      <h1>Fleet</h1>
      {err && <p className="muted">{err}</p>}
      <div className="cards">
        <div className="card"><div className="muted">Gateways</div><div className="kpi">{s ? `${s.active_gateways}/${s.gateways}` : '-'}</div><span className="pill ok">active</span></div>
        <div className="card"><div className="muted">Devices</div><div className="kpi">{s?.devices ?? '-'}</div></div>
        <div className="card"><div className="muted">Stale devices</div><div className="kpi">{s?.stale_devices ?? '-'}</div>{s && s.stale_devices > 0 ? <span className="pill warn">check</span> : <span className="pill ok">fresh</span>}</div>
        <div className="card"><div className="muted">Open alerts</div><div className="kpi">{s?.open_alerts ?? '-'}</div>{s && s.open_alerts > 0 ? <span className="pill bad">action</span> : <span className="pill ok">clear</span>}</div>
      </div>
      <p className="muted" style={{ marginTop: 12 }}>Refreshes every 15s. A device is stale when nothing was measured in the last 15 minutes.</p>

      <h2>Devices</h2>
      {devices.length === 0 ? (
        <Empty title="No devices yet" hint="Claim a gateway and add your first sensor. The wizard takes a few minutes." action={<Link to="/onboarding"><button>Add a device</button></Link>} />
      ) : (
        <div className="cards">
          {devices.slice(0, 12).map(d => {
            const pts = latest[d.id] ?? [];
            const newest = pts.reduce((m, p) => Math.max(m, Date.parse(p.observed_at)), 0);
            const fresh = newest > 0 && Date.now() - newest < 15 * 60 * 1000;
            return (
              <Link key={d.id} to={`/devices/${d.id}`} className="card" style={{ color: 'inherit', display: 'block' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
                  <b>{d.name}</b>
                  <span className={`pill ${fresh ? 'ok' : 'warn'}`}>{fresh ? 'live' : 'stale'}</span>
                </div>
                <div className="muted" style={{ marginBottom: 8 }}>{d.profile}</div>
                {pts.slice(0, 3).map(p => (
                  <div key={p.point_id} style={{ display: 'flex', justifyContent: 'space-between' }}>
                    <span className="muted">{p.point_id}</span>
                    <span style={{ fontVariantNumeric: 'tabular-nums', fontWeight: 600 }}>{Number(p.value.toFixed(2))} {p.unit}</span>
                  </div>
                ))}
                {pts.length === 0 && <span className="muted">No readings yet</span>}
              </Link>
            );
          })}
        </div>
      )}

      <h2>Recent alerts</h2>
      {alerts.length === 0 ? <p className="muted">Nothing needs attention.</p> : (
        <table>
          <tbody>
            {alerts.map(a => (
              <tr key={a.id}>
                <td><span className={`pill ${a.severity === 'critical' ? 'bad' : a.severity === 'warning' ? 'warn' : 'ok'}`}>{a.severity}</span></td>
                <td>{a.message}</td>
                <td className="muted">{new Date(a.created_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
