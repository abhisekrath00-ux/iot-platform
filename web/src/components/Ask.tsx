import { useState } from 'react';
import { api } from '../lib/api';

interface Answer {
  interpreted_as?: string; method?: string; kind?: string; error?: string; examples?: string[];
  rows?: { id: string; name?: string; severity?: string; message?: string; status?: string }[];
  value?: number; unit?: string; observed_at?: string; device?: { name: string };
}

// A plain-English question box. It is phrase matching over a few fixed read-only queries, not a
// language model, and it always shows how it read the question.
export default function Ask() {
  const [q, setQ] = useState('');
  const [a, setA] = useState<Answer | null>(null);
  async function go(e: React.FormEvent) {
    e.preventDefault();
    try { setA(await api<Answer>('/v1/ask', { method: 'POST', body: JSON.stringify({ question: q }) })); }
    catch (err) {
      const m = String(err);
      try { setA(JSON.parse(m.slice(m.indexOf('{')))); } catch { setA({ error: m }); }
    }
  }
  return (
    <div className="card" style={{ marginBottom: 16 }}>
      <form onSubmit={go} style={{ display: 'flex', gap: 8 }}>
        <input aria-label="Ask a question" placeholder='Ask: "critical alerts", "latest temperature of boiler-1", "offline devices"' value={q} onChange={e => setQ(e.target.value)} />
        <button type="submit" disabled={!q.trim()}>Ask</button>
      </form>
      {a && (
        <div style={{ marginTop: 10 }} role="status">
          {a.interpreted_as && <p className="muted" style={{ margin: '0 0 6px' }}>Read as: {a.interpreted_as}. Rule-based, read-only.</p>}
          {a.error && <p>{a.error}{a.examples && <> Try: {a.examples.map(x => `"${x}"`).join(', ')}.</>}</p>}
          {a.kind === 'value' && <p><b>{a.device?.name}</b>: {a.value} {a.unit} <span className="muted">at {a.observed_at && new Date(a.observed_at + 'Z').toLocaleString()}</span></p>}
          {a.rows && a.rows.length === 0 && <p className="muted">Nothing found.</p>}
          {a.rows && a.rows.map(r => <div key={r.id}>{r.severity ? <><span className="pill">{r.severity}</span> {r.message}</> : r.name}</div>)}
        </div>
      )}
    </div>
  );
}
