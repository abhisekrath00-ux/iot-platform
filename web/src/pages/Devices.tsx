import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, Device } from '../lib/api';
import Empty from '../components/Empty';

export default function Devices() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [q, setQ] = useState('');
  const [tag, setTag] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [err, setErr] = useState('');

  const load = () => {
    const p = new URLSearchParams();
    if (q.trim()) p.set('q', q.trim());
    if (tag) p.set('tag', tag);
    const qs = p.toString();
    api<Device[]>(`/v1/devices${qs ? `?${qs}` : ''}`).then(d => { setDevices(d); setErr(''); }).catch(e => setErr(String(e)));
  };
  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [q, tag]);

  const allTags = useMemo(() => Array.from(new Set(devices.flatMap(d => d.tags ?? []))).sort(), [devices]);
  const save = (id: string) => {
    const tags = draft.split(',').map(s => s.trim()).filter(Boolean);
    api(`/v1/devices/${id}/tags`, { method: 'PUT', body: JSON.stringify({ tags }) })
      .then(() => { setEditing(null); load(); }).catch(e => setErr(`Could not save tags: ${e}`));
  };
  const filtered = q.trim() !== '' || tag !== '';

  return (
    <>
      <h1>Devices</h1>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input style={{ flex: 1, maxWidth: 360 }} placeholder="Search name, id or profile" value={q} onChange={e => setQ(e.target.value)} />
        {tag && <button className="ghost" onClick={() => setTag('')}>tag: {tag} ✕</button>}
      </div>
      {allTags.length > 0 && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
          {allTags.map(t => <button key={t} className={tag === t ? '' : 'ghost'} onClick={() => setTag(tag === t ? '' : t)}>{t}</button>)}
        </div>
      )}
      {err && <p className="muted">{err}</p>}
      {devices.length === 0 && !err ? (
        filtered
          ? <Empty title="No matching devices" hint="Clear the search or tag filter to see the whole fleet." action={<button onClick={() => { setQ(''); setTag(''); }}>Clear filters</button>} />
          : <Empty title="No devices yet" hint="Add a device to start collecting data." action={<Link to="/onboarding"><button>Add a device</button></Link>} />
      ) : (
      <table>
        <thead><tr><th>Name</th><th>Profile</th><th>Gateway</th><th>Tags</th><th>Added</th></tr></thead>
        <tbody>
          {devices.map(d => (
            <tr key={d.id}>
              <td><Link to={`/devices/${d.id}`} style={{ color: 'var(--accent)' }}>{d.name}</Link></td>
              <td>{d.profile}</td><td>{d.gateway_id}</td>
              <td>
                {editing === d.id ? (
                  <span style={{ display: 'flex', gap: 6 }}>
                    <input value={draft} autoFocus placeholder="plant-a, critical" onChange={e => setDraft(e.target.value)} onKeyDown={e => e.key === 'Enter' && save(d.id)} />
                    <button onClick={() => save(d.id)}>Save</button>
                    <button className="ghost" onClick={() => setEditing(null)}>Cancel</button>
                  </span>
                ) : (
                  <span style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'center' }}>
                    {(d.tags ?? []).map(t => <span key={t} className="pill ok">{t}</span>)}
                    <button className="ghost" onClick={() => { setEditing(d.id); setDraft((d.tags ?? []).join(', ')); }}>{(d.tags ?? []).length ? 'Edit' : 'Add tags'}</button>
                  </span>
                )}
              </td>
              <td className="muted">{new Date(d.created_at).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>)}
    </>
  );
}
