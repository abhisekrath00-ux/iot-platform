import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface St { enrolled: boolean; required_for_approval: boolean; available: boolean; }

// Your own authenticator app, used when your workspace requires a code to approve control commands.
export default function Authenticator() {
  const [st, setSt] = useState<St | null>(null);
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState('');
  const [msg, setMsg] = useState('');
  const load = () => api<St>('/v1/me/totp').then(setSt).catch(() => undefined);
  useEffect(() => { load(); }, []);
  async function begin() { setMsg(''); try { setSetup(await api('/v1/me/totp/begin', { method: 'POST' })); } catch (e) { setMsg(String(e)); } }
  async function confirm(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try { await api('/v1/me/totp/confirm', { method: 'POST', body: JSON.stringify({ code }) }); setSetup(null); setCode(''); setMsg('Authenticator is on.'); load(); } catch (e2) { setMsg(String(e2)); }
  }
  async function remove(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try { await api('/v1/me/totp', { method: 'DELETE', body: JSON.stringify({ code }) }); setCode(''); setMsg('Removed.'); load(); } catch (e2) { setMsg(String(e2)); }
  }
  if (!st) return null;
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>Authenticator app (second factor)</b>
      <p className="muted">{st.required_for_approval ? 'Your workspace requires a code to approve control commands.' : 'Once on, it is also asked for when you sign in with a password. Optional for control approval unless an admin switches on "Authenticator code to approve control commands".'}</p>
      {!st.available && <p className="muted">The server has no SECRETS_KEY, so this cannot be stored.</p>}
      {st.enrolled ? (
        <form onSubmit={remove} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span className="pill ok">on</span>
          <input aria-label="Current code" inputMode="numeric" maxLength={7} placeholder="current 6-digit code to remove" value={code} onChange={e => setCode(e.target.value)} style={{ maxWidth: 240 }} />
          <button className="ghost" type="submit" disabled={code.trim().length < 6}>Remove</button>
        </form>
      ) : setup ? (
        <form onSubmit={confirm}>
          <p className="muted">In your authenticator app add a key manually (time-based, 6 digits, SHA-1), or open the link on a phone.</p>
          <p><code style={{ userSelect: 'all' }}>{setup.secret}</code></p>
          <p className="muted" style={{ fontSize: 12, wordBreak: 'break-all' }}>{setup.uri}</p>
          <div style={{ display: 'flex', gap: 8 }}>
            <input aria-label="Code from the app" inputMode="numeric" maxLength={7} placeholder="6-digit code" value={code} onChange={e => setCode(e.target.value)} style={{ maxWidth: 160 }} />
            <button type="submit" disabled={code.trim().length < 6}>Confirm</button>
          </div>
        </form>
      ) : <button onClick={begin} disabled={!st.available}>Set up authenticator</button>}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
