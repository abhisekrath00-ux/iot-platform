// Guided, deterministic chat templates for the Assistant page. They run fully offline (no model):
// each wizard asks its questions one at a time, shows a summary, and only writes after Confirm.
import { api } from '../lib/api';
import { EDGE_TEMPLATES } from '../lib/edgeSetup';

export interface Opt { value: string; label: string }
export interface WizardStep {
  key: string;
  prompt: string;
  kind: 'text' | 'number' | 'select';
  options?: Opt[];
  loadOptions?: (answers: Record<string, string>) => Promise<Opt[]>;
  validate?: (v: string) => string | null;
  skipIf?: (answers: Record<string, string>) => boolean;
  skippable?: boolean;
}
export interface Wizard {
  id: string;
  title: string;
  description: string;
  steps: WizardStep[];
  // Base roles that can finish this wizard; undefined means everyone signed in. Mirrors the server's
  // per-handler role checks (the server still enforces; this only keeps unusable chips out of the UI).
  roles?: string[];
  summary: (a: Record<string, string>) => string;
  // ctx.idempotencyKey identifies one wizard run: a retry after an uncertain response replays the
  // first result server-side instead of writing twice.
  run: (a: Record<string, string>, ctx?: { idempotencyKey?: string }) => Promise<string>;
}

const nonEmpty = (what: string) => (v: string) => v.trim() ? null : `${what} is required`;
const isNumber = (v: string) => v.trim() !== '' && !isNaN(Number(v)) ? null : 'Please answer with a number';

const CHANNEL_TYPES: Opt[] = [
  { value: 'email', label: 'Email address' },
  { value: 'slack', label: 'Slack channel' },
  { value: 'telegram', label: 'Telegram (needs a paired chat - Settings)' },
  { value: 'webhook', label: 'Webhook (HTTPS URL)' },
  { value: 'teams', label: 'Microsoft Teams (webhook URL)' },
  { value: 'sms', label: 'SMS (needs a gateway)' },
  { value: 'whatsapp', label: 'WhatsApp (needs a business account)' },
];

