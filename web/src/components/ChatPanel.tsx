import { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { api, download } from '../lib/api';
import { Markdown } from '../lib/markdown';
import { readSSE } from '../lib/sse';

interface Pending { id: string; method: string; path: string; body?: string; summary: string; }
interface FileOffer { label: string; path: string; filename: string; }
interface Step { tool: string; detail: string; status: string; }
interface Turn {
  role: 'user' | 'assistant'; content: string;
  steps?: Step[]; plan?: string[]; pending?: Pending[]; files?: FileOffer[]; live?: boolean; error?: boolean; stopped?: boolean;
}
interface Status { configured: boolean; state: string; model?: string; runtime_model?: string; runtime_quantization?: string; }

// Only report downloads built by the server are offered; anything else is ignored.
export function safeDownload(path: string): boolean { return /^\/v1\/reports\/[A-Za-z0-9._-]{1,128}\/download\?format=(csv|html|pdf|xlsx)$/.test(path); }

const DOT: Record<string, string> = { ready: '#16a34a', loading: '#d97706', unknown: '#6b7280', down: '#dc2626', unreachable: '#dc2626', error: '#dc2626', not_configured: '#6b7280' };
const WORDS: Record<string, string> = {
  ready: 'Model ready', loading: 'Model loading', unknown: 'Model connected (health unknown)', down: 'Model runtime is down',
  unreachable: 'Model unreachable', error: 'Model address invalid', not_configured: 'No model connected',
};

// The chat side panel, available on every screen. It streams the answer and each step, knows which
// screen you are on, can be stopped, and shows every change as a card that waits for your confirm.
// The platform works the same whether or not a model is running.
export default function ChatPanel() {
  const loc = useLocation();
  const [open, setOpen] = useState(false);
  const [turns, setTurns] = useState<Turn[]>(() => { try { return JSON.parse(sessionStorage.getItem('iot.chat') ?? '[]'); } catch { return []; } });
  const [text, setText] = useState('');
  const [dlErr, setDlErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState<Status | null>(null);
  const [acts, setActs] = useState<Record<string, string>>({});
  const abort = useRef<AbortController | null>(null);
  const end = useRef<HTMLDivElement>(null);

  useEffect(() => { sessionStorage.setItem('iot.chat', JSON.stringify(turns.filter(t => !t.live).slice(-40))); }, [turns]);
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }); }, [turns, open]);
  useEffect(() => {
    if (!open) return;
    let alive = true;
    const poll = () => api<Status>('/v1/ai/status').then(s => alive && setStatus(s)).catch(() => alive && setStatus(null));
    poll(); const t = setInterval(poll, 15000);
    return () => { alive = false; clearInterval(t); };
  }, [open]);

  const patch = (f: (t: Turn) => Turn) => setTurns(ts => ts.map((t, i) => (i === ts.length - 1 ? f(t) : t)));

  async function send(e: React.FormEvent) {
    e.preventDefault();
    const q = text.trim(); if (!q || busy) return;
    const history = [...turns.filter(t => !t.live && !t.error && !t.stopped), { role: 'user', content: q } as Turn];
    setTurns([...turns, { role: 'user', content: q }, { role: 'assistant', content: '', live: true, steps: [] }]);
    setText(''); setBusy(true);
    const ac = new AbortController(); abort.current = ac;
    try {
      const token = localStorage.getItem('iot.token') ?? '';
      const res = await fetch('/v1/assistant/chat?stream=1', {
        method: 'POST', signal: ac.signal,
        headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
        body: JSON.stringify({ page: loc.pathname, messages: history.slice(-12).map(t => ({ role: t.role, content: t.content })) }),
      });
      if (!res.ok) {
        let msg = await res.text();
        try { msg = JSON.parse(msg).error ?? msg; } catch { /* plain text */ }
        patch(t => ({ ...t, live: false, error: true, content: msg }));
      } else {
        await readSSE(res, (ev, d) => {
          if (ev === 'delta') patch(t => ({ ...t, content: t.content + String(d) }));
          else if (ev === 'discard') patch(t => ({ ...t, content: '' }));
          else if (ev === 'step') patch(t => ({ ...t, steps: [...(t.steps ?? []), d as Step] }));
          else if (ev === 'plan') patch(t => ({ ...t, plan: d as string[] }));
          else if (ev === 'final') { const f = d as { reply: string; pending: Pending[]; files?: FileOffer[]; plan: string[]; trace: Step[] }; patch(t => ({ ...t, live: false, content: f.reply, pending: f.pending ?? [], files: f.files ?? [], plan: f.plan ?? t.plan, steps: f.trace ?? t.steps })); }
          else if (ev === 'error') patch(t => ({ ...t, live: false, error: true, content: (d as { error: string }).error }));
        });
        patch(t => (t.live ? { ...t, live: false, error: true, content: t.content || 'The connection ended before an answer arrived.' } : t));
      }
    } catch (err) {
      if ((err as Error).name === 'AbortError') patch(t => ({ ...t, live: false, stopped: true, content: (t.content ? t.content + '\n\n' : '') + '(stopped)' }));
      else patch(t => ({ ...t, live: false, error: true, content: String(err) }));
    }
    abort.current = null; setBusy(false);
  }

  async function decide(id: string, what: 'confirm' | 'reject') {
    try {
      const r = await api<{ status: string; result_code?: number }>(`/v1/assistant/actions/${id}/${what}`, { method: 'POST' });
      setActs(a => ({ ...a, [id]: what === 'reject' ? 'rejected' : r.status === 'executed' ? 'done' : `failed (${r.result_code})` }));
    } catch (e) { setActs(a => ({ ...a, [id]: String(e) })); }
  }

  const st = status?.state ?? 'unknown';
  return (
    <>
      {!open && <button className="chat-fab" aria-label="Open the assistant" onClick={() => setOpen(true)} style={{ position: 'fixed', right: 18, bottom: 18, zIndex: 40, borderRadius: 24, padding: '10px 16px', boxShadow: '0 4px 14px rgba(0,0,0,.3)' }}>Ask the assistant</button>}
      {open && (
        <aside role="complementary" aria-label="Assistant" style={{ position: 'fixed', top: 0, right: 0, bottom: 0, width: 400, maxWidth: '100vw', zIndex: 45, display: 'flex', flexDirection: 'column', background: 'var(--card, #fff)', color: 'inherit', borderLeft: '1px solid rgba(127,127,127,.35)', boxShadow: '-6px 0 20px rgba(0,0,0,.25)' }}>
          <header style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', borderBottom: '1px solid rgba(127,127,127,.3)' }}>
            <b style={{ flex: 1 }}>Assistant</b>
            <span title={WORDS[st]} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 12 }}><span style={{ width: 9, height: 9, borderRadius: 9, background: DOT[st] ?? '#6b7280' }} />{WORDS[st] ?? st}</span>
            <button className="ghost" onClick={() => { setTurns([]); sessionStorage.removeItem('iot.chat'); }} disabled={busy || turns.length === 0}>Clear</button>
            <button className="ghost" aria-label="Close the assistant" onClick={() => setOpen(false)}>x</button>
          </header>
          <div style={{ flex: 1, overflowY: 'auto', padding: 12 }}>
            {turns.length === 0 && <p className="muted">Ask about devices, alerts or the platform. It reads as you, shows each step, and waits for your confirmation before changing anything. You are on <code>{loc.pathname}</code>.</p>}
            {status && !status.configured && <p role="status" className="muted">No model is connected. An admin can connect one under Settings. The rest of the platform is unaffected.</p>}
            {turns.map((t, i) => (
              <div key={i} style={{ margin: '10px 0' }}>
                <div className="muted" style={{ fontSize: 11 }}>{t.role === 'user' ? 'You' : 'Assistant'}</div>
                {t.role === 'user' ? <div style={{ whiteSpace: 'pre-wrap' }}>{t.content}</div> : (
                  <div role={t.error ? 'alert' : undefined} style={t.error ? { color: '#dc2626' } : undefined}>
                    {t.plan && t.plan.length > 0 && <ol className="muted" style={{ margin: '4px 0', paddingLeft: 20, fontSize: 12 }}>{t.plan.map((p, j) => <li key={j}>{p}</li>)}</ol>}
                    {t.steps && t.steps.length > 0 && (
                      <details open={t.live} style={{ margin: '4px 0' }}>
                        <summary className="muted" style={{ fontSize: 12 }}>{t.steps.length} step{t.steps.length === 1 ? '' : 's'}</summary>
                        {t.steps.map((s, j) => <div key={j} className="muted" style={{ fontSize: 11 }}><span className={`pill ${s.status === 'ok' ? 'ok' : s.status === 'proposed' ? '' : 'warn'}`}>{s.status}</span> {s.tool}: {s.detail}</div>)}
                      </details>
                    )}
                    {t.live && !t.content && <span className="muted">Thinking...</span>}
                    {t.error ? <div>{t.content}</div> : <Markdown text={t.content} />}
                    {t.files?.filter(f => safeDownload(f.path)).map(f => (
                      <div key={f.path} style={{ margin: '8px 0' }}>
                        <button className="ghost" onClick={() => download(f.path, f.filename).catch(e => setDlErr(String(e)))}>{f.label}</button>
                      </div>
                    ))}
                    {t.pending?.map(p => (
                      <div key={p.id} className="card" style={{ margin: '8px 0', padding: '8px 10px' }}>
                        <b>Waiting for you:</b> {p.summary}
                        <div className="muted" style={{ fontSize: 11 }}>{p.method} {p.path}{p.body ? ` ${p.body}` : ''}</div>
                        {acts[p.id] ? <span className="pill">{acts[p.id]}</span>
                          : <div style={{ marginTop: 6, display: 'flex', gap: 8 }}><button onClick={() => decide(p.id, 'confirm')}>Confirm</button><button className="ghost" onClick={() => decide(p.id, 'reject')}>Reject</button></div>}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
            <div ref={end} />
          </div>
          {dlErr && <div role="alert" style={{ padding: '4px 10px', color: '#dc2626', fontSize: 12 }}>{dlErr}</div>}
          <form onSubmit={send} style={{ display: 'flex', gap: 8, padding: 10, borderTop: '1px solid rgba(127,127,127,.3)' }}>
            <input aria-label="Message the assistant" placeholder="What should I look at?" value={text} onChange={e => setText(e.target.value)} disabled={busy} />
            {busy ? <button type="button" className="ghost" onClick={() => abort.current?.abort()}>Stop</button> : <button type="submit" disabled={!text.trim()}>Send</button>}
          </form>
          <p className="muted" style={{ fontSize: 11, margin: 0, padding: '0 10px 8px' }}>Answers come from a local model and can be wrong. Check values before you confirm a change.</p>
        </aside>
      )}
    </>
  );
}
