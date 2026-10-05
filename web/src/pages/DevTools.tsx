import { useCallback, useEffect, useState } from 'react';
import { api, download } from '../lib/api';

interface Row { id: number; ts: string; service: string; level: 'warn' | 'error'; message: string; }

export default function DevTools() {
  const [rows, setRows] = useState<Row[]>([]);
  const [level, setLevel] = useState('');
  const [q, setQ] = useState('');
  const [service, setService] = useState('');
  const [mins, setMins] = useState(1440);
  const [err, setErr] = useState('');
  const [msg, setMsg] = useState('');
  const load = useCallback(() => {
    const p = new URLSearchParams({ minutes: String(mins), limit: '300' });
    if (level) p.set('level', level);
    if (q) p.set('q', q);
    if (service) p.set('service', service);
    api<{ logs: Row[] }>(`/v1/system/logs?${p}`).then((x) => { setRows(x.logs ?? []); setErr(''); }).catch((e) => setErr(String(e.message ?? e)));
  }, [level, q, service, mins]);
  useEffect(() => { load(); }, [load]);
  if (err) return <div><h2>Dev tools</h2><p role="alert">{err.includes('403') ? 'Admins only.' : err}</p></div>;
  const services = Array.from(new Set(rows.map((r) => r.service)));
  return (
    <div>
      <h2>Dev tools</h2>
      <p className="muted">Warning and error lines captured from the API (secrets redacted before storage). Other services' logs are not captured here; use <code>hexthings logs &lt;service&gt;</code>.</p>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
        <select style={{ width: "auto" }} aria-label="Level" value={level} onChange={(e) => setLevel(e.target.value)}><option value="">All levels</option><option value="error">Errors</option><option value="warn">Warnings</option></select>
        <select style={{ width: "auto" }} aria-label="Service" value={service} onChange={(e) => setService(e.target.value)}><option value="">All services</option>{services.map((s) => <option key={s}>{s}</option>)}</select>
        <select style={{ width: "auto" }} aria-label="Time range" value={mins} onChange={(e) => setMins(Number(e.target.value))}><option value={60}>Last hour</option><option value={1440}>Last 24 hours</option><option value={10080}>Last 7 days</option><option value={43200}>Last 30 days</option></select>
        <input style={{ width: 220 }} aria-label="Search" placeholder="Search messages" value={q} onChange={(e) => setQ(e.target.value)} />
        <button onClick={load}>Refresh</button>
        <button className="secondary" onClick={() => download('/v1/system/support', 'hexthings-support.txt').then(() => setMsg('Support bundle downloaded (secrets redacted).')).catch((e) => setMsg(String(e.message)))}>Download support bundle</button>
      </div>
      {msg && <p role="status">{msg}</p>}
      <table>
        <thead><tr><th>Time</th><th>Level</th><th>Service</th><th>Message</th></tr></thead>
        <tbody>
          {rows.map((r) => <tr key={r.id}><td style={{ whiteSpace: 'nowrap' }}>{new Date(r.ts).toLocaleString()}</td><td style={{ color: r.level === 'error' ? '#dc2626' : '#d97706' }}>{r.level}</td><td>{r.service}</td><td style={{ fontFamily: 'monospace', wordBreak: 'break-word' }}>{r.message}</td></tr>)}
          {!rows.length && <tr><td colSpan={4} className="muted">No matching log lines. That is good news if you expected none.</td></tr>}
        </tbody>
      </table>
    </div>
  );
}
