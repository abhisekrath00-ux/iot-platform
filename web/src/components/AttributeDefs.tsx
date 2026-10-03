import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Def { key: string; type: string; enum_values: string[]; unit: string; description: string; required: boolean; }

// Typed attributes: define a name, a type (text, number, yes/no, or a list of allowed values) and
// whether it is required. Device attributes are then checked against it. Other names stay free-form.
export default function AttributeDefs() {
  const [defs, setDefs] = useState<Def[]>([]);
  const [key, setKey] = useState('');
  const [type, setType] = useState('string');
  const [vals, setVals] = useState('');
  const [req, setReq] = useState(false);
  const [msg, setMsg] = useState('');
  const load = () => api<Def[]>('/v1/attribute-defs').then(setDefs).catch(() => undefined);
  useEffect(() => { load(); }, []);
  async function add(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      await api(`/v1/attribute-defs/${encodeURIComponent(key)}`, { method: 'PUT', body: JSON.stringify({ type, enum_values: type === 'enum' ? vals.split(',').map(s => s.trim()).filter(Boolean) : [], required: req }) });
      setKey(''); setVals(''); setReq(false); load();
    } catch (e2) { setMsg(String(e2)); }
  }
  const del = (k: string) => api(`/v1/attribute-defs/${encodeURIComponent(k)}`, { method: 'DELETE' }).then(load).catch(e => setMsg(String(e)));
  return (
    <div className="card" style={{ maxWidth: 760, marginTop: 20 }}>
      <b>Device attribute types</b>
      <p className="muted">Define attributes once (type, allowed values, required). Saving a device's attributes is then checked against them. Admins only; names you do not define stay free-form.</p>
      <form onSubmit={add} style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
        <input aria-label="Attribute name" placeholder="name, e.g. zone" value={key} onChange={e => setKey(e.target.value)} style={{ maxWidth: 160 }} />
        <select aria-label="Attribute type" value={type} onChange={e => setType(e.target.value)} style={{ width: 120, flex: 'none' }}><option value="string">text</option><option value="number">number</option><option value="boolean">yes/no</option><option value="enum">one of</option></select>
        {type === 'enum' && <input aria-label="Allowed values" placeholder="A, B, C" value={vals} onChange={e => setVals(e.target.value)} style={{ maxWidth: 180 }} />}
        <label className="muted"><input type="checkbox" style={{ width: 'auto' }} checked={req} onChange={e => setReq(e.target.checked)} /> required</label>
        <button type="submit" disabled={!key.trim()}>Add</button>
      </form>
      {defs.length > 0 && <table style={{ marginTop: 10 }}><thead><tr><th>Name</th><th>Type</th><th>Required</th><th /></tr></thead>
        <tbody>{defs.map(d => <tr key={d.key}><td>{d.key}</td><td>{d.type === 'enum' ? `one of ${d.enum_values.join(', ')}` : d.type}</td><td>{d.required ? 'yes' : ''}</td><td><button className="ghost" onClick={() => del(d.key)}>Remove</button></td></tr>)}</tbody></table>}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