export const WIZARDS: Wizard[] = [
  {
    id: 'add-device',
    title: 'Add a device',
    description: 'Register a device on a gateway with a profile.',
    roles: ['admin', 'operator', 'installer'],
    steps: [
      {
        key: 'gateway_id', prompt: 'Which gateway is the device behind?', kind: 'select',
        loadOptions: async () => (await api<{ id: string; name?: string; serial?: string }[]>('/v1/gateways'))
          .map(g => ({ value: g.id, label: g.name || g.serial || g.id })),
      },
      {
        key: 'profile', prompt: 'Which device profile (model/type)?', kind: 'select',
        loadOptions: async () => (await api<{ name: string }[]>('/v1/profiles'))
          .map(p2 => ({ value: p2.name, label: p2.name })),
      },
      { key: 'name', prompt: 'What should the device be called?', kind: 'text', validate: nonEmpty('A name') },
    ],
    summary: a => `Create device "${a.name}" (profile ${a.profile}) on gateway ${a.gateway_id_label}.`,
    run: async (a, ctx) => {
      const r = await api<{ id: string }>('/v1/devices', { method: 'POST', headers: ctx?.idempotencyKey ? { 'Idempotency-Key': ctx.idempotencyKey } : {}, body: JSON.stringify({ gateway_id: a.gateway_id, profile: a.profile, name: a.name, config: {} }) });
      return `Device "${a.name}" created (id ${r.id}). It shows up once the gateway polls it. See Devices.`;
    },
  },
  {
    id: 'alert-rule',
    title: 'Create an alert rule',
    description: 'Threshold alert on one device point.',
    roles: ['admin', 'operator'],
    steps: [
      {
        key: 'device_id', prompt: 'Which device?', kind: 'select',
        loadOptions: async () => (await api<{ id: string; name: string }[]>('/v1/devices'))
          .map(d => ({ value: d.id, label: d.name })),
      },
      {
        key: 'point_id', prompt: 'Which point?', kind: 'select',
        loadOptions: async a => (await api<{ point_id: string }[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(a.device_id)}`))
          .map(p2 => ({ value: p2.point_id, label: p2.point_id })),
      },
      { key: 'op', prompt: 'Alert when the value goes above or below?', kind: 'select', options: [{ value: '>', label: 'Above (>)' }, { value: '<', label: 'Below (<)' }] },
      { key: 'threshold', prompt: 'At what value?', kind: 'number', validate: isNumber },
      { key: 'severity', prompt: 'Severity?', kind: 'select', options: [{ value: 'info', label: 'Info' }, { value: 'warning', label: 'Warning' }, { value: 'critical', label: 'Critical' }] },
      { key: 'name', prompt: 'Name for the rule?', kind: 'text', validate: nonEmpty('A name') },
    ],
    summary: a => `Create a ${a.severity} rule "${a.name}": point ${a.point_id} on ${a.device_id_label} ${a.op} ${a.threshold}. It is enabled once created; it only notifies channels you have enabled.`,
    run: async (a, ctx) => {
      await api('/v1/rules', {
        method: 'POST',
        headers: ctx?.idempotencyKey ? { 'Idempotency-Key': ctx.idempotencyKey } : {},
        body: JSON.stringify({
          name: a.name, enabled: true,
          definition: { device_id: a.device_id, point_id: a.point_id, op: a.op, threshold: Number(a.threshold), severity: a.severity, message: a.name },
        }),
      });
      return `Rule "${a.name}" created and enabled. It alerts only through channels you have enabled in Settings.`;
    },
  },
  {
    id: 'add-channel',
    title: 'Add a notification channel',
    description: 'Where alerts go: email, Slack, Telegram and more.',
    roles: ['admin'],
    steps: [
      { key: 'type', prompt: 'Which type of channel?', kind: 'select', options: CHANNEL_TYPES },
      {
        key: 'target', prompt: 'Where should alerts go? (email address, Slack channel id, paired Telegram chat id, webhook URL, phone number)', kind: 'text',
        validate: nonEmpty('A destination'),
      },
    ],
    summary: a => `Add a ${a.type} channel to ${a.target}. Nothing is delivered to it until you verify it with Send test and press Enable in Settings.`,
    run: async (a, ctx) => {
      if (a.type === 'telegram') {
        return 'Telegram needs a paired chat first: in Settings, pick Telegram, press Get pairing code and send it to the bot inside your chat. Then add the channel there with Find chats.';
      }
      await api('/v1/notifications/channels', { method: 'POST', headers: ctx?.idempotencyKey ? { 'Idempotency-Key': ctx.idempotencyKey } : {}, body: JSON.stringify({ type: a.type, target: a.target }) });
      return `Channel added. It is OFF: verify it with Send test in Settings, then press Enable.`;
    },
  },
  {
    id: 'energy-meter',
    title: 'Set up an energy meter',
    description: 'Start from a built-in register map for a real meter model.',
    steps: [
      {
        key: 'template', prompt: 'Which meter model?', kind: 'select',
        options: EDGE_TEMPLATES.map(t => ({ value: t.id, label: t.name + (t.provisional ? ' (provisional map)' : '') })),
      },
    ],
    summary: a => {
      const t = EDGE_TEMPLATES.find(t2 => t2.id === a.template);
      return `Set up ${t?.name} (${t?.points.length} points, Modbus ${t?.link.toUpperCase()}).`;
    },
    run: async a => {
      const t = EDGE_TEMPLATES.find(t2 => t2.id === a.template);
      if (!t) return 'Unknown template.';
      const prov = t.provisional ? ' This map is PROVISIONAL: Edge setup asks you to confirm the open questions before generating config.' : '';
      return `Open Edge setup and pick "${t.name}" from the template list: it generates the edge config for you (${t.points.length} points, ${t.link.toUpperCase()}).${prov} Maps come from vendor documents and have not run on real hardware - verify against your meter before relying on values.`;
    },
  },
  {
    id: 'meter-watch',
    title: 'Watch a power meter',
    description: 'Create a standard set of alert rules for a meter in one go: voltage band, optional frequency band, optional overload. You choose the nominal system and every limit.',
    roles: ['admin', 'operator'],
    steps: [
      {
        key: 'device_id', prompt: 'Which meter (device)?', kind: 'select',
        loadOptions: async () => (await api<{ id: string; name: string }[]>('/v1/devices'))
          .map(d => ({ value: d.id, label: d.name })),
      },
      {
        key: 'nominal', prompt: 'What is the metered system\'s nominal voltage and frequency? This only frames the illustrative hint numbers below - you enter the actual limits yourself.', kind: 'select',
        options: [
          { value: '230ln-50', label: '230 V phase-to-neutral, 50 Hz' },
          { value: '400ll-50', label: '400 V line-to-line, 50 Hz' },
          { value: '277ln-60', label: '277 V phase-to-neutral (480 V line-to-line), 60 Hz' },
          { value: '120-60', label: '120 V, 60 Hz' },
          { value: 'other', label: 'Other nominal or frequency' },
        ],
      },
      {
        key: 'v_basis', prompt: 'Which voltage do you want to watch? A unit of volts alone cannot tell phase-to-neutral from line-to-line - confirm the basis yourself.', kind: 'select',
        options: [
          { value: 'ln', label: 'Phase-to-neutral (L-N) voltages' },
          { value: 'll', label: 'Line-to-line (L-L) voltages' },
          { value: 'none', label: 'No voltage watch' },
        ],
      },
      {
        key: 'voltage_point', prompt: 'Which point is the voltage to watch? Only points the device reports in volts are listed - check the live value matches the basis you just chose (L-N vs L-L).', kind: 'select',
        skipIf: a => a.v_basis === 'none',
        loadOptions: async a => (await api<{ point_id: string; value: number; unit: string }[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(a.device_id)}`))
          .filter(p2 => p2.unit === 'V')
          .map(p2 => ({ value: p2.point_id, label: `${p2.point_id} (now ${p2.value} V)` })),
      },
      { key: 'v_high', prompt: 'Alert ABOVE what voltage (V)? Enter the value that fits your installation. Illustrative, editable suggestions: +10% of nominal is 253 for 230 V L-N, 440 for 400 V L-L, 132 for 120 V - these are not equipment-protection or compliance limits.', kind: 'number', validate: isNumber, skipIf: a => a.v_basis === 'none' },
      { key: 'v_low', prompt: 'Alert BELOW what voltage (V)? Illustrative, editable suggestions for 230 V L-N: -10% is 207, -15% is 195.5 - again not protection or compliance limits, choose what fits your installation.', kind: 'number', validate: isNumber, skipIf: a => a.v_basis === 'none' },
      {
        key: 'freq_watch', prompt: 'Watch the frequency too? You enter the band yourself in the next two steps - any band you set is a configuration choice, not an equipment-protection or compliance limit.', kind: 'select',
        loadOptions: async a => {
          const fp = (await api<{ point_id: string; unit: string }[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(a.device_id)}`)).find(p2 => p2.unit === 'Hz');
          return fp
            ? [{ value: 'yes', label: `Yes, watch ${fp.point_id}` }, { value: 'no', label: 'No frequency watch' }]
            : [{ value: 'no', label: 'No frequency watch (no point the device reports in Hz)' }];
        },
      },
      { key: 'f_hi', prompt: 'Alert ABOVE what frequency (Hz)? Illustrative, editable suggestions - 1% above nominal: 50.5 for a 50 Hz system, 60.6 for 60 Hz. Not protection or compliance limits.', kind: 'number', validate: isNumber, skipIf: a => a.freq_watch !== 'yes' },
      { key: 'f_lo', prompt: 'Alert BELOW what frequency (Hz)? Illustrative, editable suggestions - 1% below nominal: 49.5 for a 50 Hz system, 59.4 for 60 Hz. Not protection or compliance limits.', kind: 'number', validate: isNumber, skipIf: a => a.freq_watch !== 'yes' },
      {
        key: 'overload', prompt: 'Overload watch? One current limit applies to every point the device reports in amps - including neutral or aggregate currents if your meter reports them. The review lists the exact points.', kind: 'select',
        loadOptions: async a => {
          const amps = (await api<{ point_id: string; unit: string }[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(a.device_id)}`)).filter(p2 => p2.unit === 'A').map(p2 => p2.point_id);
          return amps.length
            ? [{ value: 'watch', label: `Watch: ${amps.join(', ')}` }, { value: 'none', label: 'No overload watch' }]
            : [{ value: 'none', label: 'No overload watch (no point the device reports in A)' }];
        },
      },
      { key: 'i_limit', prompt: 'Alert when a current point goes above how many amps?', kind: 'number', validate: v => /^\d+(\.\d+)?$/.test(v.trim()) && Number(v) > 0 ? null : 'Answer with a number of amps above zero', skipIf: a => a.overload !== 'watch' },
      { key: 'severity', prompt: 'Severity for these rules?', kind: 'select', options: [{ value: 'info', label: 'Info' }, { value: 'warning', label: 'Warning' }, { value: 'critical', label: 'Critical' }] },
    ],
    summary: a => {
      const parts: string[] = [];
      if (a.v_basis && a.v_basis !== 'none') parts.push(`voltage basis ${a.v_basis_label} (your choice): ${a.voltage_point_label} > ${a.v_high} V and < ${a.v_low} V`);
      if (a.freq_watch === 'yes') parts.push(`frequency point ${(a.freq_watch_label || '').replace(/^Yes, watch /, '')} outside ${a.f_lo}-${a.f_hi} Hz`);
      if (a.overload === 'watch') parts.push(`every reported current point (${(a.overload_label || '').replace(/^Watch: /, '')}) > ${a.i_limit} A`);
      if (parts.length === 0) return 'Nothing selected - every watch is off, nothing will be created.';
      return `Nominal system: ${a.nominal_label}. Create ${a.severity} rules on ${a.device_id_label}: ${parts.join('; ')}. The rules are ENABLED the moment they are created - any channel already enabled in Settings can receive these alerts immediately.`;
    },
    run: async (a, ctx) => {
      if (a.v_basis && a.v_basis !== 'none' && !(Number(a.v_high) > Number(a.v_low))) throw new Error('400 The high limit must be above the low limit. Nothing was created.');
      if (a.freq_watch === 'yes' && !(Number(a.f_hi) > Number(a.f_lo))) throw new Error('400 The high frequency limit must be above the low limit. Nothing was created.');
      const key = (suffix: string): Record<string, string> => ctx?.idempotencyKey ? { 'Idempotency-Key': `${ctx.idempotencyKey}-${suffix}` } : {};
      const sev = a.severity, dev = a.device_id, base = { device_id: dev, severity: sev };
      type Plan = { suffix: string; name: string; def: Record<string, unknown>; label: string };
      const plan: Plan[] = [];
      const skipped: string[] = [];
      const latest = async () => (await api<{ point_id: string; unit: string }[]>(`/v1/telemetry/latest?device_id=${encodeURIComponent(dev)}`));
      if (a.v_basis && a.v_basis !== 'none') {
        plan.push(
          { suffix: 'vhigh', name: `High voltage on ${a.device_id_label}`, label: `voltage > ${a.v_high} V`, def: { ...base, point_id: a.voltage_point, op: '>', threshold: Number(a.v_high), message: `Voltage above ${a.v_high} V` } },
          { suffix: 'vlow', name: `Low voltage on ${a.device_id_label}`, label: `voltage < ${a.v_low} V`, def: { ...base, point_id: a.voltage_point, op: '<', threshold: Number(a.v_low), message: `Voltage below ${a.v_low} V` } },
        );
      }
      if (a.freq_watch === 'yes') {
        const fp = (await latest()).find(p2 => p2.unit === 'Hz');
        if (!fp) skipped.push('frequency watch (no point the device reports in Hz)');
        else {
          plan.push(
            { suffix: 'fhi', name: `High frequency on ${a.device_id_label}`, label: `frequency > ${a.f_hi} Hz`, def: { ...base, point_id: fp.point_id, op: '>', threshold: Number(a.f_hi), message: `Frequency above ${a.f_hi} Hz` } },
            { suffix: 'flo', name: `Low frequency on ${a.device_id_label}`, label: `frequency < ${a.f_lo} Hz`, def: { ...base, point_id: fp.point_id, op: '<', threshold: Number(a.f_lo), message: `Frequency below ${a.f_lo} Hz` } },
          );
        }
      }
      if (a.overload === 'watch') {
        const amps = (await latest()).filter(p2 => p2.unit === 'A');
        if (amps.length === 0) skipped.push('overload watch (no point the device reports in A)');
        else for (const p2 of amps) plan.push({ suffix: `i-${p2.point_id}`, name: `Overcurrent ${p2.point_id} on ${a.device_id_label}`, label: `${p2.point_id} > ${a.i_limit} A`, def: { ...base, point_id: p2.point_id, op: '>', threshold: Number(a.i_limit), message: `Current ${p2.point_id} above ${a.i_limit} A` } });
      }
      if (plan.length === 0 && skipped.length === 0) throw new Error('400 Nothing was created - every watch was set to none.');
      const created: string[] = [], failed: string[] = [];
      for (const r of plan) {
        try {
          await api('/v1/rules', { method: 'POST', headers: key(r.suffix), body: JSON.stringify({ name: r.name, enabled: true, definition: r.def }) });
          created.push(r.label);
        } catch (e) {
          failed.push(`${r.label} (${String(e).slice(0, 80)})`);
        }
      }
      const tally = `Created: ${created.length ? created.join('; ') : 'none'}.` + (skipped.length ? ` Skipped: ${skipped.join('; ')}.` : '') + (failed.length ? ` FAILED: ${failed.join('; ')}.` : '');
      if (failed.length) {
        throw new Error(`${tally} Press Confirm again to retry - already-created rules replay safely (idempotency), only the missing ones are written.`);
      }
      return `${tally} All enabled now; they alert only through channels you have enabled in Settings.`;
    },
  },
  {
    id: 'ack-alert',
    title: 'Acknowledge an alert',
    description: 'Mark an open alert as seen.',
    roles: ['admin', 'operator'],
    steps: [
      {
        key: 'alert_id', prompt: 'Which open alert?', kind: 'select',
        loadOptions: async () => (await api<{ id: string; message?: string; name?: string; severity?: string }[]>('/v1/alerts?status=open'))
          .map(al => ({ value: al.id, label: `${al.severity ?? ''} ${al.message ?? al.name ?? al.id}`.trim() })),
      },
    ],
    summary: a => `Acknowledge "${a.alert_id_label}" - it is marked as seen by you.`,
    run: async a => {
      await api(`/v1/alerts/${encodeURIComponent(a.alert_id)}/ack`, { method: 'POST' });
      return 'Alert acknowledged.';
    },
  },
  {
    id: 'invite-user',
    title: 'Invite a user',
    description: 'Send an invite to join this tenant.',
    roles: ['admin'],
    steps: [
      { key: 'email', prompt: 'Email address?', kind: 'text', validate: v => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v.trim()) ? null : 'Please answer with an email address' },
      { key: 'role', prompt: 'Role?', kind: 'select', options: [{ value: 'viewer', label: 'Viewer (read only)' }, { value: 'operator', label: 'Operator' }, { value: 'admin', label: 'Admin' }] },
    ],
    summary: a => `Invite ${a.email} as ${a.role}. Confirming sends an invitation email to that address.`,
    run: async (a, ctx) => {
      await api('/v1/users/invites', { method: 'POST', headers: ctx?.idempotencyKey ? { 'Idempotency-Key': ctx.idempotencyKey } : {}, body: JSON.stringify({ email: a.email.trim(), role: a.role, send_email: true }) });
      return `Invite sent to ${a.email} (${a.role}).`;
    },
  },
];
