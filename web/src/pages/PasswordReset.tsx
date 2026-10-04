import { useState } from 'react';

/** /forgot-password asks for a reset link by email; /reset-password#token sets the new password. Only when the operator configured mail. */
export default function PasswordReset({ mode }: { mode: 'forgot' | 'reset' }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [msg, setMsg] = useState('');
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);
  const token = window.location.hash.replace(/^#/, '');
  const post = async (path: string, body: object) => {
    setMsg(''); setBusy(true);
    try {
      const res = await fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      if (res.status === 404) { setMsg('Password reset by email is not enabled here. Ask your administrator.'); return; }
      if (res.status === 429) { setMsg('Too many attempts. Try again in a few minutes.'); return; }
      if (!res.ok) { setMsg((await res.text()).trim() || 'Something went wrong.'); return; }
      setDone(true);
    } catch { setMsg('Could not reach the server.'); } finally { setBusy(false); }
  };
  return (
    <div style={{ maxWidth: 380, margin: '12vh auto', padding: 16 }}>
      <div className="card">
        <h1 style={{ marginTop: 0 }}>{mode === 'forgot' ? 'Reset your password' : 'Choose a new password'}</h1>
        {done ? (
          <p>{mode === 'forgot' ? 'If that address has an account, a reset link is on its way. It works once and expires in an hour.' : 'Your password was changed. You can sign in now.'} <a href="/">Sign in</a></p>
        ) : mode === 'forgot' ? (
          <form onSubmit={e => { e.preventDefault(); post('/auth/forgot', { email }); }}>
            <label htmlFor="fp-email">Email</label>
            <input id="fp-email" type="email" autoComplete="username" value={email} onChange={e => setEmail(e.target.value)} required />
            {msg && <p className="muted" role="alert">{msg}</p>}
            <button type="submit" disabled={busy} style={{ marginTop: 12 }}>Send reset link</button>
          </form>
        ) : (
          <form onSubmit={e => { e.preventDefault(); post('/auth/reset', { token, password }); }}>
            <label htmlFor="rp-pw">New password (12 to 128 characters)</label>
            <input id="rp-pw" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} required />
            {msg && <p className="muted" role="alert">{msg}</p>}
            <button type="submit" disabled={busy || !token} style={{ marginTop: 12 }}>Set password</button>
          </form>
        )}
      </div>
    </div>
  );
}
