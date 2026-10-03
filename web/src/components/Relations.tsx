import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Asset { id: string; name: string; }
interface Rel { from_kind: string; from_id: string; relation: string; to_kind: string; to_id: string; }
interface Hit { kind: string; id: string; distance: number; }

// Typed, directed links between assets: "Pump house feeds Boiler house". They are separate from the
// plant tree (which says where something sits) and say what depends on what. Impact follows one
// relation forward and lists everything downstream.
export default function Relations({ assets }: { assets: Asset[] }) {
  const [sel, setSel] = useState('');
  const [rels, setRels] = useState<Rel[]>([]);
  const [from, setFrom] = useState('');
  const [name, setName] = useState('feeds');
  const [to, setTo] = useState('');
  const [impact, setImpact] = useState<{ rel: string; hits: Hit[] } | null>(null);
  const [msg, setMsg] = useState('');
  const label = (id: string) => assets.find(a => a.id === id)?.name ?? id;
  const load = (id: string) => id
    ? api<Rel[]>(`/v1/relations?kind=asset&id=${encodeURIComponent(id)}`).then(setRels).catch(e => setMsg(String(e)))
    : setRels([]);
  useEffect(() => { setImpact(null); load(sel); }, [sel]);
  async function add() {
    setMsg('');
    try {
      await api('/v1/relations', { method: 'POST', body: JSON.stringify({ from_kind: 'asset', from_id: from, relation: name, to_kind: 'asset', to_id: to }) });
      setSel(from); load(from);
    } catch (e) { setMsg(String(e)); }
  }
  async function remove(r: Rel) {
    setMsg('');
    try { await api('/v1/relations', { method: 'DELETE', body: JSON.stringify(r) }); load(sel); } catch (e) { setMsg(String(e)); }
  }
  async function showImpact(rel: string) {
    setMsg('');
    try { setImpact({ rel, hits: await api<Hit[]>(`/v1/relations/downstream?kind=asset&id=${encodeURIComponent(sel)}&relation=${encodeURIComponent(rel)}`) }); } catch (e) { setMsg(String(e)); }
  }
  const names = [...new Set(rels.filter(r => r.from_id === sel).map(r => r.relation))];
  return (
    <div className="card" style={{ maxWidth: 900, marginBottom: 20 }}>
      <b>Relations</b>
      <p className="muted">Say what depends on what, for example a pump house feeds a boiler house. This is separate from the plant tree. Operators and admins can edit.</p>
      <label htmlFor="rel-sel">Show relations of</label>
      <select id="rel-sel" value={sel} onChange={e => setSel(e.target.value)}>
        <option value="">Select an asset</option>
        {assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
      </select>
      {sel && rels.length === 0 && <p className="muted">No relations for {label(sel)}.</p>}
      {rels.map(r => (
        <div key={`${r.from_id}|${r.relation}|${r.to_id}`} style={{ display: 'flex', gap: 8, alignItems: 'center', margin: '6px 0' }}>
          <span>{label(r.from_id)}</span><span className="pill">{r.relation}</span><span>{label(r.to_id)}</span>
          <button className="ghost" onClick={() => remove(r)}>Remove</button>
        </div>
      ))}
      {names.map(n => <button key={n} className="ghost" style={{ marginRight: 6 }} onClick={() => showImpact(n)}>If {label(sel)} fails: what does "{n}" reach?</button>)}
      {impact && (impact.hits.length === 0
        ? <p className="muted">Nothing downstream over "{impact.rel}".</p>
        : <ul>{impact.hits.map(h => <li key={h.id}>{label(h.id)} <span className="muted">({h.distance} step{h.distance === 1 ? '' : 's'} away)</span></li>)}</ul>)}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginTop: 12 }}>
        <select aria-label="From asset" value={from} onChange={e => setFrom(e.target.value)} style={{ width: 200 }}>
          <option value="">from...</option>{assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
        </select>
        <input aria-label="Relation" list="rel-names" value={name} onChange={e => setName(e.target.value.toLowerCase())} style={{ width: 150 }} />
        <datalist id="rel-names"><option value="feeds" /><option value="powers" /><option value="backs_up" /><option value="depends_on" /></datalist>
        <select aria-label="To asset" value={to} onChange={e => setTo(e.target.value)} style={{ width: 200 }}>
          <option value="">to...</option>{assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
        </select>
        <button disabled={!from || !to || !name} onClick={add}>Add relation</button>
      </div>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
