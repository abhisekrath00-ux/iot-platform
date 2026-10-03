import { useEffect, useState } from 'react';
import Empty from '../components/Empty';
import { api } from '../lib/api';

interface UserRow { id: string; email: string; display_name: string; role: string; disabled: boolean; has_password: boolean; last_login_at: string | null; customer_id: string | null; }
const ROLES = ['admin', 'operator', 'installer', 'viewer'];
const ROLE_HELP: Record<string, string> = {
  admin: 'Everything, including users, secrets, API keys and customers.',
  operator: 'Day-to-day changes: flows, rules, reports, device setup, control requests and approvals (never your own request).',
  installer: 'Read-only, plus bulk device import.',
  viewer: 'Read-only.',
};

export default function Users() {
  const [rows, setRows] = useState<UserRow[]>([]);
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [role, setRole] = useState('viewer');
  const [password, setPassword] = useState('');
  const [msg, setMsg] = useState('');
  const [invites, setInvites] = useState<{ id: string; email: string; role: string; expires_at: string }[]>([]);
  const [invEmail, setInvEmail] = useState('');
  const [invRole, setInvRole] = useState('viewer');
  const [link, setLink] = useState('');
  const [customers, setCustomers] = useState<{ id: string; name: string }[]>([]);
  const [invCust, setInvCust] = useState('');
  const load = () => {
    api<UserRow[]>('/v1/users').then(setRows).catch(e => setMsg(String(e)));
    api<typeof customers>('/v1/customers').then(setCustomers).catch(() => setCustomers([]));
    api<typeof invites>('/v1/users/invites').then(setInvites).catch(() => setInvites([]));
  };
  useEffect(load, []);
  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setMsg('');
    try { await fn(); setMsg(ok); load(); } catch (e) { setMsg(String(e)); }
  };
  const reset = (u: UserRow) => {
    const pw = window.prompt(`New password for ${u.email} (12 to 128 characters)`);
    if (pw) act(() => api(`/v1/users/${u.id}/password`, { method: 'PUT', body: JSON.stringify({ password: pw }) }), 'Password set.');
  };
  return (
    <>
      <h1>Users</h1>
      <p className="muted">People who can sign in to this workspace. Single sign-on users are matched by email. Passwords only work if the deployment enables local sign-in. Admin session only. You cannot change or disable your own account, and the workspace always keeps one active admin.</p>
      {msg && <p className="muted" role="status">{msg}</p>}
      {rows.length === 0 ? <Empty title="No users" hint="Add the first one below." /> : (
        <table>
          <thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Password</th><th>Last sign-in</th><th>Status</th><th /></tr></thead>
          <tbody>{rows.map(u => (
            <tr key={u.id}>
              <td>{u.display_name}</td><td>{u.email}</td>
              <td><select aria-label={`Role for ${u.email}`} value={u.role} onChange={e => act(() => api(`/v1/users/${u.id}`, { method: 'PUT', body: JSON.stringify({ role: e.target.value }) }), 'Role changed.')}>{ROLES.map(r => <option key={r}>{r}</option>)}</select></td>
              <td>{u.has_password ? 'set' : 'SSO only'}</td>
              <td>{u.last_login_at ? new Date(u.last_login_at).toLocaleString() : 'never'}</td>
              <td>{u.disabled ? <span className="pill">disabled</span> : 'active'}</td>
              <td>
                <button className="ghost" onClick={() => act(() => api(`/v1/users/${u.id}`, { method: 'PUT', body: JSON.stringify({ disabled: !u.disabled }) }), u.disabled ? 'Enabled.' : 'Disabled. Their sessions stop working now.')}>{u.disabled ? 'Enable' : 'Disable'}</button>{' '}
                <button className="ghost" onClick={() => reset(u)}>Set password</button>
              </td>
            </tr>))}</tbody>
        </table>
      )}
      <div className="card" style={{ maxWidth: 640, margin: '20px 0' }}>
        <b>Invite by link</b>
        <p className="muted">Needs local sign-in. The link is shown once and works once for 7 days. Send it through a channel you trust; the platform sends no email.</p>
        <form onSubmit={async e => { e.preventDefault(); setLink(''); try { const j = await api<{ token: string }>('/v1/users/invites', { method: 'POST', body: JSON.stringify({ email: invEmail, role: invRole, customer_id: invCust || null }) }); setLink(`${window.location.origin}/accept-invite#${j.token}`); setInvEmail(''); load(); } catch (er) { setMsg(String(er)); } }}>
          <label htmlFor="iv-email">Email</label>
          <input id="iv-email" type="email" value={invEmail} onChange={e => setInvEmail(e.target.value)} required />
          <label htmlFor="iv-role">Role</label>
          <select id="iv-role" value={invRole} onChange={e => setInvRole(e.target.value)}>{ROLES.map(r => <option key={r}>{r}</option>)}</select>
          <label htmlFor="iv-cust">Limit to a customer (optional)</label>
          <select id="iv-cust" value={invCust} onChange={e => setInvCust(e.target.value)}><option value="">Whole workspace</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
          <button type="submit">Create invitation link</button>
        </form>
        {link && <p><b>Copy now, it is not shown again:</b><br /><code style={{ wordBreak: 'break-all' }}>{link}</code></p>}
        {invites.length > 0 && <table><thead><tr><th>Pending</th><th>Role</th><th>Expires</th><th /></tr></thead><tbody>{invites.map(i => (
          <tr key={i.id}><td>{i.email}</td><td>{i.role}</td><td>{new Date(i.expires_at).toLocaleDateString()}</td>
            <td><button className="ghost" onClick={() => act(() => api(`/v1/users/invites/${i.id}`, { method: 'DELETE' }), 'Invitation revoked.')}>Revoke</button></td></tr>))}</tbody></table>}
      </div>
      <div className="card" style={{ maxWidth: 640, margin: '20px 0' }}>
        <b>Add a user</b>
        <form onSubmit={e => { e.preventDefault(); act(() => api('/v1/users', { method: 'POST', body: JSON.stringify({ email, display_name: name, role, password: password || undefined }) }), 'User added.').then(() => { setEmail(''); setName(''); setPassword(''); }); }}>
          <label htmlFor="us-email">Email</label>
          <input id="us-email" type="email" value={email} onChange={e => setEmail(e.target.value)} required />
          <label htmlFor="us-name">Name</label>
          <input id="us-name" value={name} onChange={e => setName(e.target.value)} required />
          <label htmlFor="us-role">Role</label>
          <select id="us-role" value={role} onChange={e => setRole(e.target.value)}>{ROLES.map(r => <option key={r}>{r}</option>)}</select>
          <p className="muted">{ROLE_HELP[role]}</p>
          <label htmlFor="us-pw">Initial password (optional, only if local sign-in is enabled)</label>
          <input id="us-pw" type="password" autoComplete="new-password" value={password} onChange={e => setPassword(e.target.value)} />
          <button type="submit">Add user</button>
        </form>
      </div>
    </>
  );
}
