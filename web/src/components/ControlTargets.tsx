import { useEffect, useState } from 'react';
import { api, Device } from '../lib/api';

interface Target { id: string; name: string; kind: 'modbus_write' | 'alarm_output'; gateway_id: string; device_id: string; point_id: string; min?: number; max?: number; allowed_values?: number[]; max_per_hour: number; approval_mode: 'approval' | 'automatic'; enabled: boolean }

/** What a target will accept, in words. */
export function describeTarget(t: Pick<Target, 'kind' | 'min' | 'max' | 'allowed_values'>): string {
  if (t.kind === 'alarm_output') return 'on / off';
  if (t.allowed_values?.length) return `one of ${t.allowed_values.join(', ')}`;
  return `${t.min} to ${t.max}`;
}

// The allowlist of things a flow may ask to change. Everything starts disabled and approval-only.
// "Automatic" exists only for alarm outputs (siren, buzzer) and is the admin's explicit choice.
export default function ControlTargets() {
  const [rows, setRows] = useState<Target[] | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [err, setErr] = useState('');
  const [f, setF] = useState({ name: '', kind: 'alarm_output', device: '', point: '', min: '', max: '', mode: 'approval' });
  const load = () => api<Target[]>('/v1/control-targets').then(setRows).catch(() => setRows(null));
  useEffect(() => { load(); api<Device[]>('/v1/devices').then(setDevices).catch(() => {}); }, []);
  if (rows === null) return null; // not allowed to see targets
  const post = (path: string, body: unknown) => api(path, { method: 'POST', body: JSON.stringify(body) }).then(() => { setErr(''); load(); }).catch(e => setErr(String(e.message).replace(/^\d+:\s*/, '')));
  const add = () => {
    const d = devices.find(x => x.id === f.device);
    if (!f.name.trim() || !d || !f.point.trim()) { setErr('Name, device and point are required.'); return; }
    post('/v1/control-targets', { name: f.name.trim(), kind: f.kind, gateway_id: d.gateway_id, device_id: d.id, point_id: f.point.trim(),
      ...(f.kind === 'modbus_write' ? { min: Number(f.min), max: Number(f.max) } : {}), approval_mode: f.mode }).then(() => setF({ ...f, name: '' }));
  };
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <h2 style={{ marginTop: 0 }}>Control targets</h2>
      <p className="muted" style={{ marginTop: 0 }}>The only things a flow may ask to change. New targets are off and need approval from a second person. Automatic is available only for alarm outputs such as a siren or buzzer, and only if an admin turns it on.</p>
      {rows.length === 0 && <p className="muted">No targets yet.</p>}
      {rows.length > 0 && <table>
        <thead><tr><th>Name</th><th>Device / point</th><th>Accepts</th><th>Mode</th><th>State</th></tr></thead>
        <tbody>{rows.map(t => (
          <tr key={t.id}>
            <td>{t.name}</td><td className="muted">{t.device_id} / {t.point_id}</td><td>{describeTarget(t)}</td>
            <td>{t.kind === 'alarm_output'
              ? <select aria-label={`Mode for ${t.name}`} value={t.approval_mode} style={{ width: 'auto' }} onChange={e => post(`/v1/control-targets/${t.id}/mode`, { approval_mode: e.target.value })}>
                  <option value="approval">Needs approval</option><option value="automatic">Automatic</option></select>
              : <span className="muted">Needs approval</span>}</td>
            <td><button className="ghost" onClick={() => post(`/v1/control-targets/${t.id}/enabled`, { enabled: !t.enabled })}>{t.enabled ? 'Turn off' : 'Turn on'}</button>{' '}
              <span className={`pill ${t.enabled ? 'ok' : ''}`}>{t.enabled ? 'on' : 'off'}</span></td>
          </tr>))}</tbody>
      </table>}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10, alignItems: 'center' }}>
        <input aria-label="Target name" placeholder="Name, e.g. Plant siren" value={f.name} maxLength={80} onChange={e => setF({ ...f, name: e.target.value })} style={{ width: 170 }} />
        <select aria-label="Target kind" value={f.kind} onChange={e => setF({ ...f, kind: e.target.value, mode: 'approval' })} style={{ width: 'auto' }}><option value="alarm_output">Alarm output (on/off)</option><option value="modbus_write">Modbus write (range)</option></select>
        <select aria-label="Target device" value={f.device} onChange={e => setF({ ...f, device: e.target.value })} style={{ width: 'auto' }}><option value="">Device...</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name}</option>)}</select>
        <input aria-label="Target point" placeholder="Point" value={f.point} onChange={e => setF({ ...f, point: e.target.value })} style={{ width: 100 }} />
        {f.kind === 'modbus_write' && <><input aria-label="Minimum" placeholder="min" value={f.min} onChange={e => setF({ ...f, min: e.target.value })} style={{ width: 60 }} /><input aria-label="Maximum" placeholder="max" value={f.max} onChange={e => setF({ ...f, max: e.target.value })} style={{ width: 60 }} /></>}
        {f.kind === 'alarm_output' && <select aria-label="Approval mode" value={f.mode} onChange={e => setF({ ...f, mode: e.target.value })} style={{ width: 'auto' }}><option value="approval">Needs approval</option><option value="automatic">Automatic</option></select>}
        <button onClick={add}>Add target</button>
      </div>
      {err && <p role="alert" className="muted">{err}</p>}
    </div>
  );
}
