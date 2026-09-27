import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Channel { id: string; type: string; target: string; enabled: boolean; }

export default function Settings() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [type, setType] = useState('email');
  const [target, setTarget] = useState('');
  const [msg, setMsg] = useState('');

  const load = () => api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/notifications/channels', { method: 'POST', body: JSON.stringify({ type, target }) });
      setTarget('');
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Settings</h1>
      <div className="card" style={{ maxWidth: 480, marginBottom: 20 }}>
        <b>Notification channels</b>
        <p className="muted">Alerts from your rules go to these destinations. Email uses your SMTP server (deployment env), Slack uses a bot token.</p>
        <form onSubmit={submit}>
          <label>Type</label>
          <select value={type} onChange={e => setType(e.target.value)}>
            <option value="email">Email</option><option value="slack">Slack channel</option>
          </select>
          <label>{type === 'email' ? 'Email address' : 'Slack channel ID'}</label>
          <input value={target} onChange={e => setTarget(e.target.value)} placeholder={type === 'email' ? 'ops@yourcompany.com' : 'C0123456789'} required />
          <div style={{ marginTop: 14 }}><button type="submit">Add channel</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
        <table style={{ marginTop: 16 }}>
          <thead><tr><th>Type</th><th>Target</th><th>Status</th></tr></thead>
          <tbody>
            {channels.map(c => <tr key={c.id}><td>{c.type}</td><td>{c.target}</td><td className="muted">{c.enabled ? 'on' : 'off'}</td></tr>)}
          </tbody>
        </table>
      </div>
      <div className="cards">
        <div className="card"><b>Users & roles</b><p className="muted">Admin, operator, installer, viewer. Tenant-scoped; SSO/OIDC before GA.</p></div>
        <div className="card"><b>Gateways</b><p className="muted">Enroll by one-time claim code, rotate certificates, revoke lost hardware.</p></div>
        <div className="card"><b>Audit log</b><p className="muted">Append-only record of every change and control action (admin view via API).</p></div>
      </div>
    </>
  );
}
