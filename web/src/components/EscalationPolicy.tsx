import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Step { severity: string; step: number; after_minutes: number; channel_id: string; }
interface Channel { id: string; type: string; target: string; enabled: boolean; }

// Escalation: if an alert stays open and unacknowledged, notify more channels after set delays.
// Steps are numbered per severity (1, 2, 3...) and each delay must be longer than the one before.
export default function EscalationPolicy() {
  const [steps, setSteps] = useState<Step[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [msg, setMsg] = useState('');
  useEffect(() => {
    api<{ steps: Step[] }>('/v1/escalation').then(r => setSteps(r.steps)).catch(() => undefined);
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
      await api('/v1/escalation', { method: 'PUT', body: JSON.stringify({ steps }) });
      setMsg('Saved.');
    } catch (e) { setMsg(String(e)); }
  }
  return (
    <div className="card" style={{ maxWidth: 900, marginTop: 20 }}>
      <b>Escalation</b>
      <p className="muted">An alert nobody acknowledges is sent to more channels after these delays. Acknowledging or resolving it stops the chain. Steps for a severity replace the "any" steps for that severity. Admins only.</p>
      {steps.length === 0 && <p className="muted">No escalation steps. Alerts go only to your notification channels.</p>}
      {steps.map((s, i) => (
        <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 6, flexWrap: 'wrap' }}>
          <span className="muted">Step {s.step}</span>
          <select style={{ width: 150 }} aria-label={`Severity for step ${i + 1}`} value={s.severity} onChange={e => upd(i, { severity: e.target.value })}>
            <option value="">any severity</option><option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
          </select>
          <span className="muted">after</span>
          <input aria-label={`Minutes for step ${i + 1}`} type="number" min={1} max={10080} style={{ width: 80, minWidth: 80 }} value={s.after_minutes} onChange={e => upd(i, { after_minutes: +e.target.value })} />
          <span className="muted">min, notify</span>
          <select style={{ width: 240 }} aria-label={`Channel for step ${i + 1}`} value={s.channel_id} onChange={e => upd(i, { channel_id: e.target.value })}>
            <option value="">choose...</option>{channels.map(c => <option key={c.id} value={c.id}>{c.type}: {c.target}</option>)}
          </select>
          <button className="ghost" onClick={() => setSteps(renumber(steps.filter((_, j) => j !== i)))}>Remove</button>
        </div>
      ))}
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button className="ghost" disabled={steps.length >= 15} onClick={() => setSteps(renumber([...steps, { severity: '', step: 1, after_minutes: (steps.at(-1)?.after_minutes ?? 0) + 15, channel_id: channels[0]?.id ?? '' }]))}>+ step</button>
        <button onClick={save}>Save policy</button>
      </div>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
