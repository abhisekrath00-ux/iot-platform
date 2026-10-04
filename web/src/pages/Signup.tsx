import { useState } from 'react';

/** Create a workspace and become its first admin. Only when the operator turned on SELF_SIGNUP. */
export default function Signup() {
  const [workspace, setWorkspace] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(''); setBusy(true);
    try {
      const res = await fetch('/auth/signup', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ workspace, display_name: name, email, password }) });
      if (res.status === 404) { setMsg('Sign-up is not enabled here. Ask your administrator for an invitation.'); return; }
      if (res.status === 429) { setMsg('Too many attempts. Try again in a few minutes.'); return; }
      if (!res.ok) { setMsg((await res.text()).trim() || 'Could not create the workspace.'); return; }
      const { token } = await res.json();
      localStorage.setItem('iot.token', token);
      window.location.assign('/');
    } catch { setMsg('Could not reach the server.'); } finally { setBusy(false); }
  };
  return (
    <div style={{ maxWidth: 400, margin: '10vh auto', padding: 16 }}>
      <div className="card">
        <h1 style={{ marginTop: 0 }}>Create a workspace</h1>
        <form onSubmit={submit}>
          <label htmlFor="su-ws">Workspace name</label>
          <input id="su-ws" value={workspace} onChange={e => setWorkspace(e.target.value)} maxLength={128} required />
          <label htmlFor="su-name">Your name</label>
          <input id="su-name" value={name} onChange={e => setName(e.target.value)} maxLength={128} />
          <label htmlFor="su-email">Email</label>
          <input id="su-email" type="email" autoComplete="username" value={email} onChange={e => setEmail(e.target.value)} required />
          <label htmlFor="su-pw">Password (12 to 128 characters)</label>
          <input id="su-pw" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
          {msg && <p className="muted" role="alert">{msg}</p>}
          <button type="submit" disabled={busy} style={{ marginTop: 12 }}>Create workspace</button>
        </form>
        <p className="muted" style={{ marginBottom: 0 }}>Your email is not verified. You become the workspace admin. <a href="/">Back to sign in</a></p>
      </div>
    </div>
  );
}
