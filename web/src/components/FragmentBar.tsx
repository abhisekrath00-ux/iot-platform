import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { Graph, GNode, GEdge, isStart } from '../lib/graph';

interface Frag { id: string; name: string; nodes: number; }

/** Reusable flow pieces. Insert copies the nodes into this flow. Insert as subflow adds a live reference: the server expands it when you save a draft and pins the fragment version, so editing the fragment never changes a published flow until you refresh and publish. */
export default function FragmentBar({ g, setG }: { g: Graph; setG: (fn: (g: Graph) => Graph) => void }) {
  const [frags, setFrags] = useState<Frag[]>([]);
  const [pick, setPick] = useState('');
  const [name, setName] = useState('');
  const [msg, setMsg] = useState('');
  const load = () => { api<Frag[]>('/v1/flow-fragments').then(setFrags).catch(() => {}); };
  useEffect(load, []);

  const insert = async () => {
    setMsg('');
    let n = 1;
    while (g.nodes.some(x => x.id.startsWith(`f${n}_`))) n++;
    const maxX = Math.max(60, ...g.nodes.map(x => x.x + 170));
    try {
      const r = await api<{ nodes: GNode[]; edges: GEdge[]; entry: string }>(`/v1/flow-fragments/${pick}/instantiate`, { method: 'POST', body: JSON.stringify({ prefix: `f${n}`, x: maxX, y: 40 }) });
      setG(cur => ({ nodes: [...cur.nodes, ...r.nodes], edges: [...cur.edges, ...r.edges] }));
      setMsg(`Inserted. Connect an arrow into "${r.entry}" and out of the last node.`);
    } catch (e) { setMsg(String(e)); }
  };
  const insertRef = () => {
    setMsg('');
    let n = 1;
    while (g.nodes.some(x => x.id === `sf${n}` || x.id.startsWith(`sf${n}_`))) n++;
    const maxX = Math.max(60, ...g.nodes.map(x => x.x + 170));
    setG(cur => ({ nodes: [...cur.nodes, { id: `sf${n}`, type: 'subflow', fragment_id: pick, x: maxX, y: 40 } as GNode], edges: cur.edges }));
    setMsg(`Added subflow "sf${n}" (latest version, pinned when you save the draft). Connect arrows into and out of it.`);
  };
  const save = async () => {
    setMsg('');
    const starts = new Set(g.nodes.filter(isStart).map(x => x.id));
    const graph = { nodes: g.nodes.filter(x => !starts.has(x.id)), edges: g.edges.filter(e => !starts.has(e.from) && !starts.has(e.to)) };
    try { await api('/v1/flow-fragments', { method: 'POST', body: JSON.stringify({ name, graph }) }); setName(''); setMsg('Saved as a fragment.'); load(); }
    catch (e) { setMsg(String(e)); }
  };

  return (
    <div className="fe-toolbar" style={{ flexWrap: 'wrap' }}>
      <span className="muted">Fragments:</span>
      <select aria-label="Fragment" value={pick} onChange={e => setPick(e.target.value)} style={{ width: 'auto' }}>
        <option value="">Pick one</option>
        {frags.map(f => <option key={f.id} value={f.id}>{f.name} ({f.nodes} nodes)</option>)}
      </select>
      <button type="button" className="ghost" disabled={!pick} onClick={insert}>Insert copy</button>
      <button type="button" className="ghost" disabled={!pick} onClick={insertRef}>Insert as subflow</button>
      <button type="button" className="ghost" disabled={!pick} onClick={() => api(`/v1/flow-fragments/${pick}`, { method: 'DELETE' }).catch(() => {}).then(() => { setPick(''); load(); })}>Delete</button>
      <input aria-label="New fragment name" value={name} onChange={e => setName(e.target.value)} placeholder="Save this flow (minus the start node) as..." style={{ maxWidth: 300 }} maxLength={80} />
      <button type="button" className="ghost" disabled={!name.trim()} onClick={save}>Save fragment</button>
      {msg && <span className="muted" role="status">{msg}</span>}
    </div>
  );
}
