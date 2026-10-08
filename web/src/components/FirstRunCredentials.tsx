import { useState } from 'react';
import { api } from '../lib/api';

/** Blocking pop-up shown while the account still uses the first-run default login. The server also refuses every
 *  other request until this succeeds, so closing or hiding this dialog does not open anything up. */
export default function FirstRunCredentials({ email }: { email?: string }) {
  const [current, setCurrent] = useState('');
  const [newEmail, setNewEmail] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [msg, setMsg] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (pw !== pw2) { setMsg('The two new passwords do not match.'); return; }
    setMsg(''); setBusy(true);
    try {
      const r = await api<{ token: string }>('/v1/me/credentials', { method: 'POST', body: JSON.stringify({ current, new_email: newEmail.trim(), new: pw }) });
      localStorage.setItem('iot.token', r.token);
      window.location.assign('/');
    } catch (err) {
      const t = String((err as Error).message).replace(/^\d+:\s*/, '');
      setMsg(t || 'Could not change the credentials.');
    } finally { setBusy(false); }
  };
  return (
    <div role="dialog" aria-modal="true" aria-labelledby="frc-title" style={{ position: 'fixed', inset: 0, zIndex: 100, background: 'rgba(0,0,0,0.65)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }}>
      <form className="card" onSubmit={submit} style={{ width: 'min(440px, 100%)' }}>
        <h2 id="frc-title" style={{ marginTop: 0 }}>Set your own sign-in</h2>
        <p className="muted">You signed in with the default login{email ? ` (${email})` : ''}. Choose your own email and password before you continue. Nothing else works until you do.</p>
        <label htmlFor="frc-cur">Current (default) password</label>
        <input id="frc-cur" type="password" autoComplete="current-password" value={current} onChange={e => setCurrent(e.target.value)} required />
        <label htmlFor="frc-email">New email (your sign-in id)</label>
        <input id="frc-email" type="email" autoComplete="username" value={newEmail} onChange={e => setNewEmail(e.target.value)} required />
        <label htmlFor="frc-pw">New password (12 to 128 characters)</label>
        <input id="frc-pw" type="password" autoComplete="new-password" value={pw} onChange={e => setPw(e.target.value)} minLength={12} maxLength={128} required />
        <label htmlFor="frc-pw2">Repeat new password</label>
        <input id="frc-pw2" type="password" autoComplete="new-password" value={pw2} onChange={e => setPw2(e.target.value)} required />
        {msg && <p role="alert" style={{ color: '#dc2626' }}>{msg}</p>}
        <button type="submit" disabled={busy} style={{ marginTop: 12 }}>Save and continue</button>
      </form>
    </div>
  );
}
