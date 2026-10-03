import { useEffect, useState } from 'react';
import { api, Device } from '../lib/api';

interface Win { id: string; name: string; device_id: string | null; asset_id: string | null; starts_at: string; ends_at: string; ended_at: string | null; active: boolean }

/** Hours from a number the user typed; null when it is not 1-168. */
export function windowHours(v: string): number | null {
  const n = Number(v);
  return Number.isFinite(n) && n >= 1 && n <= 168 ? n : null;
}

// Maintenance windows: planned work on a device or asset. Warning and info alerts are recorded
// but not notified while one is active. Critical alerts are never quieted.
export default function Maintenance() {
  const [wins, setWins] = useState<Win[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [assets, setAssets] = useState<{ id: string; name: string }[]>([]);
  const [name, setName] = useState('');
  const [target, setTarget] = useState('');
  const [hours, setHours] = useState('4');
  const [err, setErr] = useState('');
  const load = () => api<Win[]>('/v1/maintenance').then(setWins).catch(() => {});
  useEffect(() => {
    load();
    api<Device[]>('/v1/devices').then(setDevices).catch(() => {});
    api<{ id: string; name: string }[]>('/v1/assets').then(setAssets).catch(() => {});
  }, []);
  const label = (w: Win) => w.device_id ? (devices.find(d => d.id === w.device_id)?.name ?? w.device_id) : (assets.find(a => a.id === w.asset_id)?.name ?? w.asset_id);
  const start = async () => {
    setErr('');
    const h = windowHours(hours);
    if (!name.trim() || !target || h === null) { setErr('Give it a name, pick a device or asset, and 1 to 168 hours.'); return; }
    const [kind, id] = target.split(':');
    try {
      await api('/v1/maintenance', { method: 'POST', body: JSON.stringify({ name: name.trim(), [kind === 'd' ? 'device_id' : 'asset_id']: id, ends_at: new Date(Date.now() + h * 3600_000).toISOString() }) });
      setName(''); load();
    } catch (e) { setErr(String((e as Error).message).replace(/^\d+:\s*/, '')); }
  };
  const end = (id: string) => api(`/v1/maintenance/${id}/end`, { method: 'POST' }).then(load).catch(e => setErr(String(e.message)));
  const live = wins.filter(w => w.active);
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <h2 style={{ marginTop: 0 }}>Maintenance windows</h2>
      <p className="muted" style={{ marginTop: 0 }}>While a window is on, warning and info alerts for that device or asset are recorded but not sent. Critical alerts are never held back. If a held alert is still open when the window ends, one notice goes out.</p>
      {live.length === 0 && <p className="muted">No window is active.</p>}
      {live.map(w => (
        <div key={w.id} style={{ display: 'flex', gap: 10, alignItems: 'center', padding: '6px 0', borderTop: '1px solid var(--line)' }}>
          <b>{w.name}</b><span className="muted">{label(w)} until {new Date(w.ends_at).toLocaleString()}</span>
          <button className="ghost" onClick={() => end(w.id)}>End now</button>
        </div>
      ))}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10, alignItems: 'center' }}>
        <input aria-label="Window name" placeholder="Name, e.g. Pump service" value={name} maxLength={80} onChange={e => setName(e.target.value)} style={{ width: 200 }} />
        <select aria-label="Device or asset" value={target} onChange={e => setTarget(e.target.value)} style={{ width: 'auto' }}>
          <option value="">Device or asset...</option>
          <optgroup label="Devices">{devices.map(d => <option key={d.id} value={`d:${d.id}`}>{d.name}</option>)}</optgroup>
          <optgroup label="Assets">{assets.map(a => <option key={a.id} value={`a:${a.id}`}>{a.name}</option>)}</optgroup>
        </select>
        <input aria-label="Hours" value={hours} onChange={e => setHours(e.target.value)} style={{ width: 60 }} /> <span className="muted">hours</span>
        <button onClick={start}>Start window</button>
      </div>
      {err && <p role="alert" className="muted">{err}</p>}
    </div>
  );
}
