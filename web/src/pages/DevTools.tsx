import { useCallback, useEffect, useRef, useState } from 'react';
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
  const [loading,setLoading]=useState(true),[receivedAt,setReceivedAt]=useState<Date|null>(null),[downloading,setDownloading]=useState(false);
  const sequence=useRef(0),bundlePending=useRef(false);
  const load = useCallback(() => {
    const seq=++sequence.current;setLoading(true);setRows([]);setReceivedAt(null);setErr('');
    const p = new URLSearchParams({ minutes: String(mins), limit: '300' });
    if (level) p.set('level', level);
    if (q) p.set('q', q);
    if (service) p.set('service', service);
    api<{ logs: Row[] }>(`/v1/system/logs?${p}`).then(x=>{if(seq!==sequence.current)return;setRows(x.logs??[]);setReceivedAt(new Date());setErr('')}).catch(e=>{if(seq===sequence.current)setErr(String(e.message??e))}).finally(()=>{if(seq===sequence.current)setLoading(false)});
  }, [level, q, service, mins]);
  useEffect(() => { load();return()=>{++sequence.current}; }, [load]);
  async function supportBundle(){if(bundlePending.current)return;bundlePending.current=true;setDownloading(true);setMsg('');try{await download('/v1/system/support','hexthings-support.txt');setMsg('Support bundle download requested. Review its contents before sharing.')}catch(e){setMsg(`Support bundle failed: ${e instanceof Error?e.message:String(e)}`)}finally{bundlePending.current=false;setDownloading(false)}}
  const services = Array.from(new Set([...rows.map((r) => r.service),...(service?[service]:[])]));
  return (
    <div>
      <h2>Dev tools</h2>
      <p className="muted">Warning and error lines captured from the API. Redaction is applied before storage; review exports before sharing. Other services' logs are not captured here; use <code>hexthings logs &lt;service&gt;</code>.</p>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 12 }}>
        <select style={{ width: "auto" }} aria-label="Level" value={level} onChange={(e) => setLevel(e.target.value)}><option value="">All levels</option><option value="error">Errors</option><option value="warn">Warnings</option></select>
        <select style={{ width: "auto" }} aria-label="Service" value={service} onChange={(e) => setService(e.target.value)}><option value="">All services</option>{services.map((s) => <option key={s}>{s}</option>)}</select>
        <select style={{ width: "auto" }} aria-label="Time range" value={mins} onChange={(e) => setMins(Number(e.target.value))}><option value={60}>Last hour</option><option value={1440}>Last 24 hours</option><option value={10080}>Last 7 days</option><option value={43200}>Last 30 days</option></select>
        <input style={{ width: 220 }} aria-label="Search" placeholder="Search messages" value={q} onChange={(e) => setQ(e.target.value)} />
        <button onClick={load}>Refresh</button>
        <button className="secondary" disabled={downloading} onClick={supportBundle}>{downloading?'Preparing support bundle...':'Download support bundle'}</button>
      </div>
      {msg && <p role="status">{msg}</p>}
      {loading&&<p role="status">Loading log lines...</p>}
      {err&&<p role="alert">{err.includes('403')?'Admins only.':`Log refresh failed: ${err}. Previous rows were cleared. Adjust filters or Refresh to retry.`}</p>}
      {receivedAt&&<p className="muted">Response received: {receivedAt.toLocaleString()}. Showing at most 300 matching rows, not a complete log archive. Response time is not the last log event time.</p>}
      <div style={{overflowX:'auto'}}><table>
        <thead><tr><th>Time</th><th>Level</th><th>Service</th><th>Message</th></tr></thead>
        <tbody>
          {rows.map((r) => <tr key={r.id}><td style={{ whiteSpace: 'nowrap' }}>{new Date(r.ts).toLocaleString()}</td><td style={{ color: r.level === 'error' ? '#dc2626' : '#d97706' }}>{r.level}</td><td>{r.service}</td><td style={{ fontFamily: 'monospace', wordBreak: 'break-word' }}>{r.message}</td></tr>)}
          {!loading&&!err&&!rows.length && <tr><td colSpan={4} className="muted">No matching stored log lines in this response. This does not prove the system is healthy or that other services had no errors.</td></tr>}
        </tbody>
      </table></div>
    </div>
  );
}
