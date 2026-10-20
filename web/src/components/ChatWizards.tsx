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
  skippable?: boolean;
}
export interface Wizard {
  id: string;
  title: string;
  description: string;
  steps: WizardStep[];
  summary: (a: Record<string, string>) => string;
  run: (a: Record<string, string>) => Promise<string>;
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
    run: async a => {
      const r = await api<{ id: string }>('/v1/devices', { method: 'POST', body: JSON.stringify({ gateway_id: a.gateway_id, profile: a.profile, name: a.name, config: {} }) });
      return `Device "${a.name}" created (id ${r.id}). It shows up once the gateway polls it. See Devices.`;
    },
  },
  {
    id: 'alert-rule',
    title: 'Create an alert rule',
    description: 'Threshold alert on one device point.',
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
    run: async a => {
      await api('/v1/rules', {
        method: 'POST',
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
    steps: [
      { key: 'type', prompt: 'Which type of channel?', kind: 'select', options: CHANNEL_TYPES },
      {
        key: 'target', prompt: 'Where should alerts go? (email address, Slack channel id, paired Telegram chat id, webhook URL, phone number)', kind: 'text',
        validate: nonEmpty('A destination'),
      },
    ],
    summary: a => `Add a ${a.type} channel to ${a.target}. Nothing is delivered to it until you verify it with Send test and press Enable in Settings.`,
    run: async a => {
      if (a.type === 'telegram') {
        return 'Telegram needs a paired chat first: in Settings, pick Telegram, press Get pairing code and send it to the bot inside your chat. Then add the channel there with Find chats.';
      }
      await api('/v1/notifications/channels', { method: 'POST', body: JSON.stringify({ type: a.type, target: a.target }) });
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
    id: 'ack-alert',
    title: 'Acknowledge an alert',
    description: 'Mark an open alert as seen.',
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
    steps: [
      { key: 'email', prompt: 'Email address?', kind: 'text', validate: v => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v.trim()) ? null : 'Please answer with an email address' },
      { key: 'role', prompt: 'Role?', kind: 'select', options: [{ value: 'viewer', label: 'Viewer (read only)' }, { value: 'operator', label: 'Operator' }, { value: 'admin', label: 'Admin' }] },
    ],
    summary: a => `Invite ${a.email} as ${a.role}. Confirming sends an invitation email to that address.`,
    run: async a => {
      await api('/v1/users/invites', { method: 'POST', body: JSON.stringify({ email: a.email.trim(), role: a.role, send_email: true }) });
      return `Invite sent to ${a.email} (${a.role}).`;
    },
  },
];
