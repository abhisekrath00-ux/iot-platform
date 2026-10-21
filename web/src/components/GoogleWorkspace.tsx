import { useEffect, useState } from 'react';
import { api } from '../lib/api';

// Google Workspace connection card (Settings > Integrations).
// Optional integration: the deployment works fully without it. Consent
// happens on Google's site; this page only starts it and shows the result.
// Only surfaces with a live consumer are requested: Gmail send (the Gmail
// notification channel and its Send test path), Calendar read (the on-call
// calendar sync) and Drive files (report export to Drive/Sheets). When the
// workspace is connected the card shows exactly which of them the grant
// covers, read from the scope string recorded at consent - older connections
// predate that record and show "unknown - reconnect to confirm", never a guess.

interface GStatus { configured: boolean; connected: boolean; surfaces?: Record<string, boolean | 'unknown'>; }

const SURFACE_LABELS: Record<string, string> = {
  gmail_send: 'Send email from your Gmail address',
  calendar_oncall: 'Read calendars (on-call sync)',
  drive_reports: 'Create report files in your Drive',
};

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
      const r = await api<{ url: string }>('/v1/integrations/google/connect', { method: 'POST', body: JSON.stringify({ surfaces: ['gmail_send', 'calendar_oncall', 'drive_reports'] }) });
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
        address (channel type Gmail above), lets an on-call schedule follow a Google Calendar (Escalation settings),
        and lets reports be exported to your Drive (Reports page). The sign-in happens on Google's site; this
        deployment stores only the finished grant, and Disconnect removes it here. Google will ask for three
        permissions: send email on your behalf, read your calendars, and create new Drive files (it cannot see
        your existing files). Nothing is sent, no calendar is read and no file is created until you use a feature.
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
          {st.connected && st.surfaces && (
            <span style={{ flexBasis: '100%' }} data-testid="google-surfaces">
              {Object.entries(SURFACE_LABELS).map(([k, label]) => (
                <span key={k} className="muted" style={{ display: 'block', marginTop: 2 }}>
                  {label}: {st.surfaces![k] === true ? 'granted' : st.surfaces![k] === false ? 'not granted' : 'unknown - reconnect to confirm'}
                </span>
              ))}
            </span>
          )}
        </p>
      )}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
