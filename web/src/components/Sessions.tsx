import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Session { id: string; created_at: string; last_seen_at: string; ip: string; user_agent: string; current: boolean; }

// Your tracked sign-ins (this browser and any other since sessions were
// tracked). Revoking one signs that device out at once; "Sign out everywhere"
// in the menu ends them all, including any not listed.
export default function Sessions() {
  const [rows, setRows] = useState<Session[] | null>(null);
  const [scoped, setScoped] = useState(false);
  const [msg, setMsg] = useState('');
  const load = () => api<Session[]>('/v1/me/sessions').then(setRows).catch(() => setRows([]));
  useEffect(() => {
    load();
    api<{ customer_id?: string }>('/v1/me').then(m => setScoped(!!m.customer_id)).catch(() => undefined);
  }, []);
  async function revoke(s: Session) {
    setMsg('');
    try {
      await api(`/v1/me/sessions/${s.id}`, { method: 'DELETE' });
      if (s.current) { localStorage.removeItem('iot.token'); window.location.assign('/'); return; }
      setMsg('That device was signed out.');
      load();
    } catch (e) { setMsg(String(e)); }
  }
  if (!rows || rows.length === 0) return null; // API keys, the assistant and pre-tracking tokens have no list
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>Your sessions</b>
      <p className="muted">Sign-ins since sessions were tracked. Older sign-ins are not listed: they end within 12 hours of signing in, or at once with Sign out everywhere in the menu. Revoking a listed device signs it out at once.</p>
      {msg && <p className="muted">{msg}</p>}
      <table style={{ marginTop: 8 }}>
        <thead><tr><th>Signed in</th><th>Last active</th><th>Address</th><th></th></tr></thead>
        <tbody>
          {rows.map(s => (
            <tr key={s.id}>
              <td>{new Date(s.created_at).toLocaleString()}{s.current && <span className="pill ok" style={{ marginLeft: 8 }}>this device</span>}</td>
              <td className="muted">{new Date(s.last_seen_at).toLocaleString()}</td>
              <td className="muted" style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={s.user_agent}>{s.ip || s.user_agent || '-'}</td>
              <td>{!scoped && <button className="ghost" onClick={() => revoke(s)}>{s.current ? 'Sign out' : 'Revoke'}</button>}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {scoped && <p className="muted">Per-device sign-out is not available to customer-scoped accounts; changing your password ends every session.</p>}
    </div>
  );
}
