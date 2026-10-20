import Ask from '../components/Ask';
import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, AlertRow, Device, LatestPoint } from '../lib/api';
import Empty from '../components/Empty';

interface FleetStatus { gateways: number; active_gateways: number; devices: number; stale_devices: number; open_alerts: number; }

export default function Fleet() {
  const [s, setS] = useState<FleetStatus | null>(null);
  const [statusFailed, setStatusFailed] = useState(false);
  const [err, setErr] = useState('');
  const [scoped, setScoped] = useState(false);
  const [meLoaded, setMeLoaded] = useState(false);
  const [devices, setDevices] = useState<Device[]>([]);
  const [latest, setLatest] = useState<Record<string, LatestPoint[]>>({});
  const [alerts, setAlerts] = useState<AlertRow[]>([]);
  const [alertCount, setAlertCount] = useState(0);
  const [assets, setAssets] = useState<{ id: string; name: string; parent_id: string | null }[]>([]);
  const [asset, setAsset] = useState('');
  // Customer-scoped users are refused /v1/fleet, /v1/assets and the assistant;
  // their page is built only from the endpoints their scope allows.
  useEffect(() => {
    api<{ customer_id?: string }>('/v1/me').then(m => {
      const sc = !!m.customer_id;
      setScoped(sc);
      setMeLoaded(true);
      if (!sc) api<typeof assets>('/v1/assets').then(setAssets).catch(() => {});
    }).catch(() => setMeLoaded(true));
  }, []);
  useEffect(() => {
    if (!meLoaded || scoped) return;
    const load = () => api<FleetStatus>('/v1/fleet').then(r => { setS(r); setStatusFailed(false); }).catch(() => { setS(null); setStatusFailed(true); });
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [meLoaded, scoped]);
  useEffect(() => {
    const loadDevices = async () => {
      try {
        const ds = await api<Device[]>(asset ? `/v1/devices?asset_id=${encodeURIComponent(asset)}` : '/v1/devices');
        setDevices(ds);
        const entries = await Promise.all(ds.slice(0, 12).map(async d =>
          [d.id, await api<LatestPoint[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(d.id)}`).catch(() => [])] as const));
        setLatest(Object.fromEntries(entries));
        const al = await api<AlertRow[]>('/v1/alerts');
        setAlertCount(al.length);
        setAlerts(al.slice(0, 5));
      } catch (e) { setErr(String(e)); }
    };
    loadDevices();
    const t = setInterval(loadDevices, 15000);
    return () => clearInterval(t);
  }, [asset]);
  const openAlerts = alertCount;
  return (
    <>
      <h1>Fleet</h1>
      {meLoaded && !scoped && <Ask />}
      {err && <p className="muted">{err}</p>}
      <div className="cards">
        <div className="card"><div className="muted">Gateways</div><div className="kpi">{s ? `${s.active_gateways}/${s.gateways}` : '-'}</div>
          {s ? <span className="pill ok">active</span> : <span className="pill">not available</span>}</div>
        <div className="card"><div className="muted">Devices</div><div className="kpi">{scoped ? devices.length : (s?.devices ?? '-')}</div></div>
        <div className="card"><div className="muted">Stale devices</div><div className="kpi">{s?.stale_devices ?? '-'}</div>
          {s ? (s.stale_devices > 0 ? <span className="pill warn">check</span> : <span className="pill ok">fresh</span>) : <span className="pill">not available</span>}</div>
        <div className="card"><div className="muted">Open alerts</div><div className="kpi">{scoped ? openAlerts : (s?.open_alerts ?? '-')}</div>
          {scoped ? (openAlerts > 0 ? <span className="pill bad">action</span> : <span className="pill ok">clear</span>)
            : s ? (s.open_alerts > 0 ? <span className="pill bad">action</span> : <span className="pill ok">clear</span>) : <span className="pill">not available</span>}</div>
      </div>
      {statusFailed && !scoped && <p className="muted" role="alert">Fleet totals could not be loaded; the cards show only what loaded.</p>}
      {scoped && <p className="muted">Showing only this customer's devices and alerts; gateway and fleet-wide totals are not part of a customer account.</p>}
      {!scoped && <p className="muted" style={{ marginTop: 12 }}>Refreshes every 15s. A device is stale when nothing was measured in the last 15 minutes.</p>}

      <h2>Devices</h2>
      {assets.length > 0 && <div style={{ marginBottom: 12 }}>
        <label htmlFor="fleet-asset" className="muted">Show devices under </label>
        <select id="fleet-asset" value={asset} onChange={e => setAsset(e.target.value)} style={{ width: 'auto' }}>
          <option value="">All assets</option>
          {assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
        </select>
      </div>}
      {devices.length === 0 ? (
        scoped
          ? <Empty title="No devices in this customer yet" hint="A workspace admin can assign devices to this customer." />
          : <Empty title="No devices yet" hint="Claim a gateway and add your first sensor. The wizard takes a few minutes." action={<Link to="/onboarding"><button>Add a device</button></Link>} />
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
