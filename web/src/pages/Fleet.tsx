import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface FleetStatus { gateways: number; active_gateways: number; devices: number; stale_devices: number; open_alerts: number; }

export default function Fleet() {
  const [s, setS] = useState<FleetStatus | null>(null);
  const [err, setErr] = useState('');
  useEffect(() => {
    const load = () => api<FleetStatus>('/v1/fleet').then(setS).catch(e => setErr(String(e)));
    load();
    const t = setInterval(load, 15000);
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
      <p className="muted" style={{ marginTop: 16 }}>Refreshes every 15s. A device is stale when nothing was measured in the last 15 minutes.</p>
    </>
  );
}
