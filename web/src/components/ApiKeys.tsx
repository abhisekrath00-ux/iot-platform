import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Key { id: string; name: string; role: string; expires_at: string; last_used_at: string | null; revoked_at: string | null; scopes?: string[]; }

const SCOPES = ['devices', 'telemetry', 'alerts', 'dashboards', 'reports', 'flows', 'rules', 'points', 'profiles', 'search', 'export'];

export default function ApiKeys() {
  const [keys, setKeys] = useState<Key[]>([]);
  const [name, setName] = useState('');
  const [role, setRole] = useState('viewer');
  const [days, setDays] = useState(90);
  const [scopes, setScopes] = useState<string[]>([]);
  const [token, setToken] = useState('');
  const [msg, setMsg] = useState('');

  const load = () => api<Key[]>('/v1/api-keys').then(setKeys).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      const r = await api<{ token: string }>('/v1/api-keys', { method: 'POST', body: JSON.stringify({ name, role, expires_in_days: days, scopes }) });
      setToken(r.token); setName(''); setScopes([]); load();
    } catch (e2) { setMsg(String(e2)); }
  }
  const revoke = (id: string) => api(`/v1/api-keys/${id}`, { method: 'DELETE' }).then(load).catch(e => setMsg(String(e)));

  return (
    <div className="card" style={{ maxWidth: 720, marginBottom: 20 }}>
      <b>API keys</b>
      <p className="muted">For SCADA, BI and scripts. Keys are read-only or operator, expire, and can be revoked. They never get admin and cannot manage other keys. Send as <code>Authorization: Bearer hxk_…</code>.</p>
      {token && (
        <div className="card" style={{ margin: '12px 0' }}>
          <b>Copy this key now. It is shown once.</b>
          <pre style={{ overflowX: 'auto', margin: '8px 0' }}>{token}</pre>
          <button className="ghost" onClick={() => setToken('')}>I have saved it</button>
        </div>
      )}
      <form onSubmit={create} style={{ display: 'flex', gap: 8, flexWrap: 'wrap', margin: '12px 0' }}>
        <input required maxLength={80} placeholder="Key name, e.g. SCADA historian" value={name} onChange={e => setName(e.target.value)} />
        <select value={role} onChange={e => setRole(e.target.value)}><option value="viewer">viewer (read-only)</option><option value="operator">operator</option></select>
        <input type="number" min={1} max={365} style={{ width: 90 }} value={days} onChange={e => setDays(Number(e.target.value))} title="Days until expiry" />
        <button type="submit">Create key</button>
        <div style={{ flexBasis: '100%' }}>
          <span className="muted" style={{ fontSize: 12 }}>Limit to endpoints (none checked = every endpoint the role allows):</span>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 14px', marginTop: 4 }}>
            {SCOPES.map(sc => (
              <label key={sc} style={{ display: 'flex', gap: 6, alignItems: 'center', margin: 0, textTransform: 'none', letterSpacing: 0, fontWeight: 500 }}>
                <input type="checkbox" style={{ width: 'auto' }} checked={scopes.includes(sc)}
                  onChange={e => setScopes(e.target.checked ? [...scopes, sc] : scopes.filter(x => x !== sc))} />{sc}
              </label>
            ))}
          </div>
        </div>
      </form>
      {msg && <p className="muted">{msg}</p>}
      {keys.length === 0 ? <p className="muted">No keys yet.</p> : (
        <table>
          <thead><tr><th>Name</th><th>Role</th><th>Scope</th><th>Expires</th><th>Last used</th><th></th></tr></thead>
          <tbody>
            {keys.map(k => (
              <tr key={k.id}>
                <td>{k.name}</td><td>{k.role}</td><td className="muted">{k.scopes && k.scopes.length ? k.scopes.join(', ') : 'all'}</td>
                <td className="muted">{new Date(k.expires_at).toLocaleDateString()}</td>
                <td className="muted">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : 'never'}</td>
                <td>{k.revoked_at ? <span className="muted">revoked</span> : <button className="ghost" onClick={() => revoke(k.id)}>Revoke</button>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
