import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Channel { id: string; type: string; target: string; enabled: boolean; }
interface Sched { id: string; name: string; anchor: string; shift_hours: number; channel_ids: string[]; on_call_channel: string; shift_ends: string; }

// On-call rotations: each notification channel takes a turn. An escalation step can name a schedule
// instead of one fixed channel, and whoever is on duty when the step fires gets it.
export default function OnCall() {
  const [list, setList] = useState<Sched[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [name, setName] = useState('');
  const [hours, setHours] = useState(24);
  const [start, setStart] = useState(() => new Date().toISOString().slice(0, 16));
  const [picked, setPicked] = useState<string[]>([]);
  const [msg, setMsg] = useState('');
  const load = () => api<Sched[]>('/v1/oncall').then(setList).catch(() => undefined);
  useEffect(() => {
    load();
    api<Channel[]>('/v1/notifications/channels').then(c => setChannels(c.filter(x => x.enabled))).catch(() => undefined);
  }, []);
  const label = (id: string) => { const c = channels.find(x => x.id === id); return c ? `${c.type}: ${c.target}` : id; };
  async function create() {
    setMsg('');
    try {
      await api('/v1/oncall', { method: 'POST', body: JSON.stringify({ name, anchor: new Date(start).toISOString(), shift_hours: hours, channel_ids: picked }) });
      setName(''); setPicked([]); load();
    } catch (e) { setMsg(String(e)); }
  }
  async function remove(id: string) {
    setMsg('');
    try { await api(`/v1/oncall/${id}`, { method: 'DELETE' }); load(); } catch (e) { setMsg(String(e)); }
  }
  return (
    <div className="card" style={{ maxWidth: 900, marginTop: 20 }}>
      <b>On-call rotations</b>
      <p className="muted">Channels take turns, one shift each, in the order listed. Use a rotation as the target of an escalation step above. Admins only.</p>
      {list.length === 0 && <p className="muted">No rotations yet.</p>}
      {list.map(sc => (
        <div key={sc.id} style={{ display: 'flex', gap: 12, alignItems: 'center', marginBottom: 6, flexWrap: 'wrap' }}>
          <b>{sc.name}</b>
          <span className="muted">{sc.shift_hours} h shifts, {sc.channel_ids.length} in rotation. On call now: {label(sc.on_call_channel)} until {new Date(sc.shift_ends).toLocaleString()}</span>
          <button className="ghost" onClick={() => remove(sc.id)}>Delete</button>
        </div>
      ))}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginTop: 10 }}>
        <input aria-label="Rotation name" placeholder="Name" value={name} onChange={e => setName(e.target.value)} style={{ width: 160 }} />
        <label className="muted">first shift starts <input aria-label="First shift start" type="datetime-local" value={start} onChange={e => setStart(e.target.value)} style={{ width: 210 }} /></label>
        <label className="muted">each shift <input aria-label="Shift hours" type="number" min={1} max={720} value={hours} onChange={e => setHours(+e.target.value)} style={{ width: 72, minWidth: 72, flex: 'none' }} /> h</label>
      </div>
      <div style={{ marginTop: 8 }}>
        <span className="muted">Rotation order (click to add, click again to remove): </span>
        {channels.map(c => {
          const i = picked.indexOf(c.id);
          return <button key={c.id} className={i >= 0 ? '' : 'ghost'} style={{ marginRight: 6 }} onClick={() => setPicked(i >= 0 ? picked.filter(x => x !== c.id) : [...picked, c.id])}>{i >= 0 ? `${i + 1}. ` : ''}{c.type}: {c.target}</button>;
        })}
      </div>
      <button style={{ marginTop: 10 }} disabled={!name || picked.length === 0} onClick={create}>Add rotation</button>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
