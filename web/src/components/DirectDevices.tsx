import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Site { id: string; name: string; }
interface Direct { id: string; serial: string; status: string; auth: string; }
interface Created { gateway_id: string; username?: string; password?: string; claim_code?: string; enroll_string?: string; publish_topic?: string; warning?: string; }

// Direct MQTT devices: network MCUs that publish straight to the broker. The
// admin chooses certificate (default) or password per device. Password auth is
// off until an admin enables it for the tenant, and says why it is weaker.
export default function DirectDevices() {
  const [sites, setSites] = useState<Site[]>([]);
  const [devices, setDevices] = useState<Direct[]>([]);
  const [pwOn, setPwOn] = useState(false);
  const [warning, setWarning] = useState('');
  const [site, setSite] = useState('');
  const [serial, setSerial] = useState('');
  const [mode, setMode] = useState<'cert' | 'password'>('cert');
  const [created, setCreated] = useState<Created | null>(null);
  const [msg, setMsg] = useState('');

  const load = () => {
    api<Direct[]>('/v1/direct-devices').then(setDevices).catch(e => setMsg(String(e)));
    api<{ password_enabled: boolean; warning: string }>('/v1/direct-auth/policy').then(p => { setPwOn(p.password_enabled); setWarning(p.warning); }).catch(() => undefined);
  };
  useEffect(() => { load(); api<Site[]>('/v1/sites').then(s => { setSites(s); if (s[0]) setSite(s[0].id); }).catch(() => undefined); }, []);

  async function setPolicy(on: boolean) {
    setMsg('');
    try { await api('/v1/direct-auth/policy', { method: 'PUT', body: JSON.stringify({ password_enabled: on }) }); load(); }
    catch (e) { setMsg(String(e)); }
  }
  async function create(e: React.FormEvent) {
    e.preventDefault(); setMsg(''); setCreated(null);
    try {
      const r = mode === 'password'
        ? await api<Created>('/v1/direct-devices/password', { method: 'POST', body: JSON.stringify({ site_id: site, serial }) })
        : await api<Created>('/v1/enrollment/tokens', { method: 'POST', body: JSON.stringify({ site_id: site, serial, kind: 'direct' }) });
      setCreated(r); setSerial(''); load();
    } catch (err) { setMsg(String(err)); }
  }
  async function act(id: string, what: 'rotate' | 'revoke') {
    setMsg(''); setCreated(null);
    try {
      const r = await api<Created>(`/v1/direct-devices/${id}/${what}`, { method: 'POST' });
      if (what === 'rotate') setCreated({ ...r, gateway_id: id });
      load();
    } catch (err) { setMsg(String(err)); }
  }

  return (
    <div className="card" style={{ maxWidth: 640, marginBottom: 20 }} role="region" aria-label="Direct MQTT devices">
      <b>Direct MQTT devices</b>
      <p className="muted">Network devices (ESP32, STM32) that publish straight to the broker. Telemetry only: they cannot receive commands. Admins only.</p>
      <div style={{ margin: '10px 0' }}>
        <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <input type="checkbox" checked={pwOn} onChange={e => setPolicy(e.target.checked)} style={{ width: 'auto' }} />
          Allow password authentication (off by default)
        </label>
        {pwOn && <p role="alert" style={{ color: '#b45309', margin: '6px 0' }}>{warning}</p>}
      </div>
      <form onSubmit={create}>
        <label>Site</label>
        <select value={site} onChange={e => setSite(e.target.value)}>{sites.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</select>
        <label>Device serial (broker username)</label>
        <input value={serial} onChange={e => setSerial(e.target.value)} placeholder="MCU-0001" required />
        <label>Authentication</label>
        <select value={mode} onChange={e => setMode(e.target.value as 'cert' | 'password')}>
          <option value="cert">Client certificate (recommended)</option>
          <option value="password" disabled={!pwOn}>Password{pwOn ? ' (weaker)' : ' (enable above first)'}</option>
        </select>
        <div style={{ marginTop: 14 }}><button type="submit">Create device</button></div>
      </form>
      {msg && <p className="muted">{msg}</p>}
      {created && (
        <div className="card" style={{ marginTop: 12 }} role="status">
          <b>Shown once, copy it now</b>
          {created.claim_code && <p>Claim code: <code>{created.claim_code}</code> (the device redeems it with its CSR to get a certificate)</p>}
          {created.enroll_string && <p>Enroll string: <code>{created.enroll_string}</code></p>}
          {created.password && <>
            <p>Username: <code>{created.username ?? ''}</code></p>
            <p>Password: <code>{created.password}</code></p>
          </>}
          {created.publish_topic && <p>Publish topic: <code>{created.publish_topic}</code></p>}
        </div>
      )}
      <table style={{ marginTop: 16 }}>
        <thead><tr><th>Serial</th><th>Auth</th><th>Status</th><th /></tr></thead>
        <tbody>
          {devices.map(d => (
            <tr key={d.id}><td>{d.serial}</td><td>{d.auth === 'password' ? 'password' : 'certificate'}</td><td className="muted">{d.status}</td>
              <td>
                {d.auth === 'password' && d.status === 'active' && <button type="button" onClick={() => act(d.id, 'rotate')}>Rotate</button>}{' '}
                {d.status !== 'revoked' && <button type="button" onClick={() => { if (window.confirm(`Revoke ${d.serial}?`)) act(d.id, 'revoke'); }}>Revoke</button>}
              </td></tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
