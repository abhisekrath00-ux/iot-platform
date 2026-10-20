import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Session { id: string; created_at: string; last_seen_at: string; ip: string; user_agent: string; current: boolean; }

// Your active sign-ins (this browser and any other). Revoking one signs that
// device out at once; "Sign out everywhere" in the menu ends them all.
export default function Sessions() {
  const [rows, setRows] = useState<Session[] | null>(null);
  const [msg, setMsg] = useState('');
  const load = () => api<Session[]>('/v1/me/sessions').then(setRows).catch(() => setRows([]));
  useEffect(() => { load(); }, []);
  async function revoke(s: Session) {
    setMsg('');
    try {
      await api(`/v1/me/sessions/${s.id}`, { method: 'DELETE' });
      if (s.current) { localStorage.removeItem('iot.token'); window.location.assign('/'); return; }
      setMsg('That device was signed out.');
      load();
    } catch (e) { setMsg(String(e)); }
  }
  if (!rows || rows.length === 0) return null; // API keys, the assistant and pre-feature tokens have no list
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>Your sessions</b>
      <p className="muted">Every place this account is signed in. Revoking a device signs it out at once.</p>
      {msg && <p className="muted">{msg}</p>}
      <table style={{ marginTop: 8 }}>
        <thead><tr><th>Signed in</th><th>Last active</th><th>Address</th><th></th></tr></thead>
        <tbody>
          {rows.map(s => (
            <tr key={s.id}>
              <td>{new Date(s.created_at).toLocaleString()}{s.current && <span className="pill ok" style={{ marginLeft: 8 }}>this device</span>}</td>
              <td className="muted">{new Date(s.last_seen_at).toLocaleString()}</td>
              <td className="muted" style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={s.user_agent}>{s.ip || s.user_agent || '-'}</td>
              <td><button className="ghost" onClick={() => revoke(s)}>{s.current ? 'Sign out' : 'Revoke'}</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
