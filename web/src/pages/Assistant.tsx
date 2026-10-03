import { useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';

interface Msg { role: 'user' | 'assistant'; content: string; }
interface Pending { id: string; method: string; path: string; body?: string; summary: string; status?: string; }
interface Trace { tool: string; detail: string; status: string; }
interface Reply { reply: string; plan: string[]; trace: Trace[]; pending: Pending[]; model?: string; error?: string; fallback?: string; }

// The assistant: it plans, calls the platform as you, and reports back. Reads happen at once.
// Every change waits here for your confirmation, and nothing it proposes can approve a control command.
export default function Assistant() {
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [runs, setRuns] = useState<Reply[]>([]);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [acts, setActs] = useState<Record<string, string>>({});
  const end = useRef<HTMLDivElement>(null);
  useEffect(() => { end.current?.scrollIntoView({ behavior: 'smooth' }); }, [runs, msgs]);

  async function send(e: React.FormEvent) {
    e.preventDefault();
    const next: Msg[] = [...msgs, { role: 'user', content: text }];
    setMsgs(next); setText(''); setBusy(true); setErr('');
    try {
      const r = await api<Reply>('/v1/assistant/chat', { method: 'POST', body: JSON.stringify({ messages: next }) });
      setMsgs([...next, { role: 'assistant', content: r.reply }]);
      setRuns(rs => [...rs, r]);
    } catch (e2) {
      const m = String(e2);
      try { const j = JSON.parse(m.slice(m.indexOf('{'))); setErr(j.error + (j.fallback ? ' You can still use the question box on the Fleet page.' : '')); } catch { setErr(m); }
    }
    setBusy(false);
  }
  async function decide(id: string, what: 'confirm' | 'reject') {
    try {
      const r = await api<{ status: string; result_code?: number }>(`/v1/assistant/actions/${id}/${what}`, { method: 'POST' });
      setActs(a => ({ ...a, [id]: what === 'reject' ? 'rejected' : r.status === 'executed' ? 'done' : `failed (${r.result_code})` }));
    } catch (e) { setActs(a => ({ ...a, [id]: String(e) })); }
  }
  let ri = -1;
  return (
    <>
      <h1>Assistant</h1>
      <p className="muted">Ask it to do things: "acknowledge the pump alert", "summarise this week's alerts on Line A", "draft a report of boiler temperature". It acts as you, shows its plan and steps, and waits for your confirmation before changing anything. It cannot approve control commands.</p>
      {err && <p role="alert">{err}</p>}
      <div className="card" style={{ maxWidth: 900, minHeight: 240 }}>
        {msgs.length === 0 && <p className="muted">Nothing yet.</p>}
        {msgs.map((m, i) => {
          const run = m.role === 'assistant' ? runs[++ri] : undefined;
          return (
            <div key={i} style={{ margin: '10px 0' }}>
              <div className="muted" style={{ fontSize: 12 }}>{m.role === 'user' ? 'You' : 'Assistant'}</div>
              <div style={{ whiteSpace: 'pre-wrap' }}>{m.content}</div>
              {run && run.plan.length > 0 && <ol className="muted" style={{ margin: '6px 0' }}>{run.plan.map((p, j) => <li key={j}>{p}</li>)}</ol>}
              {run && run.trace.length > 0 && (
                <details style={{ margin: '6px 0' }}>
                  <summary className="muted">{run.trace.length} step{run.trace.length === 1 ? '' : 's'} taken</summary>
                  {run.trace.map((t, j) => <div key={j} className="muted" style={{ fontSize: 12 }}><span className={`pill ${t.status === 'ok' ? 'ok' : t.status === 'proposed' ? '' : 'warn'}`}>{t.status}</span> {t.tool}: {t.detail}</div>)}
                </details>
              )}
              {run && run.pending.map(p => (
                <div key={p.id} className="card" style={{ margin: '8px 0', padding: '10px 14px' }}>
                  <b>Waiting for you:</b> {p.summary}
                  <div className="muted" style={{ fontSize: 12 }}>{p.method} {p.path}{p.body ? ` ${p.body}` : ''}</div>
                  {acts[p.id]
                    ? <span className="pill">{acts[p.id]}</span>
                    : <div style={{ marginTop: 6, display: 'flex', gap: 8 }}><button onClick={() => decide(p.id, 'confirm')}>Confirm</button><button className="ghost" onClick={() => decide(p.id, 'reject')}>Reject</button></div>}
                </div>
              ))}
            </div>
          );
        })}
        {busy && <p className="muted">Working...</p>}
        <div ref={end} />
      </div>
      <form onSubmit={send} style={{ display: 'flex', gap: 8, maxWidth: 900, marginTop: 12 }}>
        <input aria-label="Message the assistant" placeholder="What should I do?" value={text} onChange={e => setText(e.target.value)} disabled={busy} />
        <button type="submit" disabled={busy || !text.trim()}>Send</button>
      </form>
      <p className="muted" style={{ fontSize: 12, maxWidth: 900 }}>Answers come from the model your admin connected and can be wrong. Check values before you confirm a change.</p>
    </>
  );
}
