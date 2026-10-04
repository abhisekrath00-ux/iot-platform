import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import ApiKeys from '../components/ApiKeys';
import RetentionCard from '../components/RetentionCard';
import UsageCard from '../components/UsageCard';
import DirectDevices from '../components/DirectDevices';
import EdgeRules from '../components/EdgeRules';
import FeatureToggles from '../components/FeatureToggles';
import Authenticator from '../components/Authenticator';
import AttributeDefs from '../components/AttributeDefs';
import AISettings from '../components/AISettings';
import AssistantChannels from '../components/AssistantChannels';

interface Channel { id: string; type: string; target: string; enabled: boolean; }

export default function Settings() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [type, setType] = useState('email');
  const [target, setTarget] = useState('');
  const [msg, setMsg] = useState('');
  const [bName, setBName] = useState('');
  const [bAccent, setBAccent] = useState('');
  const [bMsg, setBMsg] = useState('');

  const load = () => api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(e => setMsg(String(e)));
  useEffect(() => { load(); api<{ product_name: string; accent: string }>('/v1/branding').then(b => { setBName(b.product_name); setBAccent(b.accent); }).catch(() => {}); }, []);

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
            <option value="email">Email</option><option value="slack">Slack channel</option><option value="webhook">Webhook (HTTPS URL)</option>
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
      <div className="card" style={{ maxWidth: 640, margin: '20px 0' }}>
        <b>Branding</b>
        <p className="muted">Product name and accent colour for this tenant. Admins only. The accent must keep white button text readable (contrast 4.5:1).</p>
        <form onSubmit={async e => {
          e.preventDefault(); setBMsg('');
          try { await api('/v1/branding', { method: 'PUT', body: JSON.stringify({ product_name: bName, accent: bAccent }) }); setBMsg('Saved. Reload to apply.'); }
          catch (err) { setBMsg(String(err)); }
        }}>
          <label htmlFor="b-name">Product name</label>
          <input id="b-name" value={bName} maxLength={40} onChange={e => setBName(e.target.value)} placeholder="Hexmon IoT" />
          <label htmlFor="b-accent">Accent colour (#rrggbb)</label>
          <input id="b-accent" value={bAccent} onChange={e => setBAccent(e.target.value)} placeholder="#0071e3" pattern="#[0-9a-fA-F]{6}|" />
          <div style={{ marginTop: 14 }}><button type="submit">Save branding</button></div>
          <label htmlFor="b-logo">Logo (PNG or JPEG, up to 100 KB, 16 to 1024 px)</label>
          <input id="b-logo" type="file" accept="image/png,image/jpeg" onChange={async e => {
            const f = e.target.files?.[0]; if (!f) return;
            const token = localStorage.getItem('iot.token') ?? '';
            const res = await fetch('/v1/branding/logo', { method: 'PUT', body: f, headers: { Authorization: `Bearer ${token}` } });
            setBMsg(res.ok ? 'Logo saved. Reload to apply.' : `${res.status}: ${await res.text()}`);
          }} />
          <button type="button" className="ghost" style={{ marginTop: 8 }} onClick={() => fetch('/v1/branding/logo', { method: 'DELETE', headers: { Authorization: `Bearer ${localStorage.getItem('iot.token') ?? ''}` } }).then(() => setBMsg('Logo removed. Reload to apply.'))}>Remove logo</button>
        </form>
        {bMsg && <p className="muted" role="status">{bMsg}</p>}
      </div>
      <UsageCard />
      <RetentionCard />
      <DirectDevices />
      <FeatureToggles />
      <Authenticator />
      <AttributeDefs />
      <AISettings />
      <AssistantChannels />
      <EdgeRules />
      <ApiKeys />
      <div className="cards">
        <div className="card"><b>Users & roles</b><p className="muted">Admin, operator, installer, viewer. Tenant-scoped; SSO/OIDC before GA.</p></div>
        <div className="card"><b>Gateways</b><p className="muted">Enroll by one-time claim code, rotate certificates, revoke lost hardware.</p></div>
        <div className="card"><b>Audit log</b><p className="muted">Append-only record of every change and control action (admin view via API).</p></div>
      </div>
    </>
  );
}
