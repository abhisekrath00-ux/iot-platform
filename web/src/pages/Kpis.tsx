import { useEffect, useState } from 'react';
import Empty from '../components/Empty';
import { api } from '../lib/api';

interface Kpi { id: string; name: string; expression: string; unit: string; value: number | null; stale: boolean; error?: string; }

export default function Kpis() {
  const [kpis, setKpis] = useState<Kpi[]>([]);
  const [name, setName] = useState('');
  const [expression, setExpression] = useState('');
  const [unit, setUnit] = useState('');
  const [msg, setMsg] = useState('');
  const load = () => { api<Kpi[]>('/v1/kpis').then(setKpis).catch(e => setMsg(String(e))); };
  useEffect(() => { load(); const t = setInterval(load, 15000); return () => clearInterval(t); }, []);

  return (
    <>
      <h1>KPIs</h1>
      <p className="muted">Derived values from live points, for example <code>{'{meter-1.kwh} / {line-a.units}'}</code>. Operators are + - * / and parentheses. Refreshes every 15 s.</p>
      {kpis.length === 0
        ? <Empty title="No KPIs yet" hint="Create one below. Each reference is {device.point}." />
        : <div className="cards">
            {kpis.map(k => (
              <div key={k.id} className="card">
                <div className="muted">{k.name}</div>
                <div className="kpi">{k.value === null ? '-' : Number(k.value.toFixed(3))} <small>{k.unit}</small></div>
                {k.error && <span className="pill bad">{k.error}</span>}
                {k.stale && !k.error && <span className="pill warn">stale input</span>}
                <div className="muted" style={{ fontSize: 12, marginTop: 6 }}><code>{k.expression}</code></div>
                <button className="ghost" style={{ marginTop: 8 }} aria-label={`Delete ${k.name}`} onClick={() => api(`/v1/kpis/${k.id}`, { method: 'DELETE' }).then(load).catch(e => setMsg(String(e)))}>Delete</button>
              </div>
            ))}
          </div>}
      <div className="card" style={{ maxWidth: 640, marginTop: 20 }}>
        <b>New KPI</b>
        <form onSubmit={async e => {
          e.preventDefault(); setMsg('');
          try { await api('/v1/kpis', { method: 'POST', body: JSON.stringify({ name, expression, unit }) }); setName(''); setExpression(''); setUnit(''); load(); }
          catch (err) { setMsg(String(err)); }
        }}>
          <label htmlFor="k-name">Name</label>
          <input id="k-name" value={name} onChange={e => setName(e.target.value)} placeholder="Energy per unit" required />
          <label htmlFor="k-expr">Expression</label>
          <input id="k-expr" value={expression} onChange={e => setExpression(e.target.value)} placeholder="{meter-1.kwh} / 100" required />
          <label htmlFor="k-unit">Unit (optional)</label>
          <input id="k-unit" value={unit} onChange={e => setUnit(e.target.value)} placeholder="kWh/unit" maxLength={16} />
          <div style={{ marginTop: 14 }}><button type="submit">Create KPI</button></div>
        </form>
        {msg && <p className="err-box" role="alert">{msg}</p>}
      </div>
    </>
  );
}
