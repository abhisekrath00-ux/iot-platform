import { useEffect, useState } from 'react';
import { api } from '../lib/api';

type Flags = { function_nodes: boolean; http_nodes: boolean; require_totp_approval: boolean };

const ITEMS: { key: keyof Flags; title: string; hint: string }[] = [
  { key: 'function_nodes', title: 'Function nodes (JavaScript)', hint: 'Sandboxed JavaScript in flows (50 ms and memory limits). Admins only can save flows that use it.' },
  { key: 'http_nodes', title: 'HTTP request nodes', hint: 'Flows may call fixed HTTP(S) addresses and use the answer. Loopback and metadata addresses stay blocked; private networks are reachable. Admins only can save flows that use it.' },
  { key: 'require_totp_approval', title: 'Authenticator code to approve control commands', hint: 'Approvers enter a one-time code from their authenticator app (enroll under Settings). Sign-in itself is by your SSO provider.' },
];

// Per-tenant switches for flow nodes that reach outside the platform. Off by default; admin only.
export default function FeatureToggles() {
  const [f, setF] = useState<Flags | null>(null);
  const [msg, setMsg] = useState('');
  useEffect(() => { api<Flags>('/v1/features').then(setF).catch(e => setMsg(String(e))); }, []);
  async function set(k: keyof Flags, enabled: boolean) {
    setMsg('');
    try {
      await api(`/v1/features/${k}`, { method: 'PUT', body: JSON.stringify({ enabled }) });
      setF(p => (p ? { ...p, [k]: enabled } : p));
    } catch (e) { setMsg(String(e)); }
  }
  if (!f) return msg ? <p className="muted">{msg}</p> : null;
  return (
    <div className="card" style={{ maxWidth: 760, marginBottom: 20 }} role="region" aria-label="Flow node features">
      <b>Flow node features</b>
      <p className="muted">Switches for flow nodes that run code or reach other systems. Changing one is recorded in the audit log. Admins only.</p>
      {ITEMS.map(i => (
        <label key={i.key} style={{ display: 'block', marginBottom: 8 }}>
          <input type="checkbox" checked={f[i.key]} onChange={e => set(i.key, e.target.checked)} style={{ width: 'auto', marginRight: 8 }} />
          {i.title} <span className="muted">- {i.hint}</span>
        </label>
      ))}
      {msg && <p className="muted" role="alert">{msg}</p>}
    </div>
  );
}
