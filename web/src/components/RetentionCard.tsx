import { useEffect, useState } from 'react';
import { api } from '../lib/api';

// Per-tenant data retention. Blank = inherit the deployment default. Shortening
// a period deletes data on the next hourly job run, so the form says so.
export default function RetentionCard() {
  const [raw, setRaw] = useState('');
  const [hourly, setHourly] = useState('');
  const [msg, setMsg] = useState('');
  useEffect(() => {
    api<{ raw_days: number | null; hourly_days: number | null }>('/v1/retention')
      .then(r => { setRaw(r.raw_days?.toString() ?? ''); setHourly(r.hourly_days?.toString() ?? ''); }).catch(() => undefined);
  }, []);
  async function save(e: React.FormEvent) {
    e.preventDefault(); setMsg('');
    try {
      await api('/v1/retention', { method: 'PUT', body: JSON.stringify({ raw_days: raw === '' ? null : parseInt(raw), hourly_days: hourly === '' ? null : parseInt(hourly) }) });
      setMsg('Saved. Data older than these limits is removed on the next hourly run; daily summaries are kept.');
    } catch (err) { setMsg(String(err)); }
  }
  return (
    <div className="card" style={{ maxWidth: 480, marginBottom: 20 }} role="region" aria-label="Data retention">
      <b>Data retention</b>
      <p className="muted">Raw readings are summarised into hourly and daily rows before they are deleted. Admins only.</p>
      <form onSubmit={save}>
        <label htmlFor="rt-raw">Keep raw readings (days, 1 to 3650, blank = deployment default)</label>
        <input id="rt-raw" type="number" min={1} max={3650} value={raw} onChange={e => setRaw(e.target.value)} />
        <label htmlFor="rt-hr">Keep hourly summaries (days, 30 to 7300, blank = forever)</label>
        <input id="rt-hr" type="number" min={30} max={7300} value={hourly} onChange={e => setHourly(e.target.value)} />
        <div style={{ marginTop: 12 }}><button type="submit">Save retention</button></div>
      </form>
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
