import { useEffect, useMemo, useState } from 'react';
import DeviceGroups from '../components/DeviceGroups';
import { Link } from 'react-router-dom';
import { api, Device } from '../lib/api';
import Empty from '../components/Empty';

// The bulk endpoint answers 400 with {"problems":["line 3: ..."]}; show one problem per line.
function importProblems(e: unknown): string {
  const msg = String(e instanceof Error ? e.message : e);
  const i = msg.indexOf('{');
  if (i >= 0) {
    try {
      const j = JSON.parse(msg.slice(i));
      if (Array.isArray(j.problems)) return j.problems.join('\n');
    } catch { /* fall through */ }
  }
  return msg;
}

export default function Devices() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [q, setQ] = useState('');
  const [readOnly, setReadOnly] = useState(false);
  useEffect(() => { api<{ role: string; customer_id?: string }>('/v1/me').then(m => setReadOnly(m.role === 'viewer' || !!m.customer_id)).catch(() => {}); }, []);
  const [tag, setTag] = useState('');
  const [group, setGroup] = useState('');
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [err, setErr] = useState('');
  const [imp, setImp] = useState<{ text: string; would: number } | null>(null);
  const [impMsg, setImpMsg] = useState('');
  const pickFile = async (f?: File) => {
    if (!f) return;
    const text = await f.text();
    setImpMsg('');
    try {
      const r = await api<{ would_create: number }>('/v1/devices/bulk?dry_run=1', { method: 'POST', body: text, headers: { 'Content-Type': 'text/csv' } });
      setImp({ text, would: r.would_create });
    } catch (e) { setImp(null); setImpMsg(`Import rejected, nothing was created.\n${importProblems(e)}`); }
  };
  const confirmImport = () => {
    if (!imp) return;
    api<{ created: number }>('/v1/devices/bulk', { method: 'POST', body: imp.text, headers: { 'Content-Type': 'text/csv' } })
      .then(r => { setImpMsg(`Created ${r.created} devices.`); setImp(null); load(); })
      .catch(e => setImpMsg(`Import failed, nothing was created.\n${importProblems(e)}`));
  };

  const [hDev, setHDev] = useState('');
  const [hist, setHist] = useState<{ text: string; rows: number } | null>(null);
  const [histMsg, setHistMsg] = useState('');
  const pickHistory = async (f?: File) => {
    if (!f || !hDev) { setHistMsg('Choose a device first.'); return; }
    const text = await f.text();
    setHistMsg('');
    try {
      const r = await api<{ rows: number }>(`/v1/telemetry/import?device_id=${encodeURIComponent(hDev)}&dry_run=1`, { method: 'POST', body: text, headers: { 'Content-Type': 'text/csv' } });
      setHist({ text, rows: r.rows });
    } catch (e) { setHist(null); setHistMsg(`History import rejected, nothing was stored.\n${importProblems(e)}`); }
  };
  const confirmHistory = () => {
    if (!hist) return;
    api<{ imported: number; duplicates: number }>(`/v1/telemetry/import?device_id=${encodeURIComponent(hDev)}`, { method: 'POST', body: hist.text, headers: { 'Content-Type': 'text/csv' } })
      .then(r => { setHistMsg(`Imported ${r.imported} readings (${r.duplicates} already present). Alerts and flows are not run on imported history.`); setHist(null); })
      .catch(e => setHistMsg(`History import failed, nothing was stored.\n${importProblems(e)}`));
  };

  const load = () => {
    const p = new URLSearchParams();
    if (q.trim()) p.set('q', q.trim());
    if (tag) p.set('tag', tag);
    if (group) p.set('group_id', group);
    const qs = p.toString();
    api<Device[]>(`/v1/devices${qs ? `?${qs}` : ''}`).then(d => { setDevices(d); setErr(''); }).catch(e => setErr(String(e)));
  };
  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [q, tag, group]);

  const allTags = useMemo(() => Array.from(new Set(devices.flatMap(d => d.tags ?? []))).sort(), [devices]);
  const save = (id: string) => {
    const tags = draft.split(',').map(s => s.trim()).filter(Boolean);
    api(`/v1/devices/${id}/tags`, { method: 'PUT', body: JSON.stringify({ tags }) })
      .then(() => { setEditing(null); load(); }).catch(e => setErr(`Could not save tags: ${e}`));
  };
  const filtered = q.trim() !== '' || tag !== '' || group !== '';

  return (
    <>
      <h1>Devices</h1>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input style={{ flex: 1, maxWidth: 360 }} placeholder="Search name, id or profile" value={q} onChange={e => setQ(e.target.value)} />
        {tag && <button className="ghost" onClick={() => setTag('')}>tag: {tag} ✕</button>}
      </div>
      {!readOnly && <div style={{ marginBottom: 12 }}>
        <label className="muted" style={{ fontSize: 12 }}>Import CSV (gateway_id, profile_id, name, tags, asset_id){' '}
          <input type="file" accept=".csv,text/csv" onChange={e => { void pickFile(e.target.files?.[0]); e.target.value = ''; }} />
        </label>
        <div style={{ marginTop: 8 }}>
          <label className="muted" style={{ fontSize: 12 }}>Import history (admin; CSV ts, point, value, unit) into{' '}
            <select value={hDev} onChange={e => setHDev(e.target.value)}><option value="">choose device</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}</select>{' '}
            <input type="file" accept=".csv,text/csv" onChange={e => { void pickHistory(e.target.files?.[0]); e.target.value = ''; }} />
          </label>
          {hist && <span> Valid: {hist.rows} readings. <button onClick={confirmHistory}>Import {hist.rows}</button> <button className="ghost" onClick={() => setHist(null)}>Cancel</button></span>}
          {histMsg && <pre className="muted" style={{ whiteSpace: 'pre-wrap' }}>{histMsg}</pre>}
        </div>
        {imp && <span> Valid: {imp.would} devices. <button onClick={confirmImport}>Create {imp.would}</button> <button className="ghost" onClick={() => setImp(null)}>Cancel</button></span>}
        {impMsg && <p className="muted" style={{ whiteSpace: 'pre-wrap' }}>{impMsg}</p>}
      </div>}
      {!readOnly && <DeviceGroups selected={group} onSelect={setGroup} />}
      {allTags.length > 0 && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 12 }}>
          {allTags.map(t => <button key={t} className={tag === t ? '' : 'ghost'} onClick={() => setTag(tag === t ? '' : t)}>{t}</button>)}
        </div>
      )}
      {err && <p className="muted">{err}</p>}
      {devices.length === 0 && !err ? (
        filtered
          ? <Empty title="No matching devices" hint="Clear the search or tag filter to see the whole fleet." action={<button onClick={() => { setQ(''); setTag(''); setGroup(''); }}>Clear filters</button>} />
          : <Empty title="No devices yet" hint={readOnly ? "No devices are assigned to you yet." : "Add a device to start collecting data."} action={readOnly ? undefined : <Link to="/onboarding"><button>Add a device</button></Link>} />
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
