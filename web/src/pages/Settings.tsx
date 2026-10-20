import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import ApiKeys from '../components/ApiKeys';
import RetentionCard from '../components/RetentionCard';
import UsageCard from '../components/UsageCard';
import DirectDevices from '../components/DirectDevices';
import EdgeRules from '../components/EdgeRules';
import FeatureToggles from '../components/FeatureToggles';
import Authenticator from '../components/Authenticator';
import Sessions from '../components/Sessions';
import AttributeDefs from '../components/AttributeDefs';
import AISettings from '../components/AISettings';
import ServerAddresses from '../components/ServerAddresses';
import AIProfiles from '../components/AIProfiles';
import AIActivity from '../components/AIActivity';
import AssistantChannels from '../components/AssistantChannels';
import { ChannelGuide, targetHint } from '../components/ChannelGuide';

interface Channel { id: string; type: string; target: string; enabled: boolean; }

export default function Settings() {
  const [aiRev, setAiRev] = useState(0);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [type, setType] = useState('email');
  const [target, setTarget] = useState('');
  const [msg, setMsg] = useState('');
  const [tgChats, setTgChats] = useState<{ id: string; type: string; title: string }[] | null>(null);
  const [tgMsg, setTgMsg] = useState('');
  const [tgCode, setTgCode] = useState('');
  const [bName, setBName] = useState('');
  const [bAccent, setBAccent] = useState('');
  const [bMsg, setBMsg] = useState('');

  const load = () => api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(e => setMsg(String(e)));
  useEffect(() => { load(); api<{ product_name: string; accent: string }>('/v1/branding').then(b => { setBName(b.product_name); setBAccent(b.accent); }).catch(() => {}); }, []);

  async function testCh(c: Channel) {
    setMsg(`Sending a test to ${c.type} ${c.target}...`);
    try {
      const r = await api<{ ok: boolean; error?: string }>(`/v1/notifications/channels/${c.id}/test`, { method: 'POST' });
      setMsg(r.ok ? `Test sent to ${c.target}. Check that it arrived.` : `Test failed: ${r.error}`);
    } catch (e) { setMsg(String(e)); }
  }
  async function toggleCh(c: Channel) {
    try { await api(`/v1/notifications/channels/${c.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: !c.enabled }) }); load(); } catch (e) { setMsg(String(e)); }
  }
  async function delCh(c: Channel) {
    if (!window.confirm(`Delete the ${c.type} channel ${c.target}?`)) return;
    try { await api(`/v1/notifications/channels/${c.id}`, { method: 'DELETE' }); setMsg('Channel deleted.'); load(); } catch (e) { setMsg(String(e)); }
  }

  function friendlyTgError(e: unknown): string {
    const m = String(e);
    if (m.includes('429') || m.includes('wait a few seconds')) return 'One moment - wait about 10 seconds, then press Find chats again.';
    if (m.includes('409') || m.includes('not configured')) return 'Telegram is not configured on this deployment yet. Your platform operator sets TELEGRAM_BOT_TOKEN on the server.';
    return m;
  }
  async function getPairCode() {
    setTgMsg(''); setTgCode('');
    try {
      const r = await api<{ code: string; expires_in_minutes: number }>('/v1/notifications/telegram/pair-code', { method: 'POST' });
      setTgCode(r.code);
    } catch (e) { setTgMsg(friendlyTgError(e)); }
  }
  async function findTgChats() {
    setTgMsg('Checking for your code...'); setTgChats(null);
    try {
      const r = await api<{ chats: { id: string; type: string; title: string }[]; error?: string }>('/v1/notifications/telegram/chats');
      if (r.error) { setTgMsg(friendlyTgError(r.error)); return; }
      setTgChats(r.chats);
      if (r.chats.length === 0) {
        setTgMsg(tgCode
          ? 'Nothing paired yet. Send /pair ' + tgCode + ' to the bot in your chat, wait a few seconds, then press Find chats again.'
          : 'No chats paired yet. Press Get pairing code first, then send the code to the bot inside your chat.');
      } else { setTgMsg(''); setTgCode(''); }
    } catch (e) { setTgMsg(friendlyTgError(e)); }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/notifications/channels', { method: 'POST', body: JSON.stringify({ type, target }) });
      setTarget('');
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Settings</h1>
      <div className="card" style={{ maxWidth: 760, marginBottom: 20 }}>
        <b>Notification channels</b>
        <p className="muted">Alerts from your rules go to these destinations. Email uses your SMTP server (deployment env), Slack uses a bot token, Telegram uses a bot token (deployment env, TELEGRAM_BOT_TOKEN). A new channel stays off until you press Enable on its row, so no alert is delivered before you have checked the destination. Send test works while a channel is off.</p>
        <form onSubmit={submit}>
          <label>Type</label>
          <select value={type} onChange={e => setType(e.target.value)}>
            <option value="email">Email</option><option value="slack">Slack channel</option><option value="webhook">Webhook (HTTPS URL)</option><option value="teams">Microsoft Teams (webhook URL)</option><option value="sms">SMS (needs a gateway)</option><option value="whatsapp">WhatsApp (needs a business account)</option><option value="telegram">Telegram (bot chat)</option>
          </select>
          <label>{({ email: 'Email address', slack: 'Slack channel ID', webhook: 'Webhook URL', teams: 'Teams incoming-webhook URL (https)', sms: 'Phone number (+country code)', whatsapp: 'WhatsApp number (+country code)', telegram: 'Telegram chat ID' } as Record<string, string>)[type]}</label>
          <input value={target} onChange={e => setTarget(e.target.value)} placeholder={({ email: 'ops@yourcompany.com', slack: 'C0123456789', webhook: 'https://example.com/hook', teams: 'https://...webhook.office.com/...', sms: '+4915112345678', whatsapp: '+919812345678', telegram: '-1001234567890 (a group) or 123456789 (a person)' } as Record<string, string>)[type]} required />
          {targetHint(type, target) && <div className="muted" role="status" style={{ marginTop: 4 }}>{targetHint(type, target)}</div>}
          {type !== 'telegram' && <ChannelGuide type={type} />}
          {type === 'telegram' && (
            <div className="muted" style={{ marginTop: 6 }}>
              <p style={{ marginTop: 0 }}>
                Setup: in Telegram, create a bot with @BotFather and give the token to your platform operator (they set TELEGRAM_BOT_TOKEN on the server; never paste it here).
                Add the bot to your chat or group. Then prove the chat is yours:
              </p>
              <ol style={{ margin: '4px 0 8px 18px' }}>
                <li>Press Get pairing code below.</li>
                <li>Send <b>/pair CODE</b> to the bot inside your chat (the code works for 10 minutes, once).</li>
                <li>Press Find chats - your chat appears as a card; press Use this chat to fill in its id. Only chats you paired this way can be used.</li>
              </ol>
              <p style={{ margin: '0 0 8px' }}>
                <b>Pairing a group is an audience decision:</b> everyone in the group - now and anyone who joins later -
                can read the bot's answers there (<b>/alerts</b> lists this site's open alerts) and will receive its alert
                messages. Pair only groups that may see this site's alerts. Access is per chat, not per person: you cannot
                revoke one member's view inside a paired group - delete the channel/pairing to stop the whole chat.
              </p>
              <p style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <button type="button" className="ghost" onClick={getPairCode}>Get pairing code</button>
                {tgCode && <span>Your code: <b style={{ letterSpacing: 1 }}>{tgCode}</b> (10 minutes, single use)</span>}
              </p>
              <p><button type="button" className="ghost" onClick={findTgChats}>Find chats</button></p>
              {tgMsg && <p>{tgMsg}</p>}
              {tgChats && tgChats.length > 0 && (
                <div data-testid="tg-chat-review" style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 8 }}>
                  {tgChats.map(c => (
                    <div key={c.id} data-testid="tg-chat-card" style={{ background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 'var(--radius)', padding: '10px 12px' }}>
                      <div style={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{c.title || '(no name)'}</div>
                      <div className="muted" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>{c.type} - id {c.id}</div>
                      {(c.type === 'group' || c.type === 'supergroup' || c.type === 'channel') && (
                        <div className="muted" style={{ fontSize: 12, marginTop: 2 }}>All current and future members will see this site's alerts here.</div>
                      )}
                      <div style={{ marginTop: 8 }}>
                        <button type="button" className="ghost" onClick={() => { setTarget(c.id); setTgChats(null); setTgMsg(''); }}>Use this chat</button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
          <div style={{ marginTop: 14 }}><button type="submit">Add channel</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
        <table style={{ marginTop: 16 }}>
          <thead><tr><th>Type</th><th>Target</th><th>Status</th><th></th></tr></thead>
          <tbody>
            {channels.map(c => <tr key={c.id}><td>{c.type}</td><td>{c.target}</td><td className="muted">{c.enabled ? 'on' : 'off'}</td>
              <td style={{ whiteSpace: 'nowrap' }}>
                <button type="button" className="ghost" onClick={() => testCh(c)}>Send test</button>{' '}
                <button type="button" className="ghost" onClick={() => toggleCh(c)}>{c.enabled ? 'Disable' : 'Enable'}</button>{' '}
                <button type="button" className="ghost" onClick={() => delCh(c)}>Delete</button>
              </td></tr>)}
          </tbody>
        </table>
      </div>
      <div className="card" style={{ maxWidth: 640, margin: '20px 0' }}>
        <b>Branding</b>
        <p className="muted">Product name and accent colour for this tenant. Admins only. The accent must keep white button text readable (contrast 4.5:1).</p>
        <form onSubmit={async e => {
          e.preventDefault(); setBMsg('');
          try { await api('/v1/branding', { method: 'PUT', body: JSON.stringify({ product_name: bName, accent: bAccent }) }); setBMsg('Saved. Reload to apply.'); }
          catch (err) { setBMsg(String(err)); }
        }}>
          <label htmlFor="b-name">Product name</label>
          <input id="b-name" value={bName} maxLength={40} onChange={e => setBName(e.target.value)} placeholder="HexThings" />
          <label htmlFor="b-accent">Accent colour (#rrggbb)</label>
          <input id="b-accent" value={bAccent} onChange={e => setBAccent(e.target.value)} placeholder="#0071e3" pattern="#[0-9a-fA-F]{6}|" />
          <div style={{ marginTop: 14 }}><button type="submit">Save branding</button></div>
          <label htmlFor="b-logo">Logo (PNG or JPEG, up to 100 KB, 16 to 1024 px)</label>
          <input id="b-logo" type="file" accept="image/png,image/jpeg" onChange={async e => {
            const f = e.target.files?.[0]; if (!f) return;
            const token = localStorage.getItem('iot.token') ?? '';
            const res = await fetch('/v1/branding/logo', { method: 'PUT', body: f, headers: { Authorization: `Bearer ${token}` } });
            setBMsg(res.ok ? 'Logo saved. Reload to apply.' : `${res.status}: ${await res.text()}`);
          }} />
          <button type="button" className="ghost" style={{ marginTop: 8 }} onClick={() => fetch('/v1/branding/logo', { method: 'DELETE', headers: { Authorization: `Bearer ${localStorage.getItem('iot.token') ?? ''}` } }).then(() => setBMsg('Logo removed. Reload to apply.'))}>Remove logo</button>
        </form>
        {bMsg && <p className="muted" role="status">{bMsg}</p>}
      </div>
      <UsageCard />
      <RetentionCard />
      <DirectDevices />
      <FeatureToggles />
      <Authenticator />
      <Sessions />
      <AttributeDefs />
      <ServerAddresses />
      <AIProfiles onChange={() => setAiRev(v => v + 1)} />
      <AISettings key={aiRev} />
      <AIActivity />
      <AssistantChannels />
      <EdgeRules />
      <ApiKeys />
      <div className="cards">
        <div className="card"><b>Users & roles</b><p className="muted">Admin, operator, installer, viewer. Tenant-scoped; SSO/OIDC before GA.</p></div>
        <div className="card"><b>Gateways</b><p className="muted">Enroll by one-time claim code, rotate certificates, revoke lost hardware.</p></div>
        <div className="card"><b>Audit log</b><p className="muted">Append-only record of every change and control action (admin view via API).</p></div>
      </div>
    </>
  );
}
