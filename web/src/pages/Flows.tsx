import { useEffect, useState } from 'react';
import Empty from '../components/Empty';
import { api } from '../lib/api';

interface Rule { id: string; name: string; definition: any; version: number; enabled: boolean; }
interface FlowDef { trigger: { device_id: string; point_id: string; op: string; value: number }; steps: any[]; cooldown_seconds?: number; latch?: boolean; }
interface FlowRow { id: string; name: string; definition: FlowDef; enabled: boolean; }
interface Channel { id: string; type: string; target: string; }

// Threshold rules (v1) with the flow-builder UI coming next. Rules evaluate in
// the ingest worker and dispatch to the tenant's email/Slack channels.
export default function Flows() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [name, setName] = useState('');
  const [pointId, setPointId] = useState('');
  const [deviceId, setDeviceId] = useState('');
  const [op, setOp] = useState('>');
  const [threshold, setThreshold] = useState('');
  const [severity, setSeverity] = useState('warning');
  const [msg, setMsg] = useState('');
  const [flows, setFlows] = useState<FlowRow[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [fname, setFname] = useState('');
  const [fdevice, setFdevice] = useState('');
  const [fpoint, setFpoint] = useState('');
  const [fop, setFop] = useState('>');
  const [fvalue, setFvalue] = useState('');
  const [fcool, setFcool] = useState('');
  const [flatch, setFlatch] = useState(false);
  const [steps, setSteps] = useState<any[]>([{ type: 'notify', channel_id: '', message: '' }]);

  const load = () => {
    api<Rule[]>('/v1/rules').then(setRules).catch(e => setMsg(String(e)));
    api<FlowRow[]>('/v1/flows').then(setFlows).catch(() => {});
    api<Channel[]>('/v1/notifications/channels').then(setChannels).catch(() => {});
  };
  useEffect(() => { load(); }, []);

  function setStep(i: number, patch: any) {
    setSteps(ss => ss.map((st, j) => (j === i ? { ...st, ...patch } : st)));
  }

  async function submitFlow(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/flows', {
        method: 'POST',
        body: JSON.stringify({
          name: fname,
          definition: {
            trigger: { device_id: fdevice, point_id: fpoint, op: fop, value: parseFloat(fvalue) },
            steps: steps.map(st => st.type === 'condition' ? { type: st.type, op: st.op, value: parseFloat(st.value) }
              : st.type === 'delay' ? { type: st.type, seconds: +st.seconds }
              : { type: st.type, channel_id: st.channel_id, message: st.message }),
            ...(+fcool > 0 ? { cooldown_seconds: Math.round(+fcool * 60) } : {}),
            ...(flatch ? { latch: true } : {})
          }
        })
      });
      setFname(''); setSteps([{ type: 'notify', channel_id: '', message: '' }]);
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    try {
      await api('/v1/rules', {
        method: 'POST',
        body: JSON.stringify({
          name,
          enabled: true,
          definition: {
            device_id: deviceId || undefined,
            point_id: pointId,
            op,
            threshold: parseFloat(threshold),
            severity
          }
        })
      });
      setName(''); setPointId(''); setDeviceId(''); setThreshold('');
      load();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <>
      <h1>Flows & rules</h1>
      <div className="card" style={{ maxWidth: 480, marginBottom: 20 }}>
        <b>New threshold rule</b>
        <form onSubmit={submit}>
          <label>Rule name</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="High current on main meter" required />
          <label>Point</label>
          <input value={pointId} onChange={e => setPointId(e.target.value)} placeholder="current" required />
          <label>Device (optional - all devices with this point if empty)</label>
          <input value={deviceId} onChange={e => setDeviceId(e.target.value)} placeholder="meter-1" />
          <label>Condition</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <select value={op} onChange={e => setOp(e.target.value)} style={{ width: 70 }}>
              <option value=">">&gt;</option><option value="<">&lt;</option>
            </select>
            <input value={threshold} onChange={e => setThreshold(e.target.value)} placeholder="100" type="number" step="any" required />
            <select value={severity} onChange={e => setSeverity(e.target.value)}>
              <option value="info">info</option><option value="warning">warning</option><option value="critical">critical</option>
            </select>
          </div>
          <div style={{ marginTop: 14 }}><button type="submit">Create rule</button></div>
        </form>
        {msg && <p className="muted">{msg}</p>}
      </div>
      {rules.length === 0 && <Empty title="No rules yet" hint="Create a threshold rule above to raise alerts when a point crosses a limit." />}
      {rules.length > 0 && <table>
        <thead><tr><th>Name</th><th>Condition</th><th>Severity</th><th>Status</th></tr></thead>
        <tbody>
          {rules.map(r => (
            <tr key={r.id}>
              <td>{r.name}</td>
              <td className="muted">{r.definition?.point_id} {r.definition?.op} {r.definition?.threshold}{r.definition?.device_id ? ` on ${r.definition.device_id}` : ''}</td>
              <td><span className={`pill ${r.definition?.severity === 'critical' ? 'bad' : r.definition?.severity === 'warning' ? 'warn' : 'ok'}`}>{r.definition?.severity}</span></td>
              <td className="muted">{r.enabled ? 'enabled' : 'disabled'}</td>
            </tr>
          ))}
        </tbody>
      </table>}
      <h2 style={{ marginTop: 28 }}>Automation flows</h2>
      <p className="muted">Trigger on a reading, optionally check a condition and wait, then notify. Flows evaluate in the ingest worker alongside rules.</p>
      <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
        <b>New flow</b>
        <form onSubmit={submitFlow}>
          <label>Name</label>
          <input value={fname} onChange={e => setFname(e.target.value)} placeholder="High usage alert" required />
          <label>When device / point</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <input value={fdevice} onChange={e => setFdevice(e.target.value)} placeholder="meter-1" required />
            <input value={fpoint} onChange={e => setFpoint(e.target.value)} placeholder="kwh" required />
          </div>
          <label>Trigger</label>
          <div style={{ display: 'flex', gap: 8 }}>
            <select value={fop} onChange={e => setFop(e.target.value)} style={{ width: 80 }}>
              {['>', '<', '>=', '<=', '==', '!='].map(o => <option key={o} value={o}>{o}</option>)}
            </select>
            <input value={fvalue} onChange={e => setFvalue(e.target.value)} type="number" step="any" required />
          </div>
          <label>Do not repeat for (minutes, optional)</label>
          <input value={fcool} onChange={e => setFcool(e.target.value)} type="number" min={0} max={1440} placeholder="e.g. 30 to avoid alert storms" />
          <label style={{ display: 'flex', gap: 8, alignItems: 'center', textTransform: 'none', letterSpacing: 0 }}>
            <input type="checkbox" checked={flatch} onChange={e => setFlatch(e.target.checked)} style={{ width: 'auto' }} />
            Notify once, then stay quiet until the reading returns to normal (latch)
          </label>
          <label>Steps</label>
          {steps.map((st, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, marginBottom: 6, alignItems: 'center' }}>
              <select value={st.type} onChange={e => setStep(i, { type: e.target.value })} style={{ width: 110 }}>
                <option value="condition">condition</option>
                <option value="delay">delay</option>
                <option value="notify">notify</option>
              </select>
              {st.type === 'condition' && <>
                <select value={st.op ?? '<'} onChange={e => setStep(i, { op: e.target.value })} style={{ width: 70 }}>
                  {['>', '<', '>=', '<=', '==', '!='].map(o => <option key={o} value={o}>{o}</option>)}
                </select>
                <input value={st.value ?? ''} onChange={e => setStep(i, { value: e.target.value })} type="number" step="any" required />
              </>}
              {st.type === 'delay' && <input value={st.seconds ?? ''} onChange={e => setStep(i, { seconds: e.target.value })} type="number" min={1} max={3600} placeholder="seconds" required />}
              {st.type === 'notify' && <>
                <select value={st.channel_id ?? ''} onChange={e => setStep(i, { channel_id: e.target.value })} required>
                  <option value="">channel...</option>
                  {channels.map(c => <option key={c.id} value={c.id}>{c.type}: {c.target}</option>)}
                </select>
                <input value={st.message ?? ''} onChange={e => setStep(i, { message: e.target.value })} placeholder="usage {value} kWh high" />
              </>}
              <button type="button" onClick={() => setSteps(ss => ss.filter((_, j) => j !== i))} disabled={steps.length === 1}>x</button>
            </div>
          ))}
          <button type="button" onClick={() => setSteps(ss => [...ss, { type: 'condition', op: '<', value: '' }])}>+ step</button>
          <div style={{ marginTop: 14 }}><button type="submit">Create flow</button></div>
        </form>
      </div>
      {flows.map(f => (
        <div key={f.id} className="card" style={{ marginBottom: 10 }}>
          <b>{f.name}</b> <span className="muted">{f.definition.trigger.device_id}/{f.definition.trigger.point_id} {f.definition.trigger.op} {f.definition.trigger.value} - {f.definition.steps.length} steps{f.definition.cooldown_seconds ? ` - quiet for ${Math.round(f.definition.cooldown_seconds / 60)} min after a notification` : ''}{f.definition.latch ? ' - latched' : ''}</span>
          <div className="muted">{f.enabled ? 'enabled' : 'disabled'}</div>
        </div>
      ))}
    </>
  );
}
