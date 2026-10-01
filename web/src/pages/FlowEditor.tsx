import { History, emptyHistory, record, redo, undo } from '../lib/history';
import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
import {
  GEdge, GNode, Graph, LABELS, NODE_H, NODE_W, NodeType, addNode, connect, edgePath, emptyGraph,
  portCount, portY, problems, prunePorts, removeNode
} from '../lib/graph';

interface Channel { id: string; type: string; target: string; }
interface FlowRow { id: string; name: string; definition: any; enabled: boolean; published_version?: number | null; latest_version?: number | null; }
interface TestResult { matched: boolean; actions: { channel_id: string; message: string; delay_seconds: number }[]; debug: { node: string; message: string }[]; }

const OPS = ['>', '<', '>=', '<=', '==', '!='];
const SW_OPS = ['==', '!=', '>', '<', '>=', '<=', 'contains', 'else'];

// One open flow. flowId is set once it exists on the server; version is the
// stored version this tab last saved or opened.
interface Tab { key: string; flowId?: string; version?: number; name: string; g: Graph; dirty: boolean; sel: string | null; msg: string; result: TestResult | null; testValue: string; hist: History; }

let tabCounter = 0;
const newTab = (over: Partial<Tab> = {}): Tab => ({ key: `t${++tabCounter}`, name: '', g: emptyGraph(), dirty: false, sel: null, msg: '', result: null, testValue: '', hist: emptyHistory(), ...over });

