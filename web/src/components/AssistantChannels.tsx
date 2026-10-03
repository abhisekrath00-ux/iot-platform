import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface CS { slack_enabled: boolean; email_enabled: boolean; has_slack_secret: boolean; has_email_secret: boolean; secrets_available: boolean; slack_url: string; email_url: string; }
interface Link { id: string; user_id: string; kind: string; address: string; status: string; autorun_low_risk: boolean; }

// Message the assistant from Slack (direct messages) or email. Each person links their own Slack
// member id or mailbox with a one-time code. Admins switch the channels on and set the secrets.
export default function AssistantChannels({ admin }: { admin: boolean }) {
  const [cs, setCs] = useState<CS | null>(null);
  const [links, setLinks] = useState<Link[]>([]);
  const [slackOn, setSlackOn] = useState(false);
  const [emailOn, setEmailOn] = useState(false);
  const [slackSec, setSlackSec] = useState('');
  const [mailSec, setMailSec] = useState('');
  const [kind, setKind] = useState('slack');
  const [addr, setAddr] = useState('');
  const [msg, setMsg] = useState('');
  const loadLinks = () => api<Link[]>('/v1/assistant/links').then(setLinks).catch(() => undefined);
  const loadCs = () => { if (admin) api<CS>('/v1/assistant/channel-settings').then(r => { setCs(r); setSlackOn(r.slack_enabled); setEmailOn(r.email_enabled); }).catch(() => undefined); };
  useEffect(() => { loadLinks(); loadCs(); }, [admin]);
  async function saveCs() {
    setMsg('');
    try {
      await api('/v1/assistant/channel-settings', { method: 'PUT', body: JSON.stringify({ slack_enabled: slackOn, email_enabled: emailOn, ...(slackSec ? { slack_signing_secret: slackSec } : {}), ...(mailSec ? { email_inbound_secret: mailSec } : {}) }) });
      setSlackSec(''); setMailSec(''); setMsg('Saved.'); loadCs();
    } catch (e) { setMsg(String(e)); }
  }
  async function start(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      const r = await api<{ next: string; expires_in_minutes: number }>('/v1/assistant/links', { method: 'POST', body: JSON.stringify({ kind, address: addr }) });
      setMsg(`${r.next} (valid ${r.expires_in_minutes} minutes, shown once)`); setAddr(''); loadLinks();
    } catch (e2) { setMsg(String(e2)); }
  }
  const del = (id: string) => api(`/v1/assistant/links/${id}`, { method: 'DELETE' }).then(loadLinks).catch(e => setMsg(String(e)));
  const autorun = (l: Link) => api(`/v1/assistant/links/${l.id}/autorun`, { method: 'PUT', body: JSON.stringify({ enabled: !l.autorun_low_risk }) }).then(loadLinks).catch(e => setMsg(String(e)));
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>Assistant in Slack and email</b>
      <p className="muted">Message the assistant and it does the task, with your permissions. Changes wait for you to reply YES. It cannot approve control commands from chat. Slack works in direct messages only. Email needs a mail gateway that signs each message and reports DKIM and SPF, so a forged From line is ignored.</p>
      {admin && cs && (
        <div style={{ marginBottom: 14 }}>
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8 }}><input type="checkbox" style={{ width: 'auto', flex: 'none' }} checked={slackOn} onChange={e => setSlackOn(e.target.checked)} /> Slack (Events API URL: <code>{cs.slack_url}</code>)</label>
          <input type="password" autoComplete="off" aria-label="Slack signing secret" placeholder={cs.has_slack_secret ? 'Slack signing secret stored; leave empty to keep' : 'Slack app signing secret'} value={slackSec} onChange={e => setSlackSec(e.target.value)} disabled={!cs.secrets_available} />
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8 }}><input type="checkbox" style={{ width: 'auto', flex: 'none' }} checked={emailOn} onChange={e => setEmailOn(e.target.checked)} /> Email (gateway posts to <code>{cs.email_url}</code>)</label>
          <input type="password" autoComplete="off" aria-label="Email gateway secret" placeholder={cs.has_email_secret ? 'Gateway secret stored; leave empty to keep' : 'Shared secret for the gateway HMAC'} value={mailSec} onChange={e => setMailSec(e.target.value)} disabled={!cs.secrets_available} />
          <button onClick={saveCs} style={{ marginTop: 8 }}>Save channels</button>
        </div>
      )}
      <form onSubmit={start} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <select aria-label="Channel" value={kind} onChange={e => setKind(e.target.value)} style={{ width: 110, flex: 'none' }}><option value="slack">Slack</option><option value="email">Email</option></select>
        <input aria-label="Slack member id or email" placeholder={kind === 'slack' ? 'Slack member id, e.g. U01ABCDEF' : 'you@company.com'} value={addr} onChange={e => setAddr(e.target.value)} />
        <button type="submit" disabled={!addr.trim()}>Link me</button>
      </form>
      {links.length > 0 && (
        <table style={{ marginTop: 10 }}><thead><tr><th>Channel</th><th>Address</th><th>User</th><th>Status</th>{admin && <th>Run low-risk without YES</th>}<th /></tr></thead>
          <tbody>{links.map(l => (
            <tr key={l.id}><td>{l.kind}</td><td>{l.address}</td><td>{l.user_id}</td><td>{l.status}</td>
              {admin && <td>{l.status === 'verified' ? <label><input type="checkbox" style={{ width: 'auto' }} checked={l.autorun_low_risk} onChange={() => autorun(l)} /> ack/comment on alerts</label> : '-'}</td>}
              <td><button className="ghost" onClick={() => del(l.id)}>Remove</button></td></tr>
          ))}</tbody></table>
      )}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
