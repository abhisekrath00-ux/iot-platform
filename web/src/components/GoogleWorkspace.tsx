import { useEffect, useState } from 'react';
import { api } from '../lib/api';

// Google Workspace connection card (Settings > Integrations).
// Optional integration: the deployment works fully without it. Consent
// happens on Google's site; this page only starts it and shows the result.
// Only surfaces with a live consumer are requested - Gmail send (the Gmail
// notification channel and its Send test path) and Calendar read (the on-call
// calendar sync). The Drive scope exists server-side but has no consumer yet,
// so it is not requested here.

interface GStatus { configured: boolean; connected: boolean; }

export default function GoogleWorkspace() {
  const [st, setSt] = useState<GStatus | null>(null);
  const [hidden, setHidden] = useState(false);
  const [msg, setMsg] = useState('');

  const load = () => api<GStatus>('/v1/integrations/google/status').then(setSt).catch(e => {
    if (String(e).includes('403') || String(e).toLowerCase().includes('forbidden')) setHidden(true);
  });
  useEffect(() => {
    load();
    if (new URLSearchParams(window.location.search).get('google') === 'connected') {
      setMsg('Google account connected. Add a channel of type Gmail above, then Send test it before you Enable it.');
      window.history.replaceState({}, '', window.location.pathname);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function connect() {
    setMsg('');
    try {
      const r = await api<{ url: string }>('/v1/integrations/google/connect', { method: 'POST', body: JSON.stringify({ surfaces: ['gmail_send', 'calendar_oncall'] }) });
      window.open(r.url, '_blank', 'noopener');
      setMsg('Finish the sign-in in the new tab - Google brings you back to this page when it is done.');
    } catch (e) { setMsg(String(e)); }
  }
  async function disconnect() {
    if (!window.confirm('Disconnect the Google account? Gmail channels stop delivering until you reconnect.')) return;
    setMsg('');
    try { await api('/v1/integrations/google', { method: 'DELETE' }); setMsg('Disconnected.'); load(); } catch (e) { setMsg(String(e)); }
  }

  if (hidden) return null; // the status endpoint is admin-only; non-admins do not see this card
  return (
    <div className="card" style={{ maxWidth: 760, marginBottom: 20 }} data-testid="google-workspace-card">
      <b>Integrations: Google Workspace</b>
      <p className="muted">
        Optional - everything works without this. Connecting one Google account lets alerts be sent from your Gmail
        address (channel type Gmail above) and lets an on-call schedule follow a Google Calendar (Escalation settings).
        The sign-in happens on Google's site; this deployment stores only the finished grant, and Disconnect removes it
        here. Google will ask for two read/send permissions: send email on your behalf and read your calendars.
        Nothing is sent and no calendar is read until you add a Gmail channel or link a calendar to a schedule.
      </p>
      {st && !st.configured && (
        <p className="muted" role="status" data-testid="google-not-configured">
          Google sign-in is not configured on this deployment yet - this card stays inert until your platform operator
          sets GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET on the server.
        </p>
      )}
      {st && st.configured && (
        <p style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          {st.connected
            ? <><span data-testid="google-connected" style={{ fontWeight: 600 }}>Connected</span><button type="button" className="ghost" onClick={disconnect}>Disconnect</button></>
            : <button type="button" onClick={connect}>Connect Google account</button>}
        </p>
      )}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