// Visual editor for node-graph flows, with a manager for many flows and one
// tab per open flow. Everything is saved as an unpublished draft; publishing is
// a separate, explicit button. The server validates.
export default function FlowEditor() {
  const [tabs, setTabs] = useState<Tab[]>(() => [newTab()]);
  const [active, setActive] = useState(() => `t${tabCounter}`);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [flows, setFlows] = useState<FlowRow[]>([]);
  const [fnOn, setFnOn] = useState(false);
  const [mgrMsg, setMgrMsg] = useState('');
  const [link, setLink] = useState<{ from: string; port: number } | null>(null);
  const [mouse, setMouse] = useState({ x: 0, y: 0 });
  const drag = useRef<{ id: string; dx: number; dy: number } | null>(null);
  const svg = useRef<SVGSVGElement>(null);

  const tab = tabs.find(t => t.key === active) ?? tabs[0];
  const patchTab = (key: string, p: Partial<Tab> | ((t: Tab) => Partial<Tab>)) =>
    setTabs(ts => ts.map(t => {
      if (t.key !== key) return t;
      const np = typeof p === 'function' ? p(t) : p;
      // an edit to the graph from the user (not a load, undo or redo) is recorded for undo
      if (np.g && np.g !== t.g && np.hist === undefined) return { ...t, ...np, hist: record(t.hist, t.g, Date.now()) };
      return { ...t, ...np };
    }));
  const stepHistory = (dir: 'undo' | 'redo') => {
    const r = (dir === 'undo' ? undo : redo)(tab.hist, tab.g);
    if (r) patchTab(tab.key, { g: r.g, hist: r.h, dirty: true, sel: null });
  };
  const setG = (fn: (g: Graph) => Graph, dirty = true) => patchTab(tab.key, t => ({ g: fn(t.g), dirty: dirty || t.dirty }));
  const g = tab.g;

  const loadFlows = () => api<FlowRow[]>('/v1/flows').then(setFlows).catch(() => {});
  useEffect(() => {
    api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(() => {});
    loadFlows();
    api<{ function_nodes: boolean }>('/v1/features').then(f => setFnOn(f.function_nodes)).catch(() => {});
  }, []);

  const node = g.nodes.find(n => n.id === tab.sel) ?? null;
  const upd = (id: string, patch: Partial<GNode>) =>
    setG(cur => prunePorts({ ...cur, nodes: cur.nodes.map(n => (n.id === id ? { ...n, ...patch } : n)) }));

  function pt(e: React.PointerEvent) {
    const r = svg.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  function tryConnect(from: string, port: number, to: string) {
    const r = connect(g, from, String(port), to);
    if (typeof r === 'string') patchTab(tab.key, { msg: r }); else patchTab(tab.key, { g: r, dirty: true, msg: '' });
  }

  // ---- tabs ----
  function addTab(t: Tab) { setTabs(ts => [...ts, t]); setActive(t.key); }
  function closeTab(key: string) {
    const t = tabs.find(x => x.key === key)!;
    if (t.dirty && !window.confirm(`Close "${t.name || 'Untitled flow'}" without saving?`)) return;
    const rest = tabs.filter(x => x.key !== key);
    const next = rest.length ? rest : [newTab()];
    setTabs(next);
    if (key === active) setActive(next[next.length - 1].key);
  }

  // ---- manager ----
  async function openFlow(f: FlowRow) {
    const open = tabs.find(t => t.flowId === f.id);
    if (open) { setActive(open.key); return; }
    try {
      const version = f.latest_version ?? f.published_version ?? 1;
      const v = await api<{ version: number; definition: any }>(`/v1/flows/${f.id}/versions/${version}`);
      const graph: Graph = v.definition.graph ?? (await api<{ graph: Graph }>('/v1/flows/convert', { method: 'POST', body: JSON.stringify({ definition: v.definition }) })).graph;
      const gg: Graph = { nodes: graph.nodes.map(n => ({ ...n, x: n.x ?? 40, y: n.y ?? 40 })) as GNode[], edges: graph.edges.map(e => ({ ...e, port: e.port || '0' })) };
      const t = newTab({ flowId: f.id, version: v.version, name: f.name, g: gg });
      // replace an untouched blank tab instead of stacking one next to it
      setTabs(ts => (ts.length === 1 && !ts[0].flowId && !ts[0].dirty ? [t] : [...ts, t]));
      setActive(t.key);
    } catch (e) { setMgrMsg(String(e)); }
  }
  async function manage(f: FlowRow, what: 'duplicate' | 'rename' | 'toggle' | 'delete') {
    setMgrMsg('');
    try {
      if (what === 'duplicate') {
        await api(`/v1/flows/${f.id}/duplicate`, { method: 'POST', body: '{}' });
        setMgrMsg(`Duplicated "${f.name}" as an unpublished draft.`);
      } else if (what === 'rename') {
        const n = window.prompt('New name', f.name);
        if (!n || n === f.name) return;
        await api(`/v1/flows/${f.id}`, { method: 'PATCH', body: JSON.stringify({ name: n }) });
        setTabs(ts => ts.map(t => (t.flowId === f.id ? { ...t, name: n } : t)));
      } else if (what === 'toggle') {
        await api(`/v1/flows/${f.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: !f.enabled }) });
      } else {
        if (!window.confirm(`Delete "${f.name}" with all its versions and run history? This cannot be undone.`)) return;
        await api(`/v1/flows/${f.id}`, { method: 'DELETE' });
        setTabs(ts => { const rest = ts.filter(t => t.flowId !== f.id); return rest.length ? rest : [newTab()]; });
        setActive(a => (tabs.find(t => t.key === a)?.flowId === f.id ? '' : a));
      }
      loadFlows();
    } catch (e) { setMgrMsg(String(e)); }
  }
  useEffect(() => { if (!tabs.some(t => t.key === active)) setActive(tabs[tabs.length - 1].key); }, [tabs, active]);

  // ---- per-tab actions ----
  const def = () => ({ graph: { nodes: g.nodes, edges: g.edges } });
  async function test() {
    patchTab(tab.key, { msg: '', result: null });
    try {
      const r = await api<TestResult>('/v1/flows/graph/test', { method: 'POST', body: JSON.stringify({ definition: def(), value: parseFloat(tab.testValue) }) });
      patchTab(tab.key, { result: r });
    } catch (e) { patchTab(tab.key, { msg: String(e) }); }
  }
  async function save() {
    patchTab(tab.key, { msg: '' });
    try {
      if (tab.flowId) {
        const r = await api<{ version: number }>(`/v1/flows/${tab.flowId}/draft`, { method: 'POST', body: JSON.stringify({ definition: def() }) });
        patchTab(tab.key, { version: r.version, dirty: false, msg: `Saved as draft version ${r.version}. It does not run until you publish it.` });
      } else {
        const r = await api<{ id: string; version: number }>('/v1/flows?draft=1', { method: 'POST', body: JSON.stringify({ name: tab.name, definition: def() }) });
        patchTab(tab.key, { flowId: r.id, version: r.version, dirty: false, msg: 'Saved as a draft. It does not run until you publish it.' });
      }
      loadFlows();
    } catch (e) { patchTab(tab.key, { msg: String(e) }); }
  }
  async function publish() {
    if (!tab.flowId || !tab.version || tab.dirty) return;
    if (!window.confirm(`Publish "${tab.name}" version ${tab.version}? It will start notifying when readings match.`)) return;
    try {
      await api(`/v1/flows/${tab.flowId}/publish`, { method: 'POST', body: JSON.stringify({ version: tab.version }) });
      patchTab(tab.key, { msg: `Published version ${tab.version}.` });
      loadFlows();
    } catch (e) { patchTab(tab.key, { msg: String(e) }); }
  }

  const hints = problems(g);
  const width = Math.max(900, ...g.nodes.map(n => n.x + NODE_W + 60));
  const height = Math.max(460, ...g.nodes.map(n => n.y + NODE_H + 60));
  const types: NodeType[] = ['switch', 'change', 'condition', 'delay', 'debug', 'notify', 'template', 'range', 'rate_limit', ...(fnOn ? ['function' as NodeType] : [])];
  const status = (f: FlowRow) => (f.published_version ? `published v${f.published_version}${f.latest_version && f.latest_version > f.published_version ? ` (draft v${f.latest_version} pending)` : ''}` : 'draft, not published') + (f.enabled ? '' : ' - disabled');

  return (
    <>
      <h1>Flow editor</h1>
      <p className="muted">Build flows as graphs: a reading comes in, nodes route and change it, notify nodes send. Saved as drafts; nothing runs until you publish.</p>

      <div className="card" style={{ marginBottom: 14 }}>
        <div className="fe-toolbar" style={{ marginTop: 0 }}>
          <b>Your flows</b>
          <span className="muted">{flows.length} total</span>
          <button type="button" onClick={() => addTab(newTab())}>+ New flow</button>
        </div>
        {flows.length === 0 ? <p className="muted">No flows yet. Click New flow.</p> : (
          <table className="fe-flows">
            <thead><tr><th>Name</th><th>Status</th><th></th></tr></thead>
            <tbody>
              {flows.map(f => (
                <tr key={f.id}>
                  <td><button type="button" className="link" onClick={() => openFlow(f)}>{f.name}</button></td>
                  <td className="muted">{status(f)}</td>
                  <td className="fe-actions">
                    <button type="button" className="ghost" onClick={() => openFlow(f)}>Open</button>
                    <button type="button" className="ghost" onClick={() => manage(f, 'duplicate')}>Duplicate</button>
                    <button type="button" className="ghost" onClick={() => manage(f, 'rename')}>Rename</button>
                    <button type="button" className="ghost" onClick={() => manage(f, 'toggle')}>{f.enabled ? 'Disable' : 'Enable'}</button>
                    <button type="button" className="ghost" onClick={() => manage(f, 'delete')}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {mgrMsg && <p className="muted" role="status">{mgrMsg}</p>}
      </div>

      <div className="fe-tabs" role="tablist" aria-label="Open flows">
        {tabs.map(t => (
          <span key={t.key} className={`fe-tab${t.key === tab.key ? ' on' : ''}`}>
            <button type="button" role="tab" aria-selected={t.key === tab.key} onClick={() => setActive(t.key)}>
              {t.name || 'Untitled flow'}{t.dirty ? ' \u2022' : ''}
            </button>
            <button type="button" className="x" aria-label={`Close ${t.name || 'Untitled flow'}`} onClick={() => closeTab(t.key)}>{'\u00d7'}</button>
          </span>
        ))}
        <button type="button" className="fe-tab-add" aria-label="New flow tab" onClick={() => addTab(newTab())}>+</button>
      </div>

      <div className="fe-toolbar">
        <input aria-label="Flow name" value={tab.name} disabled={!!tab.flowId} title={tab.flowId ? 'Use Rename in the list above' : ''} onChange={e => patchTab(tab.key, { name: e.target.value, dirty: true })} placeholder="Flow name" style={{ maxWidth: 260 }} />
        <span className="muted">Add:</span>
        {types.map(t => <button key={t} type="button" className="ghost" onClick={() => setG(cur => addNode(cur, t, 60 + (cur.nodes.length % 5) * 30, 40 + cur.nodes.length * 40 % 300))}>+ {LABELS[t]}</button>)}
      </div>
      <div className="fe-layout">
        <div className="fe-canvas card" style={{ padding: 0 }}>
          <svg
            ref={svg} width={width} height={height} role="application" aria-label="Flow graph canvas"
            onPointerMove={e => {
              const p = pt(e);
              setMouse(p);
              if (drag.current) { const d = drag.current; upd(d.id, { x: Math.max(0, p.x - d.dx), y: Math.max(0, p.y - d.dy) }); }
            }}
            onPointerUp={() => { drag.current = null; setLink(null); }}
            onPointerLeave={() => { drag.current = null; }}
            onPointerDown={e => { if (e.target === svg.current) patchTab(tab.key, { sel: null }); }}
          >
            {g.edges.map((e: GEdge) => (
              <path key={`${e.from}-${e.port}-${e.to}`} d={edgePath(g, e)} className="fe-edge" fill="none"
                onClick={() => setG(cur => ({ ...cur, edges: cur.edges.filter(x => x !== e) }))}>
                <title>Click to remove this connection</title>
              </path>
            ))}
            {link && (() => {
              const a = g.nodes.find(n => n.id === link.from)!;
              return <path d={`M${a.x + NODE_W},${portY(a, link.port)} L${mouse.x},${mouse.y}`} className="fe-edge fe-ghost" fill="none" />;
            })()}
            {g.nodes.map(n => (
              <g key={n.id} transform={`translate(${n.x},${n.y})`} className={`fe-node fe-${n.type}${tab.sel === n.id ? ' sel' : ''}`}
                onPointerDown={e => { e.stopPropagation(); patchTab(tab.key, { sel: n.id }); const p = pt(e); drag.current = { id: n.id, dx: p.x - n.x, dy: p.y - n.y }; }}
                onPointerUp={e => { if (link && link.from !== n.id) { e.stopPropagation(); tryConnect(link.from, link.port, n.id); setLink(null); } }}
                tabIndex={0} role="button" aria-label={`${LABELS[n.type]} node ${n.name || n.id}`}
                onFocus={() => patchTab(tab.key, { sel: n.id })}
                onKeyDown={e => { if (e.key === 'Delete' || e.key === 'Backspace') { if (n.type !== 'trigger') { setG(cur => removeNode(cur, n.id)); patchTab(tab.key, { sel: null }); } } }}>
                <rect width={NODE_W} height={NODE_H} rx={14} />
                <text x={14} y={22} className="fe-title">{LABELS[n.type]}</text>
                <text x={14} y={40} className="fe-sub">{summary(n)}</text>
                {n.type !== 'trigger' && <circle cx={0} cy={NODE_H / 2} r={6} className="fe-port" />}
                {Array.from({ length: portCount(n) }, (_, i) => (
                  <circle key={i} cx={NODE_W} cy={portY(n, i) - n.y} r={7} className="fe-port out"
                    onPointerDown={e => { e.stopPropagation(); setLink({ from: n.id, port: i }); }}>
                    <title>Drag to another node to connect</title>
                  </circle>
                ))}
              </g>
            ))}
          </svg>
        </div>
        <div className="fe-side">
          {node ? <Props n={node} g={g} channels={channels} upd={p => upd(node.id, p)}
            connectTo={(port, to) => tryConnect(node.id, port, to)}
            remove={() => { setG(cur => removeNode(cur, node.id)); patchTab(tab.key, { sel: null }); }} /> :
            <div className="card"><b>Select a node</b><p className="muted">Click a node to edit it. Drag from the round dot on its right edge to another node to connect them. Click a line to remove it. Delete removes the selected node.</p></div>}
        </div>
      </div>
      <div className="card" style={{ marginTop: 16 }}>
        {hints.length > 0 && <ul className="muted" style={{ marginTop: 0 }}>{hints.map(h => <li key={h}>{h}</li>)}</ul>}
        <div className="fe-toolbar">
          <input aria-label="Test value" type="number" step="any" value={tab.testValue} onChange={e => patchTab(tab.key, { testValue: e.target.value })} placeholder="test reading" style={{ maxWidth: 160 }} />
          <button type="button" className="ghost" onClick={() => stepHistory('undo')} disabled={tab.hist.past.length === 0}>Undo</button>
          <button type="button" className="ghost" onClick={() => stepHistory('redo')} disabled={tab.hist.future.length === 0}>Redo</button>
          <button type="button" className="ghost" onClick={test} disabled={tab.testValue === ''}>Test (nothing is sent)</button>
          <button type="button" onClick={save} disabled={!tab.name || hints.length > 0 || (!!tab.flowId && !tab.dirty)}>{tab.flowId ? 'Save as new draft version' : 'Save draft'}</button>
          <button type="button" className="ghost" onClick={publish} disabled={!tab.flowId || tab.dirty}>Publish saved version</button>
        </div>
        {tab.msg && <p className="muted" role="status">{tab.msg}</p>}
        {tab.result && <div role="status">
          <b>{tab.result.matched ? 'Flow starts for this reading' : 'The reading node does not match; nothing happens'}</b>
          {tab.result.actions.map((a, i) => <div key={i} className="muted">notify {channels.find(c => c.id === a.channel_id)?.target ?? a.channel_id}{a.delay_seconds ? ` after ${a.delay_seconds}s` : ''}: {a.message}</div>)}
          {tab.result.debug.map((d, i) => <div key={i} className="muted">debug {d.node}: {d.message}</div>)}
        </div>}
      </div>
    </>
  );
}

function summary(n: GNode): string {
  switch (n.type) {
    case 'trigger': return n.device_id ? `${n.device_id}/${n.point_id} ${n.op} ${n.value}` : 'set device and point';
    case 'switch': return `${n.property} - ${n.rules?.length ?? 0} rule${n.rules?.length === 1 ? '' : 's'}`;
    case 'change': return `${n.changes?.length ?? 0} change${n.changes?.length === 1 ? '' : 's'}`;
    case 'condition': return `value ${n.op} ${n.value}`;
    case 'delay': return `${n.seconds}s`;
    case 'notify': return n.message || 'notify';
    case 'debug': return n.message || 'debug';
    case 'rate_limit': return `1 per ${n.seconds}s`;
    case 'template': return `vars.${n.target}`;
    case 'range': return `${n.in_min}-${n.in_max} to ${n.out_min}-${n.out_max}`;
    case 'function': return 'JavaScript';
  }
}

function Props({ n, g, channels, upd, connectTo, remove }: {
  n: GNode; g: Graph; channels: Channel[]; upd: (p: Partial<GNode>) => void;
  connectTo: (port: number, to: string) => void; remove: () => void;
}) {
  const [to, setTo] = useState('');
  const [port, setPort] = useState(0);
  const targets = g.nodes.filter(x => x.id !== n.id && x.type !== 'trigger');
  return (
    <div className="card">
      <b>{LABELS[n.type]} node</b> <span className="muted">{n.id}</span>
      <label htmlFor="np-name">Label</label>
      <input id="np-name" value={n.name ?? ''} maxLength={64} onChange={e => upd({ name: e.target.value })} />
      {n.type === 'trigger' && <>
        <label htmlFor="np-dev">Device</label>
        <input id="np-dev" value={n.device_id ?? ''} onChange={e => upd({ device_id: e.target.value })} placeholder="meter-1" />
        <label htmlFor="np-pt">Point</label>
        <input id="np-pt" value={n.point_id ?? ''} onChange={e => upd({ point_id: e.target.value })} placeholder="kwh" />
        <label htmlFor="np-op">Starts when value</label>
        <div style={{ display: 'flex', gap: 8 }}>
          <select id="np-op" value={n.op} onChange={e => upd({ op: e.target.value })} style={{ width: 80 }}>{OPS.map(o => <option key={o}>{o}</option>)}</select>
          <input aria-label="Trigger value" type="number" step="any" value={n.value ?? 0} onChange={e => upd({ value: parseFloat(e.target.value) || 0 })} />
        </div>
      </>}
      {n.type === 'condition' && <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <select aria-label="Condition operator" value={n.op} onChange={e => upd({ op: e.target.value })} style={{ width: 80 }}>{OPS.map(o => <option key={o}>{o}</option>)}</select>
        <input aria-label="Condition value" type="number" step="any" value={n.value ?? 0} onChange={e => upd({ value: parseFloat(e.target.value) || 0 })} />
      </div>}
      {n.type === 'delay' && <><label htmlFor="np-sec">Wait (seconds, 1 to 3600)</label>
        <input id="np-sec" type="number" min={1} max={3600} value={n.seconds ?? 1} onChange={e => upd({ seconds: parseInt(e.target.value) || 1 })} /></>}
      {n.type === 'rate_limit' && <><label htmlFor="np-rl">Let one message through every (seconds, 1 to 86400)</label>
        <input id="np-rl" type="number" min={1} max={86400} value={n.seconds ?? 60} onChange={e => upd({ seconds: parseInt(e.target.value) || 1 })} />
        <p className="muted">Extra messages are dropped. The count is kept per API process, and the test button does not apply it.</p></>}
      {n.type === 'notify' && <>
        <label htmlFor="np-ch">Channel</label>
        <select id="np-ch" value={n.channel_id ?? ''} onChange={e => upd({ channel_id: e.target.value })}>
          <option value="">choose...</option>{channels.map(c => <option key={c.id} value={c.id}>{c.type}: {c.target}</option>)}
        </select>
        <label htmlFor="np-msg">Message</label>
        <input id="np-msg" value={n.message ?? ''} maxLength={500} onChange={e => upd({ message: e.target.value })} />
        <p className="muted">Use {'{value}'}, {'{device_id}'}, {'{point_id}'}, {'{vars.name}'}</p>
      </>}
      {n.type === 'debug' && <><label htmlFor="np-dm">Log message</label>
        <input id="np-dm" value={n.message ?? ''} maxLength={500} onChange={e => upd({ message: e.target.value })} /></>}
      {n.type === 'switch' && <>
        <label htmlFor="np-prop">Check</label>
        <input id="np-prop" value={n.property ?? ''} onChange={e => upd({ property: e.target.value })} placeholder="value or vars.name" />
        <label htmlFor="np-mode">When several rules match</label>
        <select id="np-mode" value={n.mode ?? 'first'} onChange={e => upd({ mode: e.target.value })}>
          <option value="first">use the first only</option><option value="all">send to all</option>
        </select>
        <label>Rules (each is its own output)</label>
        {(n.rules ?? []).map((r, i) => (
          <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6 }}>
            <select aria-label={`Rule ${i + 1} operator`} value={r.op} onChange={e => upd({ rules: n.rules!.map((x, j) => (j === i ? { ...x, op: e.target.value } : x)) })} style={{ width: 96 }}>
              {SW_OPS.map(o => <option key={o}>{o}</option>)}</select>
            {r.op !== 'else' && <input aria-label={`Rule ${i + 1} value`} value={r.value ?? ''} onChange={e => {
              const v = e.target.value; const num = v !== '' && !isNaN(Number(v));
              upd({ rules: n.rules!.map((x, j) => (j === i ? { ...x, value: num ? Number(v) : v } : x)) });
            }} />}
            <button type="button" className="ghost" aria-label={`Remove rule ${i + 1}`} disabled={(n.rules?.length ?? 0) <= 1} onClick={() => upd({ rules: n.rules!.filter((_, j) => j !== i) })}>x</button>
          </div>
        ))}
        <button type="button" className="ghost" disabled={(n.rules?.length ?? 0) >= 10} onClick={() => upd({ rules: [...(n.rules ?? []), { op: '>', value: 0 }] })}>+ rule</button>
      </>}
      {n.type === 'change' && <>
        <label>Changes</label>
        {(n.changes ?? []).map((c, i) => (
          <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 6, flexWrap: 'wrap' }}>
            <select aria-label={`Change ${i + 1} action`} value={c.action} onChange={e => upd({ changes: n.changes!.map((x, j) => (j === i ? { ...x, action: e.target.value } : x)) })} style={{ width: 84 }}>
              {['set', 'delete', 'move', 'add', 'mul'].map(o => <option key={o}>{o}</option>)}</select>
            <input aria-label={`Change ${i + 1} property`} value={c.property} onChange={e => upd({ changes: n.changes!.map((x, j) => (j === i ? { ...x, property: e.target.value } : x)) })} placeholder="value or vars.x" style={{ width: 120 }} />
            {c.action === 'move' && <input aria-label={`Change ${i + 1} target`} value={c.to ?? ''} onChange={e => upd({ changes: n.changes!.map((x, j) => (j === i ? { ...x, to: e.target.value } : x)) })} placeholder="to vars.y" style={{ width: 100 }} />}
            {c.action !== 'delete' && c.action !== 'move' && <input aria-label={`Change ${i + 1} value`} value={c.value ?? ''} onChange={e => {
              const v = e.target.value; const num = v !== '' && !isNaN(Number(v));
              upd({ changes: n.changes!.map((x, j) => (j === i ? { ...x, value: num ? Number(v) : v } : x)) });
            }} style={{ width: 100 }} />}
            <button type="button" className="ghost" aria-label={`Remove change ${i + 1}`} disabled={(n.changes?.length ?? 0) <= 1} onClick={() => upd({ changes: n.changes!.filter((_, j) => j !== i) })}>x</button>
          </div>
        ))}
        <button type="button" className="ghost" disabled={(n.changes?.length ?? 0) >= 10} onClick={() => upd({ changes: [...(n.changes ?? []), { action: 'set', property: 'vars.name', value: '' }] })}>+ change</button>
      </>}
      {n.type === 'template' && <>
        <label htmlFor="np-tpl">Text ({'{value}'}, {'{device_id}'}, {'{vars.x}'})</label>
        <input id="np-tpl" maxLength={500} value={n.template ?? ''} onChange={e => upd({ template: e.target.value })} />
        <label htmlFor="np-tg">Store in variable</label>
        <input id="np-tg" maxLength={40} value={n.target ?? ''} onChange={e => upd({ target: e.target.value })} />
      </>}
      {n.type === 'range' && <>
        {([['in_min', 'Input min'], ['in_max', 'Input max'], ['out_min', 'Output min'], ['out_max', 'Output max']] as const).map(([k, l]) => (
          <div key={k}><label htmlFor={`np-${k}`}>{l}</label>
            <input id={`np-${k}`} type="number" step="any" value={n[k] ?? 0} onChange={e => upd({ [k]: parseFloat(e.target.value) || 0 })} /></div>
        ))}
        <label><input type="checkbox" checked={!!n.clamp} onChange={e => upd({ clamp: e.target.checked })} style={{ width: 'auto' }} /> Clamp to output range</label>
      </>}
      {n.type === 'function' && <>
        <label htmlFor="np-code">JavaScript (admin only; runs in a sandbox)</label>
        <textarea id="np-code" rows={8} value={n.code ?? ''} maxLength={4000} onChange={e => upd({ code: e.target.value })} style={{ fontFamily: 'ui-monospace, monospace', width: '100%' }} />
        <p className="muted">Receives <code>msg</code>, returns it. 50 ms and memory limits apply. See docs/function-nodes.md.</p>
      </>}
      {portCount(n) > 0 && <>
        <label>Connect this node to</label>
        <div style={{ display: 'flex', gap: 6 }}>
          {portCount(n) > 1 && <select aria-label="Output" value={port} onChange={e => setPort(+e.target.value)} style={{ width: 110 }}>
            {Array.from({ length: portCount(n) }, (_, i) => <option key={i} value={i}>output {i + 1}</option>)}</select>}
          <select aria-label="Target node" value={to} onChange={e => setTo(e.target.value)}>
            <option value="">choose...</option>{targets.map(t => <option key={t.id} value={t.id}>{t.name || `${LABELS[t.type]} ${t.id}`}</option>)}</select>
          <button type="button" className="ghost" disabled={!to} onClick={() => { connectTo(port, to); setTo(''); }}>Connect</button>
        </div>
      </>}
      {n.type !== 'trigger' && <div style={{ marginTop: 14 }}><button type="button" className="ghost" onClick={remove}>Delete node</button></div>}
    </div>
  );
}
