import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface CS { slack_enabled: boolean; email_enabled: boolean; has_slack_secret: boolean; has_email_secret: boolean; secrets_available: boolean; slack_url: string; email_url: string; }
interface Link { id: string; user_id: string; kind: string; address: string; status: string; autorun_low_risk: boolean; }

// Message the assistant from Slack (direct messages) or email. Each person links their own Slack
// member id or mailbox with a one-time code. Admins switch the channels on and set the secrets.
export default function AssistantChannels() {
  const [cs, setCs] = useState<CS | null>(null);
  const [links, setLinks] = useState<Link[]>([]);
  const [slackOn, setSlackOn] = useState(false);
  const [emailOn, setEmailOn] = useState(false);
  const [slackSec, setSlackSec] = useState('');
  const [mailSec, setMailSec] = useState('');
  const [kind, setKind] = useState('slack');
  const [addr, setAddr] = useState('');
  const [msg, setMsg] = useState('');
  const [selftest, setSelftest] = useState<{ kind: string; curl_valid: string; curl_tampered: string; expect_ok: string; expect_tampered: string; expires: string; note: string } | null>(null);
  const loadLinks = () => api<Link[]>('/v1/assistant/links').then(setLinks).catch(() => undefined);
  const loadCs = () => { api<CS>('/v1/assistant/channel-settings').then(r => { setCs(r); setSlackOn(r.slack_enabled); setEmailOn(r.email_enabled); }).catch(() => undefined); };
  const admin = cs !== null; // only admins can read the channel settings
  useEffect(() => { loadLinks(); loadCs(); }, []);
  async function saveCs() {
    setMsg('');
    try {
      await api('/v1/assistant/channel-settings', { method: 'PUT', body: JSON.stringify({ slack_enabled: slackOn, email_enabled: emailOn, ...(slackSec ? { slack_signing_secret: slackSec } : {}), ...(mailSec ? { email_inbound_secret: mailSec } : {}) }) });
      setSlackSec(''); setMailSec(''); setMsg('Saved.'); loadCs();
    } catch (e) { setMsg(String(e)); }
  }
  async function runSelftest(k: string) {
    setMsg('');
    try { setSelftest(await api('/v1/assistant/selftest/' + k)); }
    catch (e) { setSelftest(null); setMsg(String(e)); }
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
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8 }}><input type="checkbox" style={{ width: 'auto', flex: 'none' }} checked={slackOn} onChange={e => setSlackOn(e.target.checked)} /> Slack</label>
          <div className="muted" style={{ fontSize: 12, margin: '2px 0 6px 26px', overflowWrap: 'anywhere' }}>Events API URL: <code>{cs.slack_url}</code></div>
          <input type="password" autoComplete="off" aria-label="Slack signing secret" placeholder={cs.has_slack_secret ? 'Slack signing secret stored; leave empty to keep' : 'Slack app signing secret'} value={slackSec} onChange={e => setSlackSec(e.target.value)} disabled={!cs.secrets_available} />
          <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8 }}><input type="checkbox" style={{ width: 'auto', flex: 'none' }} checked={emailOn} onChange={e => setEmailOn(e.target.checked)} /> Email</label>
          <div className="muted" style={{ fontSize: 12, margin: '2px 0 6px 26px', overflowWrap: 'anywhere' }}>Gateway posts to: <code>{cs.email_url}</code></div>
          <input type="password" autoComplete="off" aria-label="Email gateway secret" placeholder={cs.has_email_secret ? 'Gateway secret stored; leave empty to keep' : 'Shared secret for the gateway HMAC'} value={mailSec} onChange={e => setMailSec(e.target.value)} disabled={!cs.secrets_available} />
          <button onClick={saveCs} style={{ marginTop: 8 }}>Save channels</button>
          <details style={{ marginTop: 10 }}>
            <summary>Slack setup steps (needs internet)</summary>
            <ol className="muted" style={{ margin: '6px 0 4px 18px', padding: 0 }}>
              <li>Create the app at api.slack.com/apps (from scratch, your workspace).</li>
              <li>Copy its Signing Secret (Basic Information) into the field above and Save.</li>
              <li>Add bot scopes chat:write, im:history, im:read, im:write (OAuth & Permissions).</li>
              <li>Event Subscriptions: Request URL = your server's public address + <code>{cs.slack_url}</code>; subscribe to the bot event message.im. Do step 2 first - Slack's URL check is only answered when the signature matches.</li>
              <li>Install the app, copy the Bot User OAuth Token (xoxb-...) to your platform operator for SLACK_BOT_TOKEN on the server. Without it the assistant reads but cannot reply.</li>
              <li>Each person: Link me below with their Slack member id (Slack profile, ..., Copy member ID), then DM the shown code to the bot within 15 minutes.</li>
            </ol>
            <p className="muted" style={{ margin: '0 0 6px' }}>Only direct-message events are accepted; anything posted to a channel is ignored. The in-product assistant has the full setup guide - ask it about any step.</p>
          </details>
          <details style={{ marginTop: 6 }}>
            <summary>Email setup steps (works on a LAN with your own mail gateway)</summary>
            <ol className="muted" style={{ margin: '6px 0 4px 18px', padding: 0 }}>
              <li>Your mail gateway receives the assistant's mail, checks DKIM and SPF, and POSTs each message as signed JSON to your server's address + <code>{cs.email_url}</code>. The gateway itself is not part of the install.</li>
              <li>Choose a shared secret with the gateway and paste it above, tick Email, Save.</li>
              <li>Gateway contract: JSON with message_id, from, subject, text, dkim, spf; headers X-Timestamp and X-Signature = hex HMAC-SHA256(secret, timestamp + "." + raw body). Messages are dropped unless the signature matches, the timestamp is fresh, the id is new, and both dkim and spf pass.</li>
              <li>Outbound replies use the server's SMTP settings, so those must work too.</li>
              <li>Each person: Link me below with their email address, then send the shown code FROM that mailbox within 15 minutes.</li>
            </ol>
            <p className="muted" style={{ margin: '0 0 6px' }}>The in-product assistant has the full setup guide - ask it about any step.</p>
          </details>
          <div style={{ marginTop: 10 }}>
            <span className="muted" style={{ fontSize: 12 }}>Check the signing path before calling Slack or your gateway: the server builds a signed sample request with your stored secret (the secret never leaves the server). Run it against your server address. This proves the server checks signatures correctly; it does not prove Slack or your gateway can actually deliver messages.</span>
            <div style={{ display: 'flex', gap: 8, marginTop: 6, flexWrap: 'wrap' }}>
              {cs.has_slack_secret && <button type="button" onClick={() => runSelftest('slack')}>Check Slack signing</button>}
              {cs.has_email_secret && <button type="button" onClick={() => runSelftest('email')}>Check email signing</button>}
            </div>
            {selftest && <div style={{ marginTop: 8 }}>
              <p className="muted" style={{ margin: '0 0 4px', fontSize: 12 }}>{selftest.expect_ok}</p>
              <pre aria-label="Valid signed self-test command" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', fontSize: 11, margin: 0 }}>{selftest.curl_valid}</pre>
              <p className="muted" style={{ margin: '6px 0 4px', fontSize: 12 }}>{selftest.expect_tampered}</p>
              <pre aria-label="Tampered self-test command" style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all', fontSize: 11, margin: 0 }}>{selftest.curl_tampered}</pre>
              <p className="muted" style={{ margin: '4px 0 0', fontSize: 12 }}>{selftest.expires} {selftest.note}</p>
            </div>}
          </div>
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
