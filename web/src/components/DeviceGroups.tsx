import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Group { id: string; name: string; description: string; devices: number; }
interface Dev { id: string; name: string; }

// Named sets of devices. Click a group to filter the fleet by it; "Manage" edits membership.
export default function DeviceGroups({ selected, onSelect }: { selected: string; onSelect: (id: string) => void }) {
  const [groups, setGroups] = useState<Group[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [edit, setEdit] = useState<string | null>(null);
  const [all, setAll] = useState<Dev[]>([]);
  const [members, setMembers] = useState<Set<string>>(new Set());
  const [msg, setMsg] = useState('');
  const load = () => api<Group[]>('/v1/groups').then(setGroups).catch(() => undefined);
  useEffect(() => { load(); }, []);
  async function create(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try { await api('/v1/groups', { method: 'POST', body: JSON.stringify({ name }) }); setName(''); load(); } catch (e2) { setMsg(String(e2)); }
  }
  async function startEdit(g: Group) {
    const [d, gg] = await Promise.all([api<Dev[]>('/v1/devices'), api<{ device_ids: string[] }>(`/v1/groups/${g.id}`)]);
    setAll(d); setMembers(new Set(gg.device_ids)); setEdit(g.id);
  }
  async function save() {
    if (!edit) return;
    try { await api(`/v1/groups/${edit}/devices`, { method: 'PUT', body: JSON.stringify({ device_ids: [...members] }) }); setEdit(null); load(); setMsg('Saved.'); } catch (e) { setMsg(String(e)); }
  }
  async function del(g: Group) {
    if (!window.confirm(`Delete group "${g.name}"? The devices stay.`)) return;
    await api(`/v1/groups/${g.id}`, { method: 'DELETE' }).catch(e => setMsg(String(e)));
    if (selected === g.id) onSelect('');
    load();
  }
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
        <span className="muted" style={{ fontSize: 12 }}>Groups</span>
        {groups.map(g => <button key={g.id} className={selected === g.id ? '' : 'ghost'} onClick={() => onSelect(selected === g.id ? '' : g.id)}>{g.name} ({g.devices})</button>)}
        <button className="ghost" onClick={() => setOpen(!open)}>{open ? 'Close' : 'Manage groups'}</button>
      </div>
      {open && (
        <div className="card" style={{ marginTop: 8, maxWidth: 640 }}>
          <form onSubmit={create} style={{ display: 'flex', gap: 8 }}>
            <input aria-label="New group name" placeholder="New group, e.g. Boiler house" value={name} onChange={e => setName(e.target.value)} />
            <button type="submit" disabled={!name.trim()}>Add group</button>
          </form>
          {groups.map(g => (
            <div key={g.id} style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 6 }}>
              <span style={{ flex: 1 }}>{g.name} <span className="muted">({g.devices} devices)</span></span>
              <button className="ghost" onClick={() => startEdit(g)}>Edit devices</button>
              <button className="ghost" onClick={() => del(g)}>Delete</button>
            </div>
          ))}
          {edit && (
            <div style={{ marginTop: 10 }}>
              <div style={{ maxHeight: 220, overflow: 'auto' }}>
                {all.map(d => (
                  <label key={d.id} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                    <input type="checkbox" style={{ width: 'auto' }} checked={members.has(d.id)} onChange={() => { const n = new Set(members); if (n.has(d.id)) n.delete(d.id); else n.add(d.id); setMembers(n); }} />
                    {d.name || d.id}
                  </label>
                ))}
              </div>
              <button onClick={save} style={{ marginTop: 8 }}>Save members</button> <button className="ghost" onClick={() => setEdit(null)}>Cancel</button>
            </div>
          )}
          {msg && <p className="muted" role="status">{msg}</p>}
        </div>
      )}
    </div>
  );
}
