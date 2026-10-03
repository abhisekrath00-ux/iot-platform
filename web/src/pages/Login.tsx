import { useState } from 'react';

/** Local sign-in (only when the deployment sets LOCAL_LOGIN=1) with a link to SSO. */
export default function Login() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setMsg(''); setBusy(true);
    try {
      const res = await fetch('/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email, password }) });
      if (res.status === 404) { setMsg('Password sign-in is not enabled here. Use single sign-on.'); return; }
      if (res.status === 429) { setMsg('Too many attempts. Try again in a few minutes.'); return; }
      if (!res.ok) { setMsg('Invalid email or password.'); return; }
      const { token } = await res.json();
      localStorage.setItem('iot.token', token);
      window.location.assign('/');
    } catch { setMsg('Could not reach the server.'); } finally { setBusy(false); }
  };
  return (
    <div style={{ maxWidth: 380, margin: '12vh auto', padding: 16 }}>
      <div className="card">
        <h1 style={{ marginTop: 0 }}>Sign in</h1>
        <form onSubmit={submit}>
          <label htmlFor="li-email">Email</label>
          <input id="li-email" type="email" autoComplete="username" value={email} onChange={e => setEmail(e.target.value)} required />
          <label htmlFor="li-pw">Password</label>
          <input id="li-pw" type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} required />
          {msg && <p className="muted" role="alert">{msg}</p>}
          <button type="submit" disabled={busy}>Sign in</button>
        </form>
        <p className="muted" style={{ marginBottom: 0 }}><a href="/auth/oidc/login">Sign in with single sign-on</a></p>
      </div>
    </div>
  );
}
