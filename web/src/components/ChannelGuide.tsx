// Guided, per-type setup help for notification channels (Settings page).
// Pure presentation: no live behavior, secrets always go to the operator/env,
// never into this page. Telegram keeps its own pairing flow in Settings.

const GUIDES: Record<string, { title: string; steps: string[]; note?: string }> = {
  slack: {
    title: 'Slack setup (about 10 minutes, one time)',
    steps: [
      'Create a Slack app: open https://api.slack.com/apps, choose "Create New App" - "From scratch", name it (for example "Site alerts"), pick your workspace.',
      'In the app, open "OAuth & Permissions" and add the Bot Token Scope chat:write (post messages). Add channels:read and groups:read only if you want to list channel IDs through the API - not needed otherwise.',
      'Press "Install to Workspace" and copy the "Bot User OAuth Token" (starts with xoxb-).',
      'Give the token to your platform operator - they set SLACK_BOT_TOKEN on the server. Never paste the token into this page.',
      'Invite the bot to your channel: type /invite @YourBotName inside the channel. Slack does not let a bot post to a channel it has not joined.',
      'Find the channel ID: open the channel, click its name at the top - the ID is at the bottom of the dialog (starts with C, or G for private channels). Paste it below.',
      'Add the channel here, press Send test, and Enable it once the test message arrives.',
    ],
    note: 'Air-gapped deployments: the operator can point SLACK_API_BASE at a Slack-compatible relay instead of slack.com.',
  },
  email: {
    title: 'Email setup',
    steps: [
      'Your platform operator configures the deployment\'s mail server once: SMTP_HOST, SMTP_PORT (default 587), SMTP_USER, SMTP_PASS and ALERT_FROM_EMAIL on the server.',
      'Add one recipient address below. Want several recipients? Add the channel again for each address.',
      'Press Send test - the mail comes from ALERT_FROM_EMAIL, so check the spam folder the first time. Enable the channel once the test arrives.',
    ],
  },
  teams: {
    title: 'Microsoft Teams setup',
    steps: [
      'In Teams, open the channel menu (...) - "Connectors" (or "Workflows" in new Teams) and add "Incoming Webhook".',
      'Name it (for example "Site alerts") and copy the webhook URL (starts with https).',
      'Paste the URL below, press Send test, and Enable once the test card arrives in the channel.',
    ],
  },
  webhook: {
    title: 'Webhook setup',
    steps: [
      'Point this at any HTTPS endpoint you control that accepts an HTTP POST with a JSON alert body.',
      'Paste the URL below, press Send test, and Enable once your endpoint logs the test.',
    ],
  },
  sms: {
    title: 'SMS',
    steps: ['SMS needs an SMS gateway configured by your platform operator. Until then this type cannot deliver.'],
  },
  whatsapp: {
    title: 'WhatsApp',
    steps: ['WhatsApp needs a business account integration configured by your platform operator. Until then this type cannot deliver.'],
  },
};

export function ChannelGuide({ type }: { type: string }) {
  const g = GUIDES[type];
  if (!g) return null;
  return (
    <div className="muted" data-testid={`guide-${type}`} style={{ marginTop: 6, background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 'var(--radius)', padding: '10px 12px' }}>
      <b style={{ color: 'var(--text)' }}>{g.title}</b>
      <ol style={{ margin: '6px 0 0 18px', padding: 0, display: 'flex', flexDirection: 'column', gap: 4 }}>
        {g.steps.map((s, i) => <li key={i}>{s}</li>)}
      </ol>
      {g.note && <p style={{ margin: '8px 0 0' }}>{g.note}</p>}
    </div>
  );
}

// Soft format hints for the target field. The server stays authoritative -
// these only warn, never block.
const HINTS: Record<string, { ok: (v: string) => boolean; hint: string }> = {
  email: { ok: v => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v), hint: 'Looks off: an email address like ops@yourcompany.com.' },
  slack: { ok: v => /^[CGD][A-Z0-9]{8,}$/.test(v), hint: 'Looks off: a Slack channel ID starts with C, G or D followed by capitals/digits (for example C0123456789) - find it at the bottom of the channel\'s About dialog, not the channel name.' },
  webhook: { ok: v => /^https:\/\/.+/.test(v), hint: 'Looks off: the URL must start with https://.' },
  teams: { ok: v => /^https:\/\/.+/.test(v), hint: 'Looks off: the webhook URL must start with https://.' },
  sms: { ok: v => /^\+[1-9]\d{6,14}$/.test(v), hint: 'Looks off: international format starting with +, digits only (for example +4915112345678).' },
  whatsapp: { ok: v => /^\+[1-9]\d{6,14}$/.test(v), hint: 'Looks off: international format starting with +, digits only (for example +919812345678).' },
  telegram: { ok: v => /^-?\d{5,}$/.test(v), hint: 'Looks off: a numeric chat id (negative for groups). Use Find chats above instead of typing it by hand.' },
};

export function targetHint(type: string, value: string): string {
  if (!value) return '';
  const h = HINTS[type];
  return h && !h.ok(value.trim()) ? h.hint : '';
}
