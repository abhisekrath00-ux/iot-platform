import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Step { severity: string; step: number; after_minutes: number; channel_id: string; schedule_id?: string; }
interface Sched { id: string; name: string; on_call_channel: string; }
interface Channel { id: string; type: string; target: string; enabled: boolean; }

// Escalation: if an alert stays open and unacknowledged, notify more channels after set delays.
// Steps are numbered per severity (1, 2, 3...) and each delay must be longer than the one before.
const GROUPS = [
  { value: 'critical', label: 'Critical alerts' },
  { value: 'warning', label: 'Warning alerts' },
  { value: 'info', label: 'Info alerts' },
  { value: '', label: 'Any severity (used when a severity has no steps of its own)' },
];

export default function EscalationPolicy() {
  const [steps, setSteps] = useState<Step[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [schedules, setSchedules] = useState<Sched[]>([]);
  const [msg, setMsg] = useState('');
  const [rep, setRep] = useState({ every_minutes: 0, max: 0 });
  const [quiet, setQuiet] = useState({ start: '', end: '', timezone: '' });
  useEffect(() => {
    api<{ steps: Step[]; repeat?: { every_minutes: number; max: number }; quiet?: { start: string; end: string; timezone: string } }>('/v1/escalation').then(r => { setSteps(r.steps); if (r.repeat) setRep(r.repeat); if (r.quiet) setQuiet(r.quiet); }).catch(() => undefined);
    api<Sched[]>('/v1/oncall').then(setSchedules).catch(() => undefined);
    api<Channel[]>('/v1/notifications/channels').then(c => setChannels(c.filter(x => x.enabled))).catch(() => undefined);
  }, []);
  const renumber = (list: Step[]) => {
    const n: Record<string, number> = {};
    return list.map(s => ({ ...s, step: (n[s.severity] = (n[s.severity] ?? 0) + 1) }));
  };
  const upd = (i: number, p: Partial<Step>) => setSteps(renumber(steps.map((s, j) => (j === i ? { ...s, ...p } : s))));
  async function save() {
    setMsg('');
    try {
      await api('/v1/escalation', { method: 'PUT', body: JSON.stringify({ steps, repeat: rep, quiet }) });
      setMsg('Saved.');
    } catch (e) { setMsg(String(e)); }
  }
  return (
    <div className="card" style={{ maxWidth: 900, marginTop: 20 }}>
      <b>Escalation</b>
      <p className="muted">An alert nobody acknowledges is sent to more channels after these delays. Acknowledging or resolving it stops the chain. Steps for a severity replace the "any" steps for that severity. Admins only.</p>
      {steps.length === 0 && <p className="muted">No escalation steps. Alerts go only to your notification channels.</p>}
      {GROUPS.filter(g => steps.some(s => s.severity === g.value)).map(g => (
        <div key={g.value || 'any'} style={{ marginBottom: 14 }}>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>{g.label}</div>
          {steps.map((s, i) => s.severity !== g.value ? null : (
            <div key={i} style={{ display: 'grid', gridTemplateColumns: '64px 150px 170px minmax(220px, 1fr) auto', gap: 8, alignItems: 'center', marginBottom: 6 }}>
              <span className="muted">Step {s.step}</span>
              <select aria-label={`Severity for step ${i + 1}`} value={s.severity} onChange={e => upd(i, { severity: e.target.value })}>
                <option value="">any severity</option><option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
              </select>
              <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                after <input aria-label={`Minutes for step ${i + 1}`} type="number" min={1} max={10080} style={{ width: 72, minWidth: 72, flex: 'none' }} value={s.after_minutes} onChange={e => upd(i, { after_minutes: +e.target.value })} /> min
              </label>
              <select aria-label={`Who to notify for step ${i + 1}`} title={channels.find(c => c.id === s.channel_id)?.target}
                value={s.schedule_id ? `sc:${s.schedule_id}` : s.channel_id ? `ch:${s.channel_id}` : ''}
                onChange={e => { const v = e.target.value; upd(i, v.startsWith('sc:') ? { schedule_id: v.slice(3), channel_id: '' } : { channel_id: v.slice(3), schedule_id: '' }); }}>
                <option value="">notify... choose</option>
                <optgroup label="Channel">{channels.map(c => <option key={c.id} value={`ch:${c.id}`}>{c.type}: {c.target}</option>)}</optgroup>
                {schedules.length > 0 && <optgroup label="Whoever is on call">{schedules.map(sc => <option key={sc.id} value={`sc:${sc.id}`}>on call: {sc.name}</option>)}</optgroup>}
              </select>
              <button className="ghost" onClick={() => setSteps(renumber(steps.filter((_, j) => j !== i)))}>Remove</button>
            </div>
          ))}
        </div>
      ))}
      <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap', marginTop: 8 }}>
        <input type="checkbox" aria-label="Repeat the last step" checked={rep.every_minutes > 0} style={{ width: 'auto', flex: 'none' }}
          onChange={e => setRep(e.target.checked ? { every_minutes: 30, max: 3 } : { every_minutes: 0, max: 0 })} />
        Remind until acknowledged: resend the last step every
        <input aria-label="Reminder interval minutes" type="number" min={5} max={1440} disabled={rep.every_minutes === 0} style={{ width: 72, minWidth: 72, flex: 'none' }}
          value={rep.every_minutes} onChange={e => setRep({ ...rep, every_minutes: +e.target.value })} />
        min, at most
        <input aria-label="Maximum reminders" type="number" min={1} max={10} disabled={rep.every_minutes === 0} style={{ width: 64, minWidth: 64, flex: 'none' }}
          value={rep.max} onChange={e => setRep({ ...rep, max: +e.target.value })} />
        times
      </label>
      <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap', marginTop: 8 }}>
        <input type="checkbox" aria-label="Quiet hours" checked={quiet.start !== ''} style={{ width: 'auto', flex: 'none' }}
          onChange={e => setQuiet(e.target.checked ? { start: '22:00', end: '06:00', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' } : { start: '', end: '', timezone: '' })} />
        Quiet hours: hold reminders for non-critical alerts from
        <input aria-label="Quiet start" type="time" disabled={quiet.start === ''} style={{ width: 140, flex: 'none' }} value={quiet.start} onChange={e => setQuiet({ ...quiet, start: e.target.value })} />
        to
        <input aria-label="Quiet end" type="time" disabled={quiet.start === ''} style={{ width: 140, flex: 'none' }} value={quiet.end} onChange={e => setQuiet({ ...quiet, end: e.target.value })} />
        in
        <input aria-label="Quiet timezone" disabled={quiet.start === ''} style={{ width: 170, flex: 'none' }} value={quiet.timezone} onChange={e => setQuiet({ ...quiet, timezone: e.target.value })} />
      </label>
      {quiet.start !== '' && <p className="muted" style={{ fontSize: 12 }}>Escalation steps and critical alerts are never held back. Reminders held during the window go out once it ends if the alert is still unacknowledged.</p>}
      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        <button className="ghost" disabled={steps.length >= 15} onClick={() => setSteps(renumber([...steps, { severity: '', step: 1, after_minutes: (steps.at(-1)?.after_minutes ?? 0) + 15, channel_id: channels[0]?.id ?? '' }]))}>+ step</button>
        <button onClick={save}>Save policy</button>
      </div>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
