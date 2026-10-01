import { useEffect, useState } from 'react';
import { api } from '../lib/api';

interface Kpi { id: string; name: string; unit: string }

// Two rule kinds beyond a fixed threshold: a point drifting from its own
// recent history (N standard deviations), and a KPI leaving a band you set.
export default function AnomalyRuleForm({ onCreated }: { onCreated: () => void }) {
  const [kind, setKind] = useState<'sigma' | 'kpi_band'>('sigma');
  const [name, setName] = useState('');
  const [deviceId, setDeviceId] = useState('');
  const [pointId, setPointId] = useState('');
  const [sigma, setSigma] = useState('4');
  const [windowMin, setWindowMin] = useState('1440');
  const [direction, setDirection] = useState('both');
  const [kpis, setKpis] = useState<Kpi[]>([]);
  const [kpiId, setKpiId] = useState('');
  const [min, setMin] = useState('');
  const [max, setMax] = useState('');
  const [severity, setSeverity] = useState('warning');
  const [msg, setMsg] = useState('');

  useEffect(() => { api<Kpi[]>('/v1/kpis').then(setKpis).catch(() => {}); }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setMsg('');
    const definition = kind === 'sigma'
      ? { kind, device_id: deviceId || undefined, point_id: pointId, sigma: parseFloat(sigma), window_minutes: parseInt(windowMin), direction, severity }
      : { kind, kpi_id: kpiId, min: min === '' ? undefined : parseFloat(min), max: max === '' ? undefined : parseFloat(max), severity };
    try {
      await api('/v1/rules', { method: 'POST', body: JSON.stringify({ name, enabled: true, definition }) });
      setName(''); setPointId(''); setDeviceId(''); setMin(''); setMax('');
      setMsg('Rule created.');
      onCreated();
    } catch (e2) { setMsg(String(e2)); }
  }

  return (
    <div className="card" style={{ maxWidth: 480, marginBottom: 20 }}>
      <b>New anomaly or KPI rule</b>
      <p className="muted">Nothing runs until you create one. Sigma rules need at least 30 recent readings before they can fire. KPI rules are checked once a minute, and a stale KPI never alerts.</p>
      <form onSubmit={submit}>
        <label htmlFor="ar-kind">Type</label>
        <select id="ar-kind" value={kind} onChange={e => setKind(e.target.value as 'sigma' | 'kpi_band')}>
          <option value="sigma">Reading far from its own recent history</option>
          <option value="kpi_band">KPI outside a band</option>
        </select>
        <label htmlFor="ar-name">Rule name</label>
        <input id="ar-name" value={name} onChange={e => setName(e.target.value)} required />
        {kind === 'sigma' ? <>
          <label htmlFor="ar-dev">Device id (blank for any device with the point)</label>
          <input id="ar-dev" value={deviceId} onChange={e => setDeviceId(e.target.value)} />
          <label htmlFor="ar-pt">Point</label>
          <input id="ar-pt" value={pointId} onChange={e => setPointId(e.target.value)} required />
          <label htmlFor="ar-sig">Standard deviations (2 to 10)</label>
          <input id="ar-sig" type="number" min={2} max={10} step="any" value={sigma} onChange={e => setSigma(e.target.value)} required />
          <label htmlFor="ar-win">History window (minutes, 10 to 10080)</label>
          <input id="ar-win" type="number" min={10} max={10080} value={windowMin} onChange={e => setWindowMin(e.target.value)} required />
          <label htmlFor="ar-dir">Direction</label>
          <select id="ar-dir" value={direction} onChange={e => setDirection(e.target.value)}>
            <option value="both">Above or below</option><option value="above">Above only</option><option value="below">Below only</option>
          </select>
        </> : <>
          <label htmlFor="ar-kpi">KPI</label>
          <select id="ar-kpi" value={kpiId} onChange={e => setKpiId(e.target.value)} required>
            <option value="">Choose a KPI</option>
            {kpis.map(k => <option key={k.id} value={k.id}>{k.name}{k.unit ? ` (${k.unit})` : ''}</option>)}
          </select>
          <label htmlFor="ar-min">Alert below (optional)</label>
          <input id="ar-min" type="number" step="any" value={min} onChange={e => setMin(e.target.value)} />
          <label htmlFor="ar-max">Alert above (optional)</label>
          <input id="ar-max" type="number" step="any" value={max} onChange={e => setMax(e.target.value)} />
        </>}
        <label htmlFor="ar-sev">Severity</label>
        <select id="ar-sev" value={severity} onChange={e => setSeverity(e.target.value)}>
          <option>info</option><option>warning</option><option>critical</option>
        </select>
        <div style={{ marginTop: 12 }}><button type="submit">Create rule</button></div>
        {msg && <p className="muted" role="status">{msg}</p>}
      </form>
    </div>
  );
}
