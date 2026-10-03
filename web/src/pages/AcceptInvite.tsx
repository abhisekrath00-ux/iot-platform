import { useState } from 'react';

/** Invitation link landing page. The token is in the URL fragment, so it never reaches server logs. */
export default function AcceptInvite() {
  const token = window.location.hash.slice(1);
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [msg, setMsg] = useState('');
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg('');
    try {
      const res = await fetch('/auth/accept-invite', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token, display_name: name, password }) });
      if (!res.ok) { setMsg(res.status === 404 ? 'Invitations are not enabled here.' : (await res.text()).trim() || 'This invitation is invalid or has expired.'); return; }
      const j = await res.json();
      localStorage.setItem('iot.token', j.token);
      window.location.assign('/');
    } catch { setMsg('Could not reach the server.'); }
  };
  return (
    <div style={{ maxWidth: 380, margin: '12vh auto', padding: 16 }}>
      <div className="card">
        <h1 style={{ marginTop: 0 }}>Join this workspace</h1>
        <form onSubmit={submit}>
          <label htmlFor="ai-name">Your name</label>
          <input id="ai-name" value={name} onChange={e => setName(e.target.value)} required />
          <label htmlFor="ai-pw">Choose a password (12 to 128 characters)</label>
          <input id="ai-pw" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
          {msg && <p className="muted" role="alert">{msg}</p>}
          <button type="submit" disabled={!token}>Create account</button>
        </form>
      </div>
    </div>
  );
}
