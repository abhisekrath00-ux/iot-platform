import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Gateway { id: string; serial: string; status: string; kind: string; }
interface Output { name: string; kind: 'gpio_file' | 'simulate'; path?: string; active_low?: boolean; max_on_seconds?: number; }
interface Rule { id: string; name?: string; type: 'link_down' | 'threshold' | 'stale'; for_seconds: number; device?: string; point?: string; op?: string; value?: number; output: string; pattern?: string; }

// Edge alarm rules: sirens/buzzers wired to a gateway and the rules that sound
// them. They run on the edge box, so a server or network outage still alarms.
// Annunciator outputs only; process equipment stays on approved commands.
export default function EdgeRules() {
  const [gws, setGws] = useState<Gateway[]>([]);
  const [gw, setGw] = useState('');
  const [outs, setOuts] = useState<Output[]>([]);
  const [rules, setRules] = useState<Rule[]>([]);
  const [msg, setMsg] = useState('');
  const [o, setO] = useState<Output>({ name: '', kind: 'gpio_file', path: '' });
  const [r, setR] = useState<Rule>({ id: '', type: 'link_down', for_seconds: 30, output: '' });

  useEffect(() => { api<Gateway[]>('/v1/gateways').then(g => { setGws(g); if (g[0]) setGw(g[0].id); }).catch(e => setMsg(String(e))); }, []);
  useEffect(() => {
    if (!gw) return;
    api<{ outputs: Output[]; rules: Rule[] }>(`/v1/gateways/${gw}/edge-rules`).then(d => { setOuts(d.outputs); setRules(d.rules); setMsg(''); }).catch(e => setMsg(String(e)));
  }, [gw]);

  async function save(nextO = outs, nextR = rules) {
    setMsg('');
    try {
      await api(`/v1/gateways/${gw}/edge-rules`, { method: 'PUT', body: JSON.stringify({ outputs: nextO, rules: nextR }) });
      setOuts(nextO); setRules(nextR);
      setMsg('Saved. Download the gateway config again (or push it as a fleet config) and restart the agent to apply.');
    } catch (e) { setMsg(String(e)); }
  }

  return (
    <div className="card" style={{ maxWidth: 760, marginBottom: 20 }} role="region" aria-label="Edge alarm rules">
      <b>Edge alarm rules (siren / buzzer)</b>
      <p className="muted">Rules run on the edge box itself, so the alarm still sounds when the server or network is down. Only annunciator outputs can be driven here. Silence or test on the box with <code>edge-agent -silence NAME</code> / <code>-test-output NAME</code>. Admins only.</p>
      <label>Gateway</label>
      <select value={gw} onChange={e => setGw(e.target.value)}>{gws.map(g => <option key={g.id} value={g.id}>{g.serial} ({g.status})</option>)}</select>

      <h4>Outputs</h4>
      <table><thead><tr><th>Name</th><th>Kind</th><th>Path</th><th /></tr></thead><tbody>
        {outs.map(x => <tr key={x.name}><td>{x.name}</td><td>{x.kind}</td><td className="muted">{x.path ?? ''}</td>
          <td><button type="button" onClick={() => save(outs.filter(y => y.name !== x.name), rules.filter(y => y.output !== x.name))}>Remove</button></td></tr>)}
      </tbody></table>
      <form onSubmit={e => { e.preventDefault(); save([...outs, o]); setO({ name: '', kind: 'gpio_file', path: '' }); }}>
        <input value={o.name} onChange={e => setO({ ...o, name: e.target.value })} placeholder="name, e.g. siren" required aria-label="Output name" />
        <select value={o.kind} onChange={e => setO({ ...o, kind: e.target.value as Output['kind'] })} aria-label="Output kind">
          <option value="gpio_file">GPIO / LED file</option><option value="simulate">Simulated (log only)</option>
        </select>
        {o.kind === 'gpio_file' && <input value={o.path} onChange={e => setO({ ...o, path: e.target.value })} placeholder="/sys/class/gpio/gpio17/value" required aria-label="GPIO path" />}
        <button type="submit" disabled={!gw}>Add output</button>
      </form>

      <h4>Rules</h4>
      <table><thead><tr><th>ID</th><th>When</th><th>Sounds</th><th /></tr></thead><tbody>
        {rules.map(x => <tr key={x.id}><td>{x.id}</td>
          <td>{x.type === 'link_down' ? `server link down for ${x.for_seconds}s` : x.type === 'stale' ? `${x.device} silent for ${x.for_seconds}s` : `${x.device}.${x.point} ${x.op} ${x.value}${x.for_seconds ? ` for ${x.for_seconds}s` : ''}`}</td>
          <td>{x.output}{x.pattern === 'pulse' ? ' (pulse)' : ''}</td>
          <td><button type="button" onClick={() => save(outs, rules.filter(y => y.id !== x.id))}>Remove</button></td></tr>)}
      </tbody></table>
      <form onSubmit={e => { e.preventDefault(); save(outs, [...rules, r]); setR({ id: '', type: 'link_down', for_seconds: 30, output: r.output }); }}>
        <input value={r.id} onChange={e => setR({ ...r, id: e.target.value })} placeholder="rule id" required aria-label="Rule id" />
        <select value={r.type} onChange={e => setR({ ...r, type: e.target.value as Rule['type'] })} aria-label="Rule type">
          <option value="link_down">Server connection lost</option><option value="threshold">Reading threshold</option><option value="stale">Device stopped reporting</option>
        </select>
        {r.type !== 'link_down' && <input value={r.device ?? ''} onChange={e => setR({ ...r, device: e.target.value })} placeholder="device id" required aria-label="Device id" />}
        {r.type === 'threshold' && <>
          <input value={r.point ?? ''} onChange={e => setR({ ...r, point: e.target.value })} placeholder="point" required aria-label="Point" />
          <select value={r.op ?? '>'} onChange={e => setR({ ...r, op: e.target.value })} aria-label="Operator">{['>', '>=', '<', '<=', '==', '!='].map(x => <option key={x}>{x}</option>)}</select>
          <input type="number" step="any" value={r.value ?? 0} onChange={e => setR({ ...r, value: parseFloat(e.target.value) })} aria-label="Value" />
        </>}
        <input type="number" min={r.type === 'stale' ? 5 : 0} value={r.for_seconds} onChange={e => setR({ ...r, for_seconds: parseInt(e.target.value || '0') })} aria-label="Seconds" title="seconds the condition must hold" />
        <select value={r.output} onChange={e => setR({ ...r, output: e.target.value })} required aria-label="Output"><option value="">output...</option>{outs.map(x => <option key={x.name}>{x.name}</option>)}</select>
        <select value={r.pattern ?? 'steady'} onChange={e => setR({ ...r, pattern: e.target.value })} aria-label="Pattern"><option value="steady">steady</option><option value="pulse">pulse</option></select>
        <button type="submit" disabled={!gw}>Add rule</button>
      </form>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
