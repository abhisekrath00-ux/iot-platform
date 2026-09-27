import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Rule { id: string; name: string; definition: any; version: number; enabled: boolean; }

// Threshold rules (v1) with the flow-builder UI coming next. Rules evaluate in
// the ingest worker and dispatch to the tenant's email/Slack channels.
export default function Flows() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [name, setName] = useState('');
  const [pointId, setPointId] = useState('');
  const [deviceId, setDeviceId] = useState('');
  const [op, setOp] = useState('>');
  const [threshold, setThreshold] = useState('');
  const [severity, setSeverity] = useState('warning');
  const [msg, setMsg] = useState('');

  const load = () => api<Rule[]>('/v1/rules').then(setRules).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/rules', {
        method: 'POST',
        body: JSON.stringify({
          name,
          enabled: true,
          definition: {
            device_id: deviceId || undefined,
            point_id: pointId,
            op,
            threshold: parseFloat(threshold),
            severity
          }
        })
      });
      setName(''); setPointId(''); setDeviceId(''); setThreshold('');
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Flows & rules</h1>
      <div className="card" style={{ maxWidth: 480, marginBottom: 20 }}>
        <b>New threshold rule</b>
        <form onSubmit={submit}>
          <label>Rule name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="High current on main meter" required />
          <label>Point</label>
          <input value={pointId} onChange={e => setPointId(e.target.value)} placeholder="current" required />
          <label>Device (optional - all devices with this point if empty)</label>
          <input value={deviceId} onChange={e => setDeviceId(e.target.value)} placeholder="meter-1" />
          <label>Condition</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <select value={op} onChange={e => setOp(e.target.value)} style={{ width: 70 }}>
              <option value=">">&gt;</option><option value="<">&lt;</option>
            </select>
            <input value={threshold} onChange={e => setThreshold(e.target.value)} placeholder="100" type="number" step="any" required />
            <select value={severity} onChange={e => setSeverity(e.target.value)}>
              <option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
            </select>
          </div>
          <div style={{ marginTop: 14 }}><button type="submit">Create rule</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
      <table>
        <thead><tr><th>Name</th><th>Condition</th><th>Severity</th><th>Status</th></tr></thead>
        <tbody>
          {rules.map(r => (
            <tr key={r.id}>
              <td>{r.name}</td>
              <td className="muted">{r.definition?.point_id} {r.definition?.op} {r.definition?.threshold}{r.definition?.device_id ? ` on ${r.definition.device_id}` : ''}</td>
              <td><span className={`pill ${r.definition?.severity === 'critical' ? 'bad' : r.definition?.severity === 'warning' ? 'warn' : 'ok'}`}>{r.definition?.severity}</span></td>
              <td className="muted">{r.enabled ? 'enabled' : 'disabled'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted" style={{ marginTop: 14 }}>The visual drag-and-drop flow builder (trigger, condition, delay, notify blocks with simulation) builds on this same rules store.</p>
    </>
  );
}
